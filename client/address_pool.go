package client

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/pkg/logger"
)

// AddressConnectionPool manages one connection per remote address
// Thread-safe pool that prevents duplicate connections and handles lifecycle
type AddressConnectionPool struct {
	config *PoolConfig

	// Connection management - maps remote address to connection
	connections   map[string]*ManagedConnection
	connectionsMu sync.RWMutex

	// Connection establishment synchronization - prevents duplicate dials
	// Maps address to a channel that callers wait on during establishment
	establishing   map[string]chan error
	establishingMu sync.Mutex

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	closed atomic.Bool

	// Metrics
	metrics PoolMetrics

	// Logger
	logger logger.Logger
}

// PoolConfig holds configuration for the address-based connection pool
type PoolConfig struct {
	// Diameter identity
	OriginHost  string
	OriginRealm string
	ProductName string
	VendorID    uint32

	// Timeouts
	DialTimeout    time.Duration // TCP dial timeout
	SendTimeout    time.Duration // Timeout for sending messages
	CERTimeout     time.Duration // CER/CEA exchange timeout
	DWRInterval    time.Duration // Device watchdog interval
	DWRTimeout     time.Duration // Device watchdog timeout
	MaxDWRFailures int           // Max consecutive DWR failures before reconnect

	// Reconnection strategy
	ReconnectEnabled  bool          // Whether to automatically reconnect
	ReconnectInterval time.Duration // Initial reconnect delay
	MaxReconnectDelay time.Duration // Maximum reconnect delay
	ReconnectBackoff  float64       // Backoff multiplier (exponential)

	// Buffer sizes
	SendBufferSize int
	RecvBufferSize int

	// Application IDs
	AuthAppIDs []uint32
	AcctAppIDs []uint32

	// Connection lifecycle
	IdleTimeout     time.Duration // Close connection after this idle time (0 = never)
	MaxConnLifetime time.Duration // Maximum connection lifetime (0 = unlimited)

	// Health check
	HealthCheckInterval time.Duration // How often to check connection health
}

// PoolMetrics tracks pool-level metrics
type PoolMetrics struct {
	ActiveConnections      atomic.Int64  // Currently active connections
	TotalConnections       atomic.Uint64 // Total connections created
	FailedEstablishments   atomic.Uint64 // Failed connection attempts
	ReconnectAttempts      atomic.Uint64 // Number of reconnection attempts
	MessagesSent           atomic.Uint64 // Total messages sent
	MessagesReceived       atomic.Uint64 // Total messages received
	BytesSent              atomic.Uint64 // Total bytes sent
	BytesReceived          atomic.Uint64 // Total bytes received
	ConnectionsClosed      atomic.Uint64 // Total connections closed
	IdleTimeouts           atomic.Uint64 // Connections closed due to idle timeout
	MaxLifetimeExpirations atomic.Uint64 // Connections closed due to max lifetime
}

// ManagedConnection wraps a Connection with lifecycle management
type ManagedConnection struct {
	conn      *Connection
	address   string
	createdAt time.Time
	pool      *AddressConnectionPool

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// PoolMetricsSnapshot is a point-in-time snapshot of pool metrics
type PoolMetricsSnapshot struct {
	ActiveConnections      int64
	TotalConnections       uint64
	FailedEstablishments   uint64
	ReconnectAttempts      uint64
	MessagesSent           uint64
	MessagesReceived       uint64
	BytesSent              uint64
	BytesReceived          uint64
	ConnectionsClosed      uint64
	IdleTimeouts           uint64
	MaxLifetimeExpirations uint64
}

// DefaultPoolConfig returns a PoolConfig with sensible defaults
func DefaultPoolConfig() *PoolConfig {
	return &PoolConfig{
		ProductName:         "Diameter-Client",
		VendorID:            10415, // 3GPP
		DialTimeout:         10 * time.Second,
		SendTimeout:         5 * time.Second,
		CERTimeout:          5 * time.Second,
		DWRInterval:         30 * time.Second,
		DWRTimeout:          10 * time.Second,
		MaxDWRFailures:      3,
		ReconnectEnabled:    true,
		ReconnectInterval:   5 * time.Second,
		MaxReconnectDelay:   5 * time.Minute,
		ReconnectBackoff:    1.5,
		SendBufferSize:      100,
		RecvBufferSize:      100,
		IdleTimeout:         0, // Never close idle connections
		MaxConnLifetime:     0, // Unlimited lifetime
		HealthCheckInterval: 30 * time.Second,
	}
}

// Validate validates the pool configuration
func (c *PoolConfig) Validate() error {
	if c.OriginHost == "" {
		return ErrInvalidConfig{Field: "OriginHost", Reason: "must not be empty"}
	}
	if c.OriginRealm == "" {
		return ErrInvalidConfig{Field: "OriginRealm", Reason: "must not be empty"}
	}
	if c.ProductName == "" {
		return ErrInvalidConfig{Field: "ProductName", Reason: "must not be empty"}
	}
	if c.DialTimeout <= 0 {
		return ErrInvalidConfig{Field: "DialTimeout", Reason: "must be greater than 0"}
	}
	if c.DWRInterval <= 0 {
		return ErrInvalidConfig{Field: "DWRInterval", Reason: "must be greater than 0"}
	}
	if c.DWRTimeout <= 0 {
		return ErrInvalidConfig{Field: "DWRTimeout", Reason: "must be greater than 0"}
	}
	if c.DWRTimeout >= c.DWRInterval {
		return ErrInvalidConfig{Field: "DWRTimeout", Reason: "must be less than DWRInterval"}
	}
	if c.MaxDWRFailures <= 0 {
		return ErrInvalidConfig{Field: "MaxDWRFailures", Reason: "must be greater than 0"}
	}
	if c.ReconnectBackoff < 1.0 {
		return ErrInvalidConfig{Field: "ReconnectBackoff", Reason: "must be >= 1.0"}
	}
	return nil
}

// NewAddressConnectionPool creates a new address-based connection pool
func NewAddressConnectionPool(ctx context.Context, config *PoolConfig, log logger.Logger) (*AddressConnectionPool, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	if log == nil {
		log = logger.New("diameter-pool", "info")
	}

	ctx, cancel := context.WithCancel(ctx)

	pool := &AddressConnectionPool{
		config:       config,
		connections:  make(map[string]*ManagedConnection),
		establishing: make(map[string]chan error),
		ctx:          ctx,
		cancel:       cancel,
		logger:       log,
	}

	// Start health checker
	pool.startHealthChecker()

	return pool, nil
}

// Send sends a Diameter message to the specified remote address
// If no connection exists, one will be created (with CER/CEA exchange)
// Concurrent callers for the same address will block until establishment completes
func (p *AddressConnectionPool) Send(ctx context.Context, remoteAddr string, message []byte) error {
	if p.closed.Load() {
		return ErrPoolClosed{}
	}

	// Validate remote address format
	if _, _, err := net.SplitHostPort(remoteAddr); err != nil {
		return fmt.Errorf("invalid remote address %q: %w", remoteAddr, err)
	}

	// Try to get existing connection
	conn, err := p.getOrCreateConnection(ctx, remoteAddr)
	if err != nil {
		return fmt.Errorf("failed to get connection to %s: %w", remoteAddr, err)
	}

	// Send message
	sendCtx := ctx
	if p.config.SendTimeout > 0 {
		var cancel context.CancelFunc
		sendCtx, cancel = context.WithTimeout(ctx, p.config.SendTimeout)
		defer cancel()
	}

	if err := conn.SendWithContext(sendCtx, message); err != nil {
		p.logger.Errorw("Failed to send message", "remote_addr", remoteAddr, "error", err)
		return err
	}

	p.metrics.MessagesSent.Add(1)
	p.metrics.BytesSent.Add(uint64(len(message)))

	return nil
}

// getOrCreateConnection gets an existing connection or creates a new one
// Ensures only one connection per address and prevents concurrent dials
func (p *AddressConnectionPool) getOrCreateConnection(ctx context.Context, remoteAddr string) (*Connection, error) {
	// Fast path: check if connection already exists and is active
	p.connectionsMu.RLock()
	if mc, exists := p.connections[remoteAddr]; exists {
		conn := mc.conn
		p.connectionsMu.RUnlock()
		if conn.IsActive() {
			return conn, nil
		}
	} else {
		p.connectionsMu.RUnlock()
	}

	// Slow path: need to establish connection
	// Use mutex to ensure only one goroutine establishes per address
	p.establishingMu.Lock()

	// Check again in case another goroutine just established
	p.connectionsMu.RLock()
	if mc, exists := p.connections[remoteAddr]; exists {
		conn := mc.conn
		p.connectionsMu.RUnlock()
		if conn.IsActive() {
			p.establishingMu.Unlock()
			return conn, nil
		}
	} else {
		p.connectionsMu.RUnlock()
	}

	// Check if another goroutine is currently establishing
	if waitCh, isEstablishing := p.establishing[remoteAddr]; isEstablishing {
		// Another goroutine is establishing, wait for it
		p.establishingMu.Unlock()

		p.logger.Debugw("Waiting for connection establishment", "remote_addr", remoteAddr)

		select {
		case err := <-waitCh:
			if err != nil {
				return nil, err
			}
			// Connection established by another goroutine, retrieve it
			p.connectionsMu.RLock()
			mc, exists := p.connections[remoteAddr]
			p.connectionsMu.RUnlock()
			if !exists {
				return nil, fmt.Errorf("connection established but not found")
			}
			return mc.conn, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.ctx.Done():
			return nil, ErrPoolClosed{}
		}
	}

	// We're the first to establish this connection
	waitCh := make(chan error, 1)
	p.establishing[remoteAddr] = waitCh
	p.establishingMu.Unlock()

	// Establish the connection
	conn, err := p.establishConnection(ctx, remoteAddr)

	// Notify all waiters
	p.establishingMu.Lock()
	delete(p.establishing, remoteAddr)
	p.establishingMu.Unlock()

	if err != nil {
		p.metrics.FailedEstablishments.Add(1)
		close(waitCh)
		return nil, err
	}

	// Send success to waiters
	go func() {
		waitCh <- nil
		close(waitCh)
	}()

	return conn, nil
}

// establishConnection creates and establishes a new connection to remote address
func (p *AddressConnectionPool) establishConnection(ctx context.Context, remoteAddr string) (*Connection, error) {
	p.logger.Infow("Establishing new connection", "remote_addr", remoteAddr)

	// Parse host and port
	host, port, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid address format: %w", err)
	}

	// Convert port string to int
	portNum := 3868 // default
	if port != "" {
		fmt.Sscanf(port, "%d", &portNum)
	}

	// Create DRA config for this connection
	draConfig := &DRAConfig{
		Host:              host,
		Port:              portNum,
		OriginHost:        p.config.OriginHost,
		OriginRealm:       p.config.OriginRealm,
		ProductName:       p.config.ProductName,
		VendorID:          p.config.VendorID,
		ConnectionCount:   1, // Single connection
		ConnectTimeout:    p.config.DialTimeout,
		CERTimeout:        p.config.CERTimeout,
		DWRInterval:       p.config.DWRInterval,
		DWRTimeout:        p.config.DWRTimeout,
		MaxDWRFailures:    p.config.MaxDWRFailures,
		ReconnectInterval: p.config.ReconnectInterval,
		MaxReconnectDelay: p.config.MaxReconnectDelay,
		ReconnectBackoff:  p.config.ReconnectBackoff,
		SendBufferSize:    p.config.SendBufferSize,
		RecvBufferSize:    p.config.RecvBufferSize,
		AuthAppIDs:        p.config.AuthAppIDs,
		AcctAppIDs:        p.config.AcctAppIDs,
	}

	// Create managed connection context
	connCtx, connCancel := context.WithCancel(p.ctx)

	// Create connection ID
	connID := fmt.Sprintf("conn-%s", remoteAddr)

	// Create the connection
	conn := NewConnection(connCtx, connID, draConfig)

	// Start the connection (this performs CER/CEA)
	if err := conn.Start(); err != nil {
		connCancel()
		return nil, fmt.Errorf("failed to start connection: %w", err)
	}

	// Create managed connection
	mc := &ManagedConnection{
		conn:      conn,
		address:   remoteAddr,
		createdAt: time.Now(),
		pool:      p,
		ctx:       connCtx,
		cancel:    connCancel,
	}

	// Start lifecycle management
	mc.startLifecycleManager()

	// Add to pool
	p.connectionsMu.Lock()
	// Check if connection was added while we were establishing
	if existingMC, exists := p.connections[remoteAddr]; exists {
		// Close the connection we just created and use the existing one
		p.connectionsMu.Unlock()
		connCancel()
		conn.Close()
		p.logger.Warnw("Connection already exists, using existing", "remote_addr", remoteAddr)
		return existingMC.conn, nil
	}
	p.connections[remoteAddr] = mc
	p.connectionsMu.Unlock()

	p.metrics.ActiveConnections.Add(1)
	p.metrics.TotalConnections.Add(1)

	p.logger.Infow("Connection established successfully",
		"remote_addr", remoteAddr,
		"conn_id", connID,
		"active_connections", p.metrics.ActiveConnections.Load())

	return conn, nil
}

// removeConnection removes a connection from the pool
func (p *AddressConnectionPool) removeConnection(remoteAddr string) {
	p.connectionsMu.Lock()
	delete(p.connections, remoteAddr)
	p.connectionsMu.Unlock()

	p.metrics.ActiveConnections.Add(-1)
	p.metrics.ConnectionsClosed.Add(1)

	p.logger.Infow("Connection removed from pool",
		"remote_addr", remoteAddr,
		"active_connections", p.metrics.ActiveConnections.Load())
}

// GetMetrics returns a snapshot of current metrics
func (p *AddressConnectionPool) GetMetrics() PoolMetricsSnapshot {
	return PoolMetricsSnapshot{
		ActiveConnections:      p.metrics.ActiveConnections.Load(),
		TotalConnections:       p.metrics.TotalConnections.Load(),
		FailedEstablishments:   p.metrics.FailedEstablishments.Load(),
		ReconnectAttempts:      p.metrics.ReconnectAttempts.Load(),
		MessagesSent:           p.metrics.MessagesSent.Load(),
		MessagesReceived:       p.metrics.MessagesReceived.Load(),
		BytesSent:              p.metrics.BytesSent.Load(),
		BytesReceived:          p.metrics.BytesReceived.Load(),
		ConnectionsClosed:      p.metrics.ConnectionsClosed.Load(),
		IdleTimeouts:           p.metrics.IdleTimeouts.Load(),
		MaxLifetimeExpirations: p.metrics.MaxLifetimeExpirations.Load(),
	}
}

// GetActiveConnections returns the number of active connections
func (p *AddressConnectionPool) GetActiveConnections() int64 {
	return p.metrics.ActiveConnections.Load()
}

// GetConnection returns the connection for a specific address (if exists)
func (p *AddressConnectionPool) GetConnection(remoteAddr string) (*Connection, bool) {
	p.connectionsMu.RLock()
	defer p.connectionsMu.RUnlock()

	if mc, exists := p.connections[remoteAddr]; exists {
		return mc.conn, true
	}
	return nil, false
}

// ListConnections returns all active connection addresses
func (p *AddressConnectionPool) ListConnections() []string {
	p.connectionsMu.RLock()
	defer p.connectionsMu.RUnlock()

	addresses := make([]string, 0, len(p.connections))
	for addr := range p.connections {
		addresses = append(addresses, addr)
	}
	return addresses
}

// startHealthChecker periodically checks connection health
func (p *AddressConnectionPool) startHealthChecker() {
	if p.config.HealthCheckInterval <= 0 {
		return
	}

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
				p.performHealthCheck()
			}
		}
	}()
}

// performHealthCheck checks all connections and removes dead ones
func (p *AddressConnectionPool) performHealthCheck() {
	p.connectionsMu.RLock()
	connections := make([]*ManagedConnection, 0, len(p.connections))
	for _, mc := range p.connections {
		connections = append(connections, mc)
	}
	p.connectionsMu.RUnlock()

	for _, mc := range connections {
		// Check if connection is still active
		if !mc.conn.IsActive() {
			p.logger.Warnw("Found inactive connection during health check",
				"remote_addr", mc.address,
				"state", mc.conn.GetState())

			// Connection is dead, remove it (will be recreated on next send)
			mc.cancel()
		}
	}
}

// Close gracefully closes the pool and all connections
func (p *AddressConnectionPool) Close() error {
	if !p.closed.CompareAndSwap(false, true) {
		return nil // Already closed
	}

	p.logger.Infow("Closing address connection pool",
		"active_connections", p.metrics.ActiveConnections.Load())

	// Cancel context to stop all goroutines
	p.cancel()

	// Close all connections
	p.connectionsMu.Lock()
	connections := make([]*ManagedConnection, 0, len(p.connections))
	for _, mc := range p.connections {
		connections = append(connections, mc)
	}
	p.connectionsMu.Unlock()

	// Cancel all connection contexts - lifecycle managers will handle cleanup
	for _, mc := range connections {
		mc.cancel()
	}

	// Wait for all goroutines
	p.wg.Wait()

	p.logger.Infow("Address connection pool closed")
	return nil
}

// startLifecycleManager manages connection lifecycle (idle timeout, max lifetime)
func (mc *ManagedConnection) startLifecycleManager() {
	mc.wg.Add(1)
	go func() {
		defer mc.wg.Done()
		defer mc.pool.removeConnection(mc.address)

		// Setup timers - use very long timers if disabled
		// This allows us to use them in select without nil checks
		idleTimeout := mc.pool.config.IdleTimeout
		if idleTimeout == 0 {
			idleTimeout = 100 * 365 * 24 * time.Hour // ~100 years
		}
		idleTimer := time.NewTimer(idleTimeout)
		defer idleTimer.Stop()

		maxLifetime := mc.pool.config.MaxConnLifetime
		if maxLifetime == 0 {
			maxLifetime = 100 * 365 * 24 * time.Hour // ~100 years
		}
		lifetimeTimer := time.NewTimer(maxLifetime)
		defer lifetimeTimer.Stop()

		for {
			select {
			case <-mc.ctx.Done():
				// Pool closed or connection cancelled
				mc.conn.Close()
				return

			case <-idleTimer.C:
				if mc.pool.config.IdleTimeout > 0 {
					// Check if connection is idle
					lastActivity := mc.conn.GetLastActivity()
					if time.Since(lastActivity) >= mc.pool.config.IdleTimeout {
						mc.pool.logger.Infow("Closing idle connection",
							"remote_addr", mc.address,
							"idle_duration", time.Since(lastActivity))
						mc.pool.metrics.IdleTimeouts.Add(1)
						mc.cancel()
						return
					}
					// Reset timer
					idleTimer.Reset(mc.pool.config.IdleTimeout)
				}

			case <-lifetimeTimer.C:
				if mc.pool.config.MaxConnLifetime > 0 {
					mc.pool.logger.Infow("Closing connection due to max lifetime",
						"remote_addr", mc.address,
						"lifetime", time.Since(mc.createdAt))
					mc.pool.metrics.MaxLifetimeExpirations.Add(1)
					mc.cancel()
					return
				}
			}
		}
	}()
}
