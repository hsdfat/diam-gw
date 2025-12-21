package client

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/pkg/connection"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

// DRAConfig represents configuration for a single DRA
type DRAServerConfig struct {
	Name     string // DRA identifier (e.g., "DRA-1")
	Host     string
	Port     int
	Priority int // Lower number = higher priority (1 = highest)
	Weight   int // For load balancing within same priority
}

type Command = connection.Command

// DRAPoolConfig holds configuration for multiple DRAs with priorities
type DRAPoolConfig struct {
	// DRA servers grouped by priority
	DRAs []*DRAServerConfig

	// Common Diameter identity
	OriginHost  string
	OriginRealm string
	ProductName string
	VendorID    uint32

	// Connection settings (per DRA)
	ConnectionsPerDRA int // Number of connections to each DRA

	// Timeouts
	ConnectTimeout      time.Duration
	CERTimeout          time.Duration
	DWRInterval         time.Duration
	DWRTimeout          time.Duration
	MaxDWRFailures      int // Maximum consecutive DWR failures before reconnecting
	HealthCheckInterval time.Duration

	// Reconnection strategy
	ReconnectInterval time.Duration
	MaxReconnectDelay time.Duration
	ReconnectBackoff  float64

	// Buffer sizes
	SendBufferSize int
	RecvBufferSize int
}

// DRAPool manages connections to multiple DRAs with priority-based routing
type DRAPool struct {
	config *DRAPoolConfig

	// DRA pools organized by priority
	draPools   map[string]*ConnectionPool // key: DRA name
	draConfigs []*DRAServerConfig         // sorted by priority

	// Priority groups
	priorityGroups map[int][]*DRAServerConfig // priority -> DRAs

	// Active priority tracking
	activePriority atomic.Int32
	priorityMu     sync.RWMutex

	// Message aggregation
	recvCh chan DiamConnectionInfo

	handlers   map[connection.Command]Handler
	handlerMu  sync.RWMutex
	middleware []func()

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Statistics
	stats  DRAPoolStats
	logger logger.Logger
}

// DRAPoolStats holds statistics for the entire DRA pool
type DRAPoolStats struct {
	TotalDRAs         int
	ActiveDRAs        int
	CurrentPriority   int
	TotalConnections  int
	ActiveConnections int
	TotalMessagesSent uint64
	TotalMessagesRecv uint64
	TotalMessagesDrop uint64
	FailoverCount     atomic.Uint32
}

func (p *DRAPool) SetHandler(h map[connection.Command]Handler) {
	p.handlers = h
}

func (p *DRAPool) GetHandler() map[Command]Handler {
	return p.handlers
}

func (p *DRAPool) NewMiddleware(fn func()) {
	if fn == nil {
		return
	}
	p.middleware = append(p.middleware, fn)
}

// NewDRAPool creates a new priority-based DRA pool
func NewDRAPool(ctx context.Context, config *DRAPoolConfig, log logger.Logger) (*DRAPool, error) {
	if err := validateDRAPoolConfig(config); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)

	// Sort DRAs by priority
	sortedDRAs := make([]*DRAServerConfig, len(config.DRAs))
	copy(sortedDRAs, config.DRAs)
	sort.Slice(sortedDRAs, func(i, j int) bool {
		if sortedDRAs[i].Priority == sortedDRAs[j].Priority {
			return sortedDRAs[i].Weight > sortedDRAs[j].Weight
		}
		return sortedDRAs[i].Priority < sortedDRAs[j].Priority
	})

	// Group DRAs by priority
	priorityGroups := make(map[int][]*DRAServerConfig)
	for _, dra := range sortedDRAs {
		priorityGroups[dra.Priority] = append(priorityGroups[dra.Priority], dra)
	}

	pool := &DRAPool{
		config:         config,
		draPools:       make(map[string]*ConnectionPool),
		draConfigs:     sortedDRAs,
		priorityGroups: priorityGroups,
		recvCh:         make(chan DiamConnectionInfo, config.RecvBufferSize*len(config.DRAs)*config.ConnectionsPerDRA),
		ctx:            ctx,
		cancel:         cancel,
		handlers:       make(map[connection.Command]Handler),
		logger:         log,
	}

	// Initialize with highest priority (lowest number)
	if len(sortedDRAs) > 0 {
		pool.activePriority.Store(int32(sortedDRAs[0].Priority))
	}

	// Create connection pools for each DRA
	for _, draConfig := range sortedDRAs {
		poolConfig := &DRAConfig{
			Host:              draConfig.Host,
			Port:              draConfig.Port,
			OriginHost:        config.OriginHost,
			OriginRealm:       config.OriginRealm,
			ProductName:       config.ProductName,
			VendorID:          config.VendorID,
			ConnectionCount:   config.ConnectionsPerDRA,
			ConnectTimeout:    config.ConnectTimeout,
			CERTimeout:        config.CERTimeout,
			DWRInterval:       config.DWRInterval,
			DWRTimeout:        config.DWRTimeout,
			MaxDWRFailures:    config.MaxDWRFailures,
			ReconnectInterval: config.ReconnectInterval,
			MaxReconnectDelay: config.MaxReconnectDelay,
			ReconnectBackoff:  config.ReconnectBackoff,
			SendBufferSize:    config.SendBufferSize,
			RecvBufferSize:    config.RecvBufferSize,
		}

		connPool, err := NewConnectionPool(ctx, poolConfig, log)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to create connection pool for %s: %w", draConfig.Name, err)
		}

		pool.draPools[draConfig.Name] = connPool
	}

	return pool, nil
}

// HandleFunc registers a response handler for a specific command
func (p *DRAPool) HandleFunc(cmd connection.Command, handler Handler) {
	p.handlerMu.Lock()
	defer p.handlerMu.Unlock()
	p.handlers[cmd] = handler
	p.logger.Infow("Registered handler for command", "interface", cmd.Interface, "code", cmd.Code)
}

// Start starts all DRA connection pools
func (p *DRAPool) Start() error {
	p.logger.Infow("Starting DRA pool", "total_dras", len(p.draConfigs), "connections_per_dra", p.config.ConnectionsPerDRA)

	// Start all connection pools
	var startWg sync.WaitGroup
	errCh := make(chan error, len(p.draPools))

	for _, draConfig := range p.draConfigs {
		startWg.Add(1)
		go func(dra *DRAServerConfig) {
			defer startWg.Done()

			pool := p.draPools[dra.Name]
			if err := pool.Start(); err != nil {
				p.logger.Errorw("Failed to start DRA pool", "dra", dra.Name, "error", err)
				errCh <- fmt.Errorf("%s: %w", dra.Name, err)
			} else {
				p.logger.Infow("DRA pool started", "dra", dra.Name, "priority", dra.Priority, "host", dra.Host, "port", dra.Port)
			}
		}(draConfig)
	}

	startWg.Wait()
	close(errCh)

	// Check for errors
	var errors []error
	for err := range errCh {
		errors = append(errors, err)
	}

	p.startHandleReceive()

	// Start message aggregator
	p.startMessageAggregator()

	// Start health monitor
	p.startHealthMonitor()

	// Start priority manager
	p.startPriorityManager()

	if len(errors) > 0 {
		p.logger.Warnw("Some DRAs failed to start", "errors", len(errors))
	}

	// Update initial stats
	p.updateStats()

	p.logger.Infow("DRA pool started successfully",
		"active_priority", p.activePriority.Load(),
		"total_dras", p.stats.TotalDRAs,
		"active_dras", p.stats.ActiveDRAs)

	return nil
}

// Send sends a message to active priority DRAs using load balancing
func (p *DRAPool) Send(data []byte) error {
	currentPriority := int(p.activePriority.Load())

	// Get DRAs at current priority level
	p.priorityMu.RLock()
	draList := p.priorityGroups[currentPriority]
	p.priorityMu.RUnlock()

	if len(draList) == 0 {
		return fmt.Errorf("no DRAs available at priority %d", currentPriority)
	}

	// Try to send to each DRA at current priority
	for _, dra := range draList {
		pool := p.draPools[dra.Name]
		if pool.IsHealthy() {
			if err := pool.Send(data); err == nil {
				return nil
			}
		}
	}

	return fmt.Errorf("failed to send message to any DRA at priority %d", currentPriority)
}

// SendToDRA sends a message to a specific DRA
func (p *DRAPool) SendToDRA(draName string, data []byte) error {
	pool, exists := p.draPools[draName]
	if !exists {
		return fmt.Errorf("DRA %s not found", draName)
	}

	return pool.Send(data)
}

const (
	nworker = 4
)

func (p *DRAPool) startHandleReceive() {

	for i := 0; i < nworker; i++ {
		go p.handleReceive()
	}
}

// Receive returns the aggregated receive channel
func (p *DRAPool) handleReceive() {
	for rev := range p.recvCh {
		go func() {
			msg := rev.Message
			if msg == nil {
				p.logger.Errorw("recv msg is nil")
				return
			}
			header, err := ParseMessageHeader(msg.Header)
			if err != nil {
				p.logger.Errorw("parse header error", "err", err)
				return
			}
			command := connection.Command{
				Interface: int(header.ApplicationID),
				Request:   header.IsRequest,
				Code:      int(header.CommandCode),
			}
			p.handlerMu.RLock()
			fn, ok := p.handlers[command]
			p.handlerMu.RUnlock()
			if !ok {
				p.logger.Errorw("cannot found any handler", "app-id",
					header.ApplicationID, "code", header.CommandCode, "request", header.IsRequest)
				return
			}
			for _, middleFunc := range p.middleware {
				middleFunc()
			}
			fn(rev.Message, rev.DiamConn)
		}()
	}
}

// startMessageAggregator aggregates messages from all DRA pools
func (p *DRAPool) startMessageAggregator() {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()

		// Collect receive channels from all pools
		channels := make([]<-chan DiamConnectionInfo, 0, len(p.draPools))
		for _, pool := range p.draPools {
			channels = append(channels, pool.Receive())
		}

		for {
			for _, ch := range channels {
				select {
				case <-p.ctx.Done():
					return
				case msg, ok := <-ch:
					if !ok {
						continue
					}
					select {
					case p.recvCh <- msg:
					case <-p.ctx.Done():
						return
					default:
						p.logger.Warn("DRA pool receive buffer full, dropping message")
						atomic.AddUint64(&p.stats.TotalMessagesDrop, 1)
					}
				default:
				}
			}
			time.Sleep(1 * time.Millisecond)
		}
	}()
}

// startHealthMonitor monitors health of all DRAs
func (p *DRAPool) startHealthMonitor() {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()

		ticker := time.NewTicker(p.config.HealthCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-p.ctx.Done():
				return
			case <-ticker.C:
				p.updateStats()
				p.logHealthStatus()
			}
		}
	}()
}

// startPriorityManager manages priority failover
func (p *DRAPool) startPriorityManager() {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-p.ctx.Done():
				return
			case <-ticker.C:
				p.checkAndUpdatePriority()
			}
		}
	}()
}

// checkAndUpdatePriority checks DRA health and updates active priority
func (p *DRAPool) checkAndUpdatePriority() {
	currentPriority := int(p.activePriority.Load())

	// Check if any DRAs at current priority are healthy
	currentHealthy := p.countHealthyDRAsAtPriority(currentPriority)

	if currentHealthy > 0 {
		// Check if we can fail back to higher priority
		for priority := 1; priority < currentPriority; priority++ {
			healthyCount := p.countHealthyDRAsAtPriority(priority)
			if healthyCount > 0 {
				p.logger.Infow("Failing back to higher priority",
					"old_priority", currentPriority,
					"new_priority", priority,
					"healthy_dras", healthyCount)
				p.activePriority.Store(int32(priority))
				p.stats.FailoverCount.Add(1)
				return
			}
		}
		return
	}

	// Current priority has no healthy DRAs, failover to next priority
	for priority := currentPriority + 1; priority <= 10; priority++ {
		healthyCount := p.countHealthyDRAsAtPriority(priority)
		if healthyCount > 0 {
			p.logger.Warnw("Failing over to lower priority",
				"old_priority", currentPriority,
				"new_priority", priority,
				"healthy_dras", healthyCount)
			p.activePriority.Store(int32(priority))
			p.stats.FailoverCount.Add(1)
			return
		}
	}

	p.logger.Errorw("No healthy DRAs at any priority level")
}

// countHealthyDRAsAtPriority returns count of healthy DRAs at given priority
func (p *DRAPool) countHealthyDRAsAtPriority(priority int) int {
	p.priorityMu.RLock()
	draList := p.priorityGroups[priority]
	p.priorityMu.RUnlock()

	count := 0
	for _, dra := range draList {
		if pool := p.draPools[dra.Name]; pool.IsHealthy() {
			count++
		}
	}
	return count
}

// updateStats updates pool statistics
func (p *DRAPool) updateStats() {
	p.stats.TotalDRAs = len(p.draConfigs)
	p.stats.CurrentPriority = int(p.activePriority.Load())
	p.stats.ActiveDRAs = 0
	p.stats.TotalConnections = 0
	p.stats.ActiveConnections = 0
	p.stats.TotalMessagesSent = 0
	p.stats.TotalMessagesRecv = 0

	for _, dra := range p.draConfigs {
		pool := p.draPools[dra.Name]
		poolStats := pool.GetStats()

		p.stats.TotalConnections += poolStats.TotalConnections
		p.stats.ActiveConnections += poolStats.ActiveConnections
		p.stats.TotalMessagesSent += poolStats.TotalMessagesSent
		p.stats.TotalMessagesRecv += poolStats.TotalMessagesRecv

		if pool.IsHealthy() {
			p.stats.ActiveDRAs++
		}
	}
}

// logHealthStatus logs health status of all DRAs
func (p *DRAPool) logHealthStatus() {
	p.logger.Infow("=== DRA Pool Health Status ===")
	p.logger.Infow("Overall",
		"active_priority", p.stats.CurrentPriority,
		"total_dras", p.stats.TotalDRAs,
		"active_dras", p.stats.ActiveDRAs,
		"total_connections", p.stats.TotalConnections,
		"active_connections", p.stats.ActiveConnections,
		"failover_count", p.stats.FailoverCount.Load())

	for _, dra := range p.draConfigs {
		pool := p.draPools[dra.Name]
		stats := pool.GetStats()
		active := pool.IsHealthy()

		status := "HEALTHY"
		if !active {
			status = "DOWN"
		}

		p.logger.Infow("DRA Status",
			"name", dra.Name,
			"priority", dra.Priority,
			"status", status,
			"active_conns", stats.ActiveConnections,
			"msgs_sent", stats.TotalMessagesSent,
			"msgs_recv", stats.TotalMessagesRecv)
	}
	p.logger.Infow("================================")
}

// GetStats returns aggregated statistics
func (p *DRAPool) GetStats() DRAPoolStats {
	p.updateStats()
	return p.stats
}

// GetDRAPool returns connection pool for specific DRA
func (p *DRAPool) GetDRAPool(draName string) *ConnectionPool {
	return p.draPools[draName]
}

// GetActivePriority returns current active priority
func (p *DRAPool) GetActivePriority() int {
	return int(p.activePriority.Load())
}

// GetDRAsByPriority returns DRAs at given priority level
func (p *DRAPool) GetDRAsByPriority(priority int) []*DRAServerConfig {
	p.priorityMu.RLock()
	defer p.priorityMu.RUnlock()

	dras := p.priorityGroups[priority]
	result := make([]*DRAServerConfig, len(dras))
	copy(result, dras)
	return result
}

// IsHealthy returns true if at least one DRA is healthy
func (p *DRAPool) IsHealthy() bool {
	for _, pool := range p.draPools {
		if pool.IsHealthy() {
			return true
		}
	}
	return false
}

// Close gracefully closes all DRA pools
func (p *DRAPool) Close() error {
	p.logger.Infow("Closing DRA pool")

	p.cancel()

	// Close all pools
	var closeWg sync.WaitGroup
	for name, pool := range p.draPools {
		closeWg.Add(1)
		go func(n string, cp *ConnectionPool) {
			defer closeWg.Done()
			if err := cp.Close(); err != nil {
				p.logger.Errorw("Error closing DRA pool", "dra", n, "error", err)
			}
		}(name, pool)
	}

	closeWg.Wait()
	p.wg.Wait()
	close(p.recvCh)

	p.logger.Infow("DRA pool closed")
	return nil
}

// Helper function to validate DRAPoolConfig
func validateDRAPoolConfig(config *DRAPoolConfig) error {
	if len(config.DRAs) == 0 {
		return fmt.Errorf("no DRAs configured")
	}
	if config.OriginHost == "" {
		return fmt.Errorf("OriginHost is required")
	}
	if config.OriginRealm == "" {
		return fmt.Errorf("OriginRealm is required")
	}
	if config.ConnectionsPerDRA <= 0 {
		return fmt.Errorf("ConnectionsPerDRA must be greater than 0")
	}

	// Check for duplicate DRA names
	names := make(map[string]bool)
	for _, dra := range config.DRAs {
		if names[dra.Name] {
			return fmt.Errorf("duplicate DRA name: %s", dra.Name)
		}
		names[dra.Name] = true

		if dra.Priority <= 0 {
			return fmt.Errorf("DRA %s: priority must be greater than 0", dra.Name)
		}
	}

	return nil
}

// DefaultDRAPoolConfig returns default configuration
func DefaultDRAPoolConfig() *DRAPoolConfig {
	return &DRAPoolConfig{
		ProductName:         "Diameter-GW-S13",
		VendorID:            10415,
		ConnectionsPerDRA:   1,
		ConnectTimeout:      10 * time.Second,
		CERTimeout:          5 * time.Second,
		DWRInterval:         30 * time.Second,
		DWRTimeout:          10 * time.Second,
		MaxDWRFailures:      3,
		HealthCheckInterval: 10 * time.Second,
		ReconnectInterval:   5 * time.Second,
		MaxReconnectDelay:   5 * time.Minute,
		ReconnectBackoff:    1.5,
		SendBufferSize:      100,
		RecvBufferSize:      100,
	}
}
