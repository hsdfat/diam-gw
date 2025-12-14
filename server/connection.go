package server

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsdfat8/diam-gw/client"
	"github.com/hsdfat8/diam-gw/commands/base"
	"github.com/hsdfat8/diam-gw/models_base"
	"github.com/hsdfat8/diam-gw/pkg/logger"
)

// Connection represents a single client connection to the server
type Connection struct {
	conn           net.Conn
	config         *ConnectionConfig
	state          atomic.Int32
	sendChan       chan []byte
	receiveChan    chan []byte
	processChan    chan []byte  // Internal channel for processing
	closeChan      chan struct{}
	closeOnce      sync.Once
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	lastActivity   time.Time
	activityMu     sync.RWMutex
	stats              ConnectionStats
	originHost         string
	originRealm        string
	supportedAppIDs    []uint32 // Application IDs from CER (e.g., S13=16777252, S6a=16777251)
	authAppIDs         []uint32 // Auth-Application-IDs from CER
	acctAppIDs         []uint32 // Acct-Application-IDs from CER
	vendorSpecificApps []uint32 // Vendor-Specific-Application-IDs
	pendingReqs        map[uint32]chan []byte
	pendingMu          sync.RWMutex
	nextHopByHopID     atomic.Uint32
	logger             logger.Logger
}

// ConnectionConfig holds configuration for a server connection
type ConnectionConfig struct {
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	WatchdogInterval  time.Duration
	WatchdogTimeout   time.Duration
	MaxMessageSize    int
	SendChannelSize   int
	RecvChannelSize   int
	OriginHost        string
	OriginRealm       string
	ProductName       string
	VendorID          uint32
	HandleWatchdog    bool // If true, handle DWR locally, otherwise forward
}

// ConnectionStats tracks connection statistics
type ConnectionStats struct {
	MessagesSent     atomic.Uint64
	MessagesReceived atomic.Uint64
	BytesSent        atomic.Uint64
	BytesReceived    atomic.Uint64
	Errors           atomic.Uint64
}

// DefaultConnectionConfig returns default configuration
func DefaultConnectionConfig() *ConnectionConfig {
	return &ConnectionConfig{
		ReadTimeout:      30 * time.Second,
		WriteTimeout:     10 * time.Second,
		WatchdogInterval: 30 * time.Second,
		WatchdogTimeout:  10 * time.Second,
		MaxMessageSize:   65535,
		SendChannelSize:  100,
		RecvChannelSize:  100,
		OriginHost:       "diameter-server.example.com",
		OriginRealm:      "example.com",
		ProductName:      "Diameter Gateway",
		VendorID:         10415,
		HandleWatchdog:   true,
	}
}

// NewConnection creates a new server connection
func NewConnection(conn net.Conn, config *ConnectionConfig, log logger.Logger) *Connection {
	if config == nil {
		config = DefaultConnectionConfig()
	}
	if log == nil {
		log = logger.New("server-connection", "info")
	}

	ctx, cancel := context.WithCancel(context.Background())

	c := &Connection{
		conn:        conn,
		config:      config,
		sendChan:    make(chan []byte, config.SendChannelSize),
		receiveChan: make(chan []byte, config.RecvChannelSize),
		processChan: make(chan []byte, config.RecvChannelSize),
		closeChan:   make(chan struct{}),
		ctx:         ctx,
		cancel:      cancel,
		pendingReqs: make(map[uint32]chan []byte),
		logger:      log,
	}

	c.state.Store(int32(client.StateConnecting))
	c.updateActivity()
	c.nextHopByHopID.Store(1)

	return c
}

// Start begins the connection processing
func (c *Connection) Start() error {
	c.logger.Info("Starting connection from %s", c.conn.RemoteAddr().String())

	// Start goroutines
	c.wg.Add(3)
	go c.readLoop()
	go c.writeLoop()
	go c.processLoop()

	return nil
}

// Close closes the connection
func (c *Connection) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.logger.Info("Closing connection to %s", c.conn.RemoteAddr().String())
		c.state.Store(int32(client.StateClosed))
		close(c.closeChan)
		c.cancel()
		err = c.conn.Close()
		c.wg.Wait()
		c.logger.Info("Connection closed. Stats: sent=%d, recv=%d, errors=%d",
			c.stats.MessagesSent.Load(),
			c.stats.MessagesReceived.Load(),
			c.stats.Errors.Load())
	})
	return err
}

// Send sends a message to the client
func (c *Connection) Send(data []byte) error {
	select {
	case c.sendChan <- data:
		return nil
	case <-c.closeChan:
		return fmt.Errorf("connection closed")
	case <-time.After(5 * time.Second):
		return fmt.Errorf("send timeout")
	}
}

// Receive returns the receive channel
func (c *Connection) Receive() <-chan []byte {
	return c.receiveChan
}

// GetState returns the current connection state
func (c *Connection) GetState() client.ConnectionState {
	return client.ConnectionState(c.state.Load())
}

// GetStats returns connection statistics
func (c *Connection) GetStats() ConnectionStats {
	return c.stats
}

// GetOriginHost returns the client's origin host
func (c *Connection) GetOriginHost() string {
	return c.originHost
}

// GetOriginRealm returns the client's origin realm
func (c *Connection) GetOriginRealm() string {
	return c.originRealm
}

// GetSupportedAppIDs returns all supported application IDs
func (c *Connection) GetSupportedAppIDs() []uint32 {
	return c.supportedAppIDs
}

// GetAuthAppIDs returns auth application IDs
func (c *Connection) GetAuthAppIDs() []uint32 {
	return c.authAppIDs
}

// SupportsInterface checks if connection supports a specific Diameter interface
func (c *Connection) SupportsInterface(appID uint32) bool {
	for _, id := range c.supportedAppIDs {
		if id == appID {
			return true
		}
	}
	return false
}

// GetInterfaceNames returns human-readable interface names
func (c *Connection) GetInterfaceNames() []string {
	names := make([]string, 0, len(c.supportedAppIDs))
	for _, appID := range c.supportedAppIDs {
		switch appID {
		case 16777251:
			names = append(names, "S6a")
		case 16777252:
			names = append(names, "S13")
		case 16777238:
			names = append(names, "Gx")
		case 16777236:
			names = append(names, "Cx")
		case 16777216:
			names = append(names, "Sh")
		default:
			names = append(names, fmt.Sprintf("App-%d", appID))
		}
	}
	return names
}

// updateActivity updates the last activity timestamp
func (c *Connection) updateActivity() {
	c.activityMu.Lock()
	c.lastActivity = time.Now()
	c.activityMu.Unlock()
}

// readLoop reads messages from the TCP connection
func (c *Connection) readLoop() {
	defer c.wg.Done()
	defer c.logger.Info("Read loop exited")

	for {
		select {
		case <-c.closeChan:
			return
		default:
		}

		// Set read deadline
		if err := c.conn.SetReadDeadline(time.Now().Add(c.config.ReadTimeout)); err != nil {
			c.logger.Error("Failed to set read deadline: %v", err)
			c.Close()
			return
		}

		// Read message header (20 bytes)
		header := make([]byte, 20)
		if _, err := io.ReadFull(c.conn, header); err != nil {
			if err != io.EOF && !isClosedError(err) {
				c.logger.Error("Failed to read header: %v", err)
				c.stats.Errors.Add(1)
			}
			c.Close()
			return
		}

		// Parse message length from header
		msgLen := binary.BigEndian.Uint32([]byte{0, header[1], header[2], header[3]})
		if msgLen < 20 || msgLen > uint32(c.config.MaxMessageSize) {
			c.logger.Error("Invalid message length: %d", msgLen)
			c.stats.Errors.Add(1)
			c.Close()
			return
		}

		// Read message body
		body := make([]byte, msgLen-20)
		if len(body) > 0 {
			if _, err := io.ReadFull(c.conn, body); err != nil {
				c.logger.Error("Failed to read body: %v", err)
				c.stats.Errors.Add(1)
				c.Close()
				return
			}
		}

		// Combine header and body
		message := append(header, body...)
		c.stats.MessagesReceived.Add(1)
		c.stats.BytesReceived.Add(uint64(len(message)))
		c.updateActivity()

		// Send to process loop
		select {
		case c.processChan <- message:
		case <-c.closeChan:
			return
		}
	}
}

// writeLoop writes messages to the TCP connection
func (c *Connection) writeLoop() {
	defer c.wg.Done()
	defer c.logger.Info("Write loop exited")

	for {
		select {
		case <-c.closeChan:
			return
		case msg := <-c.sendChan:
			if err := c.conn.SetWriteDeadline(time.Now().Add(c.config.WriteTimeout)); err != nil {
				c.logger.Error("Failed to set write deadline: %v", err)
				c.stats.Errors.Add(1)
				continue
			}

			if _, err := c.conn.Write(msg); err != nil {
				c.logger.Error("Failed to write message: %v", err)
				c.stats.Errors.Add(1)
				c.Close()
				return
			}

			c.stats.MessagesSent.Add(1)
			c.stats.BytesSent.Add(uint64(len(msg)))
			c.updateActivity()
		}
	}
}

// processLoop processes incoming messages
func (c *Connection) processLoop() {
	defer c.wg.Done()
	defer c.logger.Info("Process loop exited")

	for {
		select {
		case <-c.closeChan:
			return
		case msg := <-c.processChan:
			if err := c.handleMessage(msg); err != nil {
				c.logger.Error("Failed to handle message: %v", err)
				c.stats.Errors.Add(1)
			}
		}
	}
}

// handleMessage processes a received message
func (c *Connection) handleMessage(msg []byte) error {
	if len(msg) < 20 {
		return fmt.Errorf("message too short")
	}

	// Parse message header
	msgInfo, err := client.ParseMessageHeader(msg)
	if err != nil {
		return fmt.Errorf("failed to parse header: %w", err)
	}

	c.logger.Debug("Received message: code=%d, flags=%+v, H2H=%d, E2E=%d",
		msgInfo.CommandCode, msgInfo.Flags, msgInfo.HopByHopID, msgInfo.EndToEndID)

	// Handle based on command code
	switch msgInfo.CommandCode {
	case 257: // CER
		return c.handleCER(msg, msgInfo)
	case 280: // DWR
		if c.config.HandleWatchdog {
			return c.handleDWR(msg, msgInfo)
		}
		// Forward to application
		select {
		case c.receiveChan <- msg:
		case <-c.closeChan:
		}
	case 282: // DPR
		return c.handleDPR(msg, msgInfo)
	default:
		// Check if it's a response
		if !msgInfo.Flags.Request {
			// Deliver to pending request
			c.pendingMu.RLock()
			respChan, exists := c.pendingReqs[msgInfo.HopByHopID]
			c.pendingMu.RUnlock()

			if exists {
				select {
				case respChan <- msg:
				case <-time.After(time.Second):
					c.logger.Warn("Failed to deliver response for H2H %d", msgInfo.HopByHopID)
				}
			} else {
				c.logger.Warn("No pending request for H2H %d", msgInfo.HopByHopID)
			}
		} else {
			// Forward request to application
			select {
			case c.receiveChan <- msg:
			case <-c.closeChan:
			}
		}
	}

	return nil
}

// handleCER handles Capabilities Exchange Request
func (c *Connection) handleCER(msg []byte, msgInfo *client.MessageInfo) error {
	c.logger.Info("Handling CER")

	// Parse CER to extract peer identity
	cer := &base.CapabilitiesExchangeRequest{}
	if err := cer.Unmarshal(msg); err != nil {
		c.logger.Error("Failed to unmarshal CER: %v", err)
		return err
	}

	c.originHost = string(cer.OriginHost)
	c.originRealm = string(cer.OriginRealm)

	// Extract supported application IDs
	c.authAppIDs = make([]uint32, 0)
	c.acctAppIDs = make([]uint32, 0)
	c.supportedAppIDs = make([]uint32, 0)

	// Auth-Application-IDs
	for _, appID := range cer.AuthApplicationId {
		c.authAppIDs = append(c.authAppIDs, uint32(appID))
		c.supportedAppIDs = append(c.supportedAppIDs, uint32(appID))
	}

	// Acct-Application-IDs
	for _, appID := range cer.AcctApplicationId {
		c.acctAppIDs = append(c.acctAppIDs, uint32(appID))
		c.supportedAppIDs = append(c.supportedAppIDs, uint32(appID))
	}

	// Vendor-Specific-Application-IDs (contains app IDs in grouped AVP)
	for _, vsAppID := range cer.VendorSpecificApplicationId {
		if vsAppID != nil {
			// Auth app ID from vendor-specific
			if vsAppID.AuthApplicationId != nil {
				id := uint32(*vsAppID.AuthApplicationId)
				c.authAppIDs = append(c.authAppIDs, id)
				c.supportedAppIDs = append(c.supportedAppIDs, id)
			}
			// Acct app ID from vendor-specific
			if vsAppID.AcctApplicationId != nil {
				id := uint32(*vsAppID.AcctApplicationId)
				c.acctAppIDs = append(c.acctAppIDs, id)
				c.supportedAppIDs = append(c.supportedAppIDs, id)
			}
		}
	}

	interfaceNames := c.GetInterfaceNames()
	c.logger.Info("Peer identity: %s@%s, Interfaces: %v",
		c.originHost, c.originRealm, interfaceNames)

	// Create CEA response
	cea := base.NewCapabilitiesExchangeAnswer()
	cea.Header.HopByHopID = msgInfo.HopByHopID
	cea.Header.EndToEndID = msgInfo.EndToEndID
	cea.ResultCode = 2001 // DIAMETER_SUCCESS
	cea.OriginHost = models_base.DiameterIdentity(c.config.OriginHost)
	cea.OriginRealm = models_base.DiameterIdentity(c.config.OriginRealm)
	cea.ProductName = models_base.UTF8String(c.config.ProductName)
	cea.VendorId = models_base.Unsigned32(c.config.VendorID)

	ceaBytes, err := cea.Marshal()
	if err != nil {
		c.logger.Error("Failed to marshal CEA: %v", err)
		return err
	}

	if err := c.Send(ceaBytes); err != nil {
		c.logger.Error("Failed to send CEA: %v", err)
		return err
	}

	c.state.Store(int32(client.StateOpen))
	c.logger.Info("CER/CEA exchange completed successfully")
	return nil
}

// handleDWR handles Device Watchdog Request
func (c *Connection) handleDWR(msg []byte, msgInfo *client.MessageInfo) error {
	c.logger.Debug("Handling DWR")

	// Create DWA response
	dwa := base.NewDeviceWatchdogAnswer()
	dwa.Header.HopByHopID = msgInfo.HopByHopID
	dwa.Header.EndToEndID = msgInfo.EndToEndID
	dwa.ResultCode = 2001
	dwa.OriginHost = models_base.DiameterIdentity(c.config.OriginHost)
	dwa.OriginRealm = models_base.DiameterIdentity(c.config.OriginRealm)

	dwaBytes, err := dwa.Marshal()
	if err != nil {
		return err
	}

	return c.Send(dwaBytes)
}

// handleDPR handles Disconnect Peer Request
func (c *Connection) handleDPR(msg []byte, msgInfo *client.MessageInfo) error {
	c.logger.Info("Handling DPR")

	// Create DPA response
	dpa := base.NewDisconnectPeerAnswer()
	dpa.Header.HopByHopID = msgInfo.HopByHopID
	dpa.Header.EndToEndID = msgInfo.EndToEndID
	dpa.ResultCode = 2001
	dpa.OriginHost = models_base.DiameterIdentity(c.config.OriginHost)
	dpa.OriginRealm = models_base.DiameterIdentity(c.config.OriginRealm)

	dpaBytes, err := dpa.Marshal()
	if err != nil {
		return err
	}

	if err := c.Send(dpaBytes); err != nil {
		return err
	}

	// Close connection after sending DPA
	time.AfterFunc(time.Second, func() {
		c.Close()
	})

	return nil
}

// SendRequest sends a request and waits for response
func (c *Connection) SendRequest(msg []byte, timeout time.Duration) ([]byte, error) {
	if len(msg) < 20 {
		return nil, fmt.Errorf("message too short")
	}

	// Parse H2H ID from message
	h2hID := binary.BigEndian.Uint32(msg[12:16])

	// Create response channel
	respChan := make(chan []byte, 1)
	c.pendingMu.Lock()
	c.pendingReqs[h2hID] = respChan
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pendingReqs, h2hID)
		c.pendingMu.Unlock()
	}()

	// Send message
	if err := c.Send(msg); err != nil {
		return nil, err
	}

	// Wait for response
	select {
	case resp := <-respChan:
		return resp, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("request timeout")
	case <-c.closeChan:
		return nil, fmt.Errorf("connection closed")
	}
}

// isClosedError checks if error is a closed connection error
func isClosedError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return errStr == "use of closed network connection" ||
		errStr == "EOF"
}
