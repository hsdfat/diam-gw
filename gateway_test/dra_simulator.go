package gateway_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync/atomic"
	"time"

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
			SendChannelSize:  100,
			RecvChannelSize:  100,
			HandleWatchdog:   true, // Handle DWR/DWA locally
		},
		RecvChannelSize: 1000,
	}

	// Create server
	d.server = server.NewServer(config, d.logger)

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
	return d.requestCount.Load()
}

// GetResponseCount returns total responses sent
func (d *DRASimulator) GetResponseCount() uint64 {
	return d.responseCount.Load()
}

// GetErrorCount returns total errors
func (d *DRASimulator) GetErrorCount() uint64 {
	return d.errorCount.Load()
}
