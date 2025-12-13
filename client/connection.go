package client

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsdfat8/diam-gw/commands/base"
	"github.com/hsdfat8/diam-gw/commands/s13"
	"github.com/hsdfat8/diam-gw/models_base"
	"github.com/hsdfat8/diam-gw/pkg/logger"
)

// Connection represents a single Diameter connection to a DRA
type Connection struct {
	id     string
	config *DRAConfig

	// TCP connection
	conn   net.Conn
	connMu sync.RWMutex

	// State management
	state   atomic.Value // stores ConnectionState
	stateMu sync.Mutex

	// Diameter protocol
	hopByHopID atomic.Uint32
	endToEndID atomic.Uint32

	// Activity tracking
	lastActivity time.Time
	activityMu   sync.RWMutex

	// Heartbeat
	dwrTicker        *time.Ticker
	dwrStop          chan struct{}
	dwrStopMu        sync.Mutex
	dwrFailureCount  atomic.Int32
	dwrLastFailTime  time.Time
	dwrLastFailTimeMu sync.RWMutex

	// Message channels
	sendCh chan []byte
	recvCh chan []byte

	// Pending responses
	pendingMu sync.RWMutex
	pending   map[uint32]chan []byte // hop-by-hop ID -> response channel

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Statistics
	stats ConnectionStats
}

// ConnectionStats holds connection statistics
type ConnectionStats struct {
	MessagesSent     atomic.Uint64
	MessagesReceived atomic.Uint64
	BytesSent        atomic.Uint64
	BytesReceived    atomic.Uint64
	Reconnects       atomic.Uint32
	lastErrorMu      sync.RWMutex
	lastError        error
}

// GetLastError returns the last error
func (cs *ConnectionStats) GetLastError() error {
	cs.lastErrorMu.RLock()
	defer cs.lastErrorMu.RUnlock()
	return cs.lastError
}

// SetLastError sets the last error
func (cs *ConnectionStats) SetLastError(err error) {
	cs.lastErrorMu.Lock()
	defer cs.lastErrorMu.Unlock()
	cs.lastError = err
}

// NewConnection creates a new Diameter connection
func NewConnection(ctx context.Context, id string, config *DRAConfig) *Connection {
	ctx, cancel := context.WithCancel(ctx)

	c := &Connection{
		id:      id,
		config:  config,
		sendCh:  make(chan []byte, config.SendBufferSize),
		recvCh:  make(chan []byte, config.RecvBufferSize),
		pending: make(map[uint32]chan []byte),
		ctx:     ctx,
		cancel:  cancel,
	}

	c.state.Store(StateDisconnected)
	return c
}

// ID returns the connection identifier
func (c *Connection) ID() string {
	return c.id
}

// GetState returns the current connection state
func (c *Connection) GetState() ConnectionState {
	return c.state.Load().(ConnectionState)
}

// setState updates the connection state
func (c *Connection) setState(newState ConnectionState) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	oldState := c.GetState()
	if oldState == newState {
		return
	}

	c.state.Store(newState)
	logger.Log.Infow("State transition", "conn_id", c.id, "old_state", oldState.String(), "new_state", newState.String())
}

// IsActive returns true if the connection is active
func (c *Connection) IsActive() bool {
	return c.GetState().IsActive()
}

// Start initiates the connection
func (c *Connection) Start() error {
	logger.Log.Infow("Starting connection", "conn_id", c.id, "host", c.config.Host, "port", c.config.Port)

	if err := c.connect(); err != nil {
		return err
	}

	return nil
}

// connect establishes TCP connection and performs handshake
func (c *Connection) connect() error {
	c.setState(StateConnecting)

	// Establish TCP connection
	addr := fmt.Sprintf("%s:%d", c.config.Host, c.config.Port)
	dialer := net.Dialer{
		Timeout: c.config.ConnectTimeout,
	}

	conn, err := dialer.DialContext(c.ctx, "tcp", addr)
	if err != nil {
		c.setState(StateFailed)
		c.stats.SetLastError(err)
		return fmt.Errorf("failed to connect: %w", err)
	}

	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()

	logger.Log.Infow("TCP connection established", "conn_id", c.id)

	// Start read and write loops
	c.startReadLoop()
	c.startWriteLoop()
	c.startHealthMonitor()

	// Perform CER/CEA handshake
	if err := c.performHandshake(); err != nil {
		c.close()
		c.setState(StateFailed)
		c.stats.SetLastError(err)
		return fmt.Errorf("handshake failed: %w", err)
	}

	logger.Log.Infow("Connection established successfully", "conn_id", c.id)
	c.setState(StateOpen)
	c.updateActivity()

	// Reset DWR failure counter on successful connection
	c.dwrFailureCount.Store(0)

	// Start watchdog timer
	c.startWatchdog()

	return nil
}

// performHandshake performs CER/CEA exchange
func (c *Connection) performHandshake() error {
	logger.Log.Infow("Performing CER/CEA handshake", "conn_id", c.id)

	// Create CER message
	cer := base.NewCapabilitiesExchangeRequest()
	cer.OriginHost = models_base.DiameterIdentity(c.config.OriginHost)
	cer.OriginRealm = models_base.DiameterIdentity(c.config.OriginRealm)
	cer.ProductName = models_base.UTF8String(c.config.ProductName)
	cer.VendorId = models_base.Unsigned32(c.config.VendorID)

	// Set local IP address
	if localAddr := c.getLocalAddr(); localAddr != nil {
		cer.HostIpAddress = []models_base.Address{models_base.Address(localAddr)}
	}

	// Set Auth-Application-Id for S13
	cer.AuthApplicationId = []models_base.Unsigned32{
		models_base.Unsigned32(s13.S13_APPLICATION_ID),
	}

	// Set identifiers
	hopByHopID := c.nextHopByHopID()
	cer.Header.HopByHopID = hopByHopID
	cer.Header.EndToEndID = c.nextEndToEndID()

	// Marshal CER
	cerData, err := cer.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal CER: %w", err)
	}

	// Create response channel
	respCh := make(chan []byte, 1)
	c.registerPending(hopByHopID, respCh)
	defer c.unregisterPending(hopByHopID)

	// Send CER
	c.setState(StateCERSent)
	select {
	case c.sendCh <- cerData:
	case <-c.ctx.Done():
		return c.ctx.Err()
	}

	// Wait for CEA with timeout
	select {
	case ceaData := <-respCh:
		return c.handleCEA(ceaData)
	case <-time.After(c.config.CERTimeout):
		return ErrConnectionTimeout{Operation: "CER/CEA", Timeout: c.config.CERTimeout.String()}
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}

// handleCEA processes Capabilities-Exchange-Answer
func (c *Connection) handleCEA(data []byte) error {
	cea := &base.CapabilitiesExchangeAnswer{}
	if err := cea.Unmarshal(data); err != nil {
		return fmt.Errorf("failed to unmarshal CEA: %w", err)
	}

	// Check result code
	resultCode := ResultCode(cea.ResultCode)
	if !resultCode.IsSuccess() {
		return ErrHandshakeFailed{
			Reason:     resultCode.String(),
			ResultCode: uint32(resultCode),
		}
	}

	logger.Log.Infow("CEA received successfully", "conn_id", c.id, "result_code", resultCode.String())
	return nil
}

// startWatchdog starts the watchdog timer
func (c *Connection) startWatchdog() {
	c.dwrStopMu.Lock()
	defer c.dwrStopMu.Unlock()

	if c.dwrTicker != nil {
		return // Already running
	}

	c.dwrTicker = time.NewTicker(c.config.DWRInterval)
	c.dwrStop = make(chan struct{})

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()

		for {
			select {
			case <-c.ctx.Done():
				return
			case <-c.dwrStop:
				return
			case <-c.dwrTicker.C:
				if err := c.sendDWR(); err != nil {
					logger.Log.Errorw("Failed to send DWR", "conn_id", c.id, "error", err)
					c.handleFailure(err)
					return
				}
			}
		}
	}()
}

// stopWatchdog stops the watchdog timer
func (c *Connection) stopWatchdog() {
	c.dwrStopMu.Lock()
	defer c.dwrStopMu.Unlock()

	if c.dwrTicker != nil {
		c.dwrTicker.Stop()
		close(c.dwrStop)
		c.dwrTicker = nil
	}
}

// sendDWR sends a Device-Watchdog-Request
func (c *Connection) sendDWR() error {
	if !c.IsActive() {
		return ErrConnectionClosed{ConnectionID: c.id}
	}

	logger.Log.Debugw("Sending DWR", "conn_id", c.id)

	dwr := base.NewDeviceWatchdogRequest()
	dwr.OriginHost = models_base.DiameterIdentity(c.config.OriginHost)
	dwr.OriginRealm = models_base.DiameterIdentity(c.config.OriginRealm)

	hopByHopID := c.nextHopByHopID()
	dwr.Header.HopByHopID = hopByHopID
	dwr.Header.EndToEndID = c.nextEndToEndID()

	dwrData, err := dwr.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal DWR: %w", err)
	}

	// Create response channel
	respCh := make(chan []byte, 1)
	c.registerPending(hopByHopID, respCh)

	// Send DWR
	c.setState(StateDWRSent)
	select {
	case c.sendCh <- dwrData:
	case <-c.ctx.Done():
		c.unregisterPending(hopByHopID)
		return c.ctx.Err()
	}

	// Wait for DWA in background
	go func() {
		defer c.unregisterPending(hopByHopID)

		select {
		case dwaData := <-respCh:
			if err := c.handleDWA(dwaData); err != nil {
				logger.Log.Errorw("Failed to handle DWA", "conn_id", c.id, "error", err)
				c.handleDWRFailure(err)
			}
		case <-time.After(c.config.DWRTimeout):
			err := ErrConnectionTimeout{Operation: "DWR/DWA", Timeout: c.config.DWRTimeout.String()}
			c.handleDWRFailure(err)
		case <-c.ctx.Done():
		}
	}()

	return nil
}

// handleDWA processes Device-Watchdog-Answer
func (c *Connection) handleDWA(data []byte) error {
	dwa := &base.DeviceWatchdogAnswer{}
	if err := dwa.Unmarshal(data); err != nil {
		return fmt.Errorf("failed to unmarshal DWA: %w", err)
	}

	resultCode := ResultCode(dwa.ResultCode)
	if !resultCode.IsSuccess() {
		return fmt.Errorf("DWA failed: %s", resultCode.String())
	}

	logger.Log.Debugw("DWA received successfully", "conn_id", c.id)
	c.setState(StateOpen)
	c.updateActivity()

	// Reset DWR failure counter on successful response
	c.dwrFailureCount.Store(0)

	return nil
}

// handleDWRFailure handles DWR/DWA failures with threshold-based reconnection
func (c *Connection) handleDWRFailure(err error) {
	// Increment failure counter
	failureCount := c.dwrFailureCount.Add(1)

	// Update last failure time
	c.dwrLastFailTimeMu.Lock()
	c.dwrLastFailTime = time.Now()
	c.dwrLastFailTimeMu.Unlock()

	logger.Log.Warnw("DWR failure",
		"conn_id", c.id,
		"error", err,
		"failure_count", failureCount,
		"max_failures", c.config.MaxDWRFailures)

	// Check if we've exceeded the failure threshold
	if int(failureCount) >= c.config.MaxDWRFailures {
		logger.Log.Errorw("DWR failure threshold exceeded, triggering reconnection",
			"conn_id", c.id,
			"failure_count", failureCount,
			"max_failures", c.config.MaxDWRFailures)
		c.handleFailure(fmt.Errorf("exceeded max DWR failures (%d): %w", c.config.MaxDWRFailures, err))
	}
}

// handleDWR processes Device-Watchdog-Request (from peer)
func (c *Connection) handleDWR(data []byte) error {
	dwr := &base.DeviceWatchdogRequest{}
	if err := dwr.Unmarshal(data); err != nil {
		return fmt.Errorf("failed to unmarshal DWR: %w", err)
	}

	logger.Log.Debugw("DWR received, sending DWA", "conn_id", c.id)

	// Create DWA
	dwa := base.NewDeviceWatchdogAnswer()
	dwa.OriginHost = models_base.DiameterIdentity(c.config.OriginHost)
	dwa.OriginRealm = models_base.DiameterIdentity(c.config.OriginRealm)
	dwa.ResultCode = models_base.Unsigned32(ResultCodeSuccess)

	// Copy identifiers from request
	dwa.Header.HopByHopID = dwr.Header.HopByHopID
	dwa.Header.EndToEndID = dwr.Header.EndToEndID

	dwaData, err := dwa.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal DWA: %w", err)
	}

	// Send DWA
	select {
	case c.sendCh <- dwaData:
	case <-c.ctx.Done():
		return c.ctx.Err()
	}

	c.updateActivity()
	return nil
}

// Send sends a Diameter message
func (c *Connection) Send(data []byte) error {
	if !c.IsActive() {
		return ErrConnectionClosed{ConnectionID: c.id}
	}

	select {
	case c.sendCh <- data:
		return nil
	case <-c.ctx.Done():
		return c.ctx.Err()
	default:
		return fmt.Errorf("send buffer full")
	}
}

// Receive returns the receive channel
func (c *Connection) Receive() <-chan []byte {
	return c.recvCh
}

// startReadLoop starts the message read loop
func (c *Connection) startReadLoop() {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer logger.Log.Infow("Read loop exited", "conn_id", c.id)

		for {
			select {
			case <-c.ctx.Done():
				return
			default:
			}

			c.connMu.RLock()
			conn := c.conn
			c.connMu.RUnlock()

			if conn == nil {
				return
			}

			// Read header (20 bytes)
			header := make([]byte, 20)
			if err := c.readFull(conn, header); err != nil {
				if c.ctx.Err() == nil {
					logger.Log.Errorw("Failed to read header", "conn_id", c.id, "error", err)
					c.handleFailure(err)
				}
				return
			}

			// Parse message length
			length := binary.BigEndian.Uint32([]byte{0, header[1], header[2], header[3]})
			if length < 20 || length > 1<<24 {
				logger.Log.Errorw("Invalid message length", "conn_id", c.id, "length", length)
				c.handleFailure(fmt.Errorf("invalid message length: %d", length))
				return
			}

			// Read full message
			message := make([]byte, length)
			copy(message[:20], header)

			if length > 20 {
				if err := c.readFull(conn, message[20:]); err != nil {
					if c.ctx.Err() == nil {
						logger.Log.Errorw("Failed to read body", "conn_id", c.id, "error", err)
						c.handleFailure(err)
					}
					return
				}
			}

			c.stats.MessagesReceived.Add(1)
			c.stats.BytesReceived.Add(uint64(length))

			// Dispatch message
			if err := c.dispatchMessage(message); err != nil {
				logger.Log.Errorw("Failed to dispatch message", "conn_id", c.id, "error", err)
			}
		}
	}()
}

// readFull reads exactly len(buf) bytes
func (c *Connection) readFull(conn net.Conn, buf []byte) error {
	_, err := io.ReadFull(conn, buf)
	return err
}

// dispatchMessage dispatches a message based on its type
func (c *Connection) dispatchMessage(data []byte) error {
	info, err := ParseMessageHeader(data)
	if err != nil {
		return err
	}

	logger.Log.Debugw("Received message", "conn_id", c.id, "message", info.String())

	// Handle base protocol messages
	if info.IsBaseProtocol() {
		switch info.GetMessageType() {
		case MessageTypeCEA: // CER/CEA (code 257)
			if !info.IsRequest {
				return c.deliverResponse(info.HopByHopID, data)
			}
		case MessageTypeDWR: // DWR/DWA (code 280)
			if info.IsRequest {
				return c.handleDWR(data)
			} else {
				return c.deliverResponse(info.HopByHopID, data)
			}
		}
	}

	// Forward application messages to receive channel
	select {
	case c.recvCh <- data:
	case <-c.ctx.Done():
		return c.ctx.Err()
	default:
		logger.Log.Warn("Receive buffer full, dropping message", "conn_id", c.id)
	}

	return nil
}

// deliverResponse delivers a response to a waiting request
func (c *Connection) deliverResponse(hopByHopID uint32, data []byte) error {
	c.pendingMu.RLock()
	respCh, exists := c.pending[hopByHopID]
	c.pendingMu.RUnlock()

	if !exists {
		logger.Log.Debugw("No pending request for H2H ID", "conn_id", c.id, "hop_by_hop_id", hopByHopID)
		return nil
	}

	select {
	case respCh <- data:
	default:
		logger.Log.Warn("Response channel full for H2H ID", "conn_id", c.id, "hop_by_hop_id", hopByHopID)
	}

	return nil
}

// startWriteLoop starts the message write loop
func (c *Connection) startWriteLoop() {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer logger.Log.Infow("Write loop exited", "conn_id", c.id)

		for {
			select {
			case <-c.ctx.Done():
				return
			case data := <-c.sendCh:
				c.connMu.RLock()
				conn := c.conn
				c.connMu.RUnlock()

				if conn == nil {
					continue
				}

				if err := c.write(conn, data); err != nil {
					if c.ctx.Err() == nil {
						logger.Log.Errorw("Failed to write", "conn_id", c.id, "error", err)
						c.handleFailure(err)
					}
					return
				}

				c.stats.MessagesSent.Add(1)
				c.stats.BytesSent.Add(uint64(len(data)))
			}
		}
	}()
}

// write writes data to the connection
func (c *Connection) write(conn net.Conn, data []byte) error {
	_, err := conn.Write(data)
	return err
}

// startHealthMonitor starts the health monitoring goroutine
func (c *Connection) startHealthMonitor() {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-c.ctx.Done():
				return
			case <-ticker.C:
				// Check for connection timeout in DWR_SENT state
				// This is a safety net in case the DWR timeout handler doesn't fire
				if c.GetState() == StateDWRSent {
					failureCount := c.dwrFailureCount.Load()
					if int(failureCount) >= c.config.MaxDWRFailures {
						timeSinceLastFail := time.Since(c.getDWRLastFailTime())
						// If we've exceeded max failures and it's been more than DWRTimeout since last failure
						// trigger reconnection (safety net)
						if timeSinceLastFail > c.config.DWRTimeout {
							logger.Log.Errorw("Health check failed: DWR failure threshold exceeded",
								"conn_id", c.id,
								"failure_count", failureCount)
							c.handleFailure(fmt.Errorf("health check: exceeded max DWR failures (%d)", c.config.MaxDWRFailures))
							return
						}
					}
				}
			}
		}
	}()
}

// handleFailure handles connection failure
func (c *Connection) handleFailure(err error) {
	logger.Log.Errorw("Handling failure", "conn_id", c.id, "error", err)

	c.stats.SetLastError(err)
	c.setState(StateFailed)
	c.close()

	// Attempt reconnection
	c.reconnect()
}

// reconnect attempts to reconnect with exponential backoff
func (c *Connection) reconnect() {
	backoff := c.config.ReconnectInterval
	attempts := 0

	c.stats.Reconnects.Add(1)

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(backoff):
			attempts++
			logger.Log.Infow("Reconnection attempt", "conn_id", c.id, "attempt", attempts)

			c.setState(StateReconnecting)

			if err := c.connect(); err != nil {
				logger.Log.Warn("Reconnection failed", "conn_id", c.id, "error", err)

				// Exponential backoff
				backoff = time.Duration(float64(backoff) * c.config.ReconnectBackoff)
				if backoff > c.config.MaxReconnectDelay {
					backoff = c.config.MaxReconnectDelay
				}
				continue
			}

			logger.Log.Infow("Reconnection successful", "conn_id", c.id, "attempts", attempts)
			return
		}
	}
}

// close closes the connection
func (c *Connection) close() {
	c.stopWatchdog()

	c.connMu.Lock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.connMu.Unlock()
}

// Close gracefully closes the connection
func (c *Connection) Close() error {
	logger.Log.Infow("Closing connection", "conn_id", c.id)

	c.setState(StateClosed)
	c.cancel()
	c.close()

	// Wait for goroutines to finish
	c.wg.Wait()

	// Close channels
	close(c.sendCh)
	close(c.recvCh)

	return nil
}

// Helper methods

func (c *Connection) nextHopByHopID() uint32 {
	return c.hopByHopID.Add(1)
}

func (c *Connection) nextEndToEndID() uint32 {
	return c.endToEndID.Add(1)
}

func (c *Connection) updateActivity() {
	c.activityMu.Lock()
	c.lastActivity = time.Now()
	c.activityMu.Unlock()
}

func (c *Connection) getLastActivity() time.Time {
	c.activityMu.RLock()
	defer c.activityMu.RUnlock()
	return c.lastActivity
}

func (c *Connection) getDWRLastFailTime() time.Time {
	c.dwrLastFailTimeMu.RLock()
	defer c.dwrLastFailTimeMu.RUnlock()
	return c.dwrLastFailTime
}

func (c *Connection) getLocalAddr() net.IP {
	c.connMu.RLock()
	defer c.connMu.RUnlock()

	if c.conn != nil {
		if addr, ok := c.conn.LocalAddr().(*net.TCPAddr); ok {
			return addr.IP
		}
	}
	return nil
}

func (c *Connection) registerPending(hopByHopID uint32, ch chan []byte) {
	c.pendingMu.Lock()
	c.pending[hopByHopID] = ch
	c.pendingMu.Unlock()
}

func (c *Connection) unregisterPending(hopByHopID uint32) {
	c.pendingMu.Lock()
	delete(c.pending, hopByHopID)
	c.pendingMu.Unlock()
}

// GetStats returns connection statistics
func (c *Connection) GetStats() ConnectionStats {
	return c.stats
}
