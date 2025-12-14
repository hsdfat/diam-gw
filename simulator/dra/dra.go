package main

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

// DRA represents a Diameter Routing Agent simulator
type DRA struct {
	config *Config
	ctx    context.Context
	cancel context.CancelFunc

	// Network
	listener net.Listener
	connMu   sync.RWMutex
	conns    map[string]*ClientConnection

	// Session management
	sessionManager *SessionManager

	// Routing
	router *Router

	// Metrics
	metrics *Metrics

	// Lifecycle
	wg sync.WaitGroup

	// Statistics
	stats DRAStats
}

// DRAStats holds DRA-level statistics
type DRAStats struct {
	TotalConnections  atomic.Uint64
	ActiveConnections atomic.Uint64
	TotalMessages     atomic.Uint64
	MessagesSent      atomic.Uint64
	MessagesReceived  atomic.Uint64
	BytesSent         atomic.Uint64
	BytesReceived     atomic.Uint64
	CERReceived       atomic.Uint64
	CEASent           atomic.Uint64
	DWRReceived       atomic.Uint64
	DWASent           atomic.Uint64
	DWRSent           atomic.Uint64
	DWAReceived       atomic.Uint64
	Errors            atomic.Uint64
	RoutedMessages    atomic.Uint64
}

// NewDRA creates a new DRA simulator instance
func NewDRA(ctx context.Context, config *Config) (*DRA, error) {
	ctx, cancel := context.WithCancel(ctx)

	dra := &DRA{
		config:         config,
		ctx:            ctx,
		cancel:         cancel,
		conns:          make(map[string]*ClientConnection),
		sessionManager: NewSessionManager(),
	}

	// Initialize metrics
	if config.EnableMetrics {
		dra.metrics = NewMetrics(config.MetricsInterval)
	}

	// Initialize router
	if config.EnableRouting {
		router, err := NewRouter(config.BackendServers)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to create router: %w", err)
		}
		dra.router = router
	}

	return dra, nil
}

// Start starts the DRA server
func (d *DRA) Start() error {
	// Create TCP listener
	addr := fmt.Sprintf("%s:%d", d.config.Host, d.config.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	d.listener = listener
	logger.Log.Infow("DRA listening", "address", addr)

	// Start accept loop
	d.wg.Add(1)
	go d.acceptLoop()

	// Start metrics reporting
	if d.config.EnableMetrics {
		d.wg.Add(1)
		go d.metricsLoop()
	}

	// Start session cleanup
	d.wg.Add(1)
	go d.sessionCleanupLoop()

	return nil
}

// acceptLoop accepts incoming connections
func (d *DRA) acceptLoop() {
	defer d.wg.Done()

	for {
		select {
		case <-d.ctx.Done():
			return
		default:
		}

		conn, err := d.listener.Accept()
		if err != nil {
			if d.ctx.Err() != nil {
				return
			}
			logger.Log.Errorw("Accept error", "error", err)
			d.stats.Errors.Add(1)
			continue
		}

		// Check connection limit
		if int(d.stats.ActiveConnections.Load()) >= d.config.MaxConnections {
			logger.Log.Warn("Max connections reached, rejecting connection",
				"max", d.config.MaxConnections,
				"remote", conn.RemoteAddr().String())
			conn.Close()
			d.stats.Errors.Add(1)
			continue
		}

		// Handle connection
		d.handleConnection(conn)
	}
}

// handleConnection handles a new client connection
func (d *DRA) handleConnection(conn net.Conn) {
	connID := fmt.Sprintf("%s-%d", conn.RemoteAddr().String(), time.Now().UnixNano())

	clientConn := &ClientConnection{
		id:     connID,
		conn:   conn,
		dra:    d,
		sendCh: make(chan []byte, 100),
	}

	d.connMu.Lock()
	d.conns[connID] = clientConn
	d.connMu.Unlock()

	d.stats.TotalConnections.Add(1)
	d.stats.ActiveConnections.Add(1)

	logger.Log.Infow("New connection accepted", "conn_id", connID, "remote", conn.RemoteAddr().String())

	// Start connection handlers
	d.wg.Add(2)
	go clientConn.readLoop()
	go clientConn.writeLoop()
}

// removeConnection removes a connection
func (d *DRA) removeConnection(connID string) {
	d.connMu.Lock()
	delete(d.conns, connID)
	d.connMu.Unlock()

	d.stats.ActiveConnections.Add(^uint64(0)) // Decrement
	logger.Log.Infow("Connection removed", "conn_id", connID)
}

// metricsLoop periodically reports metrics
func (d *DRA) metricsLoop() {
	defer d.wg.Done()

	ticker := time.NewTicker(d.config.MetricsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.reportMetrics()
		}
	}
}

// reportMetrics reports current metrics
func (d *DRA) reportMetrics() {
	logger.Log.Infow("=== DRA Metrics ===")
	logger.Log.Infow("Connections",
		"total", d.stats.TotalConnections.Load(),
		"active", d.stats.ActiveConnections.Load())
	logger.Log.Infow("Messages",
		"total", d.stats.TotalMessages.Load(),
		"received", d.stats.MessagesReceived.Load(),
		"sent", d.stats.MessagesSent.Load(),
		"routed", d.stats.RoutedMessages.Load())
	logger.Log.Infow("Base Protocol",
		"cer_received", d.stats.CERReceived.Load(),
		"cea_sent", d.stats.CEASent.Load(),
		"dwr_received", d.stats.DWRReceived.Load(),
		"dwa_sent", d.stats.DWASent.Load(),
		"dwr_sent", d.stats.DWRSent.Load(),
		"dwa_received", d.stats.DWAReceived.Load())
	logger.Log.Infow("Data",
		"bytes_received", d.stats.BytesReceived.Load(),
		"bytes_sent", d.stats.BytesSent.Load())
	logger.Log.Infow("Sessions",
		"active", d.sessionManager.ActiveSessionCount())
	logger.Log.Infow("Errors", "count", d.stats.Errors.Load())
	logger.Log.Infow("===================")
}

// sessionCleanupLoop periodically cleans up expired sessions
func (d *DRA) sessionCleanupLoop() {
	defer d.wg.Done()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			cleaned := d.sessionManager.CleanupExpiredSessions(30 * time.Minute)
			if cleaned > 0 {
				logger.Log.Infow("Cleaned up expired sessions", "count", cleaned)
			}
		}
	}
}

// Shutdown gracefully shuts down the DRA
func (d *DRA) Shutdown(ctx context.Context) error {
	logger.Log.Infow("Shutting down DRA...")

	// Stop accepting new connections
	if d.listener != nil {
		d.listener.Close()
	}

	// Cancel context
	d.cancel()

	// Close all client connections
	d.connMu.Lock()
	for _, conn := range d.conns {
		conn.Close()
	}
	d.connMu.Unlock()

	// Wait for goroutines with timeout
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Log.Infow("All goroutines stopped")
	case <-ctx.Done():
		logger.Log.Warn("Shutdown timeout, forcing stop")
	}

	// Final metrics report
	if d.config.EnableMetrics {
		d.reportMetrics()
	}

	return nil
}

// ClientConnection represents a connection from a Diameter client
type ClientConnection struct {
	id   string
	conn net.Conn
	dra  *DRA

	// State
	handshakeComplete atomic.Bool
	peerHost          atomic.Value // string
	peerRealm         atomic.Value // string

	// Watchdog
	lastActivity time.Time
	activityMu   sync.RWMutex
	dwrTicker    *time.Ticker
	dwrStop      chan struct{}

	// Channels
	sendCh chan []byte

	// Lifecycle
	closeOnce sync.Once
	closed    atomic.Bool
}

// readLoop reads messages from the connection
func (c *ClientConnection) readLoop() {
	defer c.dra.wg.Done()
	defer c.Close()

	for {
		select {
		case <-c.dra.ctx.Done():
			return
		default:
		}

		// Set read deadline
		c.conn.SetReadDeadline(time.Now().Add(c.dra.config.ReadTimeout))

		// Read header (20 bytes)
		header := make([]byte, 20)
		if err := c.readFull(header); err != nil {
			if c.dra.ctx.Err() == nil && !c.closed.Load() {
				logger.Log.Errorw("Failed to read header", "conn_id", c.id, "error", err)
				c.dra.stats.Errors.Add(1)
			}
			return
		}

		// Parse message length
		length := binary.BigEndian.Uint32([]byte{0, header[1], header[2], header[3]})
		if length < 20 || length > 1<<24 {
			logger.Log.Errorw("Invalid message length", "conn_id", c.id, "length", length)
			c.dra.stats.Errors.Add(1)
			return
		}

		// Read full message
		message := make([]byte, length)
		copy(message[:20], header)

		if length > 20 {
			if err := c.readFull(message[20:]); err != nil {
				if c.dra.ctx.Err() == nil && !c.closed.Load() {
					logger.Log.Errorw("Failed to read body", "conn_id", c.id, "error", err)
					c.dra.stats.Errors.Add(1)
				}
				return
			}
		}

		c.dra.stats.MessagesReceived.Add(1)
		c.dra.stats.BytesReceived.Add(uint64(length))
		c.dra.stats.TotalMessages.Add(1)
		c.updateActivity()

		// Handle message
		if err := c.handleMessage(message); err != nil {
			logger.Log.Errorw("Failed to handle message", "conn_id", c.id, "error", err)
			c.dra.stats.Errors.Add(1)
		}
	}
}

// readFull reads exactly len(buf) bytes
func (c *ClientConnection) readFull(buf []byte) error {
	_, err := io.ReadFull(c.conn, buf)
	return err
}

// handleMessage processes a received message
func (c *ClientConnection) handleMessage(data []byte) error {
	// Parse message header
	if len(data) < 20 {
		return fmt.Errorf("message too short")
	}

	// Extract command code and flags
	cmdCode := binary.BigEndian.Uint32(data[4:8]) & 0x00FFFFFF
	flags := data[4]
	isRequest := (flags & 0x80) != 0

	logger.Log.Debugw("Received message",
		"conn_id", c.id,
		"command_code", cmdCode,
		"is_request", isRequest,
		"length", len(data))

	// Handle based on command code
	switch cmdCode {
	case 257: // CER/CEA
		if isRequest {
			return c.handleCER(data)
		}
	case 280: // DWR/DWA
		if isRequest {
			return c.handleDWR(data)
		} else {
			return c.handleDWA(data)
		}
	case 282: // DPR/DPA
		if isRequest {
			return c.handleDPR(data)
		}
	case 324: // MICR/MICA (S13)
		if !isRequest {
			return c.handleMICA(data)
		}
	default:
		// Application message
		if c.dra.config.EnableRouting && c.dra.router != nil {
			return c.routeMessage(data)
		} else {
			return c.sendErrorResponse(data, 3001) // DIAMETER_COMMAND_UNSUPPORTED
		}
	}

	return nil
}

// handleCER handles Capabilities-Exchange-Request
func (c *ClientConnection) handleCER(data []byte) error {
	c.dra.stats.CERReceived.Add(1)

	cer := &base.CapabilitiesExchangeRequest{}
	if err := cer.Unmarshal(data); err != nil {
		return fmt.Errorf("failed to unmarshal CER: %w", err)
	}

	logger.Log.Infow("CER received",
		"conn_id", c.id,
		"origin_host", cer.OriginHost,
		"origin_realm", cer.OriginRealm)

	// Store peer identity
	c.peerHost.Store(string(cer.OriginHost))
	c.peerRealm.Store(string(cer.OriginRealm))

	// Create CEA
	cea := base.NewCapabilitiesExchangeAnswer()
	cea.ResultCode = models_base.Unsigned32(2001) // DIAMETER_SUCCESS
	cea.OriginHost = models_base.DiameterIdentity(c.dra.config.OriginHost)
	cea.OriginRealm = models_base.DiameterIdentity(c.dra.config.OriginRealm)
	cea.ProductName = models_base.UTF8String(c.dra.config.ProductName)
	cea.VendorId = models_base.Unsigned32(c.dra.config.VendorID)

	// Set local IP
	if localAddr, ok := c.conn.LocalAddr().(*net.TCPAddr); ok {
		cea.HostIpAddress = []models_base.Address{models_base.Address(localAddr.IP)}
	}

	// Copy auth application IDs from request
	if len(cer.AuthApplicationId) > 0 {
		cea.AuthApplicationId = cer.AuthApplicationId
	}

	// Copy identifiers from request
	cea.Header.HopByHopID = cer.Header.HopByHopID
	cea.Header.EndToEndID = cer.Header.EndToEndID

	// Marshal CEA
	ceaData, err := cea.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal CEA: %w", err)
	}

	// Send CEA
	select {
	case c.sendCh <- ceaData:
		c.dra.stats.CEASent.Add(1)
		c.handshakeComplete.Store(true)
		logger.Log.Infow("CEA sent", "conn_id", c.id)

		// Start watchdog after successful handshake
		c.startWatchdog()
	case <-c.dra.ctx.Done():
		return c.dra.ctx.Err()
	}

	return nil
}

// handleDWR handles Device-Watchdog-Request
func (c *ClientConnection) handleDWR(data []byte) error {
	c.dra.stats.DWRReceived.Add(1)

	dwr := &base.DeviceWatchdogRequest{}
	if err := dwr.Unmarshal(data); err != nil {
		return fmt.Errorf("failed to unmarshal DWR: %w", err)
	}

	logger.Log.Debugw("DWR received", "conn_id", c.id)

	// Create DWA
	dwa := base.NewDeviceWatchdogAnswer()
	dwa.ResultCode = models_base.Unsigned32(2001) // DIAMETER_SUCCESS
	dwa.OriginHost = models_base.DiameterIdentity(c.dra.config.OriginHost)
	dwa.OriginRealm = models_base.DiameterIdentity(c.dra.config.OriginRealm)

	// Copy identifiers from request
	dwa.Header.HopByHopID = dwr.Header.HopByHopID
	dwa.Header.EndToEndID = dwr.Header.EndToEndID

	// Marshal DWA
	dwaData, err := dwa.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal DWA: %w", err)
	}

	// Send DWA
	select {
	case c.sendCh <- dwaData:
		c.dra.stats.DWASent.Add(1)
		logger.Log.Debugw("DWA sent", "conn_id", c.id)
	case <-c.dra.ctx.Done():
		return c.dra.ctx.Err()
	}

	return nil
}

// handleDWA handles Device-Watchdog-Answer
func (c *ClientConnection) handleDWA(data []byte) error {
	c.dra.stats.DWAReceived.Add(1)

	dwa := &base.DeviceWatchdogAnswer{}
	if err := dwa.Unmarshal(data); err != nil {
		return fmt.Errorf("failed to unmarshal DWA: %w", err)
	}

	logger.Log.Debugw("DWA received", "conn_id", c.id, "result_code", dwa.ResultCode)
	return nil
}

// handleDPR handles Disconnect-Peer-Request
func (c *ClientConnection) handleDPR(data []byte) error {
	logger.Log.Infow("DPR received, closing connection", "conn_id", c.id)

	// Send DPA
	// (simplified - should create proper DPA)

	// Close connection
	c.Close()
	return nil
}

// sendErrorResponse sends an error response for unsupported messages
func (c *ClientConnection) sendErrorResponse(requestData []byte, resultCode uint32) error {
	// Extract H2H and E2E IDs from request
	hopByHopID := binary.BigEndian.Uint32(requestData[12:16])
	// endToEndID := binary.BigEndian.Uint32(requestData[16:20])

	// Create minimal error response
	// This is a simplified implementation
	logger.Log.Warn("Sending error response",
		"conn_id", c.id,
		"result_code", resultCode,
		"hop_by_hop_id", hopByHopID)

	return nil
}

// routeMessage routes a message to backend servers
func (c *ClientConnection) routeMessage(data []byte) error {
	c.dra.stats.RoutedMessages.Add(1)

	// TODO: Implement actual routing logic
	logger.Log.Debugw("Routing message", "conn_id", c.id, "length", len(data))

	return nil
}

// startWatchdog starts sending periodic DWR
func (c *ClientConnection) startWatchdog() {
	c.dwrTicker = time.NewTicker(c.dra.config.DWRInterval)
	c.dwrStop = make(chan struct{})

	c.dra.wg.Add(1)
	go func() {
		defer c.dra.wg.Done()

		for {
			select {
			case <-c.dra.ctx.Done():
				return
			case <-c.dwrStop:
				return
			case <-c.dwrTicker.C:
				c.sendDWR()
			}
		}
	}()
}

// sendDWR sends a Device-Watchdog-Request
func (c *ClientConnection) sendDWR() error {
	dwr := base.NewDeviceWatchdogRequest()
	dwr.OriginHost = models_base.DiameterIdentity(c.dra.config.OriginHost)
	dwr.OriginRealm = models_base.DiameterIdentity(c.dra.config.OriginRealm)

	// Generate identifiers
	dwr.Header.HopByHopID = uint32(time.Now().UnixNano())
	dwr.Header.EndToEndID = uint32(time.Now().UnixNano())

	dwrData, err := dwr.Marshal()
	if err != nil {
		logger.Log.Errorw("Failed to marshal DWR", "conn_id", c.id, "error", err)
		return err
	}

	select {
	case c.sendCh <- dwrData:
		c.dra.stats.DWRSent.Add(1)
		logger.Log.Debugw("DWR sent", "conn_id", c.id)
	case <-c.dra.ctx.Done():
		return c.dra.ctx.Err()
	default:
		logger.Log.Warn("Send buffer full, dropping DWR", "conn_id", c.id)
	}

	return nil
}

// writeLoop writes messages to the connection
func (c *ClientConnection) writeLoop() {
	defer c.dra.wg.Done()
	defer c.Close()

	for {
		select {
		case <-c.dra.ctx.Done():
			return
		case data := <-c.sendCh:
			// Set write deadline
			c.conn.SetWriteDeadline(time.Now().Add(c.dra.config.WriteTimeout))

			if _, err := c.conn.Write(data); err != nil {
				if c.dra.ctx.Err() == nil && !c.closed.Load() {
					logger.Log.Errorw("Failed to write", "conn_id", c.id, "error", err)
					c.dra.stats.Errors.Add(1)
				}
				return
			}

			c.dra.stats.MessagesSent.Add(1)
			c.dra.stats.BytesSent.Add(uint64(len(data)))
		}
	}
}

// updateActivity updates the last activity timestamp
func (c *ClientConnection) updateActivity() {
	c.activityMu.Lock()
	c.lastActivity = time.Now()
	c.activityMu.Unlock()
}

// Close closes the connection
func (c *ClientConnection) Close() {
	c.closeOnce.Do(func() {
		c.closed.Store(true)

		// Stop watchdog
		if c.dwrTicker != nil {
			c.dwrTicker.Stop()
			close(c.dwrStop)
		}

		// Close connection
		if c.conn != nil {
			c.conn.Close()
		}

		// Remove from DRA
		c.dra.removeConnection(c.id)
	})
}

// handleMICA handles ME-Identity-Check-Answer
func (c *ClientConnection) handleMICA(data []byte) error {
	logger.Log.Infow("MICA received", "conn_id", c.id)

	// Parse MICA
	mica := &s13.MEIdentityCheckAnswer{}
	if err := mica.Unmarshal(data); err != nil {
		logger.Log.Errorw("Failed to unmarshal MICA", "conn_id", c.id, "error", err)
		return fmt.Errorf("failed to unmarshal MICA: %w", err)
	}

	logger.Log.Infow("MICA processed",
		"conn_id", c.id,
		"session_id", mica.SessionId,
		"result_code", mica.ResultCode,
		"equipment_status", func() string {
			if mica.EquipmentStatus != nil {
				return fmt.Sprintf("%d", *mica.EquipmentStatus)
			}
			return "nil"
		}())

	return nil
}

// SendMICR sends a ME-Identity-Check-Request to the client
func (c *ClientConnection) SendMICR(imei string) error {
	if !c.handshakeComplete.Load() {
		return fmt.Errorf("handshake not complete")
	}

	// Get peer identity
	peerHost := c.peerHost.Load()
	if peerHost == nil {
		return fmt.Errorf("peer host not set")
	}
	peerRealm := c.peerRealm.Load()
	if peerRealm == nil {
		return fmt.Errorf("peer realm not set")
	}

	// Create MICR
	micr := s13.NewMEIdentityCheckRequest()
	micr.SessionId = models_base.UTF8String(fmt.Sprintf("%s;%d;%d",
		c.dra.config.OriginHost, time.Now().Unix(), time.Now().UnixNano()))
	micr.AuthSessionState = models_base.Enumerated(1) // NO_STATE_MAINTAINED
	micr.OriginHost = models_base.DiameterIdentity(c.dra.config.OriginHost)
	micr.OriginRealm = models_base.DiameterIdentity(c.dra.config.OriginRealm)

	// Set destination host and realm
	destHost := models_base.DiameterIdentity(peerHost.(string))
	micr.DestinationHost = &destHost
	micr.DestinationRealm = models_base.DiameterIdentity(peerRealm.(string))

	// Set Terminal Information
	imeiValue := models_base.UTF8String(imei)
	micr.TerminalInformation = &s13.TerminalInformation{
		Imei: &imeiValue,
	}

	// Generate H2H and E2E IDs
	micr.Header.HopByHopID = uint32(time.Now().UnixNano() & 0xFFFFFFFF)
	micr.Header.EndToEndID = uint32(time.Now().UnixNano() & 0xFFFFFFFF)

	// Marshal MICR
	micrData, err := micr.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal MICR: %w", err)
	}

	// Send MICR
	select {
	case c.sendCh <- micrData:
		logger.Log.Infow("MICR sent", "conn_id", c.id, "imei", imei, "session_id", micr.SessionId)
		return nil
	case <-c.dra.ctx.Done():
		return c.dra.ctx.Err()
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout sending MICR")
	}
}

// BroadcastMICR sends a MICR to all connected clients
func (d *DRA) BroadcastMICR(imei string) error {
	d.connMu.RLock()
	defer d.connMu.RUnlock()

	if len(d.conns) == 0 {
		return fmt.Errorf("no connected clients")
	}

	var lastErr error
	for _, conn := range d.conns {
		if err := conn.SendMICR(imei); err != nil {
			logger.Log.Errorw("Failed to send MICR to client", "conn_id", conn.id, "error", err)
			lastErr = err
		}
	}

	return lastErr
}

// GetFirstConnection returns the first connected client
func (d *DRA) GetFirstConnection() *ClientConnection {
	d.connMu.RLock()
	defer d.connMu.RUnlock()

	for _, conn := range d.conns {
		if conn.handshakeComplete.Load() {
			return conn
		}
	}
	return nil
}
