package client

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/pkg/logger"
)

// AddressClient is a Diameter client that manages connections per remote address
// This client accepts (remoteAddr, message) pairs and automatically manages
// connection pooling, ensuring one connection per remote address
type AddressClient struct {
	pool   *AddressConnectionPool
	logger logger.Logger

	// Client-level statistics
	stats AddressClientStats

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
}

// AddressClientStats tracks client-level statistics
type AddressClientStats struct {
	TotalRequests  atomic.Uint64
	TotalResponses atomic.Uint64
	TotalErrors    atomic.Uint64
	TotalTimeouts  atomic.Uint64
}

// AddressClientStatsSnapshot is a point-in-time snapshot of client statistics
type AddressClientStatsSnapshot struct {
	TotalRequests  uint64
	TotalResponses uint64
	TotalErrors    uint64
	TotalTimeouts  uint64
	PoolMetrics    PoolMetricsSnapshot
}

// NewAddressClient creates a new address-based Diameter client
func NewAddressClient(ctx context.Context, config *PoolConfig, log logger.Logger) (*AddressClient, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if log == nil {
		log = logger.New("diameter-address-client", "info")
	}

	// Create connection pool
	pool, err := NewAddressConnectionPool(ctx, config, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)

	client := &AddressClient{
		pool:   pool,
		logger: log,
		ctx:    ctx,
		cancel: cancel,
	}

	client.logger.Infow("Address-based Diameter client created")

	return client, nil
}

// Send sends a Diameter message to the specified remote address
// The remoteAddr must be in "host:port" format (e.g., "192.168.1.100:3868")
// If no connection exists to the remote address, one will be created automatically
// If a connection is already being established by another goroutine, this call will block until ready
//
// Thread-safe: Multiple goroutines can call this method concurrently
func (c *AddressClient) Send(remoteAddr string, message []byte) error {
	return c.SendWithContext(c.ctx, remoteAddr, message)
}

// SendWithContext sends a Diameter message with context support
// The remoteAddr must be in "host:port" format (e.g., "192.168.1.100:3868")
// The context can be used to set timeouts or cancel the operation
//
// Thread-safe: Multiple goroutines can call this method concurrently
func (c *AddressClient) SendWithContext(ctx context.Context, remoteAddr string, message []byte) error {
	c.stats.TotalRequests.Add(1)

	// Send via pool (pool handles connection lookup/creation)
	if err := c.pool.Send(ctx, remoteAddr, message); err != nil {
		c.stats.TotalErrors.Add(1)

		// Check if it was a timeout
		if ctx.Err() == context.DeadlineExceeded {
			c.stats.TotalTimeouts.Add(1)
		}

		c.logger.Errorw("Failed to send message",
			"remote_addr", remoteAddr,
			"message_len", len(message),
			"error", err)
		return err
	}

	c.stats.TotalResponses.Add(1)

	c.logger.Debugw("Message sent successfully",
		"remote_addr", remoteAddr,
		"message_len", len(message))

	return nil
}

// SendWithTimeout sends a Diameter message with a specific timeout
// This is a convenience method that creates a context with timeout
//
// Thread-safe: Multiple goroutines can call this method concurrently
func (c *AddressClient) SendWithTimeout(remoteAddr string, message []byte, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()

	return c.SendWithContext(ctx, remoteAddr, message)
}

// GetStats returns a snapshot of client and pool statistics
func (c *AddressClient) GetStats() AddressClientStatsSnapshot {
	return AddressClientStatsSnapshot{
		TotalRequests:  c.stats.TotalRequests.Load(),
		TotalResponses: c.stats.TotalResponses.Load(),
		TotalErrors:    c.stats.TotalErrors.Load(),
		TotalTimeouts:  c.stats.TotalTimeouts.Load(),
		PoolMetrics:    c.pool.GetMetrics(),
	}
}

// GetActiveConnections returns the number of active connections in the pool
func (c *AddressClient) GetActiveConnections() int64 {
	return c.pool.GetActiveConnections()
}

// GetConnection returns the connection for a specific remote address (if exists)
// Returns (connection, true) if found, (nil, false) otherwise
func (c *AddressClient) GetConnection(remoteAddr string) (*Connection, bool) {
	return c.pool.GetConnection(remoteAddr)
}

// ListConnections returns all remote addresses with active connections
func (c *AddressClient) ListConnections() []string {
	return c.pool.ListConnections()
}

// Close gracefully closes the client and all connections
func (c *AddressClient) Close() error {
	c.logger.Infow("Closing address-based Diameter client")

	// Cancel context
	c.cancel()

	// Close the pool
	if err := c.pool.Close(); err != nil {
		c.logger.Errorw("Error closing connection pool", "error", err)
		return err
	}

	c.logger.Infow("Address-based Diameter client closed successfully")
	return nil
}

// GetPoolMetrics returns pool metrics (convenience method)
func (c *AddressClient) GetPoolMetrics() PoolMetricsSnapshot {
	return c.pool.GetMetrics()
}
