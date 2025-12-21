package gateway_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/commands/s6a"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

// LogicAppSimulator simulates a Logic Application client
// It uses AddressClient for outgoing connections and server package for incoming requests
type LogicAppSimulator struct {
	// Outgoing client (to send messages to gateway)
	client         *client.AddressClient
	gatewayAddress string

	// Incoming server (to receive messages from gateway - e.g., DWR)
	server      *server.Server
	listenAddr  string
	serverReady atomic.Bool

	ctx    context.Context
	cancel context.CancelFunc
	logger logger.Logger

	// Statistics
	requestCount  atomic.Uint64
	responseCount atomic.Uint64
	errorCount    atomic.Uint64

	// Configuration
	originHost     string
	originRealm    string
	nextHopByHopID atomic.Uint32
}

// NewLogicAppSimulator creates a new Logic App simulator
func NewLogicAppSimulator(ctx context.Context, seed uint32, gatewayAddress string, log logger.Logger) *LogicAppSimulator {
	ctx, cancel := context.WithCancel(ctx)

	// Calculate listen address (use a different port than gateway)
	listenAddr := "127.0.0.1:0" // Use ephemeral port

	l := &LogicAppSimulator{
		gatewayAddress: gatewayAddress,
		listenAddr:     listenAddr,
		ctx:            ctx,
		cancel:         cancel,
		logger:         log,
		originHost:     "logicapp-simulator.example.com",
		originRealm:    "example.com",
		nextHopByHopID: atomic.Uint32{},
	}
	l.nextHopByHopID.Store(seed)
	return l
}

// Connect connects to the gateway
func (l *LogicAppSimulator) Connect() error {
	// Start local server first (to handle DWR from gateway)
	if err := l.startServer(); err != nil {
		return fmt.Errorf("failed to start local server: %w", err)
	}

	// Create address client for outgoing messages
	clientConfig := &client.PoolConfig{
		OriginHost:          l.originHost,
		OriginRealm:         l.originRealm,
		ProductName:         "LogicApp-Simulator",
		VendorID:            10415,
		DialTimeout:         5 * time.Second,
		SendTimeout:         10 * time.Second,
		CERTimeout:          5 * time.Second,
		DWRInterval:         30 * time.Second,
		DWRTimeout:          10 * time.Second,
		MaxDWRFailures:      3,
		AuthAppIDs:          []uint32{16777251, 16777252}, // S6a and S13
		SendBufferSize:      1000,
		RecvBufferSize:      1000,
		ReconnectEnabled:    true,
		ReconnectInterval:   2 * time.Second,
		MaxReconnectDelay:   30 * time.Second,
		ReconnectBackoff:    1.5,
		HealthCheckInterval: 10 * time.Second,
	}

	addressClient, err := client.NewAddressClient(l.ctx, clientConfig, l.logger)
	if err != nil {
		l.stopServer()
		return fmt.Errorf("failed to create address client: %w", err)
	}

	l.client = addressClient
	l.logger.Infow("Logic App simulator connected", "gateway", l.gatewayAddress)

	return nil
}

// Close closes the connection
func (l *LogicAppSimulator) Close() error {
	l.logger.Infow("Closing Logic App simulator")
	l.cancel()

	// Close client
	if l.client != nil {
		if err := l.client.Close(); err != nil {
			l.logger.Errorw("Error closing client", "error", err)
		}
	}

	// Stop server
	l.stopServer()

	return nil
}

// startServer starts the local diameter server for incoming requests
func (l *LogicAppSimulator) startServer() error {
	config := &server.ServerConfig{
		ListenAddress:  l.listenAddr,
		MaxConnections: 10,
		ConnectionConfig: &server.ConnectionConfig{
			OriginHost:       l.originHost,
			OriginRealm:      l.originRealm,
			ProductName:      "LogicApp-Simulator",
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

	l.server = server.NewServer(config, l.logger)

	// Start server in background
	go func() {
		if err := l.server.Start(); err != nil {
			l.logger.Errorw("Server error", "error", err)
		}
	}()

	// Wait for server to be ready
	time.Sleep(100 * time.Millisecond)
	l.serverReady.Store(true)

	// Get actual listen address
	if l.server.GetListener() != nil {
		l.listenAddr = l.server.GetListener().Addr().String()
		l.logger.Infow("Logic App server started", "address", l.listenAddr)
	}

	return nil
}

// stopServer stops the local diameter server
func (l *LogicAppSimulator) stopServer() {
	if l.server != nil {
		l.server.Stop()
	}
	l.serverReady.Store(false)
}

// SendRequest sends a request and waits for response
// This method is kept for backward compatibility with tests
func (l *LogicAppSimulator) SendRequest(cmdCode uint32, appID uint32) ([]byte, error) {
	if l.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Generate H2H and E2E IDs
	h2h := l.nextHopByHopID.Add(1)
	e2e := h2h // Use same for simplicity

	// Create message
	msg := s6a.NewUpdateLocationRequest()
	msg.SessionId = models_base.UTF8String("client.example.com;1234567890;1")
	msg.AuthSessionState = models_base.Enumerated(1)
	msg.OriginHost = models_base.DiameterIdentity("client.example.com")
	msg.OriginRealm = models_base.DiameterIdentity("client.example.com")
	msg.DestinationRealm = models_base.DiameterIdentity("server.example.com")
	msg.UserName = models_base.UTF8String("452040000000010")
	msg.RatType = models_base.Enumerated(1)
	msg.UlrFlags = models_base.Unsigned32(1)
	msg.VisitedPlmnId = models_base.OctetString([]byte{0x00, 0xF1, 0x10})
	msg.Header.HopByHopID = h2h
	msg.Header.EndToEndID = e2e
	data, err := msg.Marshal()
	if err != nil {
		return nil, err
	}
	// Create context with timeout

	ctx, cancel := context.WithTimeout(l.ctx, 10*time.Second)
	defer cancel()

	// Send via address client (fire and forget - no response channel)
	l.requestCount.Add(1)
	resp, err := l.client.SendWithContext(ctx, l.gatewayAddress, data)
	if err != nil {
		l.errorCount.Add(1)
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// For the simulator, we need to receive the response
	// Since AddressClient doesn't return responses, we need to implement a response handler
	// For now, create a simple response by echoing the request with response flag
	// In real implementation, this would come from the connection's response channel

	// Wait for response (simplified - in real scenario we'd track pending requests)
	// For testing purposes, create a mock response
	return resp, nil
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
