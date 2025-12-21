package gateway_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/commands/base"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

// S13EIRSimulator simulates an EIR (Equipment Identity Register) for S13 interface
// It acts as a server that receives ME-Identity-Check-Request (MICR) and responds with MICA
type S13EIRSimulator struct {
	server          *server.Server
	gatewayAddress  string
	listenAddr      string
	ctx             context.Context
	cancel          context.CancelFunc
	logger          logger.Logger

	// Statistics
	requestsReceived atomic.Uint64
	responsesSent    atomic.Uint64
	errorCount       atomic.Uint64

	// Configuration
	originHost  string
	originRealm string
}

// S13EIRStats holds statistics for the EIR simulator
type S13EIRStats struct {
	RequestsReceived uint64
	ResponsesSent    uint64
	Errors           uint64
}

// NewS13EIRSimulator creates a new S13 EIR simulator
func NewS13EIRSimulator(ctx context.Context, gatewayAddress, localAddress string, log logger.Logger) *S13EIRSimulator {
	ctx, cancel := context.WithCancel(ctx)

	return &S13EIRSimulator{
		gatewayAddress: gatewayAddress,
		listenAddr:     localAddress, // Use ephemeral port
		ctx:            ctx,
		cancel:         cancel,
		logger:         log,
		originHost:     "eir-s13.example.com",
		originRealm:    "example.com",
	}
}

// Start starts the EIR simulator server
func (e *S13EIRSimulator) Start() error {
	// Create server configuration
	config := &server.ServerConfig{
		ListenAddress:  e.listenAddr,
		MaxConnections: 10,
		ConnectionConfig: &server.ConnectionConfig{
			OriginHost:       e.originHost,
			OriginRealm:      e.originRealm,
			ProductName:      "S13-EIR-Simulator",
			VendorID:         10415,
			ReadTimeout:      30 * time.Second,
			WriteTimeout:     10 * time.Second,
			WatchdogInterval: 30 * time.Second,
			WatchdogTimeout:  10 * time.Second,
			MaxMessageSize:   65535,
			SendChannelSize:  100,
			RecvChannelSize:  100,
			HandleWatchdog:   true, // Handle DWR/DWA locally
		},
		RecvChannelSize: 100,
	}

	// Create server
	e.server = server.NewServer(config, e.logger)

	// Register handlers
	e.registerHandlers()

	// Start message processor
	go e.processMessages()

	// Start server in background
	go func() {
		if err := e.server.Start(); err != nil {
			e.logger.Errorw("EIR server error", "error", err)
		}
	}()

	// Wait for server to be ready
	time.Sleep(100 * time.Millisecond)

	// Get actual listen address
	if e.server.GetListener() != nil {
		e.listenAddr = e.server.GetListener().Addr().String()
	}

	e.logger.Infow("S13 EIR simulator started", "address", e.listenAddr)
	return nil
}

// Stop stops the EIR simulator
func (e *S13EIRSimulator) Stop() error {
	e.logger.Infow("Stopping S13 EIR simulator")
	e.cancel()

	if e.server != nil {
		return e.server.Stop()
	}

	return nil
}

// registerHandlers registers base protocol and S13 application handlers
func (e *S13EIRSimulator) registerHandlers() {
	// Register base protocol handlers
	e.server.HandleFunc(server.Command{Interface: 0, Code: 257}, e.handleCER)
	e.server.HandleFunc(server.Command{Interface: 0, Code: 280}, e.handleDWR)
	e.server.HandleFunc(server.Command{Interface: 0, Code: 282}, e.handleDPR)

	// Register S13 application handler
	// ME-Identity-Check-Request (Command Code 324, Application ID 16777252)
	e.server.HandleFunc(server.Command{Interface: 16777252, Code: 324}, e.handleMICR)
}

// processMessages processes incoming messages from the server's receive channel
func (e *S13EIRSimulator) processMessages() {
	recvChan := e.server.Receive()

	for {
		select {
		case <-e.ctx.Done():
			return
		case msgCtx, ok := <-recvChan:
			if !ok {
				return
			}

			// Handle application messages that weren't handled by registered handlers
			// (base protocol is handled by registered handlers)
			if err := e.handleApplicationMessage(msgCtx); err != nil {
				e.logger.Errorw("Failed to handle application message", "error", err)
				e.errorCount.Add(1)
			}
		}
	}
}

// handleCER handles Capabilities-Exchange-Request
func (e *S13EIRSimulator) handleCER(msg *server.Message, conn server.Conn) {
	e.logger.Debugw("Handling CER from gateway",
		"remote", conn.RemoteAddr().String(),
		"length", msg.Length)

	// Unmarshal CER
	cer := &base.CapabilitiesExchangeRequest{}
	fullMsg := append(msg.Header, msg.Body...)
	if err := cer.Unmarshal(fullMsg); err != nil {
		e.logger.Errorw("Failed to unmarshal CER", "error", err)
		return
	}

	// Create CEA
	cea := base.NewCapabilitiesExchangeAnswer()
	cea.Header.HopByHopID = cer.Header.HopByHopID
	cea.Header.EndToEndID = cer.Header.EndToEndID
	cea.ResultCode = models_base.Unsigned32(2001) // DIAMETER_SUCCESS
	cea.OriginHost = models_base.DiameterIdentity(e.originHost)
	cea.OriginRealm = models_base.DiameterIdentity(e.originRealm)
	cea.ProductName = models_base.UTF8String("S13-EIR-Simulator")
	cea.VendorId = models_base.Unsigned32(10415)

	// Set local IP address
	if localAddr, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		cea.HostIpAddress = []models_base.Address{models_base.Address(localAddr.IP)}
	}

	// Advertise S13 support (Application ID 16777252)
	cea.AuthApplicationId = []models_base.Unsigned32{16777252}

	// Marshal CEA
	ceaBytes, err := cea.Marshal()
	if err != nil {
		e.logger.Errorw("Failed to marshal CEA", "error", err)
		return
	}

	// Send CEA
	if _, err := conn.Write(ceaBytes); err != nil {
		e.logger.Errorw("Failed to send CEA", "error", err)
	} else {
		e.logger.Debugw("Sent CEA with S13 support")
	}
}

// handleDWR handles Device-Watchdog-Request
func (e *S13EIRSimulator) handleDWR(msg *server.Message, conn server.Conn) {
	e.logger.Debugw("Handling DWR from gateway")

	// Reconstruct full message
	fullMsg := append(msg.Header, msg.Body...)

	// Create DWA using helper
	dwa, err := createDWA(fullMsg, e.originHost, e.originRealm)
	if err != nil {
		e.logger.Errorw("Failed to create DWA", "error", err)
		return
	}

	// Send DWA
	if _, err := conn.Write(dwa); err != nil {
		e.logger.Errorw("Failed to send DWA", "error", err)
	} else {
		e.logger.Debugw("Sent DWA")
	}
}

// handleDPR handles Disconnect-Peer-Request
func (e *S13EIRSimulator) handleDPR(msg *server.Message, conn server.Conn) {
	e.logger.Infow("Handling DPR from gateway")
	// Acknowledge disconnect
}

// handleMICR handles ME-Identity-Check-Request (S13 specific)
func (e *S13EIRSimulator) handleMICR(msg *server.Message, conn server.Conn) {
	e.requestsReceived.Add(1)

	e.logger.Infow("Received ME-Identity-Check-Request (MICR)",
		"remote", conn.RemoteAddr().String(),
		"length", msg.Length)

	// For testing, we'll create a simple MICA (ME-Identity-Check-Answer)
	// In production, this would parse the IMEI and check equipment status

	// Create MICA by copying MICR and clearing request flag
	fullMsg := append(msg.Header, msg.Body...)
	mica := make([]byte, len(fullMsg))
	copy(mica, fullMsg)

	// Clear Request flag (bit 7 of flags byte)
	mica[4] &= ^byte(0x80)

	e.logger.Infow("Sending ME-Identity-Check-Answer (MICA)",
		"h2h", extractHopByHopID(mica),
		"e2e", extractEndToEndID(mica))

	// Send MICA
	if _, err := conn.Write(mica); err != nil {
		e.logger.Errorw("Failed to send MICA", "error", err)
		e.errorCount.Add(1)
	} else {
		e.responsesSent.Add(1)
		e.logger.Debugw("Sent MICA successfully")
	}
}

// handleApplicationMessage handles application messages from receive channel
func (e *S13EIRSimulator) handleApplicationMessage(msgCtx *server.MessageContext) error {
	msg := msgCtx.Message
	conn := msgCtx.Connection

	if len(msg) < 20 {
		return fmt.Errorf("message too short")
	}

	// Parse command code and application ID
	cmdCode := binary.BigEndian.Uint32([]byte{0, msg[5], msg[6], msg[7]})
	appID := binary.BigEndian.Uint32(msg[8:12])
	isRequest := (msg[4] & 0x80) != 0

	e.logger.Debugw("Processing application message",
		"cmd_code", cmdCode,
		"app_id", appID,
		"is_request", isRequest)

	// Base protocol messages are already handled by registered handlers
	if appID == 0 {
		return nil
	}

	// S13 application messages
	if appID == 16777252 && isRequest {
		// MICR is already handled by registered handler
		// This is for any other S13 messages
		return e.handleGenericS13Request(conn, msg)
	}

	return nil
}

// handleGenericS13Request handles generic S13 requests
func (e *S13EIRSimulator) handleGenericS13Request(conn *server.Connection, req []byte) error {
	// Create response by copying request and clearing Request flag
	resp := make([]byte, len(req))
	copy(resp, req)
	resp[4] &= ^byte(0x80)

	// Send response
	if err := conn.Send(resp); err != nil {
		return fmt.Errorf("failed to send response: %w", err)
	}

	e.responsesSent.Add(1)
	return nil
}

// GetStats returns EIR statistics
func (e *S13EIRSimulator) GetStats() S13EIRStats {
	return S13EIRStats{
		RequestsReceived: e.requestsReceived.Load(),
		ResponsesSent:    e.responsesSent.Load(),
		Errors:           e.errorCount.Load(),
	}
}
