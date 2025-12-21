package gateway_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/commands/base"
	"github.com/hsdfat/diam-gw/commands/s13"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

// DRASimulator simulates a DRA server for testing using the server package
type DRASimulator struct {
	server  *server.Server
	address string
	ctx     context.Context
	cancel  context.CancelFunc
	logger  logger.Logger

	// Active connections (gateway connections)
	connections   map[string]server.Conn
	connectionsMu sync.RWMutex

	// Statistics
	requestCount  atomic.Uint64
	responseCount atomic.Uint64
	errorCount    atomic.Uint64

	// Configuration
	originHost  string
	originRealm string
}

// NewDRASimulator creates a new DRA simulator
func NewDRASimulator(ctx context.Context, address string, log logger.Logger) *DRASimulator {
	ctx, cancel := context.WithCancel(ctx)

	return &DRASimulator{
		address:     address,
		ctx:         ctx,
		cancel:      cancel,
		logger:      log,
		connections: make(map[string]server.Conn),
		originHost:  "dra-simulator.example.com",
		originRealm: "example.com",
	}
}

// Start starts the DRA simulator
func (d *DRASimulator) Start() error {
	// Create server configuration
	config := &server.ServerConfig{
		ListenAddress:  d.address,
		MaxConnections: 1000,
		ConnectionConfig: &server.ConnectionConfig{
			OriginHost:       d.originHost,
			OriginRealm:      d.originRealm,
			ProductName:      "DRA-Simulator",
			VendorID:         10415,
			ReadTimeout:      30 * time.Second,
			WriteTimeout:     10 * time.Second,
			WatchdogInterval: 30 * time.Second,
			WatchdogTimeout:  10 * time.Second,
			MaxMessageSize:   65535,
			SendChannelSize:  1000,
			RecvChannelSize:  1000,
			HandleWatchdog:   true, // Handle DWR/DWA locally
		},
		RecvChannelSize: 1000,
	}

	// Create server
	d.server = server.NewServer(config, d.logger)

	// Register base protocol handlers
	d.registerHandlers()

	// Start message processor
	go d.processMessages()

	// Start server in background
	go func() {
		if err := d.server.Start(); err != nil {
			d.logger.Errorw("DRA simulator server error", "error", err)
		}
	}()

	// Wait for server to be ready
	time.Sleep(100 * time.Millisecond)

	d.logger.Infow("DRA simulator started", "address", d.address)
	return nil
}

// Stop stops the DRA simulator
func (d *DRASimulator) Stop() error {
	d.logger.Infow("Stopping DRA simulator")
	d.cancel()

	if d.server != nil {
		return d.server.Stop()
	}

	return nil
}

// processMessages processes incoming messages
func (d *DRASimulator) processMessages() {
	recvChan := d.server.Receive()

	for {
		select {
		case <-d.ctx.Done():
			return
		case msgCtx, ok := <-recvChan:
			if !ok {
				return
			}

			d.requestCount.Add(1)

			// Handle the message
			if err := d.handleMessage(msgCtx); err != nil {
				d.logger.Errorw("Failed to handle message", "error", err)
				d.errorCount.Add(1)
			}
		}
	}
}

// handleMessage handles a received message
func (d *DRASimulator) handleMessage(msgCtx *server.MessageContext) error {
	msg := msgCtx.Message
	conn := msgCtx.Connection

	if len(msg) < 20 {
		return fmt.Errorf("message too short")
	}

	// Parse command code
	cmdCode := binary.BigEndian.Uint32([]byte{0, msg[5], msg[6], msg[7]})

	// Check if request (R flag)
	isRequest := (msg[4] & 0x80) != 0

	d.logger.Debugw("DRA received message",
		"cmd_code", cmdCode,
		"is_request", isRequest,
		"length", len(msg))

	if !isRequest {
		// It's a response, ignore (shouldn't happen in DRA simulator)
		return nil
	}

	// Handle base protocol - server handles CER/CEA, DWR/DWA automatically
	// We only need to handle application messages

	// Check if it's base protocol (Application-ID = 0 and specific command codes)
	appID := binary.BigEndian.Uint32(msg[8:12])
	if appID == 0 && (cmdCode == 257 || cmdCode == 280 || cmdCode == 282) {
		// Base protocol (CER/DWR/DPR) - server handles these automatically
		// The server.Connection class processes these in its internal message loop
		// and sends appropriate answers (CEA/DWA/DPA)
		d.logger.Debugw("Base protocol message handled by server", "cmd_code", cmdCode)
		return nil
	}

	// Application request - send answer
	return d.handleApplicationRequest(conn, msg)
}

// handleApplicationRequest handles application requests (e.g., ULR, AIR)
func (d *DRASimulator) handleApplicationRequest(conn *server.Connection, req []byte) error {
	// Create response by copying request and clearing Request flag
	resp := make([]byte, len(req))
	copy(resp, req)

	// Clear Request flag
	resp[4] &= ^byte(0x80)

	d.logger.Debugw("DRA sending application answer",
		"cmd_code", binary.BigEndian.Uint32([]byte{0, resp[5], resp[6], resp[7]}),
		"h2h", extractHopByHopID(resp))

	// Send response
	if err := conn.Send(resp); err != nil {
		return fmt.Errorf("failed to send response: %w", err)
	}

	d.responseCount.Add(1)
	return nil
}

// GetRequestCount returns total requests received
func (d *DRASimulator) GetRequestCount() uint64 {
	return d.server.GetStats().MessagesReceived
}

// GetResponseCount returns total responses sent
func (d *DRASimulator) GetResponseCount() uint64 {
	return d.server.GetStats().MessagesSent
}

// GetErrorCount returns total errors
func (d *DRASimulator) GetErrorCount() uint64 {
	return d.server.GetStats().Errors
}

// SendMICR sends a ME-Identity-Check-Request to a connected client
// This simulates DRA initiating an S13 request to the gateway
func (d *DRASimulator) SendMICR(imei string) error {
	// Get active connections
	d.connectionsMu.RLock()
	connCount := len(d.connections)
	var conn server.Conn
	for _, c := range d.connections {
		conn = c
		break // Use first connection
	}
	d.connectionsMu.RUnlock()

	d.logger.Infow("Checking for active gateway connections", "count", connCount)

	if conn == nil {
		return fmt.Errorf("no active gateway connections")
	}

	d.logger.Infow("Using gateway connection", "remote", conn.RemoteAddr().String())

	d.logger.Infow("Sending ME-Identity-Check-Request (MICR)",
		"imei", imei,
		"remote", conn.RemoteAddr().String())

	// Create MICR message
	// Command Code: 324 (ME-Identity-Check-Request)
	// Application ID: 16777252 (S13)
	h2h := uint32(time.Now().Unix()) // Use timestamp as H2H ID
	e2e := h2h

	// Create simple MICR with IMEI in body
	tmp := models_base.UTF8String(imei)
	micr := s13.NewMEIdentityCheckRequest()
	micr.SessionId = models_base.UTF8String("client.example.com;1234567890;1")
	micr.AuthSessionState = models_base.Enumerated(1)
	micr.OriginHost = models_base.DiameterIdentity("client.example.com")
	micr.OriginRealm = models_base.DiameterIdentity("client.example.com")
	micr.DestinationRealm = models_base.DiameterIdentity("server.example.com")
	micr.TerminalInformation = &s13.TerminalInformation{
		Imei:            &tmp,
	}
	data, _ := micr.Marshal()

	// Send MICR
	if _, err := conn.Write(data); err != nil {
		d.logger.Errorw("Failed to send MICR", "error", err)
		return fmt.Errorf("failed to send MICR: %w", err)
	}

	d.logger.Infow("MICR sent successfully", "h2h", h2h, "e2e", e2e)
	return nil
}

// registerHandlers registers base protocol and application handlers
func (d *DRASimulator) registerHandlers() {
	// Register CER handler (Command Code 257, Base Protocol Interface 0)
	d.server.HandleFunc(server.Command{Interface: 0, Code: 257, Request: true}, d.handleCER)

	// Register DWR handler (Command Code 280, Base Protocol Interface 0)
	d.server.HandleFunc(server.Command{Interface: 0, Code: 280, Request: true}, d.handleDWR)

	// Register DPR handler (Command Code 282, Base Protocol Interface 0)
	d.server.HandleFunc(server.Command{Interface: 0, Code: 282, Request: true}, d.handleDPR)
}

// handleCER handles Capabilities-Exchange-Request
func (d *DRASimulator) handleCER(msg *server.Message, conn server.Conn) {
	d.logger.Debugw("Handling CER",
		"remote", conn.RemoteAddr().String(),
		"length", msg.Length)

	// Unmarshal CER
	cer := &base.CapabilitiesExchangeRequest{}
	fullMsg := append(msg.Header, msg.Body...)
	if err := cer.Unmarshal(fullMsg); err != nil {
		d.logger.Errorw("Failed to unmarshal CER", "error", err)
		return
	}

	// Create CEA
	cea := base.NewCapabilitiesExchangeAnswer()
	cea.Header.HopByHopID = cer.Header.HopByHopID // Match CER's H2H ID
	cea.Header.EndToEndID = cer.Header.EndToEndID // Match CER's E2E ID
	cea.ResultCode = models_base.Unsigned32(2001) // DIAMETER_SUCCESS
	cea.OriginHost = models_base.DiameterIdentity(d.originHost)
	cea.OriginRealm = models_base.DiameterIdentity(d.originRealm)
	cea.ProductName = models_base.UTF8String("DRA-Simulator")
	cea.VendorId = models_base.Unsigned32(10415)

	// Set local IP address
	if localAddr, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		cea.HostIpAddress = []models_base.Address{models_base.Address(localAddr.IP)}
	}

	// Copy supported application IDs from CER
	cea.AuthApplicationId = cer.AuthApplicationId
	cea.AcctApplicationId = cer.AcctApplicationId
	cea.VendorSpecificApplicationId = cer.VendorSpecificApplicationId

	// Marshal CEA
	ceaBytes, err := cea.Marshal()
	if err != nil {
		d.logger.Errorw("Failed to marshal CEA", "error", err)
		return
	}

	// Send CEA
	if _, err := conn.Write(ceaBytes); err != nil {
		d.logger.Errorw("Failed to send CEA", "error", err)
	} else {
		d.logger.Debugw("Sent CEA")

		// Store the connection for later use (e.g., sending MICR)
		remoteAddr := conn.RemoteAddr().String()
		d.connectionsMu.Lock()
		d.connections[remoteAddr] = conn
		d.connectionsMu.Unlock()
		d.logger.Infow("Gateway connection established", "remote", remoteAddr)
	}
}

// handleDWR handles Device-Watchdog-Request
func (d *DRASimulator) handleDWR(msg *server.Message, conn server.Conn) {
	d.logger.Debugw("Handling DWR")

	// Reconstruct full message from header + body
	fullMsg := append(msg.Header, msg.Body...)

	// Create DWA using helper
	dwa, err := createDWA(fullMsg, d.originHost, d.originRealm)
	if err != nil {
		d.logger.Errorw("Failed to create DWA", "error", err)
		return
	}

	// Send DWA
	if _, err := conn.Write(dwa); err != nil {
		d.logger.Errorw("Failed to send DWA", "error", err)
	} else {
		d.logger.Debugw("Sent DWA")
	}
}

// handleDPR handles Disconnect-Peer-Request
func (d *DRASimulator) handleDPR(msg *server.Message, conn server.Conn) {
	d.logger.Infow("Handling DPR")

	// In a real implementation, we would:
	// 1. Send DPA
	// 2. Close the connection gracefully
	// For the simulator, we'll just acknowledge
	d.logger.Debugw("DPR acknowledged (not implemented)")
}
