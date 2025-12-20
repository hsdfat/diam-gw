package gateway_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/pkg/logger"
)

// LogicAppSimulator simulates a Logic Application client
type LogicAppSimulator struct {
	gatewayAddress string
	conn           net.Conn
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	logger         logger.Logger

	// Channels
	sendChan chan []byte
	recvChan chan []byte

	// Pending requests (H2H ID -> response channel)
	pending   map[uint32]chan []byte
	pendingMu sync.RWMutex

	// Statistics
	requestCount  atomic.Uint64
	responseCount atomic.Uint64
	errorCount    atomic.Uint64

	// Configuration
	originHost  string
	originRealm string

	// Connection state
	connected     atomic.Bool
	nextHopByHopID atomic.Uint32
}

// NewLogicAppSimulator creates a new Logic App simulator
func NewLogicAppSimulator(ctx context.Context, gatewayAddress string, log logger.Logger) *LogicAppSimulator {
	ctx, cancel := context.WithCancel(ctx)

	return &LogicAppSimulator{
		gatewayAddress: gatewayAddress,
		ctx:            ctx,
		cancel:         cancel,
		logger:         log,
		sendChan:       make(chan []byte, 100),
		recvChan:       make(chan []byte, 100),
		pending:        make(map[uint32]chan []byte),
		originHost:     "logicapp-simulator.example.com",
		originRealm:    "example.com",
	}
}

// Connect connects to the gateway
func (l *LogicAppSimulator) Connect() error {
	// Connect to gateway
	conn, err := net.DialTimeout("tcp", l.gatewayAddress, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to gateway: %w", err)
	}

	l.conn = conn
	l.logger.Infow("Connected to gateway", "address", l.gatewayAddress)

	// Start goroutines
	l.wg.Add(2)
	go l.readLoop()
	go l.writeLoop()

	// Perform CER/CEA exchange
	if err := l.performCERExchange(); err != nil {
		l.Close()
		return fmt.Errorf("CER/CEA exchange failed: %w", err)
	}

	l.connected.Store(true)
	l.logger.Infow("CER/CEA exchange completed")

	return nil
}

// Close closes the connection
func (l *LogicAppSimulator) Close() error {
	l.logger.Infow("Closing connection")
	l.cancel()

	if l.conn != nil {
		l.conn.Close()
	}

	l.wg.Wait()
	l.connected.Store(false)
	return nil
}

// readLoop reads messages from connection
func (l *LogicAppSimulator) readLoop() {
	defer l.wg.Done()

	for {
		select {
		case <-l.ctx.Done():
			return
		default:
		}

		// Read message
		msg, err := l.readMessage()
		if err != nil {
			if err != io.EOF {
				l.logger.Errorw("Read error", "error", err)
				l.errorCount.Add(1)
			}
			return
		}

		// Handle message
		l.handleMessage(msg)
	}
}

// writeLoop writes messages to connection
func (l *LogicAppSimulator) writeLoop() {
	defer l.wg.Done()

	for {
		select {
		case <-l.ctx.Done():
			return
		case msg := <-l.sendChan:
			if err := l.writeMessage(msg); err != nil {
				l.logger.Errorw("Write error", "error", err)
				l.errorCount.Add(1)
				return
			}
		}
	}
}

// readMessage reads a Diameter message
func (l *LogicAppSimulator) readMessage() ([]byte, error) {
	// Set read deadline
	l.conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// Read header
	header := make([]byte, 20)
	if _, err := io.ReadFull(l.conn, header); err != nil {
		return nil, err
	}

	// Parse length
	msgLen := binary.BigEndian.Uint32([]byte{0, header[1], header[2], header[3]})
	if msgLen < 20 || msgLen > 65535 {
		return nil, fmt.Errorf("invalid message length: %d", msgLen)
	}

	// Read body
	body := make([]byte, msgLen-20)
	if len(body) > 0 {
		if _, err := io.ReadFull(l.conn, body); err != nil {
			return nil, err
		}
	}

	// Combine
	msg := append(header, body...)
	return msg, nil
}

// writeMessage writes a Diameter message
func (l *LogicAppSimulator) writeMessage(msg []byte) error {
	l.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := l.conn.Write(msg)
	return err
}

// handleMessage handles a received message
func (l *LogicAppSimulator) handleMessage(msg []byte) {
	if len(msg) < 20 {
		l.logger.Errorw("Message too short")
		return
	}

	// Parse command code
	cmdCode := binary.BigEndian.Uint32([]byte{0, msg[5], msg[6], msg[7]})

	// Check if request (R flag)
	isRequest := (msg[4] & 0x80) != 0

	l.logger.Debugw("Received message",
		"cmd_code", cmdCode,
		"is_request", isRequest)

	if isRequest {
		// Handle base protocol requests
		switch cmdCode {
		case 280: // DWR
			l.handleDWR(msg)
		case 282: // DPR
			l.handleDPR(msg)
		default:
			l.logger.Warnw("Unexpected request", "cmd_code", cmdCode)
		}
	} else {
		// It's a response
		l.responseCount.Add(1)

		// Extract H2H ID
		h2h := extractHopByHopID(msg)

		// Deliver to pending request
		l.pendingMu.RLock()
		respChan, exists := l.pending[h2h]
		l.pendingMu.RUnlock()

		if exists {
			select {
			case respChan <- msg:
			case <-time.After(time.Second):
				l.logger.Warnw("Failed to deliver response", "h2h", h2h)
			}
		} else {
			l.logger.Warnw("No pending request for response", "h2h", h2h)
		}
	}
}

// performCERExchange performs CER/CEA exchange
func (l *LogicAppSimulator) performCERExchange() error {
	// Create CER
	cer, err := createCEA(nil, l.originHost, l.originRealm)
	if err != nil {
		// If createCEA fails, create simple CER
		cer = l.createSimpleCER()
	}

	// Set Request flag
	if len(cer) >= 20 {
		cer[4] |= 0x80
	}

	// Generate H2H ID
	h2h := l.nextHopByHopID.Add(1)
	binary.BigEndian.PutUint32(cer[12:16], h2h)

	// Create response channel
	respChan := make(chan []byte, 1)
	l.pendingMu.Lock()
	l.pending[h2h] = respChan
	l.pendingMu.Unlock()

	defer func() {
		l.pendingMu.Lock()
		delete(l.pending, h2h)
		l.pendingMu.Unlock()
	}()

	// Send CER
	if err := l.writeMessage(cer); err != nil {
		return fmt.Errorf("failed to send CER: %w", err)
	}

	l.logger.Debugw("Sent CER")

	// Wait for CEA
	select {
	case cea := <-respChan:
		l.logger.Debugw("Received CEA", "length", len(cea))
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timeout waiting for CEA")
	case <-l.ctx.Done():
		return l.ctx.Err()
	}
}

// createSimpleCER creates a minimal CER for testing
func (l *LogicAppSimulator) createSimpleCER() []byte {
	msg := make([]byte, 20)
	msg[0] = 1 // Version
	binary.BigEndian.PutUint32(msg[0:4], 20) // Length
	msg[0] = 1 // Restore version

	// Command code 257 (CER), Request flag
	binary.BigEndian.PutUint32(msg[4:8], 257)
	msg[4] = 0x80 // Set Request flag

	// Application ID 0
	binary.BigEndian.PutUint32(msg[8:12], 0)

	return msg
}

// handleDWR handles Device-Watchdog-Request
func (l *LogicAppSimulator) handleDWR(dwr []byte) {
	l.logger.Debugw("Handling DWR")

	// Create DWA
	dwa, err := createDWA(dwr, l.originHost, l.originRealm)
	if err != nil {
		l.logger.Errorw("Failed to create DWA", "error", err)
		return
	}

	// Send DWA
	select {
	case l.sendChan <- dwa:
		l.logger.Debugw("Sent DWA")
	case <-l.ctx.Done():
	case <-time.After(time.Second):
		l.logger.Warnw("Timeout sending DWA")
	}
}

// handleDPR handles Disconnect-Peer-Request
func (l *LogicAppSimulator) handleDPR(dpr []byte) {
	l.logger.Infow("Handling DPR")
	// Gateway is asking to disconnect
	l.Close()
}

// SendRequest sends a request and waits for response
func (l *LogicAppSimulator) SendRequest(cmdCode uint32, appID uint32, body []byte) ([]byte, error) {
	if !l.connected.Load() {
		return nil, fmt.Errorf("not connected")
	}

	// Generate H2H and E2E IDs
	h2h := l.nextHopByHopID.Add(1)
	e2e := h2h // Use same for simplicity

	// Create message
	msg := createTestMessage(cmdCode, appID, h2h, e2e, body)

	// Create response channel
	respChan := make(chan []byte, 1)
	l.pendingMu.Lock()
	l.pending[h2h] = respChan
	l.pendingMu.Unlock()

	defer func() {
		l.pendingMu.Lock()
		delete(l.pending, h2h)
		l.pendingMu.Unlock()
	}()

	// Send request
	l.requestCount.Add(1)
	select {
	case l.sendChan <- msg:
	case <-l.ctx.Done():
		return nil, l.ctx.Err()
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("timeout sending request")
	}

	// Wait for response
	select {
	case resp := <-respChan:
		// Verify H2H ID matches
		respH2H := extractHopByHopID(resp)
		if respH2H != h2h {
			return nil, fmt.Errorf("H2H mismatch: sent %d, received %d", h2h, respH2H)
		}
		return resp, nil
	case <-time.After(10 * time.Second):
		l.errorCount.Add(1)
		return nil, fmt.Errorf("timeout waiting for response")
	case <-l.ctx.Done():
		return nil, l.ctx.Err()
	}
}

// GetRequestCount returns total requests sent
func (l *LogicAppSimulator) GetRequestCount() uint64 {
	return l.requestCount.Load()
}

// GetResponseCount returns total responses received
func (l *LogicAppSimulator) GetResponseCount() uint64 {
	return l.responseCount.Load()
}

// GetErrorCount returns total errors
func (l *LogicAppSimulator) GetErrorCount() uint64 {
	return l.errorCount.Load()
}
