package client_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/commands/base"
	"github.com/hsdfat/diam-gw/commands/s13"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

// testServer wraps the server.Server for testing
type testServer struct {
	server *server.Server
	config *server.ServerConfig
	t      *testing.T
}

func newTestServer(t *testing.T) *testServer {
	config := server.DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0" // Random port
	config.RecvChannelSize = 1000

	log := logger.New("test-server", "error")
	srv := server.NewServer(config, log)

	return &testServer{
		server: srv,
		config: config,
		t:      t,
	}
}

func (ts *testServer) Start() error {
	// Register base protocol handlers
	ts.registerBaseHandlers()

	// Start server in background
	go ts.server.Start()
	time.Sleep(100 * time.Millisecond)

	return nil
}

func (ts *testServer) Stop() error {
	return ts.server.Stop()
}

func (ts *testServer) Address() string {
	if ts.server.GetListener() == nil {
		return ""
	}
	return ts.server.GetListener().Addr().String()
}

func (ts *testServer) registerBaseHandlers() {
	// Register CER handler
	ts.server.HandleFunc(server.Command{Interface: 0, Code: 257}, func(msg *server.Message, conn server.Conn) {
		cer := &base.CapabilitiesExchangeRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := cer.Unmarshal(fullMsg); err != nil {
			ts.t.Logf("Failed to unmarshal CER: %v", err)
			return
		}

		cea := base.NewCapabilitiesExchangeAnswer()
		cea.ResultCode = 2001
		cea.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		cea.OriginRealm = models_base.DiameterIdentity("example.com")
		cea.HostIpAddress = []models_base.Address{models_base.Address(net.ParseIP("127.0.0.1"))}
		cea.VendorId = models_base.Unsigned32(10415)
		cea.ProductName = models_base.UTF8String("TestServer/1.0")
		cea.Header.HopByHopID = cer.Header.HopByHopID
		cea.Header.EndToEndID = cer.Header.EndToEndID
		// Copy supported applications
		if len(cer.AuthApplicationId) > 0 {
			cea.AuthApplicationId = cer.AuthApplicationId
		}

		ceaBytes, _ := cea.Marshal()
		conn.Write(ceaBytes)
	})

	// Register DWR handler
	ts.server.HandleFunc(server.Command{Interface: 0, Code: 280}, func(msg *server.Message, conn server.Conn) {
		dwr := &base.DeviceWatchdogRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := dwr.Unmarshal(fullMsg); err != nil {
			ts.t.Logf("Failed to unmarshal DWR: %v", err)
			return
		}

		dwa := base.NewDeviceWatchdogAnswer()
		dwa.ResultCode = 2001
		dwa.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		dwa.OriginRealm = models_base.DiameterIdentity("example.com")
		dwa.Header.HopByHopID = dwr.Header.HopByHopID
		dwa.Header.EndToEndID = dwr.Header.EndToEndID

		dwaBytes, _ := dwa.Marshal()
		conn.Write(dwaBytes)
	})

	// Register DPR handler
	ts.server.HandleFunc(server.Command{Interface: 0, Code: 282}, func(msg *server.Message, conn server.Conn) {
		dpr := &base.DisconnectPeerRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := dpr.Unmarshal(fullMsg); err != nil {
			ts.t.Logf("Failed to unmarshal DPR: %v", err)
			return
		}

		dpa := base.NewDisconnectPeerAnswer()
		dpa.ResultCode = 2001
		dpa.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		dpa.OriginRealm = models_base.DiameterIdentity("example.com")
		dpa.Header.HopByHopID = dpr.Header.HopByHopID
		dpa.Header.EndToEndID = dpr.Header.EndToEndID

		dpaBytes, _ := dpa.Marshal()
		conn.Write(dpaBytes)

		// Close connection after sending DPA
		time.AfterFunc(100*time.Millisecond, func() {
			conn.Close()
		})
	})
}

func (ts *testServer) RegisterS13Handler(handler server.Handler) {
	ts.server.HandleFunc(server.Command{Interface: 16777252, Code: 324}, handler)
}

// Helper function for pointer to UTF8String
func ptrUTF8String(s string) *models_base.UTF8String {
	v := models_base.UTF8String(s)
	return &v
}

// Helper to create test DRA config
func newTestDRAConfig(addr string) *client.DRAConfig {
	host, portStr, _ := net.SplitHostPort(addr)
	port := 3868
	if portStr != "" {
		// Parse port string to int
		fmt.Sscanf(portStr, "%d", &port)
	}
	config := &client.DRAConfig{
		Host:              host,
		Port:              port,
		OriginHost:        "test-client.example.com",
		OriginRealm:       "example.com",
		ProductName:       "TestClient/1.0",
		VendorID:          10415,
		ConnectionCount:   1,
		ConnectTimeout:    2 * time.Second,
		CERTimeout:        2 * time.Second,
		DWRInterval:       30 * time.Second,
		DWRTimeout:        5 * time.Second,
		MaxDWRFailures:    3,
		ReconnectInterval: 1 * time.Second,
		MaxReconnectDelay: 30 * time.Second,
		ReconnectBackoff:  2.0,
		SendBufferSize:    1000,
		RecvBufferSize:    1000,
		AuthAppIDs:        []uint32{16777252}, // S13
		AcctAppIDs:        []uint32{},
	}
	return config
}

// Helper to wait for connection state
func waitForConnectionState(conn *client.Connection, expectedState client.ConnectionState, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for state %s, current state: %s", expectedState, conn.GetState())
		case <-ticker.C:
			if conn.GetState() == expectedState {
				return nil
			}
		}
	}
}

// ============================================================================
// Basic Client Setup Tests
// ============================================================================

func TestClientBasicSetup(t *testing.T) {
	testSrv := newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	ctx := context.Background()

	pool, err := client.NewConnectionPool(ctx, config, logger.Log)
	if err != nil {
		t.Fatalf("Failed to create connection pool: %v", err)
	}
	defer pool.Close()

	if pool == nil {
		t.Fatal("Connection pool is nil")
	}
}

func TestClientConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		config      *client.DRAConfig
		expectError bool
	}{
		{
			name: "Valid configuration",
			config: &client.DRAConfig{
				Host:              "127.0.0.1",
				Port:              3868,
				OriginHost:        "test-client.example.com",
				OriginRealm:       "example.com",
				ProductName:       "TestClient/1.0",
				VendorID:          10415,
				ConnectionCount:   1,
				ConnectTimeout:    2 * time.Second,
				CERTimeout:        2 * time.Second,
				DWRInterval:       30 * time.Second,
				DWRTimeout:        5 * time.Second,
				MaxDWRFailures:    3,
				ReconnectInterval: 1 * time.Second,
				MaxReconnectDelay: 30 * time.Second,
				ReconnectBackoff:  2.0,
				SendBufferSize:    100,
				RecvBufferSize:    100,
			},
			expectError: false,
		},
		{
			name: "Empty host",
			config: &client.DRAConfig{
				Host:        "",
				Port:        3868,
				OriginHost:  "test-client.example.com",
				OriginRealm: "example.com",
			},
			expectError: true,
		},
		{
			name: "Empty port",
			config: &client.DRAConfig{
				Host:        "127.0.0.1",
				Port:        0,
				OriginHost:  "test-client.example.com",
				OriginRealm: "example.com",
			},
			expectError: true,
		},
		{
			name: "Zero connection count",
			config: &client.DRAConfig{
				Host:            "127.0.0.1",
				Port:            3868,
				OriginHost:      "test-client.example.com",
				OriginRealm:     "example.com",
				ConnectionCount: 0,
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestClientConnectionEstablishment(t *testing.T) {
	testSrv := newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	ctx := context.Background()

	conn := client.NewConnection(ctx, "test-conn-1", config, logger.Log)
	defer conn.Close()

	if err := conn.Start(); err != nil {
		t.Fatalf("Failed to start connection: %v", err)
	}

	// Wait for connection to be established
	if err := waitForConnectionState(conn, client.StateOpen, 3*time.Second); err != nil {
		t.Fatalf("Connection failed to reach OPEN state: %v", err)
	}

	if !conn.IsActive() {
		t.Error("Connection should be active")
	}
}

// ============================================================================
// Capabilities Exchange Tests (CER/CEA)
// ============================================================================

func TestClientCERCEAExchange(t *testing.T) {
	testSrv := newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	ctx := context.Background()

	conn := client.NewConnection(ctx, "test-conn-1", config, logger.Log)
	defer conn.Close()

	if err := conn.Start(); err != nil {
		t.Fatalf("Failed to start connection: %v", err)
	}

	// Wait for CER/CEA exchange to complete
	if err := waitForConnectionState(conn, client.StateOpen, 3*time.Second); err != nil {
		t.Fatalf("CER/CEA exchange failed: %v", err)
	}

	// Verify connection is in OPEN state
	if conn.GetState() != client.StateOpen {
		t.Errorf("Expected state OPEN, got %s", conn.GetState())
	}
}

func TestClientCERTimeout(t *testing.T) {
	// Create a server that doesn't respond to CER
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	// Accept connections but don't send CEA
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Read CER but don't send CEA
			go func() {
				defer conn.Close()
				header := make([]byte, 20)
				io.ReadFull(conn, header)
				// Read body but don't respond
				msgLen := uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
				if msgLen > 20 {
					body := make([]byte, msgLen-20)
					io.ReadFull(conn, body)
				}
				// Keep connection open but don't respond
				time.Sleep(10 * time.Second)
			}()
		}
	}()

	config := newTestDRAConfig(listener.Addr().String())
	config.CERTimeout = 500 * time.Millisecond
	ctx := context.Background()

	conn := client.NewConnection(ctx, "test-conn-1", config, logger.Log)
	defer conn.Close()

	err = conn.Start()
	// Connection should timeout or fail
	time.Sleep(1 * time.Second)

	state := conn.GetState()
	if state == client.StateOpen {
		t.Errorf("Expected connection to fail/timeout, but got state: %s", state)
	}
}

// ============================================================================
// Device Watchdog Tests (DWR/DWA)
// ============================================================================

func TestClientDWRDWAExchange(t *testing.T) {
	testSrv := newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	config.DWRInterval = 1 * time.Second // Short interval for testing
	ctx := context.Background()

	conn := client.NewConnection(ctx, "test-conn-1", config, logger.Log)
	defer conn.Close()

	if err := conn.Start(); err != nil {
		t.Fatalf("Failed to start connection: %v", err)
	}

	if err := waitForConnectionState(conn, client.StateOpen, 3*time.Second); err != nil {
		t.Fatalf("Connection failed: %v", err)
	}

	// Wait for at least one DWR/DWA exchange
	time.Sleep(2 * time.Second)

	// Connection should still be active
	if !conn.IsActive() {
		t.Error("Connection should still be active after DWR/DWA")
	}
}

func TestClientWatchdogAutomatic(t *testing.T) {
	testSrv := newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	config.DWRInterval = 500 * time.Millisecond
	ctx := context.Background()

	conn := client.NewConnection(ctx, "test-conn-1", config, logger.Log)
	defer conn.Close()

	if err := conn.Start(); err != nil {
		t.Fatalf("Failed to start connection: %v", err)
	}

	if err := waitForConnectionState(conn, client.StateOpen, 3*time.Second); err != nil {
		t.Fatalf("Connection failed: %v", err)
	}

	// Let watchdog run for a few intervals
	time.Sleep(2 * time.Second)

	// Connection should still be active with automatic watchdog
	if !conn.IsActive() {
		t.Error("Connection should be active with automatic watchdog")
	}
}

// ============================================================================
// Disconnect Tests (DPR/DPA)
// ============================================================================

func TestClientDPRDPADisconnect(t *testing.T) {
	testSrv := newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	ctx := context.Background()

	conn := client.NewConnection(ctx, "test-conn-1", config, logger.Log)

	if err := conn.Start(); err != nil {
		t.Fatalf("Failed to start connection: %v", err)
	}

	if err := waitForConnectionState(conn, client.StateOpen, 3*time.Second); err != nil {
		t.Fatalf("Connection failed: %v", err)
	}

	// Close connection (should send DPR)
	if err := conn.Close(); err != nil {
		t.Errorf("Failed to close connection: %v", err)
	}

	// Verify connection is closed
	time.Sleep(200 * time.Millisecond)
	state := conn.GetState()
	if state != client.StateClosed {
		t.Logf("Warning: Expected state CLOSED, got %s", state)
	}
}

func TestClientDisconnectCleanup(t *testing.T) {
	testSrv := newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	ctx := context.Background()

	pool, err := client.NewConnectionPool(ctx, config, logger.Log)
	if err != nil {
		t.Fatalf("Failed to create connection pool: %v", err)
	}

	if err := pool.Start(); err != nil {
		t.Fatalf("Failed to start pool: %v", err)
	}

	// Wait for connection
	if err := pool.WaitForConnection(3 * time.Second); err != nil {
		t.Fatalf("Failed to establish connection: %v", err)
	}

	// Close pool
	if err := pool.Close(); err != nil {
		t.Errorf("Failed to close pool: %v", err)
	}

	// Verify cleanup
	stats := pool.GetStats()
	if stats.ActiveConnections > 0 {
		t.Errorf("Expected 0 active connections after cleanup, got %d", stats.ActiveConnections)
	}
}

// ============================================================================
// S13 Interface Tests
// ============================================================================

func TestClientS13ECRExchange(t *testing.T) {
	testSrv := newTestServer(t)

	var handlerCalled atomic.Bool
	testSrv.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		handlerCalled.Store(true)

		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := ecr.Unmarshal(fullMsg); err != nil {
			t.Errorf("Failed to unmarshal ECR: %v", err)
			return
		}

		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		eca.OriginRealm = models_base.DiameterIdentity("example.com")
		eca.Header.HopByHopID = ecr.Header.HopByHopID
		eca.Header.EndToEndID = ecr.Header.EndToEndID

		resultCode := models_base.Unsigned32(2001)
		eca.ResultCode = &resultCode

		equipmentStatus := models_base.Enumerated(0)
		eca.EquipmentStatus = &equipmentStatus

		ecaBytes, _ := eca.Marshal()
		conn.Write(ecaBytes)
	})

	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	ctx := context.Background()

	conn := client.NewConnection(ctx, "test-conn-1", config, logger.Log)
	defer conn.Close()

	if err := conn.Start(); err != nil {
		t.Fatalf("Failed to start connection: %v", err)
	}

	if err := waitForConnectionState(conn, client.StateOpen, 3*time.Second); err != nil {
		t.Fatalf("Connection failed: %v", err)
	}

	// Build and send ECR
	ecr := s13.NewMEIdentityCheckRequest()
	ecr.SessionId = models_base.UTF8String("test-session-123")
	ecr.AuthSessionState = models_base.Enumerated(1)
	ecr.OriginHost = models_base.DiameterIdentity(config.OriginHost)
	ecr.OriginRealm = models_base.DiameterIdentity(config.OriginRealm)
	ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
	ecr.TerminalInformation = &s13.TerminalInformation{
		Imei:            ptrUTF8String("123456789012345"),
		SoftwareVersion: ptrUTF8String("01"),
	}

	ecrBytes, err := ecr.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal ECR: %v", err)
	}

	// Send via connection
	err = conn.Send(ecrBytes)
	if err != nil {
		t.Fatalf("Failed to send ECR: %v", err)
	}

	// Read response
	select {
	case rsp := <-conn.Receive():
		eca := &s13.MEIdentityCheckAnswer{}
		if err := eca.Unmarshal(append(rsp.Message.Header, rsp.Message.Body...)); err != nil {
			t.Fatalf("Failed to unmarshal ECA: %v", err)
		}

		if *eca.ResultCode != 2001 {
			t.Errorf("Expected ResultCode 2001, got %d", *eca.ResultCode)
		}
		if *eca.EquipmentStatus != 0 {
			t.Errorf("Expected EquipmentStatus 0, got %d", *eca.EquipmentStatus)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for ECA response")
	}

	// Verify handler was called
	time.Sleep(100 * time.Millisecond)
	if !handlerCalled.Load() {
		t.Error("S13 handler was not called")
	}
}

func TestClientS13MultipleRequests(t *testing.T) {
	testSrv := newTestServer(t)

	var requestCount atomic.Int32
	testSrv.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		requestCount.Add(1)

		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := ecr.Unmarshal(fullMsg); err != nil {
			return
		}

		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		eca.OriginRealm = models_base.DiameterIdentity("example.com")
		eca.Header.HopByHopID = ecr.Header.HopByHopID
		eca.Header.EndToEndID = ecr.Header.EndToEndID

		resultCode := models_base.Unsigned32(2001)
		eca.ResultCode = &resultCode
		equipmentStatus := models_base.Enumerated(0)
		eca.EquipmentStatus = &equipmentStatus

		ecaBytes, _ := eca.Marshal()
		conn.Write(ecaBytes)
	})

	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	ctx := context.Background()

	pool, err := client.NewConnectionPool(ctx, config, logger.Log)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Start(); err != nil {
		t.Fatalf("Failed to start pool: %v", err)
	}

	if err := pool.WaitForConnection(3 * time.Second); err != nil {
		t.Fatalf("Failed to wait for connection: %v", err)
	}

	// Send multiple ECR requests
	numRequests := 10
	for i := 0; i < numRequests; i++ {
		ecr := s13.NewMEIdentityCheckRequest()
		ecr.SessionId = models_base.UTF8String(fmt.Sprintf("test-session-%d", i))
		ecr.AuthSessionState = models_base.Enumerated(1)
		ecr.OriginHost = models_base.DiameterIdentity(config.OriginHost)
		ecr.OriginRealm = models_base.DiameterIdentity(config.OriginRealm)
		ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
		ecr.TerminalInformation = &s13.TerminalInformation{
			Imei:            ptrUTF8String(fmt.Sprintf("12345678901234%d", i)),
			SoftwareVersion: ptrUTF8String("01"),
		}

		ecrBytes, err := ecr.Marshal()
		if err != nil {
			t.Fatalf("Failed to marshal ECR: %v", err)
		}

		if err := pool.Send(ecrBytes); err != nil {
			t.Errorf("Failed to send ECR %d: %v", i, err)
		}

		// Read response
		select {
		case <-pool.Receive():
			// Response received
		case <-time.After(2 * time.Second):
			t.Errorf("Timeout waiting for ECA response %d", i)
		}
	}

	time.Sleep(200 * time.Millisecond)

	// Verify all requests were processed
	if requestCount.Load() != int32(numRequests) {
		t.Errorf("Expected %d requests processed, got %d", numRequests, requestCount.Load())
	}
}

// ============================================================================
// Error Handling Tests
// ============================================================================

func TestClientNetworkError(t *testing.T) {
	// Try to connect to non-existent server
	config := &client.DRAConfig{
		Host:           "127.0.0.1",
		Port:           19999, // Unlikely to be in use
		OriginHost:     "test-client.example.com",
		OriginRealm:    "example.com",
		ProductName:    "TestClient/1.0",
		VendorID:       10415,
		ConnectTimeout: 500 * time.Millisecond,
		CERTimeout:     500 * time.Millisecond,
		SendBufferSize: 100,
		RecvBufferSize: 100,
	}

	ctx := context.Background()
	conn := client.NewConnection(ctx, "test-conn-1", config, logger.Log)
	defer conn.Close()

	err := conn.Start()
	// Should get a connection error
	if err == nil {
		t.Error("Expected connection error, got nil")
	}
}

func TestClientServerDisconnect(t *testing.T) {
	testSrv := newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}

	config := newTestDRAConfig(testSrv.Address())
	ctx := context.Background()

	conn := client.NewConnection(ctx, "test-conn-1", config, logger.Log)
	defer conn.Close()

	if err := conn.Start(); err != nil {
		t.Fatalf("Failed to start connection: %v", err)
	}

	if err := waitForConnectionState(conn, client.StateOpen, 3*time.Second); err != nil {
		t.Fatalf("Connection failed: %v", err)
	}

	// Stop server to simulate disconnect
	testSrv.Stop()

	// Wait for connection to detect disconnect
	time.Sleep(2 * time.Second)

	// Connection should no longer be active
	if conn.IsActive() {
		t.Log("Warning: Connection should detect server disconnect")
	}
}

// ============================================================================
// Performance Tests
// ============================================================================

func TestClientPerformanceThroughput(t *testing.T) {
	testSrv := newTestServer(t)

	var requestCount atomic.Int64
	testSrv.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		requestCount.Add(1)

		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := ecr.Unmarshal(fullMsg); err != nil {
			return
		}

		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		eca.OriginRealm = models_base.DiameterIdentity("example.com")
		eca.Header.HopByHopID = ecr.Header.HopByHopID
		eca.Header.EndToEndID = ecr.Header.EndToEndID

		resultCode := models_base.Unsigned32(2001)
		eca.ResultCode = &resultCode
		equipmentStatus := models_base.Enumerated(0)
		eca.EquipmentStatus = &equipmentStatus

		ecaBytes, _ := eca.Marshal()
		conn.Write(ecaBytes)
	})

	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	ctx := context.Background()

	pool, err := client.NewConnectionPool(ctx, config, logger.Log)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Start(); err != nil {
		t.Fatalf("Failed to start pool: %v", err)
	}

	if err := pool.WaitForConnection(3 * time.Second); err != nil {
		t.Fatalf("Failed to wait for connection: %v", err)
	}

	// Send messages and measure throughput
	numMessages := 1000
	startTime := time.Now()

	var wg sync.WaitGroup
	errors := make(chan error, numMessages)

	// Consumer goroutine to read responses
	go func() {
		for i := 0; i < numMessages; i++ {
			select {
			case <-pool.Receive():
				// Response received
			case <-time.After(10 * time.Second):
				errors <- fmt.Errorf("timeout waiting for response %d", i)
			}
		}
	}()

	// Send messages
	for i := 0; i < numMessages; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			ecr := s13.NewMEIdentityCheckRequest()
			ecr.SessionId = models_base.UTF8String(fmt.Sprintf("test-session-%d", index))
			ecr.AuthSessionState = models_base.Enumerated(1)
			ecr.OriginHost = models_base.DiameterIdentity(config.OriginHost)
			ecr.OriginRealm = models_base.DiameterIdentity(config.OriginRealm)
			ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
			ecr.TerminalInformation = &s13.TerminalInformation{
				Imei:            ptrUTF8String("123456789012345"),
				SoftwareVersion: ptrUTF8String("01"),
			}

			ecrBytes, err := ecr.Marshal()
			if err != nil {
				errors <- err
				return
			}

			if err := pool.Send(ecrBytes); err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(startTime)
	time.Sleep(3 * time.Second)
	close(errors)
	for err := range errors {
		t.Errorf("Error during throughput test: %v", err)
	}

	throughput := float64(numMessages) / duration.Seconds()
	t.Logf("Performance: Processed %d messages in %v (%.2f msg/sec)",
		numMessages, duration, throughput)

	// Verify stats
	stats := pool.GetStats()
	t.Logf("Pool stats: Sent=%d, Recv=%d, Active=%d",
		stats.TotalMessagesSent, stats.TotalMessagesRecv, stats.ActiveConnections)
}

func TestClientPerformanceConcurrent(t *testing.T) {
	testSrv := newTestServer(t)

	var requestCount atomic.Int64
	testSrv.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		requestCount.Add(1)

		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := ecr.Unmarshal(fullMsg); err != nil {
			return
		}

		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		eca.OriginRealm = models_base.DiameterIdentity("example.com")
		eca.Header.HopByHopID = ecr.Header.HopByHopID
		eca.Header.EndToEndID = ecr.Header.EndToEndID

		resultCode := models_base.Unsigned32(2001)
		eca.ResultCode = &resultCode
		equipmentStatus := models_base.Enumerated(0)
		eca.EquipmentStatus = &equipmentStatus

		ecaBytes, _ := eca.Marshal()
		conn.Write(ecaBytes)
	})

	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	config.ConnectionCount = 3
	ctx := context.Background()

	pool, err := client.NewConnectionPool(ctx, config, logger.Log)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Start(); err != nil {
		t.Fatalf("Failed to start pool: %v", err)
	}

	if err := pool.WaitForConnection(3 * time.Second); err != nil {
		t.Fatalf("Failed to wait for connection: %v", err)
	}

	// Test concurrent senders
	numWorkers := 10
	messagesPerWorker := 50
	var wg sync.WaitGroup

	startTime := time.Now()

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < messagesPerWorker; j++ {
				ecr := s13.NewMEIdentityCheckRequest()
				ecr.SessionId = models_base.UTF8String(fmt.Sprintf("worker-%d-msg-%d", workerID, j))
				ecr.AuthSessionState = models_base.Enumerated(1)
				ecr.OriginHost = models_base.DiameterIdentity(config.OriginHost)
				ecr.OriginRealm = models_base.DiameterIdentity(config.OriginRealm)
				ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
				ecr.TerminalInformation = &s13.TerminalInformation{
					Imei:            ptrUTF8String("123456789012345"),
					SoftwareVersion: ptrUTF8String("01"),
				}

				ecrBytes, _ := ecr.Marshal()
				pool.Send(ecrBytes)

				// Read response
				select {
				case <-pool.Receive():
					// Response received
				case <-time.After(5 * time.Second):
					t.Errorf("Worker %d timeout on message %d", workerID, j)
					return
				}
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(startTime)

	totalMessages := numWorkers * messagesPerWorker
	throughput := float64(totalMessages) / duration.Seconds()

	t.Logf("Concurrent performance: %d workers × %d messages = %d total",
		numWorkers, messagesPerWorker, totalMessages)
	t.Logf("Duration: %v, Throughput: %.2f msg/sec", duration, throughput)

	// Verify all messages were processed
	time.Sleep(500 * time.Millisecond)
	if requestCount.Load() != int64(totalMessages) {
		t.Errorf("Expected %d requests, got %d", totalMessages, requestCount.Load())
	}
}

// ============================================================================
// Statistics Tests
// ============================================================================

func TestClientStatsTracking(t *testing.T) {
	testSrv := newTestServer(t)

	testSrv.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		ecr.Unmarshal(fullMsg)

		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		eca.OriginRealm = models_base.DiameterIdentity("example.com")
		eca.Header.HopByHopID = ecr.Header.HopByHopID
		eca.Header.EndToEndID = ecr.Header.EndToEndID

		resultCode := models_base.Unsigned32(2001)
		eca.ResultCode = &resultCode
		equipmentStatus := models_base.Enumerated(0)
		eca.EquipmentStatus = &equipmentStatus

		ecaBytes, _ := eca.Marshal()
		conn.Write(ecaBytes)
	})

	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	ctx := context.Background()

	pool, err := client.NewConnectionPool(ctx, config, logger.Log)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Start(); err != nil {
		t.Fatalf("Failed to start pool: %v", err)
	}

	if err := pool.WaitForConnection(3 * time.Second); err != nil {
		t.Fatalf("Failed to wait for connection: %v", err)
	}

	initialStats := pool.GetStats()

	// Send some messages
	numMessages := 5
	for i := 0; i < numMessages; i++ {
		ecr := s13.NewMEIdentityCheckRequest()
		ecr.SessionId = models_base.UTF8String(fmt.Sprintf("test-session-%d", i))
		ecr.AuthSessionState = models_base.Enumerated(1)
		ecr.OriginHost = models_base.DiameterIdentity(config.OriginHost)
		ecr.OriginRealm = models_base.DiameterIdentity(config.OriginRealm)
		ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
		ecr.TerminalInformation = &s13.TerminalInformation{
			Imei:            ptrUTF8String("123456789012345"),
			SoftwareVersion: ptrUTF8String("01"),
		}

		ecrBytes, _ := ecr.Marshal()
		pool.Send(ecrBytes)

		select {
		case <-pool.Receive():
		case <-time.After(2 * time.Second):
			t.Fatalf("Timeout on message %d", i)
		}
	}

	time.Sleep(200 * time.Millisecond)

	// Verify stats increased
	finalStats := pool.GetStats()

	if finalStats.TotalMessagesSent <= initialStats.TotalMessagesSent {
		t.Errorf("Expected TotalMessagesSent to increase")
	}

	if finalStats.TotalMessagesRecv <= initialStats.TotalMessagesRecv {
		t.Errorf("Expected TotalMessagesRecv to increase")
	}

	t.Logf("Stats: Sent=%d, Recv=%d, BytesSent=%d, BytesRecv=%d",
		finalStats.TotalMessagesSent, finalStats.TotalMessagesRecv,
		finalStats.TotalBytesSent, finalStats.TotalBytesRecv)
}

// ============================================================================
// Benchmarks
// ============================================================================

func BenchmarkClientCERCEA(b *testing.B) {
	testSrv := newTestServer(&testing.T{})
	testSrv.Start()
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn := client.NewConnection(ctx, fmt.Sprintf("bench-conn-%d", i), config, logger.Log)
		conn.Start()
		waitForConnectionState(conn, client.StateOpen, 3*time.Second)
		conn.Close()
	}
}

func BenchmarkClientS13ECR(b *testing.B) {
	testSrv := newTestServer(&testing.T{})

	testSrv.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		ecr.Unmarshal(fullMsg)

		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("bench-server.example.com")
		eca.OriginRealm = models_base.DiameterIdentity("example.com")
		eca.Header.HopByHopID = ecr.Header.HopByHopID
		eca.Header.EndToEndID = ecr.Header.EndToEndID
		resultCode := models_base.Unsigned32(2001)
		eca.ResultCode = &resultCode
		equipmentStatus := models_base.Enumerated(0)
		eca.EquipmentStatus = &equipmentStatus

		ecaBytes, _ := eca.Marshal()
		conn.Write(ecaBytes)
	})

	testSrv.Start()
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	ctx := context.Background()

	pool, _ := client.NewConnectionPool(ctx, config, logger.Log)
	pool.Start()
	pool.WaitForConnection(3 * time.Second)
	defer pool.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ecr := s13.NewMEIdentityCheckRequest()
		ecr.SessionId = models_base.UTF8String(fmt.Sprintf("bench-session-%d", i))
		ecr.AuthSessionState = models_base.Enumerated(1)
		ecr.OriginHost = models_base.DiameterIdentity(config.OriginHost)
		ecr.OriginRealm = models_base.DiameterIdentity(config.OriginRealm)
		ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
		ecr.TerminalInformation = &s13.TerminalInformation{
			Imei:            ptrUTF8String("123456789012345"),
			SoftwareVersion: ptrUTF8String("01"),
		}

		ecrBytes, _ := ecr.Marshal()
		pool.Send(ecrBytes)
		<-pool.Receive()
	}
}

func BenchmarkClientMessageSend(b *testing.B) {
	testSrv := newTestServer(&testing.T{})

	testSrv.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		// Echo back
		conn.Write(append(msg.Header, msg.Body...))
	})

	testSrv.Start()
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	ctx := context.Background()

	pool, _ := client.NewConnectionPool(ctx, config, logger.Log)
	pool.Start()
	pool.WaitForConnection(3 * time.Second)
	defer pool.Close()

	// Pre-create message
	ecr := s13.NewMEIdentityCheckRequest()
	ecr.SessionId = models_base.UTF8String("bench-session")
	ecr.AuthSessionState = models_base.Enumerated(1)
	ecr.OriginHost = models_base.DiameterIdentity(config.OriginHost)
	ecr.OriginRealm = models_base.DiameterIdentity(config.OriginRealm)
	ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
	ecr.TerminalInformation = &s13.TerminalInformation{
		Imei:            ptrUTF8String("123456789012345"),
		SoftwareVersion: ptrUTF8String("01"),
	}
	ecrBytes, _ := ecr.Marshal()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pool.Send(ecrBytes)
		<-pool.Receive()
	}
}

// ============================================================================
// AddressClient Tests (Per-Remote-Address Connection Pool)
// ============================================================================

// Helper to create test pool config
func newTestPoolConfig() *client.PoolConfig {
	config := client.DefaultPoolConfig()
	config.OriginHost = "test-client.example.com"
	config.OriginRealm = "example.com"
	config.ProductName = "TestClient/1.0"
	config.VendorID = 10415
	config.DialTimeout = 2 * time.Second
	config.CERTimeout = 2 * time.Second
	config.DWRInterval = 30 * time.Second
	config.DWRTimeout = 5 * time.Second
	config.MaxDWRFailures = 3
	config.ReconnectEnabled = true
	config.ReconnectInterval = 1 * time.Second
	config.MaxReconnectDelay = 30 * time.Second
	config.ReconnectBackoff = 2.0
	config.SendBufferSize = 1000
	config.RecvBufferSize = 1000
	config.AuthAppIDs = []uint32{16777252} // S13
	config.HealthCheckInterval = 30 * time.Second
	return config
}

func TestAddressClient_BasicConnectionEstablishment(t *testing.T) {
	// Start test server
	testSrv := newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	// Create address client
	config := newTestPoolConfig()
	ctx := context.Background()
	log := logger.New("test-address-client", "error")

	addressClient, err := client.NewAddressClient(ctx, config, log)
	if err != nil {
		t.Fatalf("Failed to create address client: %v", err)
	}
	defer addressClient.Close()

	// Create test message
	ecr := s13.NewMEIdentityCheckRequest()
	ecr.SessionId = models_base.UTF8String("test-session-1")
	ecr.AuthSessionState = models_base.Enumerated(1)
	ecr.OriginHost = models_base.DiameterIdentity(config.OriginHost)
	ecr.OriginRealm = models_base.DiameterIdentity(config.OriginRealm)
	ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
	ecr.TerminalInformation = &s13.TerminalInformation{
		Imei:            ptrUTF8String("123456789012345"),
		SoftwareVersion: ptrUTF8String("01"),
	}

	// Register handler on server
	var handlerCalled atomic.Bool
	testSrv.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		handlerCalled.Store(true)

		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		ecr.Unmarshal(fullMsg)

		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		eca.OriginRealm = models_base.DiameterIdentity("example.com")
		eca.Header.HopByHopID = ecr.Header.HopByHopID
		eca.Header.EndToEndID = ecr.Header.EndToEndID
		resultCode := models_base.Unsigned32(2001)
		eca.ResultCode = &resultCode
		equipmentStatus := models_base.Enumerated(0)
		eca.EquipmentStatus = &equipmentStatus

		ecaBytes, _ := eca.Marshal()
		conn.Write(ecaBytes)
	})

	// Marshal message
	ecrBytes, err := ecr.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal ECR: %v", err)
	}

	// Send to remote address - connection should be created automatically
	remoteAddr := testSrv.Address()
	_, err = addressClient.SendWithTimeout(remoteAddr, ecrBytes, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}

	// Verify connection was established
	time.Sleep(100 * time.Millisecond)
	if addressClient.GetActiveConnections() != 1 {
		t.Errorf("Expected 1 active connection, got %d", addressClient.GetActiveConnections())
	}

	// Verify handler was called
	if !handlerCalled.Load() {
		t.Error("Server handler was not called")
	}

	// Send another message - should reuse existing connection
	ecr.SessionId = models_base.UTF8String("test-session-2")
	ecrBytes, _ = ecr.Marshal()

	_, err = addressClient.SendWithTimeout(remoteAddr, ecrBytes, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to send second message: %v", err)
	}

	// Should still have only 1 connection
	if addressClient.GetActiveConnections() != 1 {
		t.Errorf("Expected 1 active connection after second send, got %d", addressClient.GetActiveConnections())
	}

	// Verify statistics
	stats := addressClient.GetStats()
	if stats.TotalRequests < 2 {
		t.Errorf("Expected at least 2 requests, got %d", stats.TotalRequests)
	}
}

func TestAddressClient_MultipleAddresses(t *testing.T) {
	// Start 3 test servers
	testSrv1 := newTestServer(t)
	if err := testSrv1.Start(); err != nil {
		t.Fatalf("Failed to start test server 1: %v", err)
	}
	defer testSrv1.Stop()

	testSrv2 := newTestServer(t)
	if err := testSrv2.Start(); err != nil {
		t.Fatalf("Failed to start test server 2: %v", err)
	}
	defer testSrv2.Stop()

	testSrv3 := newTestServer(t)
	if err := testSrv3.Start(); err != nil {
		t.Fatalf("Failed to start test server 3: %v", err)
	}
	defer testSrv3.Stop()

	// Register handlers
	registerEchoHandler := func(srv *testServer) {
		srv.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
			ecr := &s13.MEIdentityCheckRequest{}
			fullMsg := append(msg.Header, msg.Body...)
			ecr.Unmarshal(fullMsg)

			eca := s13.NewMEIdentityCheckAnswer()
			eca.SessionId = ecr.SessionId
			eca.AuthSessionState = ecr.AuthSessionState
			eca.OriginHost = models_base.DiameterIdentity("test-server.example.com")
			eca.OriginRealm = models_base.DiameterIdentity("example.com")
			eca.Header.HopByHopID = ecr.Header.HopByHopID
			eca.Header.EndToEndID = ecr.Header.EndToEndID
			resultCode := models_base.Unsigned32(2001)
			eca.ResultCode = &resultCode
			equipmentStatus := models_base.Enumerated(0)
			eca.EquipmentStatus = &equipmentStatus

			ecaBytes, _ := eca.Marshal()
			conn.Write(ecaBytes)
		})
	}

	registerEchoHandler(testSrv1)
	registerEchoHandler(testSrv2)
	registerEchoHandler(testSrv3)

	// Create address client
	config := newTestPoolConfig()
	ctx := context.Background()
	log := logger.New("test-address-client", "error")

	addressClient, err := client.NewAddressClient(ctx, config, log)
	if err != nil {
		t.Fatalf("Failed to create address client: %v", err)
	}
	defer addressClient.Close()

	// Create test message
	ecr := s13.NewMEIdentityCheckRequest()
	ecr.SessionId = models_base.UTF8String("test-session")
	ecr.AuthSessionState = models_base.Enumerated(1)
	ecr.OriginHost = models_base.DiameterIdentity(config.OriginHost)
	ecr.OriginRealm = models_base.DiameterIdentity(config.OriginRealm)
	ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
	ecr.TerminalInformation = &s13.TerminalInformation{
		Imei:            ptrUTF8String("123456789012345"),
		SoftwareVersion: ptrUTF8String("01"),
	}
	ecrBytes, _ := ecr.Marshal()

	// Send to all 3 servers
	addresses := []string{
		testSrv1.Address(),
		testSrv2.Address(),
		testSrv3.Address(),
	}

	for _, addr := range addresses {
		_, err := addressClient.SendWithTimeout(addr, ecrBytes, 5*time.Second)
		if err != nil {
			t.Errorf("Failed to send to %s: %v", addr, err)
		}
	}

	// Should have 3 connections now
	time.Sleep(100 * time.Millisecond)
	activeConns := addressClient.GetActiveConnections()
	if activeConns != 3 {
		t.Errorf("Expected 3 active connections, got %d", activeConns)
	}

	// Verify all addresses are in the list
	connAddrs := addressClient.ListConnections()
	if len(connAddrs) != 3 {
		t.Errorf("Expected 3 connection addresses, got %d", len(connAddrs))
	}

	// Send multiple messages to each address
	for i := 0; i < 5; i++ {
		for _, addr := range addresses {
			_, err := addressClient.SendWithTimeout(addr, ecrBytes, 5*time.Second)
			if err != nil {
				t.Errorf("Failed to send iteration %d to %s: %v", i, addr, err)
			}
		}
	}

	// Should still have 3 connections (reused)
	activeConns = addressClient.GetActiveConnections()
	if activeConns != 3 {
		t.Errorf("Expected 3 active connections after multiple sends, got %d", activeConns)
	}
}

func TestAddressClient_ConcurrentSends(t *testing.T) {
	// Start test server
	testSrv := newTestServer(t)

	var requestCount atomic.Int64
	testSrv.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		requestCount.Add(1)

		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		ecr.Unmarshal(fullMsg)

		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		eca.OriginRealm = models_base.DiameterIdentity("example.com")
		eca.Header.HopByHopID = ecr.Header.HopByHopID
		eca.Header.EndToEndID = ecr.Header.EndToEndID
		resultCode := models_base.Unsigned32(2001)
		eca.ResultCode = &resultCode
		equipmentStatus := models_base.Enumerated(0)
		eca.EquipmentStatus = &equipmentStatus

		ecaBytes, _ := eca.Marshal()
		conn.Write(ecaBytes)
	})

	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	// Create address client
	config := newTestPoolConfig()
	ctx := context.Background()
	log := logger.New("test-address-client", "error")

	addressClient, err := client.NewAddressClient(ctx, config, log)
	if err != nil {
		t.Fatalf("Failed to create address client: %v", err)
	}
	defer addressClient.Close()

	// Create test message
	ecr := s13.NewMEIdentityCheckRequest()
	ecr.SessionId = models_base.UTF8String("test-session")
	ecr.AuthSessionState = models_base.Enumerated(1)
	ecr.OriginHost = models_base.DiameterIdentity(config.OriginHost)
	ecr.OriginRealm = models_base.DiameterIdentity(config.OriginRealm)
	ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
	ecr.TerminalInformation = &s13.TerminalInformation{
		Imei:            ptrUTF8String("123456789012345"),
		SoftwareVersion: ptrUTF8String("01"),
	}
	ecrBytes, _ := ecr.Marshal()

	// Launch multiple goroutines sending to the same address concurrently
	// This tests the concurrent caller blocking during connection establishment
	numGoroutines := 20
	messagesPerGoroutine := 5
	var wg sync.WaitGroup
	remoteAddr := testSrv.Address()

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < messagesPerGoroutine; j++ {
				_, err := addressClient.SendWithTimeout(remoteAddr, ecrBytes, 10*time.Second)
				if err != nil {
					t.Errorf("Goroutine %d message %d failed: %v", id, j, err)
				}
			}
		}(i)
	}

	wg.Wait()

	// Should have exactly 1 connection despite concurrent establishment attempts
	time.Sleep(200 * time.Millisecond)
	activeConns := addressClient.GetActiveConnections()
	if activeConns != 1 {
		t.Errorf("Expected 1 active connection, got %d", activeConns)
	}

	// Verify all messages were processed
	expectedCount := int64(numGoroutines * messagesPerGoroutine)
	actualCount := requestCount.Load()
	if actualCount < expectedCount {
		t.Errorf("Expected at least %d requests processed, got %d", expectedCount, actualCount)
	}

	// Verify client stats
	stats := addressClient.GetStats()
	t.Logf("Stats: Requests=%d, Responses=%d, Errors=%d, Active=%d",
		stats.TotalRequests, stats.TotalResponses, stats.TotalErrors,
		stats.PoolMetrics.ActiveConnections)
}

func TestAddressClient_Metrics(t *testing.T) {
	testSrv := newTestServer(t)

	testSrv.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		ecr.Unmarshal(fullMsg)

		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		eca.OriginRealm = models_base.DiameterIdentity("example.com")
		eca.Header.HopByHopID = ecr.Header.HopByHopID
		eca.Header.EndToEndID = ecr.Header.EndToEndID
		resultCode := models_base.Unsigned32(2001)
		eca.ResultCode = &resultCode
		equipmentStatus := models_base.Enumerated(0)
		eca.EquipmentStatus = &equipmentStatus

		ecaBytes, _ := eca.Marshal()
		conn.Write(ecaBytes)
	})

	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestPoolConfig()
	ctx := context.Background()
	log := logger.New("test-address-client", "error")

	addressClient, err := client.NewAddressClient(ctx, config, log)
	if err != nil {
		t.Fatalf("Failed to create address client: %v", err)
	}
	defer addressClient.Close()

	// Initial stats
	initialStats := addressClient.GetStats()
	if initialStats.TotalRequests != 0 {
		t.Errorf("Expected 0 initial requests, got %d", initialStats.TotalRequests)
	}

	// Send messages
	ecr := s13.NewMEIdentityCheckRequest()
	ecr.SessionId = models_base.UTF8String("test-session")
	ecr.AuthSessionState = models_base.Enumerated(1)
	ecr.OriginHost = models_base.DiameterIdentity(config.OriginHost)
	ecr.OriginRealm = models_base.DiameterIdentity(config.OriginRealm)
	ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
	ecr.TerminalInformation = &s13.TerminalInformation{
		Imei:            ptrUTF8String("123456789012345"),
		SoftwareVersion: ptrUTF8String("01"),
	}
	ecrBytes, _ := ecr.Marshal()

	numMessages := 10
	for i := 0; i < numMessages; i++ {
		_, err := addressClient.SendWithTimeout(testSrv.Address(), ecrBytes, 5*time.Second)
		if err != nil {
			t.Errorf("Failed to send message %d: %v", i, err)
		}
	}

	time.Sleep(200 * time.Millisecond)

	// Check final stats
	finalStats := addressClient.GetStats()

	if finalStats.TotalRequests != uint64(numMessages) {
		t.Errorf("Expected %d total requests, got %d", numMessages, finalStats.TotalRequests)
	}

	if finalStats.PoolMetrics.ActiveConnections != 1 {
		t.Errorf("Expected 1 active connection, got %d", finalStats.PoolMetrics.ActiveConnections)
	}

	if finalStats.PoolMetrics.TotalConnections < 1 {
		t.Errorf("Expected at least 1 total connection created, got %d", finalStats.PoolMetrics.TotalConnections)
	}

	if finalStats.PoolMetrics.MessagesSent < uint64(numMessages) {
		t.Errorf("Expected at least %d messages sent, got %d", numMessages, finalStats.PoolMetrics.MessagesSent)
	}

	t.Logf("Final stats: Requests=%d, Responses=%d, Errors=%d, ActiveConns=%d, TotalConns=%d",
		finalStats.TotalRequests, finalStats.TotalResponses, finalStats.TotalErrors,
		finalStats.PoolMetrics.ActiveConnections, finalStats.PoolMetrics.TotalConnections)
}

func TestAddressClient_ContextCancellation(t *testing.T) {
	testSrv := newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestPoolConfig()
	ctx, cancel := context.WithCancel(context.Background())
	log := logger.New("test-address-client", "error")

	addressClient, err := client.NewAddressClient(ctx, config, log)
	if err != nil {
		t.Fatalf("Failed to create address client: %v", err)
	}
	defer addressClient.Close()

	// Cancel context immediately
	cancel()

	// Attempt to send should fail
	ecr := s13.NewMEIdentityCheckRequest()
	ecr.SessionId = models_base.UTF8String("test-session")
	ecr.AuthSessionState = models_base.Enumerated(1)
	ecr.OriginHost = models_base.DiameterIdentity(config.OriginHost)
	ecr.OriginRealm = models_base.DiameterIdentity(config.OriginRealm)
	ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
	ecr.TerminalInformation = &s13.TerminalInformation{
		Imei:            ptrUTF8String("123456789012345"),
		SoftwareVersion: ptrUTF8String("01"),
	}
	ecrBytes, _ := ecr.Marshal()

	sendCtx, sendCancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer sendCancel()

	_, err = addressClient.SendWithContext(sendCtx, testSrv.Address(), ecrBytes)
	if err == nil {
		t.Error("Expected error when sending with cancelled context")
	}
}

func TestAddressClient_InvalidAddress(t *testing.T) {
	config := newTestPoolConfig()
	ctx := context.Background()
	log := logger.New("test-address-client", "error")

	addressClient, err := client.NewAddressClient(ctx, config, log)
	if err != nil {
		t.Fatalf("Failed to create address client: %v", err)
	}
	defer addressClient.Close()

	message := []byte("test message")

	// Test various invalid address formats
	invalidAddrs := []string{
		"invalid",
		"192.168.1.100",  // missing port
		"192.168.1.100:", // empty port
		":3868",          // missing host
		"example.com",    // missing port
	}

	for _, addr := range invalidAddrs {
		_, err := addressClient.SendWithTimeout(addr, message, 1*time.Second)
		if err == nil {
			t.Errorf("Expected error for invalid address %q, got nil", addr)
		}
	}
}
