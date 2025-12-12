package client

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsdfat8/diam-gw/pkg/logger"
)

// ConnectionPool manages multiple connections to a single DRA
type ConnectionPool struct {
	config      *DRAConfig
	connections []*Connection

	// Load balancing
	nextConnIdx atomic.Uint32

	// State tracking
	mu          sync.RWMutex
	activeCount int
	closed      bool

	// Message aggregation
	recvCh chan []byte

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Statistics
	stats PoolStats
}

// PoolStats holds pool-level statistics
type PoolStats struct {
	TotalConnections  int
	ActiveConnections int
	TotalMessagesSent uint64
	TotalMessagesRecv uint64
	TotalBytesSent    uint64
	TotalBytesRecv    uint64
	TotalReconnects   uint32
}

// NewConnectionPool creates a new connection pool
func NewConnectionPool(ctx context.Context, config *DRAConfig) (*ConnectionPool, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)

	pool := &ConnectionPool{
		config:      config,
		connections: make([]*Connection, config.ConnectionCount),
		recvCh:      make(chan []byte, config.RecvBufferSize*config.ConnectionCount),
		ctx:         ctx,
		cancel:      cancel,
	}

	// Create connections
	for i := 0; i < config.ConnectionCount; i++ {
		connID := fmt.Sprintf("%s:%d-conn%d", config.Host, config.Port, i)
		pool.connections[i] = NewConnection(ctx, connID, config)
	}

	return pool, nil
}

// Start starts all connections in the pool
func (p *ConnectionPool) Start() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrPoolClosed{}
	}
	p.mu.Unlock()

	logger.Log.Infow("Starting connection pool", "connections", p.config.ConnectionCount, "host", p.config.Host, "port", p.config.Port)

	// Start all connections
	var startErr error
	var startWg sync.WaitGroup
	errCh := make(chan error, p.config.ConnectionCount)

	for i, conn := range p.connections {
		startWg.Add(1)
		go func(idx int, c *Connection) {
			defer startWg.Done()

			// Stagger connection attempts to avoid thundering herd
			time.Sleep(time.Duration(idx) * 100 * time.Millisecond)

			if err := c.Start(); err != nil {
				logger.Log.Errorw("Failed to start connection", "conn_id", c.ID(), "error", err)
				errCh <- err
			} else {
				p.incrementActive()
			}
		}(i, conn)
	}

	startWg.Wait()
	close(errCh)

	// Check if any connection failed
	for err := range errCh {
		if startErr == nil {
			startErr = err
		}
	}

	// Start message aggregator
	p.startMessageAggregator()

	// Start health reporter
	p.startHealthReporter()

	// Check if we have at least one active connection
	if p.getActiveCount() == 0 {
		return fmt.Errorf("failed to establish any connections: %w", startErr)
	}

	logger.Log.Infow("Connection pool started", "active", p.getActiveCount(), "total", p.config.ConnectionCount)

	return nil
}

// startMessageAggregator aggregates messages from all connections
func (p *ConnectionPool) startMessageAggregator() {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()

		// Create a slice of receive channels
		channels := make([]<-chan []byte, len(p.connections))
		for i, conn := range p.connections {
			channels[i] = conn.Receive()
		}

		// Aggregate messages from all connections
		for {
			// Use select with all channels
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
						logger.Log.Warn("Pool receive buffer full, dropping message")
					}
				default:
					// Non-blocking check
				}
			}

			// Small sleep to avoid busy loop
			time.Sleep(1 * time.Millisecond)
		}
	}()
}

// startHealthReporter periodically logs pool health
func (p *ConnectionPool) startHealthReporter() {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-p.ctx.Done():
				return
			case <-ticker.C:
				stats := p.GetStats()
				logger.Log.Infow("Pool health report",
					"active", stats.ActiveConnections,
					"total", stats.TotalConnections,
					"sent", stats.TotalMessagesSent,
					"recv", stats.TotalMessagesRecv,
					"reconnects", stats.TotalReconnects)
			}
		}
	}()
}

// Send sends a message using round-robin load balancing
func (p *ConnectionPool) Send(data []byte) error {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return ErrPoolClosed{}
	}
	p.mu.RUnlock()

	// Find an active connection using round-robin
	startIdx := p.nextConnIdx.Add(1) % uint32(len(p.connections))

	for i := 0; i < len(p.connections); i++ {
		idx := (startIdx + uint32(i)) % uint32(len(p.connections))
		conn := p.connections[idx]

		if conn.IsActive() {
			return conn.Send(data)
		}
	}

	return ErrNoActiveConnections{}
}

// SendRoundRobin sends a message to a specific connection by index
func (p *ConnectionPool) SendRoundRobin(data []byte) error {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return ErrPoolClosed{}
	}
	p.mu.RUnlock()

	// Get next connection index
	idx := p.nextConnIdx.Add(1) % uint32(len(p.connections))
	conn := p.connections[idx]

	if !conn.IsActive() {
		// Fallback to any active connection
		return p.Send(data)
	}

	return conn.Send(data)
}

// SendToConnection sends a message to a specific connection
func (p *ConnectionPool) SendToConnection(connID string, data []byte) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.closed {
		return ErrPoolClosed{}
	}

	for _, conn := range p.connections {
		if conn.ID() == connID {
			return conn.Send(data)
		}
	}

	return fmt.Errorf("connection %s not found", connID)
}

// Receive returns the aggregated receive channel
func (p *ConnectionPool) Receive() <-chan []byte {
	return p.recvCh
}

// GetActiveConnections returns a list of active connections
func (p *ConnectionPool) GetActiveConnections() []*Connection {
	p.mu.RLock()
	defer p.mu.RUnlock()

	active := make([]*Connection, 0, len(p.connections))
	for _, conn := range p.connections {
		if conn.IsActive() {
			active = append(active, conn)
		}
	}

	return active
}

// GetConnection returns a connection by ID
func (p *ConnectionPool) GetConnection(connID string) *Connection {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, conn := range p.connections {
		if conn.ID() == connID {
			return conn
		}
	}

	return nil
}

// GetAllConnections returns all connections
func (p *ConnectionPool) GetAllConnections() []*Connection {
	p.mu.RLock()
	defer p.mu.RUnlock()

	conns := make([]*Connection, len(p.connections))
	copy(conns, p.connections)
	return conns
}

// GetStats returns aggregated pool statistics
func (p *ConnectionPool) GetStats() PoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := PoolStats{
		TotalConnections:  len(p.connections),
		ActiveConnections: p.activeCount,
	}

	for _, conn := range p.connections {
		connStats := conn.GetStats()
		stats.TotalMessagesSent += connStats.MessagesSent.Load()
		stats.TotalMessagesRecv += connStats.MessagesReceived.Load()
		stats.TotalBytesSent += connStats.BytesSent.Load()
		stats.TotalBytesRecv += connStats.BytesReceived.Load()
		stats.TotalReconnects += connStats.Reconnects.Load()
	}

	return stats
}

// GetConnectionStates returns the state of all connections
func (p *ConnectionPool) GetConnectionStates() map[string]ConnectionState {
	p.mu.RLock()
	defer p.mu.RUnlock()

	states := make(map[string]ConnectionState, len(p.connections))
	for _, conn := range p.connections {
		states[conn.ID()] = conn.GetState()
	}

	return states
}

// IsHealthy returns true if at least one connection is active
func (p *ConnectionPool) IsHealthy() bool {
	return p.getActiveCount() > 0
}

// WaitForConnection waits until at least one connection is active
func (p *ConnectionPool) WaitForConnection(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if p.IsHealthy() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return ErrConnectionTimeout{Operation: "WaitForConnection", Timeout: timeout.String()}
}

// Close gracefully closes all connections
func (p *ConnectionPool) Close() error {
	logger.Log.Infow("Closing connection pool")

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	// Cancel context
	p.cancel()

	// Close all connections
	var closeWg sync.WaitGroup
	for _, conn := range p.connections {
		closeWg.Add(1)
		go func(c *Connection) {
			defer closeWg.Done()
			if err := c.Close(); err != nil {
				logger.Log.Errorw("Error closing connection", "conn_id", c.ID(), "error", err)
			}
		}(conn)
	}

	closeWg.Wait()

	// Wait for aggregator
	p.wg.Wait()

	// Close receive channel
	close(p.recvCh)

	logger.Log.Infow("Connection pool closed")
	return nil
}

// Helper methods

func (p *ConnectionPool) incrementActive() {
	p.mu.Lock()
	p.activeCount++
	p.mu.Unlock()
}

func (p *ConnectionPool) decrementActive() {
	p.mu.Lock()
	p.activeCount--
	p.mu.Unlock()
}

func (p *ConnectionPool) getActiveCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.activeCount
}
