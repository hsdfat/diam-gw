package client

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/pkg/connection"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

// Client represents a Diameter client with connection pool management
type Client struct {
	config    *ClientConfig
	pool      *ConnectionPool
	logger    logger.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	stats     ClientStats
	handlers  map[connection.Command]Handler
	handlerMu sync.RWMutex
}

// ClientConfig holds client configuration
type ClientConfig struct {
	ServerAddress       string
	PoolSize            int           // Max connections in pool
	MinPoolSize         int           // Min connections to maintain
	ConnectTimeout      time.Duration
	RequestTimeout      time.Duration
	HealthCheckInterval time.Duration
	MaxRetries          int
	TLSConfig           interface{} // *tls.Config - using interface{} to avoid import
	OriginHost          string
	OriginRealm         string
	ProductName         string
	VendorId            uint32

	// Diameter protocol settings
	DWRInterval    time.Duration
	DWRTimeout     time.Duration
	MaxDWRFailures int
	AuthAppIDs     []uint32
	AcctAppIDs     []uint32
}

// ClientStats tracks client-side statistics
type ClientStats struct {
	TotalRequests    atomic.Uint64
	TotalResponses   atomic.Uint64
	TotalErrors      atomic.Uint64
	ActiveRequests   atomic.Uint64
	BytesSent        atomic.Uint64
	BytesReceived    atomic.Uint64
	PoolStats        PoolStats
	InterfaceStats   map[int]*InterfaceStats
	InterfaceStatsMu sync.RWMutex
}

// InterfaceStats tracks statistics for a specific Diameter interface (Application ID)
type InterfaceStats struct {
	MessagesReceived atomic.Uint64
	MessagesSent     atomic.Uint64
	BytesReceived    atomic.Uint64
	BytesSent        atomic.Uint64
	Errors           atomic.Uint64
	CommandStats     map[int]*CommandStats // Map of command code to stats
	CommandStatsMu   sync.RWMutex
}

// CommandStats tracks statistics for a specific command code
type CommandStats struct {
	MessagesReceived atomic.Uint64
	MessagesSent     atomic.Uint64
	BytesReceived    atomic.Uint64
	BytesSent        atomic.Uint64
	Errors           atomic.Uint64
}

// Handler is a function that handles a Diameter response message
type Handler func(msg *connection.Message, conn connection.Conn)

// ClientStatsSnapshot is a snapshot of client statistics for reading
type ClientStatsSnapshot struct {
	TotalRequests    uint64
	TotalResponses   uint64
	TotalErrors      uint64
	ActiveRequests   uint64
	BytesSent        uint64
	BytesReceived    uint64
	PoolStats        PoolStatsSnapshot
	InterfaceStats   map[int]InterfaceStatsSnapshot
}

// InterfaceStatsSnapshot is a snapshot of interface statistics
type InterfaceStatsSnapshot struct {
	ApplicationID    int
	MessagesReceived uint64
	MessagesSent     uint64
	BytesReceived    uint64
	BytesSent        uint64
	Errors           uint64
	CommandStats     map[int]CommandStatsSnapshot
}

// CommandStatsSnapshot is a snapshot of command statistics
type CommandStatsSnapshot struct {
	CommandCode      int
	MessagesReceived uint64
	MessagesSent     uint64
	BytesReceived    uint64
	BytesSent        uint64
	Errors           uint64
}

// PoolStatsSnapshot is a snapshot of pool statistics
type PoolStatsSnapshot struct {
	TotalConnections  int
	ActiveConnections int
	TotalMessagesSent uint64
	TotalMessagesRecv uint64
	TotalBytesSent    uint64
	TotalBytesRecv    uint64
	TotalReconnects   uint32
}

// NewClient creates a new Diameter client
func NewClient(config *ClientConfig, log logger.Logger) (*Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if log == nil {
		log = logger.New("diameter-client", "info")
	}

	if err := validateClientConfig(config); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	client := &Client{
		config:   config,
		logger:   log,
		ctx:      ctx,
		cancel:   cancel,
		handlers: make(map[connection.Command]Handler),
		stats: ClientStats{
			InterfaceStats: make(map[int]*InterfaceStats),
		},
	}

	// Create connection pool
	poolConfig := &DRAConfig{
		Host:              config.ServerAddress,
		Port:              3868, // Default Diameter port
		OriginHost:        config.OriginHost,
		OriginRealm:       config.OriginRealm,
		ProductName:       config.ProductName,
		VendorID:          config.VendorId,
		ConnectionCount:   config.PoolSize,
		ConnectTimeout:    config.ConnectTimeout,
		DWRInterval:       config.DWRInterval,
		DWRTimeout:        config.DWRTimeout,
		MaxDWRFailures:    config.MaxDWRFailures,
		AuthAppIDs:        config.AuthAppIDs,
		AcctAppIDs:        config.AcctAppIDs,
		SendBufferSize:    100,
		RecvBufferSize:    100,
		ReconnectInterval: 5 * time.Second,
		MaxReconnectDelay: 5 * time.Minute,
		ReconnectBackoff:  1.5,
	}

	pool, err := NewConnectionPool(ctx, poolConfig)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	client.pool = pool

	return client, nil
}

// Start initializes the client and establishes connections
func (c *Client) Start() error {
	c.logger.Infow("Starting Diameter client", "server", c.config.ServerAddress)

	if err := c.pool.Start(); err != nil {
		return fmt.Errorf("failed to start connection pool: %w", err)
	}

	c.logger.Infow("Client started successfully", "active_connections", c.pool.getActiveCount())
	return nil
}

// Stop gracefully stops the client and drains the connection pool
func (c *Client) Stop() error {
	c.logger.Infow("Stopping Diameter client")
	c.cancel()

	if err := c.pool.Close(); err != nil {
		c.logger.Errorw("Error closing connection pool", "error", err)
		return err
	}

	c.logger.Infow("Client stopped successfully")
	return nil
}

// Send sends a message and waits for a response
func (c *Client) Send(msg []byte) ([]byte, error) {
	return c.SendWithTimeout(msg, c.config.RequestTimeout)
}

// SendWithTimeout sends a message and waits for a response with a specific timeout
func (c *Client) SendWithTimeout(msg []byte, timeout time.Duration) ([]byte, error) {
	c.stats.TotalRequests.Add(1)
	c.stats.ActiveRequests.Add(1)
	defer c.stats.ActiveRequests.Add(^uint64(0)) // Decrement

	if err := c.pool.Send(msg); err != nil {
		c.stats.TotalErrors.Add(1)
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	c.stats.BytesSent.Add(uint64(len(msg)))

	// Update interface and command stats
	if len(msg) >= 20 {
		cmd, err := connection.ParseCommand(msg[:20])
		if err == nil {
			ifStats := c.getOrCreateInterfaceStats(cmd.Interface)
			ifStats.MessagesSent.Add(1)
			ifStats.BytesSent.Add(uint64(len(msg)))
			cmdStats := ifStats.getOrCreateCommandStats(cmd.Code)
			cmdStats.MessagesSent.Add(1)
			cmdStats.BytesSent.Add(uint64(len(msg)))
		}
	}

	// Wait for response from pool receive channel
	select {
	case resp := <-c.pool.Receive():
		c.stats.TotalResponses.Add(1)
		c.stats.BytesReceived.Add(uint64(len(resp)))

		// Update interface and command stats for response
		if len(resp) >= 20 {
			cmd, err := connection.ParseCommand(resp[:20])
			if err == nil {
				ifStats := c.getOrCreateInterfaceStats(cmd.Interface)
				ifStats.MessagesReceived.Add(1)
				ifStats.BytesReceived.Add(uint64(len(resp)))
				cmdStats := ifStats.getOrCreateCommandStats(cmd.Code)
				cmdStats.MessagesReceived.Add(1)
				cmdStats.BytesReceived.Add(uint64(len(resp)))
			}
		}

		return resp, nil
	case <-time.After(timeout):
		c.stats.TotalErrors.Add(1)
		return nil, fmt.Errorf("request timeout after %v", timeout)
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	}
}

// SendAsync sends a message asynchronously and calls the callback with the response
func (c *Client) SendAsync(msg []byte, callback func([]byte, error)) {
	go func() {
		resp, err := c.Send(msg)
		callback(resp, err)
	}()
}

// HandleFunc registers a response handler for a specific command
func (c *Client) HandleFunc(cmd connection.Command, handler Handler) {
	c.handlerMu.Lock()
	defer c.handlerMu.Unlock()
	c.handlers[cmd] = handler
	c.logger.Infow("Registered handler for command", "interface", cmd.Interface, "code", cmd.Code)
}

// GetStats returns a snapshot of client statistics
func (c *Client) GetStats() ClientStatsSnapshot {
	c.stats.InterfaceStatsMu.RLock()
	defer c.stats.InterfaceStatsMu.RUnlock()

	// Get pool stats from the connection pool
	poolStats := c.pool.GetStats()

	snapshot := ClientStatsSnapshot{
		TotalRequests:  c.stats.TotalRequests.Load(),
		TotalResponses: c.stats.TotalResponses.Load(),
		TotalErrors:    c.stats.TotalErrors.Load(),
		ActiveRequests: c.stats.ActiveRequests.Load(),
		BytesSent:      c.stats.BytesSent.Load(),
		BytesReceived:  c.stats.BytesReceived.Load(),
		PoolStats: PoolStatsSnapshot{
			TotalConnections:  poolStats.TotalConnections,
			ActiveConnections: poolStats.ActiveConnections,
			TotalMessagesSent: poolStats.TotalMessagesSent,
			TotalMessagesRecv: poolStats.TotalMessagesRecv,
			TotalBytesSent:    poolStats.TotalBytesSent,
			TotalBytesRecv:    poolStats.TotalBytesRecv,
			TotalReconnects:   poolStats.TotalReconnects,
		},
		InterfaceStats: make(map[int]InterfaceStatsSnapshot),
	}

	// Copy interface stats
	for appID, ifStats := range c.stats.InterfaceStats {
		ifStats.CommandStatsMu.RLock()
		cmdStatsSnapshot := make(map[int]CommandStatsSnapshot)
		for cmdCode, cmdStats := range ifStats.CommandStats {
			cmdStatsSnapshot[cmdCode] = CommandStatsSnapshot{
				CommandCode:      cmdCode,
				MessagesReceived: cmdStats.MessagesReceived.Load(),
				MessagesSent:     cmdStats.MessagesSent.Load(),
				BytesReceived:    cmdStats.BytesReceived.Load(),
				BytesSent:        cmdStats.BytesSent.Load(),
				Errors:           cmdStats.Errors.Load(),
			}
		}
		ifStats.CommandStatsMu.RUnlock()

		snapshot.InterfaceStats[appID] = InterfaceStatsSnapshot{
			ApplicationID:    appID,
			MessagesReceived: ifStats.MessagesReceived.Load(),
			MessagesSent:     ifStats.MessagesSent.Load(),
			BytesReceived:    ifStats.BytesReceived.Load(),
			BytesSent:        ifStats.BytesSent.Load(),
			Errors:           ifStats.Errors.Load(),
			CommandStats:     cmdStatsSnapshot,
		}
	}

	return snapshot
}

// getOrCreateInterfaceStats gets or creates stats for an interface
func (c *Client) getOrCreateInterfaceStats(appID int) *InterfaceStats {
	c.stats.InterfaceStatsMu.Lock()
	defer c.stats.InterfaceStatsMu.Unlock()

	if stats, exists := c.stats.InterfaceStats[appID]; exists {
		return stats
	}

	stats := &InterfaceStats{
		CommandStats: make(map[int]*CommandStats),
	}
	c.stats.InterfaceStats[appID] = stats
	return stats
}

// getOrCreateCommandStats gets or creates stats for a command within an interface
func (ifStats *InterfaceStats) getOrCreateCommandStats(cmdCode int) *CommandStats {
	ifStats.CommandStatsMu.Lock()
	defer ifStats.CommandStatsMu.Unlock()

	if stats, exists := ifStats.CommandStats[cmdCode]; exists {
		return stats
	}

	stats := &CommandStats{}
	ifStats.CommandStats[cmdCode] = stats
	return stats
}

// validateClientConfig validates the client configuration
func validateClientConfig(config *ClientConfig) error {
	if config.ServerAddress == "" {
		return fmt.Errorf("ServerAddress is required")
	}
	if config.OriginHost == "" {
		return fmt.Errorf("OriginHost is required")
	}
	if config.OriginRealm == "" {
		return fmt.Errorf("OriginRealm is required")
	}
	if config.PoolSize <= 0 {
		return fmt.Errorf("PoolSize must be greater than 0")
	}
	if config.MinPoolSize < 0 {
		return fmt.Errorf("MinPoolSize cannot be negative")
	}
	if config.MinPoolSize > config.PoolSize {
		return fmt.Errorf("MinPoolSize cannot be greater than PoolSize")
	}
	if config.ConnectTimeout <= 0 {
		config.ConnectTimeout = 10 * time.Second
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 30 * time.Second
	}
	if config.HealthCheckInterval <= 0 {
		config.HealthCheckInterval = 10 * time.Second
	}
	if config.DWRInterval <= 0 {
		config.DWRInterval = 30 * time.Second
	}
	if config.DWRTimeout <= 0 {
		config.DWRTimeout = 10 * time.Second
	}
	if config.MaxDWRFailures <= 0 {
		config.MaxDWRFailures = 3
	}
	return nil
}

// DefaultClientConfig returns a ClientConfig with sensible defaults
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		PoolSize:            5,
		MinPoolSize:         1,
		ConnectTimeout:      10 * time.Second,
		RequestTimeout:      30 * time.Second,
		HealthCheckInterval: 10 * time.Second,
		MaxRetries:          3,
		ProductName:         "Diameter-GW-Client",
		VendorId:            10415, // 3GPP
		DWRInterval:         30 * time.Second,
		DWRTimeout:          10 * time.Second,
		MaxDWRFailures:      3,
	}
}
