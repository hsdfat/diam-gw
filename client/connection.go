package client

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/commands/base"
	"github.com/hsdfat/diam-gw/commands/s13"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/connection"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

type Message = connection.Message
type DiamConnectionInfo = connection.DiamConnectionInfo

// Connection represents a single Diameter connection to a DRA
type Connection struct {
	id     string
	config *DRAConfig

	// TCP connection
	conn   connection.Conn
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
	dwrTicker         *time.Ticker
	dwrStop           chan struct{}
	dwrStopMu         sync.Mutex
	dwrFailureCount   atomic.Int32
	dwrLastFailTime   time.Time
	dwrLastFailTimeMu sync.RWMutex

	// Message channels
	sendCh chan []byte
	recvCh chan DiamConnectionInfo

	// Pending responses
	pendingMu sync.RWMutex
	pending   map[uint32]chan []byte // hop-by-hop ID -> response channel

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	reconnectDisabled bool

	// Failure callback
	onFailure   func(error)
	onFailureMu sync.RWMutex

	// Statistics
	stats  ConnectionStats
	logger logger.Logger
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

func (c *Connection) DisableReconnect() {
	c.reconnectDisabled = true
}

// SetOnFailure sets the failure callback
// The callback will be invoked when the connection fails (e.g., disconnect, DWR timeout)
// This allows the pool or other managers to react immediately to failures
func (c *Connection) SetOnFailure(callback func(error)) {
	c.onFailureMu.Lock()
	defer c.onFailureMu.Unlock()
	c.onFailure = callback
}

// callOnFailure safely calls the failure callback if set
func (c *Connection) callOnFailure(err error) {
	c.onFailureMu.RLock()
	callback := c.onFailure
	c.onFailureMu.RUnlock()

	if callback != nil {
		// Call in goroutine to avoid blocking connection cleanup
		go callback(err)
	}
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
func NewConnection(ctx context.Context, id string, config *DRAConfig, log logger.Logger) *Connection {
	ctx, cancel := context.WithCancel(ctx)

	c := &Connection{
		id:      id,
		config:  config,
		sendCh:  make(chan []byte, config.SendBufferSize),
		recvCh:  make(chan DiamConnectionInfo, config.RecvBufferSize),
		pending: make(map[uint32]chan []byte),
		ctx:     ctx,
		cancel:  cancel,
		logger:  log,
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
	c.logger.Infow("State transition", "conn_id", c.id, "old_state", oldState.String(), "new_state", newState.String())
}

// IsActive returns true if the connection is active
func (c *Connection) IsActive() bool {
	return c.GetState().IsActive()
}

// Start initiates the connection
func (c *Connection) Start() error {
	c.logger.Infow("Starting connection", "conn_id", c.id, "host", c.config.Host, "port", c.config.Port)

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
	c.conn = connection.NewConn(conn, connection.DefaultConnectionConfig())
	c.connMu.Unlock()

	c.logger.Infow("TCP connection established", "conn_id", c.id)

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

	c.logger.Infow("Connection established successfully", "conn_id", c.id)
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
	c.logger.Infow("Performing CER/CEA handshake", "conn_id", c.id)

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

	// Set Auth-Application-IDs from config (default to S13 if not specified)
	if len(c.config.AuthAppIDs) > 0 {
		cer.AuthApplicationId = make([]models_base.Unsigned32, len(c.config.AuthAppIDs))
		for i, id := range c.config.AuthAppIDs {
			cer.AuthApplicationId[i] = models_base.Unsigned32(id)
		}
	} else {
		// Default to S13
		cer.AuthApplicationId = []models_base.Unsigned32{
			models_base.Unsigned32(s13.S13_APPLICATION_ID),
		}
	}

	// Set Acct-Application-IDs from config if specified
	if len(c.config.AcctAppIDs) > 0 {
		cer.AcctApplicationId = make([]models_base.Unsigned32, len(c.config.AcctAppIDs))
		for i, id := range c.config.AcctAppIDs {
			cer.AcctApplicationId[i] = models_base.Unsigned32(id)
		}
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

	c.logger.Infow("CEA received successfully", "conn_id", c.id, "result_code", resultCode.String())
	return nil
}

// startWatchdog starts the watchdog timer
func (c *Connection) startWatchdog() {
	c.dwrStopMu.Lock()
	defer c.dwrStopMu.Unlock()

	if c.dwrTicker != nil {
		return // Already running
	}

	ticker := time.NewTicker(c.config.DWRInterval)
	c.dwrTicker = ticker
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
			case <-ticker.C:
				if err := c.sendDWR(); err != nil {
					c.logger.Errorw("Failed to send DWR", "conn_id", c.id, "error", err)
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

	c.logger.Debugw("Sending DWR", "conn_id", c.id)

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
				c.logger.Errorw("Failed to handle DWA", "conn_id", c.id, "error", err)
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

	c.logger.Debugw("DWA received successfully", "conn_id", c.id)
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

	c.logger.Warnw("DWR failure",
		"conn_id", c.id,
		"error", err,
		"failure_count", failureCount,
		"max_failures", c.config.MaxDWRFailures)

	// Check if we've exceeded the failure threshold
	if int(failureCount) >= c.config.MaxDWRFailures {
		c.logger.Errorw("DWR failure threshold exceeded, triggering reconnection",
			"conn_id", c.id,
			"failure_count", failureCount,
			"max_failures", c.config.MaxDWRFailures)
		c.handleFailure(fmt.Errorf("exceeded max DWR failures (%d): %w", c.config.MaxDWRFailures, err))
	}
}

// handleDWR processes Device-Watchdog-Request (from peer)
func (c *Connection) handleDWR(data *Message) error {
	dwr := &base.DeviceWatchdogRequest{}
	if err := dwr.Unmarshal(append(data.Header, data.Body...)); err != nil {
		return fmt.Errorf("failed to unmarshal DWR: %w", err)
	}

	c.logger.Debugw("DWR received, sending DWA", "conn_id", c.id)

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
func (c *Connection) Receive() <-chan DiamConnectionInfo {
	return c.recvCh
}

// startReadLoop starts the message read loop
func (c *Connection) startReadLoop() {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer c.logger.Infow("Read loop exited", "conn_id", c.id)

		for {
			select {
			case <-c.ctx.Done():
				return
			case <-c.conn.(connection.CloseNotifier).CloseNotify():
				c.handleFailure(fmt.Errorf("connect closed"))
			default:
			}

			c.connMu.RLock()
			conn := c.conn
			c.connMu.RUnlock()

			if conn == nil {
				return
			}

			message, err := connection.ReadMessage(conn.Connection())
			if err != nil {
				c.handleFailure(err)
				return
			}
			c.stats.MessagesReceived.Add(1)
			c.stats.BytesReceived.Add(uint64(message.Length))

			// Dispatch message
			if err := c.dispatchMessage(message); err != nil {
				c.logger.Errorw("Failed to dispatch message", "conn_id", c.id, "error", err)
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
func (c *Connection) dispatchMessage(data *Message) error {
	info, err := ParseMessageHeader(data.Header)
	if err != nil {
		return err
	}

	c.logger.Debugw("Received message", "conn_id", c.id, "message", info.String())

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
	case c.recvCh <- DiamConnectionInfo{
		Message:  data,
		DiamConn: c.conn,
	}:
	case <-c.ctx.Done():
		return c.ctx.Err()
	default:
		c.logger.Warn("Receive buffer full, dropping message", "conn_id", c.id)
	}

	return nil
}

// deliverResponse delivers a response to a waiting request
func (c *Connection) deliverResponse(hopByHopID uint32, data *Message) error {
	c.pendingMu.RLock()
	respCh, exists := c.pending[hopByHopID]
	c.pendingMu.RUnlock()

	if !exists {
		c.logger.Debugw("No pending request for H2H ID", "conn_id", c.id, "hop_by_hop_id", hopByHopID)
		return nil
	}

	select {
	case respCh <- append(data.Header, data.Body...):
	default:
		c.logger.Warn("Response channel full for H2H ID", "conn_id", c.id, "hop_by_hop_id", hopByHopID)
	}

	return nil
}

// startWriteLoop starts the message write loop
func (c *Connection) startWriteLoop() {
	c.wg.Add(8)
	for range 8 {
		go func() {
			defer c.wg.Done()
			defer c.logger.Infow("Write loop exited", "conn_id", c.id)

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

					if _, err := conn.Write(data); err != nil {
						if c.ctx.Err() == nil {
							c.logger.Errorw("Failed to write", "conn_id", c.id, "error", err)
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
							c.logger.Errorw("Health check failed: DWR failure threshold exceeded",
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
	c.logger.Errorw("Handling failure", "conn_id", c.id, "error", err, "reconnect", !c.reconnectDisabled)

	c.stats.SetLastError(err)
	c.setState(StateFailed)
	c.close()

	// Notify pool or other managers immediately
	c.callOnFailure(err)

	// Attempt reconnection
	if !c.reconnectDisabled {
		c.reconnect()
	}
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
			c.logger.Infow("Reconnection attempt", "conn_id", c.id, "attempt", attempts)

			c.setState(StateReconnecting)

			if err := c.connect(); err != nil {
				c.logger.Warn("Reconnection failed", "conn_id", c.id, "error", err)

				// Exponential backoff
				backoff = time.Duration(float64(backoff) * c.config.ReconnectBackoff)
				if backoff > c.config.MaxReconnectDelay {
					backoff = c.config.MaxReconnectDelay
				}
				continue
			}

			c.logger.Infow("Reconnection successful", "conn_id", c.id, "attempts", attempts)
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
	c.logger.Infow("Closing connection", "conn_id", c.id)

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

// SendWithContext sends a Diameter message with context support
func (c *Connection) SendWithContext(ctx context.Context, data []byte) error {
	if !c.IsActive() {
		return ErrConnectionClosed{ConnectionID: c.id}
	}

	select {
	case c.sendCh <- data:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return c.ctx.Err()
	default:
		return fmt.Errorf("send buffer full")
	}
}

// GetLastActivity returns the time of last activity on this connection
func (c *Connection) GetLastActivity() time.Time {
	c.activityMu.RLock()
	defer c.activityMu.RUnlock()
	return c.lastActivity
}
