package server

import (
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/commands/base"
	"github.com/hsdfat/diam-gw/commands/s13"
	"github.com/hsdfat/diam-gw/commands/s6a"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

// TestServerBasicSetup tests basic server creation and startup
func TestServerBasicSetup(t *testing.T) {
	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0" // Random port

	server := NewServer(config, log)
	if server == nil {
		t.Fatal("Failed to create server")
	}

	// Start server in background
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start()
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Verify server is listening
	if server.GetListener() == nil {
		t.Fatal("Server listener is nil")
	}

	// Stop server
	if err := server.Stop(); err != nil {
		t.Errorf("Failed to stop server: %v", err)
	}

	// Check if Start() returned (should be nil or context canceled)
	select {
	case err := <-errChan:
		if err != nil {
			t.Logf("Server stopped with: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Server did not stop in time")
	}
}

// TestServerHandlerRegistration tests handler registration
func TestServerHandlerRegistration(t *testing.T) {
	log := logger.New("test-server", "error")
	server := NewServer(nil, log)

	handler := func(msg *Message, conn Conn) {
		// Handler implementation
	}

	// Register handler for CER (Base Protocol, Code 257)
	cmd := Command{Interface: 0, Code: 257}
	server.HandleFunc(cmd, handler)

	// Verify handler was registered
	retrievedHandler, exists := server.getHandler(cmd)
	if !exists {
		t.Error("Handler was not registered")
	}
	if retrievedHandler == nil {
		t.Error("Retrieved handler is nil")
	}
}

// TestServerCERCEAExchange tests Capabilities Exchange Request/Answer
func TestServerCERCEAExchange(t *testing.T) {
	log := logger.New("test-server", "debug")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	config.ConnectionConfig.HandleWatchdog = true

	server := NewServer(config, log)

	// Register base protocol handlers
	registerBaseProtocolHandlers(server, t)

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond)

	addr := server.GetListener().Addr().String()
	t.Logf("Server listening on %s", addr)

	// Create test client
	client, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to connect to server: %v", err)
	}
	defer client.Close()

	// Build CER message
	cer := base.NewCapabilitiesExchangeRequest()
	cer.OriginHost = models_base.DiameterIdentity("test-client.example.com")
	cer.OriginRealm = models_base.DiameterIdentity("example.com")
	cer.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("127.0.0.1")),
	}
	cer.VendorId = models_base.Unsigned32(10415)
	cer.ProductName = models_base.UTF8String("TestClient/1.0")
	cer.Header.HopByHopID = 0x12345678
	cer.Header.EndToEndID = 0x87654321

	// Add S13 support
	cer.AuthApplicationId = []models_base.Unsigned32{16777252} // S13

	cerBytes, err := cer.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal CER: %v", err)
	}

	// Send CER
	if _, err := client.Write(cerBytes); err != nil {
		t.Fatalf("Failed to send CER: %v", err)
	}

	t.Log("Sent CER to server")

	// Read CEA
	ceaBytes := readDiameterMessage(t, client)
	if len(ceaBytes) == 0 {
		t.Fatal("Received empty CEA")
	}

	cea := &base.CapabilitiesExchangeAnswer{}
	if err := cea.Unmarshal(ceaBytes); err != nil {
		t.Fatalf("Failed to unmarshal CEA: %v", err)
	}

	t.Logf("Received CEA: ResultCode=%d, OriginHost=%s", cea.ResultCode, cea.OriginHost)

	// Verify CEA
	if cea.ResultCode != 2001 {
		t.Errorf("Expected ResultCode 2001, got %d", cea.ResultCode)
	}
	if cea.Header.HopByHopID != cer.Header.HopByHopID {
		t.Errorf("HopByHopID mismatch: sent=%d, received=%d",
			cer.Header.HopByHopID, cea.Header.HopByHopID)
	}
	if cea.Header.EndToEndID != cer.Header.EndToEndID {
		t.Errorf("EndToEndID mismatch: sent=%d, received=%d",
			cer.Header.EndToEndID, cea.Header.EndToEndID)
	}

	// Wait for handler to be called (note: CER is handled internally by Connection)
	time.Sleep(100 * time.Millisecond)

	server.Stop()
}

// TestServerDWRDWAExchange tests Device Watchdog Request/Answer
func TestServerDWRDWAExchange(t *testing.T) {
	log := logger.New("test-server", "debug")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	config.ConnectionConfig.HandleWatchdog = true

	server := NewServer(config, log)

	// Register base protocol handlers
	registerBaseProtocolHandlers(server, t)

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond)

	addr := server.GetListener().Addr().String()

	// Create and connect client
	client := newTestClient(t, addr)
	defer client.Close()

	// Send CER first
	client.sendCER(t)
	client.receiveCEA(t)

	// Build DWR message
	dwr := base.NewDeviceWatchdogRequest()
	dwr.OriginHost = models_base.DiameterIdentity("test-client.example.com")
	dwr.OriginRealm = models_base.DiameterIdentity("example.com")
	dwr.Header.HopByHopID = 0x22334455
	dwr.Header.EndToEndID = 0x55443322

	dwrBytes, err := dwr.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal DWR: %v", err)
	}

	// Send DWR
	if _, err := client.conn.Write(dwrBytes); err != nil {
		t.Fatalf("Failed to send DWR: %v", err)
	}

	t.Log("Sent DWR to server")

	// Read DWA
	dwaBytes := client.readMessage(t)
	dwa := &base.DeviceWatchdogAnswer{}
	if err := dwa.Unmarshal(dwaBytes); err != nil {
		t.Fatalf("Failed to unmarshal DWA: %v", err)
	}

	t.Logf("Received DWA: ResultCode=%d", dwa.ResultCode)

	// Verify DWA
	if dwa.ResultCode != 2001 {
		t.Errorf("Expected ResultCode 2001, got %d", dwa.ResultCode)
	}
	if dwa.Header.HopByHopID != dwr.Header.HopByHopID {
		t.Errorf("HopByHopID mismatch: sent=%d, received=%d",
			dwr.Header.HopByHopID, dwa.Header.HopByHopID)
	}

	server.Stop()
}

// TestServerS13ECRExchange tests S13 ME-Identity-Check-Request/Answer
func TestServerS13ECRExchange(t *testing.T) {
	log := logger.New("test-server", "debug")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"

	server := NewServer(config, log)

	// Register base protocol handlers first
	registerBaseProtocolHandlers(server, t)

	// Track S13 handler
	var handlerCalled bool
	var handlerMu sync.Mutex

	// Register S13 ECR handler
	server.HandleFunc(Command{Interface: 16777252, Code: 324, Request: true}, func(msg *Message, conn Conn) {
		handlerMu.Lock()
		handlerCalled = true
		handlerMu.Unlock()

		t.Logf("S13 ECR handler called, message length=%d", msg.Length)

		// Parse ECR
		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := ecr.Unmarshal(fullMsg); err != nil {
			t.Errorf("Failed to unmarshal ECR: %v", err)
			return
		}

		t.Logf("Received ECR: SessionId=%s", ecr.SessionId)

		// Build ECA response
		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		eca.OriginRealm = models_base.DiameterIdentity("example.com")
		eca.Header.HopByHopID = ecr.Header.HopByHopID
		eca.Header.EndToEndID = ecr.Header.EndToEndID

		// Set result code (success)
		resultCode := models_base.Unsigned32(2001)
		eca.ResultCode = &resultCode

		// Set Equipment-Status (WHITELISTED = 0)
		equipmentStatus := models_base.Enumerated(0)
		eca.EquipmentStatus = &equipmentStatus

		ecaBytes, err := eca.Marshal()
		if err != nil {
			t.Errorf("Failed to marshal ECA: %v", err)
			return
		}

		// Send ECA
		if _, err := conn.Write(ecaBytes); err != nil {
			t.Errorf("Failed to send ECA: %v", err)
		}

		t.Log("Sent ECA response")
	})

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond)

	addr := server.GetListener().Addr().String()

	// Create and connect client
	client := newTestClient(t, addr)
	defer client.Close()

	// Send CER first (with S13 support)
	client.sendCERWithS13(t)
	client.receiveCEA(t)

	// Build ECR message
	ecr := s13.NewMEIdentityCheckRequest()
	ecr.SessionId = models_base.UTF8String("test-client.example.com;123456789;1")
	ecr.AuthSessionState = models_base.Enumerated(1) // NO_STATE_MAINTAINED
	ecr.OriginHost = models_base.DiameterIdentity("test-client.example.com")
	ecr.OriginRealm = models_base.DiameterIdentity("example.com")
	ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
	ecr.Header.HopByHopID = 0x33445566
	ecr.Header.EndToEndID = 0x66554433

	// Set Terminal-Information
	ecr.TerminalInformation = &s13.TerminalInformation{
		Imei:            ptrUTF8String("123456789012345"),
		SoftwareVersion: ptrUTF8String("01"),
	}

	ecrBytes, err := ecr.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal ECR: %v", err)
	}

	// Send ECR
	if _, err := client.conn.Write(ecrBytes); err != nil {
		t.Fatalf("Failed to send ECR: %v", err)
	}

	t.Log("Sent ECR to server")

	// Read ECA
	ecaBytes := client.readMessage(t)
	eca := &s13.MEIdentityCheckAnswer{}
	if err := eca.Unmarshal(ecaBytes); err != nil {
		t.Fatalf("Failed to unmarshal ECA: %v", err)
	}

	t.Logf("Received ECA: ResultCode=%d, EquipmentStatus=%d",
		*eca.ResultCode, *eca.EquipmentStatus)

	// Verify ECA
	if *eca.ResultCode != 2001 {
		t.Errorf("Expected ResultCode 2001, got %d", *eca.ResultCode)
	}
	if eca.Header.HopByHopID != ecr.Header.HopByHopID {
		t.Errorf("HopByHopID mismatch: sent=%d, received=%d",
			ecr.Header.HopByHopID, eca.Header.HopByHopID)
	}

	// Verify handler was called
	time.Sleep(100 * time.Millisecond)
	handlerMu.Lock()
	if !handlerCalled {
		t.Error("S13 handler was not called")
	}
	handlerMu.Unlock()

	server.Stop()
}

// TestServerDPRDPADisconnect tests Disconnect Peer Request/Answer
func TestServerDPRDPADisconnect(t *testing.T) {
	log := logger.New("test-server", "debug")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"

	server := NewServer(config, log)

	// Register base protocol handlers
	registerBaseProtocolHandlers(server, t)

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond)

	addr := server.GetListener().Addr().String()

	// Create and connect client
	client := newTestClient(t, addr)
	defer client.Close()

	// Send CER first
	client.sendCER(t)
	client.receiveCEA(t)

	// Build DPR message
	dpr := base.NewDisconnectPeerRequest()
	dpr.OriginHost = models_base.DiameterIdentity("test-client.example.com")
	dpr.OriginRealm = models_base.DiameterIdentity("example.com")
	dpr.DisconnectCause = models_base.Enumerated(2) // BUSY
	dpr.Header.HopByHopID = 0x44556677
	dpr.Header.EndToEndID = 0x77665544

	dprBytes, err := dpr.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal DPR: %v", err)
	}

	// Send DPR
	if _, err := client.conn.Write(dprBytes); err != nil {
		t.Fatalf("Failed to send DPR: %v", err)
	}

	t.Log("Sent DPR to server")

	// Read DPA
	dpaBytes := client.readMessage(t)
	dpa := &base.DisconnectPeerAnswer{}
	if err := dpa.Unmarshal(dpaBytes); err != nil {
		t.Fatalf("Failed to unmarshal DPA: %v", err)
	}

	t.Logf("Received DPA: ResultCode=%d", dpa.ResultCode)

	// Verify DPA
	if dpa.ResultCode != 2001 {
		t.Errorf("Expected ResultCode 2001, got %d", dpa.ResultCode)
	}
	if dpa.Header.HopByHopID != dpr.Header.HopByHopID {
		t.Errorf("HopByHopID mismatch: sent=%d, received=%d",
			dpr.Header.HopByHopID, dpa.Header.HopByHopID)
	}

	// Server should close connection after DPA
	time.Sleep(2 * time.Second)

	server.Stop()
}

// TestServerMultipleConnections tests handling multiple concurrent connections
func TestServerMultipleConnections(t *testing.T) {
	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	config.MaxConnections = 10

	server := NewServer(config, log)

	// Register base protocol handlers
	registerBaseProtocolHandlers(server, t)

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond)

	addr := server.GetListener().Addr().String()

	// Create multiple clients
	numClients := 5
	clients := make([]*testClient, numClients)
	var wg sync.WaitGroup

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			client := newTestClient(t, addr)
			clients[index] = client

			// Send CER
			client.sendCER(t)
			client.receiveCEA(t)

			t.Logf("Client %d connected", index)
		}(i)
	}

	wg.Wait()

	// Verify all connections
	stats := server.GetStats()
	if stats.ActiveConnections != uint64(numClients) {
		t.Errorf("Expected %d active connections, got %d",
			numClients, stats.ActiveConnections)
	}

	// Close all clients
	for _, client := range clients {
		if client != nil {
			client.Close()
		}
	}

	time.Sleep(200 * time.Millisecond)
	server.Stop()
}

// TestServerNoHandler tests behavior when no handler is registered
func TestServerNoHandler(t *testing.T) {
	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"

	server := NewServer(config, log)

	// Register only CER handler (minimum for connection establishment)
	server.HandleFunc(Command{Interface: 0, Code: 257}, func(msg *Message, conn Conn) {
		cer := &base.CapabilitiesExchangeRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		cer.Unmarshal(fullMsg)

		cea := base.NewCapabilitiesExchangeAnswer()
		cea.ResultCode = 2001
		cea.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		cea.OriginRealm = models_base.DiameterIdentity("example.com")
		cea.HostIpAddress = []models_base.Address{models_base.Address(net.ParseIP("127.0.0.1"))}
		cea.VendorId = models_base.Unsigned32(10415)
		cea.ProductName = models_base.UTF8String("TestServer/1.0")
		cea.Header.HopByHopID = cer.Header.HopByHopID
		cea.Header.EndToEndID = cer.Header.EndToEndID

		ceaBytes, _ := cea.Marshal()
		conn.Write(ceaBytes)
	})

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond)

	addr := server.GetListener().Addr().String()

	client := newTestClient(t, addr)
	defer client.Close()

	// CER should still work (handled by Connection)
	client.sendCER(t)
	client.receiveCEA(t)

	t.Log("Test completed - server handled CER without custom handler")

	server.Stop()
}

// testClient is a helper struct for test clients
type testClient struct {
	conn net.Conn
	t    *testing.T
}

func newTestClient(t *testing.T, addr string) *testClient {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to connect to server: %v", err)
	}
	return &testClient{conn: conn, t: t}
}

func (c *testClient) Close() {
	c.conn.Close()
}

func (c *testClient) sendCER(t *testing.T) {
	cer := base.NewCapabilitiesExchangeRequest()
	cer.OriginHost = models_base.DiameterIdentity("test-client.example.com")
	cer.OriginRealm = models_base.DiameterIdentity("example.com")
	cer.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("127.0.0.1")),
	}
	cer.VendorId = models_base.Unsigned32(10415)
	cer.ProductName = models_base.UTF8String("TestClient/1.0")

	cerBytes, err := cer.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal CER: %v", err)
	}

	if _, err := c.conn.Write(cerBytes); err != nil {
		t.Fatalf("Failed to send CER: %v", err)
	}
}

func (c *testClient) sendCERWithS13(t *testing.T) {
	cer := base.NewCapabilitiesExchangeRequest()
	cer.OriginHost = models_base.DiameterIdentity("test-client.example.com")
	cer.OriginRealm = models_base.DiameterIdentity("example.com")
	cer.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("127.0.0.1")),
	}
	cer.VendorId = models_base.Unsigned32(10415)
	cer.ProductName = models_base.UTF8String("TestClient/1.0")
	cer.AuthApplicationId = []models_base.Unsigned32{16777252} // S13

	cerBytes, err := cer.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal CER: %v", err)
	}

	if _, err := c.conn.Write(cerBytes); err != nil {
		t.Fatalf("Failed to send CER: %v", err)
	}
}

func (c *testClient) receiveCEA(t *testing.T) {
	ceaBytes := c.readMessage(t)
	cea := &base.CapabilitiesExchangeAnswer{}
	if err := cea.Unmarshal(ceaBytes); err != nil {
		t.Fatalf("Failed to unmarshal CEA: %v", err)
	}

	if cea.ResultCode != 2001 {
		t.Errorf("CEA failed with ResultCode=%d", cea.ResultCode)
	}
}

func (c *testClient) readMessage(t *testing.T) []byte {
	// Set read timeout
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	header := make([]byte, 20)
	if _, err := io.ReadFull(c.conn, header); err != nil {
		t.Fatalf("Failed to read message header: %v", err)
	}

	msgLen := uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
	body := make([]byte, msgLen-20)
	if len(body) > 0 {
		if _, err := io.ReadFull(c.conn, body); err != nil {
			t.Fatalf("Failed to read message body: %v", err)
		}
	}

	return append(header, body...)
}

// Helper function for pointer to UTF8String
func ptrUTF8String(s string) *models_base.UTF8String {
	v := models_base.UTF8String(s)
	return &v
}

// registerBaseProtocolHandlers registers handlers for base Diameter protocol messages
func registerBaseProtocolHandlers(server *Server, t *testing.T) {
	// CER/CEA handler
	server.HandleFunc(Command{Interface: 0, Code: 257, Request: true}, func(msg *Message, conn Conn) {
		cer := &base.CapabilitiesExchangeRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := cer.Unmarshal(fullMsg); err != nil {
			t.Logf("Failed to unmarshal CER: %v", err)
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

		ceaBytes, _ := cea.Marshal()
		conn.Write(ceaBytes)
	})

	// DWR/DWA handler
	server.HandleFunc(Command{Interface: 0, Code: 280, Request: true}, func(msg *Message, conn Conn) {
		dwr := &base.DeviceWatchdogRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := dwr.Unmarshal(fullMsg); err != nil {
			t.Logf("Failed to unmarshal DWR: %v", err)
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

	// DPR/DPA handler
	server.HandleFunc(Command{Interface: 0, Code: 282, Request: true}, func(msg *Message, conn Conn) {
		dpr := &base.DisconnectPeerRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := dpr.Unmarshal(fullMsg); err != nil {
			t.Logf("Failed to unmarshal DPR: %v", err)
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

// readDiameterMessage reads a complete Diameter message from the connection
func readDiameterMessage(t *testing.T, conn net.Conn) []byte {
	// Set read timeout
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	header := make([]byte, 20)
	if _, err := io.ReadFull(conn, header); err != nil {
		t.Fatalf("Failed to read message header: %v", err)
	}

	msgLen := uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
	body := make([]byte, msgLen-20)
	if len(body) > 0 {
		if _, err := io.ReadFull(conn, body); err != nil {
			t.Fatalf("Failed to read message body: %v", err)
		}
	}

	return append(header, body...)
}

// BenchmarkServerCERCEA benchmarks CER/CEA exchange
func BenchmarkServerCERCEA(b *testing.B) {
	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"

	server := NewServer(config, log)
	go server.Start()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.GetListener().Addr().String()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client, err := net.Dial("tcp", addr)
		if err != nil {
			b.Fatalf("Failed to connect: %v", err)
		}

		// Build CER
		cer := base.NewCapabilitiesExchangeRequest()
		cer.OriginHost = models_base.DiameterIdentity("bench-client.example.com")
		cer.OriginRealm = models_base.DiameterIdentity("example.com")
		cer.HostIpAddress = []models_base.Address{
			models_base.Address(net.ParseIP("127.0.0.1")),
		}
		cer.VendorId = models_base.Unsigned32(10415)
		cer.ProductName = models_base.UTF8String("BenchClient/1.0")

		cerBytes, _ := cer.Marshal()
		client.Write(cerBytes)

		// Read CEA
		header := make([]byte, 20)
		io.ReadFull(client, header)
		msgLen := uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
		body := make([]byte, msgLen-20)
		if len(body) > 0 {
			io.ReadFull(client, body)
		}

		client.Close()
	}
}

// BenchmarkServerS13ECR benchmarks S13 ECR/ECA exchange
func BenchmarkServerS13ECR(b *testing.B) {
	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"

	server := NewServer(config, log)

	// Register S13 handler
	server.HandleFunc(Command{Interface: 16777252, Code: 324}, func(msg *Message, conn Conn) {
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

	go server.Start()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.GetListener().Addr().String()

	// Setup connection
	client, _ := net.Dial("tcp", addr)
	defer client.Close()

	// Send CER
	cer := base.NewCapabilitiesExchangeRequest()
	cer.OriginHost = models_base.DiameterIdentity("bench-client.example.com")
	cer.OriginRealm = models_base.DiameterIdentity("example.com")
	cer.HostIpAddress = []models_base.Address{models_base.Address(net.ParseIP("127.0.0.1"))}
	cer.VendorId = models_base.Unsigned32(10415)
	cer.ProductName = models_base.UTF8String("BenchClient/1.0")
	cer.AuthApplicationId = []models_base.Unsigned32{16777252}
	cerBytes, _ := cer.Marshal()
	client.Write(cerBytes)

	// Read CEA
	header := make([]byte, 20)
	io.ReadFull(client, header)
	msgLen := uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
	body := make([]byte, msgLen-20)
	if len(body) > 0 {
		io.ReadFull(client, body)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Build ECR
		ecr := s13.NewMEIdentityCheckRequest()
		ecr.SessionId = models_base.UTF8String(fmt.Sprintf("bench-client.example.com;%d;1", i))
		ecr.AuthSessionState = models_base.Enumerated(1)
		ecr.OriginHost = models_base.DiameterIdentity("bench-client.example.com")
		ecr.OriginRealm = models_base.DiameterIdentity("example.com")
		ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
		ecr.TerminalInformation = &s13.TerminalInformation{
			Imei:            ptrUTF8String("123456789012345"),
			SoftwareVersion: ptrUTF8String("01"),
		}

		ecrBytes, _ := ecr.Marshal()
		client.Write(ecrBytes)

		// Read ECA
		header := make([]byte, 20)
		io.ReadFull(client, header)
		msgLen := uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
		body := make([]byte, msgLen-20)
		if len(body) > 0 {
			io.ReadFull(client, body)
		}
	}
}

// TestServerStatsTracking tests that server stats are properly tracked
func TestServerStatsTracking(t *testing.T) {
	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"

	server := NewServer(config, log)
	registerBaseProtocolHandlers(server, t)

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.GetListener().Addr().String()

	// Get initial stats
	initialStats := server.GetStats()
	initialTotalConn := initialStats.TotalConnections
	initialTotalMsg := initialStats.TotalMessages
	initialMsgSent := initialStats.MessagesSent
	initialMsgRecv := initialStats.MessagesReceived

	// Create client and send CER
	client := newTestClient(t, addr)
	defer client.Close()

	client.sendCER(t)
	client.receiveCEA(t)

	// Wait for stats to update
	time.Sleep(200 * time.Millisecond)

	// Verify stats
	stats := server.GetStats()
	if stats.TotalConnections <= initialTotalConn {
		t.Errorf("Expected TotalConnections to increase, got %d (initial: %d)",
			stats.TotalConnections, initialTotalConn)
	}
	if stats.TotalMessages <= initialTotalMsg {
		t.Errorf("Expected TotalMessages to increase, got %d (initial: %d)",
			stats.TotalMessages, initialTotalMsg)
	}
	if stats.MessagesReceived <= initialMsgRecv {
		t.Errorf("Expected MessagesReceived to increase, got %d (initial: %d)",
			stats.MessagesReceived, initialMsgRecv)
	}
	if stats.MessagesSent <= initialMsgSent {
		t.Errorf("Expected MessagesSent to increase, got %d (initial: %d)",
			stats.MessagesSent, initialMsgSent)
	}
	if stats.TotalBytesReceived == 0 {
		t.Error("Expected TotalBytesReceived to be > 0")
	}
	if stats.TotalBytesSent == 0 {
		t.Error("Expected TotalBytesSent to be > 0")
	}

	t.Logf("Stats after CER: TotalConn=%d, TotalMsg=%d, Recv=%d, Sent=%d, BytesRecv=%d, BytesSent=%d",
		stats.TotalConnections, stats.TotalMessages,
		stats.MessagesReceived, stats.MessagesSent,
		stats.TotalBytesReceived, stats.TotalBytesSent)
}

// TestServerPerformanceThroughput tests message throughput
func TestServerPerformanceThroughput(t *testing.T) {
	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"

	server := NewServer(config, log)
	registerBaseProtocolHandlers(server, t)

	// Register S13 handler
	server.HandleFunc(Command{Interface: 16777252, Code: 324, Request: true}, func(msg *Message, conn Conn) {
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

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.GetListener().Addr().String()

	// Setup connection
	client := newTestClient(t, addr)
	defer client.Close()

	// Send CER first
	client.sendCERWithS13(t)
	client.receiveCEA(t)

	// Get initial stats
	initialStats := server.GetStats()
	initialTotalMsg := initialStats.TotalMessages

	// Send multiple ECR messages
	numMessages := 1000
	startTime := time.Now()

	for i := 0; i < numMessages; i++ {
		ecr := s13.NewMEIdentityCheckRequest()
		ecr.SessionId = models_base.UTF8String(fmt.Sprintf("test-client.example.com;%d;1", i))
		ecr.AuthSessionState = models_base.Enumerated(1)
		ecr.OriginHost = models_base.DiameterIdentity("test-client.example.com")
		ecr.OriginRealm = models_base.DiameterIdentity("example.com")
		ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
		ecr.TerminalInformation = &s13.TerminalInformation{
			Imei:            ptrUTF8String("123456789012345"),
			SoftwareVersion: ptrUTF8String("01"),
		}

		ecrBytes, err := ecr.Marshal()
		if err != nil {
			t.Fatalf("Failed to marshal ECR: %v", err)
		}

		if _, err := client.conn.Write(ecrBytes); err != nil {
			t.Fatalf("Failed to send ECR: %v", err)
		}

		// Read ECA
		client.readMessage(t)
	}

	duration := time.Since(startTime)

	// Wait for stats to update
	time.Sleep(200 * time.Millisecond)

	// Verify stats
	stats := server.GetStats()
	totalProcessed := stats.TotalMessages - initialTotalMsg

	if totalProcessed < uint64(numMessages) {
		t.Errorf("Expected at least %d messages processed, got %d",
			numMessages, totalProcessed)
	}

	throughput := float64(numMessages) / duration.Seconds()
	t.Logf("Performance: Processed %d messages in %v (%.2f msg/sec)",
		numMessages, duration, throughput)
	t.Logf("Stats: TotalMsg=%d, Recv=%d, Sent=%d, BytesRecv=%d, BytesSent=%d, Errors=%d",
		stats.TotalMessages, stats.MessagesReceived,
		stats.MessagesSent, stats.TotalBytesReceived,
		stats.TotalBytesSent, stats.Errors)

	// Verify no errors occurred
	if stats.Errors > 0 {
		t.Errorf("Expected no errors, got %d", stats.Errors)
	}
}

// TestServerPerformanceConcurrentConnections tests performance with multiple concurrent connections
func TestServerPerformanceConcurrentConnections(t *testing.T) {
	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	config.MaxConnections = 100

	server := NewServer(config, log)
	registerBaseProtocolHandlers(server, t)

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.GetListener().Addr().String()

	// Get initial stats
	initialStats := server.GetStats()
	initialTotalConn := initialStats.TotalConnections
	initialTotalMsg := initialStats.TotalMessages

	// Create multiple concurrent clients
	numClients := 20
	messagesPerClient := 50
	var wg sync.WaitGroup
	startTime := time.Now()

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			client := newTestClient(t, addr)
			defer client.Close()

			// Send CER
			client.sendCER(t)
			client.receiveCEA(t)

			// Send multiple DWR messages
			for j := 0; j < messagesPerClient; j++ {
				dwr := base.NewDeviceWatchdogRequest()
				dwr.OriginHost = models_base.DiameterIdentity("test-client.example.com")
				dwr.OriginRealm = models_base.DiameterIdentity("example.com")
				dwr.Header.HopByHopID = uint32(clientID*1000 + j)
				dwr.Header.EndToEndID = uint32(clientID*1000 + j + 10000)

				dwrBytes, err := dwr.Marshal()
				if err != nil {
					t.Errorf("Failed to marshal DWR: %v", err)
					return
				}

				if _, err := client.conn.Write(dwrBytes); err != nil {
					t.Errorf("Failed to send DWR: %v", err)
					return
				}

				// Read DWA
				client.readMessage(t)
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(startTime)

	// Wait for stats to update
	time.Sleep(500 * time.Millisecond)

	// Verify stats
	stats := server.GetStats()
	totalConnections := stats.TotalConnections - initialTotalConn
	totalMessages := stats.TotalMessages - initialTotalMsg
	expectedMessages := uint64(numClients * (1 + messagesPerClient)) // CER + DWRs

	if totalConnections < uint64(numClients) {
		t.Errorf("Expected at least %d connections, got %d", numClients, totalConnections)
	}

	if totalMessages < expectedMessages {
		t.Errorf("Expected at least %d messages, got %d", expectedMessages, totalMessages)
	}

	throughput := float64(totalMessages) / duration.Seconds()
	t.Logf("Performance: %d clients, %d total messages in %v (%.2f msg/sec)",
		numClients, totalMessages, duration, throughput)
	t.Logf("Stats: TotalConn=%d, ActiveConn=%d, TotalMsg=%d, Recv=%d, Sent=%d, Errors=%d",
		stats.TotalConnections, stats.ActiveConnections,
		stats.TotalMessages, stats.MessagesReceived,
		stats.MessagesSent, stats.Errors)

	// Verify active connections are cleaned up
	if stats.ActiveConnections > 0 {
		time.Sleep(1 * time.Second)
		stats = server.GetStats()
		if stats.ActiveConnections > 0 {
			t.Logf("Warning: %d active connections still remain", stats.ActiveConnections)
		}
	}
}

// TestServerPerformanceStatsAccuracy tests that stats accurately reflect server activity
func TestServerPerformanceStatsAccuracy(t *testing.T) {
	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"

	server := NewServer(config, log)
	registerBaseProtocolHandlers(server, t)

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.GetListener().Addr().String()

	// Test 1: Single connection, multiple messages
	client := newTestClient(t, addr)
	defer client.Close()

	initialStats := server.GetStats()
	initialTotalMsg := initialStats.TotalMessages
	initialMsgRecv := initialStats.MessagesReceived
	initialMsgSent := initialStats.MessagesSent

	// Send CER
	client.sendCER(t)
	client.receiveCEA(t)

	time.Sleep(100 * time.Millisecond)
	stats := server.GetStats()

	// After CER/CEA, we should have 1 received and 1 sent message
	if stats.MessagesReceived != initialMsgRecv+1 {
		t.Errorf("Expected MessagesReceived=%d, got %d",
			initialMsgRecv+1, stats.MessagesReceived)
	}
	if stats.MessagesSent != initialMsgSent+1 {
		t.Errorf("Expected MessagesSent=%d, got %d",
			initialMsgSent+1, stats.MessagesSent)
	}
	if stats.TotalMessages != initialTotalMsg+1 {
		t.Errorf("Expected TotalMessages=%d, got %d",
			initialTotalMsg+1, stats.TotalMessages)
	}

	// Send DWR
	dwr := base.NewDeviceWatchdogRequest()
	dwr.OriginHost = models_base.DiameterIdentity("test-client.example.com")
	dwr.OriginRealm = models_base.DiameterIdentity("example.com")
	dwr.Header.HopByHopID = 0x11223344
	dwr.Header.EndToEndID = 0x44332211

	dwrBytes, _ := dwr.Marshal()
	client.conn.Write(dwrBytes)
	client.readMessage(t)

	time.Sleep(100 * time.Millisecond)
	stats = server.GetStats()

	// After DWR/DWA, we should have 2 more messages
	if stats.MessagesReceived != initialMsgRecv+2 {
		t.Errorf("Expected MessagesReceived=%d, got %d",
			initialMsgRecv+2, stats.MessagesReceived)
	}
	if stats.MessagesSent != initialMsgSent+2 {
		t.Errorf("Expected MessagesSent=%d, got %d",
			initialMsgSent+2, stats.MessagesSent)
	}

	// Verify TotalMessages = MessagesReceived (since we're counting all messages)
	if stats.TotalMessages != stats.MessagesReceived {
		t.Errorf("TotalMessages (%d) should equal MessagesReceived (%d)",
			stats.TotalMessages, stats.MessagesReceived)
	}

	// Verify bytes are tracked
	if stats.TotalBytesReceived == 0 {
		t.Error("Expected TotalBytesReceived > 0")
	}
	if stats.TotalBytesSent == 0 {
		t.Error("Expected TotalBytesSent > 0")
	}

	t.Logf("Final stats: TotalMsg=%d, Recv=%d, Sent=%d, BytesRecv=%d, BytesSent=%d",
		stats.TotalMessages, stats.MessagesReceived,
		stats.MessagesSent, stats.TotalBytesReceived,
		stats.TotalBytesSent)
}

// TestServerPerformanceUnderLoad tests server performance under sustained load
func TestServerPerformanceUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	config.MaxConnections = 200

	server := NewServer(config, log)
	registerBaseProtocolHandlers(server, t)

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.GetListener().Addr().String()

	// Run load test for specified duration
	testDuration := 5 * time.Second
	numWorkers := 10
	messagesPerSecondPerWorker := 10 // Messages per second per worker

	initialStats := server.GetStats()
	startTime := time.Now()
	var wg sync.WaitGroup
	var totalSent uint64
	var totalReceived uint64

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			client := newTestClient(t, addr)
			defer client.Close()

			// Send CER
			client.sendCER(t)
			client.receiveCEA(t)
			atomic.AddUint64(&totalSent, 1)
			atomic.AddUint64(&totalReceived, 1)

			// Send messages at specified rate
			ticker := time.NewTicker(time.Second / time.Duration(messagesPerSecondPerWorker))
			defer ticker.Stop()

			for time.Since(startTime) < testDuration {
				<-ticker.C

				dwr := base.NewDeviceWatchdogRequest()
				dwr.OriginHost = models_base.DiameterIdentity("test-client.example.com")
				dwr.OriginRealm = models_base.DiameterIdentity("example.com")
				currentCount := atomic.LoadUint64(&totalSent)
				dwr.Header.HopByHopID = uint32(workerID*10000 + int(currentCount))
				dwr.Header.EndToEndID = uint32(workerID*10000 + int(currentCount) + 50000)

				dwrBytes, err := dwr.Marshal()
				if err != nil {
					t.Errorf("Failed to marshal DWR: %v", err)
					continue
				}

				if _, err := client.conn.Write(dwrBytes); err != nil {
					t.Errorf("Failed to send DWR: %v", err)
					continue
				}

				atomic.AddUint64(&totalSent, 1)

				// Read DWA
				client.readMessage(t)
				atomic.AddUint64(&totalReceived, 1)
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(startTime)

	// Wait for stats to update
	time.Sleep(500 * time.Millisecond)

	// Verify stats
	stats := server.GetStats()
	totalProcessed := stats.TotalMessages - initialStats.TotalMessages

	throughput := float64(totalProcessed) / duration.Seconds()
	t.Logf("Load test results:")
	t.Logf("  Duration: %v", duration)
	t.Logf("  Workers: %d", numWorkers)
	t.Logf("  Total messages processed: %d", totalProcessed)
	t.Logf("  Throughput: %.2f msg/sec", throughput)
	t.Logf("  Stats: TotalConn=%d, ActiveConn=%d, TotalMsg=%d, Recv=%d, Sent=%d, Errors=%d",
		stats.TotalConnections, stats.ActiveConnections,
		stats.TotalMessages, stats.MessagesReceived,
		stats.MessagesSent, stats.Errors)

	// Verify reasonable throughput (at least 50% of target)
	targetThroughput := float64(numWorkers * messagesPerSecondPerWorker)
	if throughput < targetThroughput*0.5 {
		t.Errorf("Throughput %.2f msg/sec is below 50%% of target %.2f msg/sec",
			throughput, targetThroughput)
	}

	// Verify error rate is low
	errorRate := float64(stats.Errors) / float64(totalProcessed) * 100
	if errorRate > 1.0 {
		t.Errorf("Error rate %.2f%% is too high (expected < 1%%)", errorRate)
	}
	t.Logf("  Error rate: %.2f%%", errorRate)
}

// TestServerInterfaceStatsS13 tests stats tracking for S13 interface
func TestServerInterfaceStatsS13(t *testing.T) {
	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"

	server := NewServer(config, log)
	registerBaseProtocolHandlers(server, t)

	// Register S13 ECR handler
	server.HandleFunc(Command{Interface: 16777252, Code: 324, Request: true}, func(msg *Message, conn Conn) {
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

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.GetListener().Addr().String()
	client := newTestClient(t, addr)
	defer client.Close()

	// Send CER with S13 support
	client.sendCERWithS13(t)
	client.receiveCEA(t)

	// Send multiple ECR messages
	numECR := 5
	for i := 0; i < numECR; i++ {
		ecr := s13.NewMEIdentityCheckRequest()
		ecr.SessionId = models_base.UTF8String(fmt.Sprintf("test-client.example.com;%d;1", i))
		ecr.AuthSessionState = models_base.Enumerated(1)
		ecr.OriginHost = models_base.DiameterIdentity("test-client.example.com")
		ecr.OriginRealm = models_base.DiameterIdentity("example.com")
		ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
		ecr.TerminalInformation = &s13.TerminalInformation{
			Imei:            ptrUTF8String("123456789012345"),
			SoftwareVersion: ptrUTF8String("01"),
		}

		ecrBytes, _ := ecr.Marshal()
		client.conn.Write(ecrBytes)
		client.readMessage(t)
	}

	time.Sleep(200 * time.Millisecond)

	// Verify S13 interface stats
	s13Stats, exists := server.GetInterfaceStats(16777252)
	if !exists {
		t.Fatal("S13 interface stats not found")
	}

	if s13Stats.MessagesReceived != uint64(numECR) {
		t.Errorf("Expected %d S13 messages received, got %d", numECR, s13Stats.MessagesReceived)
	}
	if s13Stats.MessagesSent != uint64(numECR) {
		t.Errorf("Expected %d S13 messages sent, got %d", numECR, s13Stats.MessagesSent)
	}
	if s13Stats.BytesReceived == 0 {
		t.Error("Expected S13 bytes received > 0")
	}
	if s13Stats.BytesSent == 0 {
		t.Error("Expected S13 bytes sent > 0")
	}

	// Verify ECR command stats (Code 324)
	ecrStats, exists := s13Stats.CommandStats[324]
	if !exists {
		t.Fatal("ECR command stats not found")
	}

	if ecrStats.MessagesReceived != uint64(numECR) {
		t.Errorf("Expected %d ECR messages received, got %d", numECR, ecrStats.MessagesReceived)
	}
	if ecrStats.MessagesSent != uint64(numECR) {
		t.Errorf("Expected %d ECA messages sent, got %d", numECR, ecrStats.MessagesSent)
	}

	t.Logf("S13 Interface Stats: Recv=%d, Sent=%d, BytesRecv=%d, BytesSent=%d",
		s13Stats.MessagesReceived, s13Stats.MessagesSent,
		s13Stats.BytesReceived, s13Stats.BytesSent)
	t.Logf("ECR Command Stats: Recv=%d, Sent=%d, BytesRecv=%d, BytesSent=%d",
		ecrStats.MessagesReceived, ecrStats.MessagesSent,
		ecrStats.BytesReceived, ecrStats.BytesSent)
}

// TestServerInterfaceStatsS6a tests stats tracking for S6a interface
func TestServerInterfaceStatsS6a(t *testing.T) {
	log := logger.New("test-server", "debug")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"

	server := NewServer(config, log)
	registerBaseProtocolHandlers(server, t)

	// Register S6a AIR handler (Authentication-Information-Request)
	var airHandlerCalled bool
	server.HandleFunc(Command{Interface: 16777251, Code: 318, Request: true}, func(msg *Message, conn Conn) {
		airHandlerCalled = true
		t.Logf("AIR handler called, message length=%d", msg.Length)
		air := &s6a.AuthenticationInformationRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := air.Unmarshal(fullMsg); err != nil {
			t.Logf("Failed to unmarshal AIR: %v", err)
			return
		}

		aia := s6a.NewAuthenticationInformationAnswer()
		aia.SessionId = air.SessionId
		aia.AuthSessionState = air.AuthSessionState
		aia.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		aia.OriginRealm = models_base.DiameterIdentity("example.com")
		aia.Header.HopByHopID = air.Header.HopByHopID
		aia.Header.EndToEndID = air.Header.EndToEndID
		resultCode := models_base.Unsigned32(2001)
		aia.ResultCode = &resultCode

		aiaBytes, err := aia.Marshal()
		if err != nil {
			t.Logf("Failed to marshal AIA: %v", err)
			return
		}
		if _, err := conn.Write(aiaBytes); err != nil {
			t.Logf("Failed to write AIA: %v", err)
		}
		t.Logf("Sent AIA response")
	})

	// Register S6a ULR handler (Update-Location-Request)
	server.HandleFunc(Command{Interface: 16777251, Code: 316, Request: true}, func(msg *Message, conn Conn) {
		ulr := &s6a.UpdateLocationRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		if err := ulr.Unmarshal(fullMsg); err != nil {
			t.Logf("Failed to unmarshal ULR: %v", err)
			return
		}

		ula := s6a.NewUpdateLocationAnswer()
		ula.SessionId = ulr.SessionId
		ula.AuthSessionState = ulr.AuthSessionState
		ula.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		ula.OriginRealm = models_base.DiameterIdentity("example.com")
		ula.Header.HopByHopID = ulr.Header.HopByHopID
		ula.Header.EndToEndID = ulr.Header.EndToEndID
		resultCode := models_base.Unsigned32(2001)
		ula.ResultCode = &resultCode

		ulaBytes, err := ula.Marshal()
		if err != nil {
			t.Logf("Failed to marshal ULA: %v", err)
			return
		}
		if _, err := conn.Write(ulaBytes); err != nil {
			t.Logf("Failed to write ULA: %v", err)
		}
	})

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.GetListener().Addr().String()
	
	// Create client connection directly like TestServerCERCEAExchange does
	clientConn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to connect to server: %v", err)
	}
	defer clientConn.Close()
	
	// client := &testClient{conn: clientConn, t: t}

	// Send CER with S6a support
	cer := base.NewCapabilitiesExchangeRequest()
	cer.OriginHost = models_base.DiameterIdentity("test-client.example.com")
	cer.OriginRealm = models_base.DiameterIdentity("example.com")
	cer.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("127.0.0.1")),
	}
	cer.VendorId = models_base.Unsigned32(10415)
	cer.ProductName = models_base.UTF8String("TestClient/1.0")
	cer.AuthApplicationId = []models_base.Unsigned32{16777251} // S6a
	cer.Header.HopByHopID = 0x12345678
	cer.Header.EndToEndID = 0x87654321
	cerBytes, err := cer.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal CER: %v", err)
	}
	if _, err := clientConn.Write(cerBytes); err != nil {
		t.Fatalf("Failed to send CER: %v", err)
	}
	
	// Use readDiameterMessage directly like TestServerCERCEAExchange does
	ceaBytes := readDiameterMessage(t, clientConn)
	if _, err := clientConn.Write(cerBytes); err != nil {
		t.Fatalf("Failed to send CER: %v", err)
	}
	
	// Use readDiameterMessage directly like TestServerCERCEAExchange does
	ceaBytes = readDiameterMessage(t, clientConn)
	cea := &base.CapabilitiesExchangeAnswer{}
	if err := cea.Unmarshal(ceaBytes); err != nil {
		t.Fatalf("Failed to unmarshal CEA: %v", err)
	}
	if cea.ResultCode != 2001 {
		t.Errorf("CEA failed with ResultCode=%d", cea.ResultCode)
	}

	// Send AIR messages
	numAIR := 3
	for i := 0; i < numAIR; i++ {
		air := s6a.NewAuthenticationInformationRequest()
		air.SessionId = models_base.UTF8String(fmt.Sprintf("test-client.example.com;%d;1", i))
		air.AuthSessionState = models_base.Enumerated(1)
		air.OriginHost = models_base.DiameterIdentity("test-client.example.com")
		air.OriginRealm = models_base.DiameterIdentity("example.com")
		air.DestinationRealm = models_base.DiameterIdentity("example.com")
		air.UserName = models_base.UTF8String("452040000000023")
		air.VisitedPlmnId = models_base.OctetString("45204")

		air.Header.HopByHopID = uint32(0x1000 + i)
		air.Header.EndToEndID = uint32(0x2000 + i)

		airBytes, err := air.Marshal()
		if err != nil {
			t.Fatalf("Failed to marshal AIR: %v", err)
		}
		if _, err := clientConn.Write(airBytes); err != nil {
			t.Fatalf("Failed to send AIR: %v", err)
		}

		readDiameterMessage(t, clientConn)
	}

	// Send ULR messages
	numULR := 2
	for i := 0; i < numULR; i++ {
		ulr := s6a.NewUpdateLocationRequest()
		ulr.SessionId = models_base.UTF8String(fmt.Sprintf("test-client.example.com;%d;1", i+100))
		ulr.AuthSessionState = models_base.Enumerated(1)
		ulr.OriginHost = models_base.DiameterIdentity("test-client.example.com")
		ulr.OriginRealm = models_base.DiameterIdentity("example.com")
		ulr.DestinationRealm = models_base.DiameterIdentity("example.com")

		ulr.UserName = models_base.UTF8String("452040000000023")
		ulr.VisitedPlmnId = models_base.OctetString("45204")
		ulr.Header.HopByHopID = uint32(0x3000 + i)
		ulr.Header.EndToEndID = uint32(0x4000 + i)

		data, err := ulr.Marshal()
		if err != nil {
			t.Fatalf("Failed to marshal ULR: %v", err)
		}
		if _, err := clientConn.Write(data); err != nil {
			t.Fatalf("Failed to send ULR: %v", err)
		}

		readDiameterMessage(t, clientConn)
	}

	time.Sleep(200 * time.Millisecond)

	// Verify handler was called
	if !airHandlerCalled {
		t.Error("AIR handler was not called - messages may not be reaching handlers")
	}

	// Verify S6a interface stats
	s6aStats, exists := server.GetInterfaceStats(16777251)
	if !exists {
		t.Fatal("S6a interface stats not found")
	}

	expectedTotal := uint64(numAIR + numULR)
	if s6aStats.MessagesReceived != expectedTotal {
		t.Errorf("Expected %d S6a messages received, got %d", expectedTotal, s6aStats.MessagesReceived)
	}
	if s6aStats.MessagesSent != expectedTotal {
		t.Errorf("Expected %d S6a messages sent, got %d", expectedTotal, s6aStats.MessagesSent)
	}

	// Verify AIR command stats (Code 318)
	airStats, exists := s6aStats.CommandStats[318]
	if !exists {
		t.Fatal("AIR command stats not found")
	}
	if airStats.MessagesReceived != uint64(numAIR) {
		t.Errorf("Expected %d AIR messages received, got %d", numAIR, airStats.MessagesReceived)
	}

	// Verify ULR command stats (Code 316)
	ulrStats, exists := s6aStats.CommandStats[316]
	if !exists {
		t.Fatal("ULR command stats not found")
	}
	if ulrStats.MessagesReceived != uint64(numULR) {
		t.Errorf("Expected %d ULR messages received, got %d", numULR, ulrStats.MessagesReceived)
	}

	t.Logf("S6a Interface Stats: Recv=%d, Sent=%d", s6aStats.MessagesReceived, s6aStats.MessagesSent)
	t.Logf("AIR Command Stats: Recv=%d, Sent=%d", airStats.MessagesReceived, airStats.MessagesSent)
	t.Logf("ULR Command Stats: Recv=%d, Sent=%d", ulrStats.MessagesReceived, ulrStats.MessagesSent)
}

// TestServerMultipleInterfacesStats tests stats tracking across multiple interfaces
func TestServerMultipleInterfacesStats(t *testing.T) {
	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"

	server := NewServer(config, log)
	registerBaseProtocolHandlers(server, t)

	// Register S13 handler
	server.HandleFunc(Command{Interface: 16777252, Code: 324, Request: true}, func(msg *Message, conn Conn) {
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

	// Register S6a handler
	server.HandleFunc(Command{Interface: 16777251, Code: 318, Request: true}, func(msg *Message, conn Conn) {
		air := &s6a.AuthenticationInformationRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		air.Unmarshal(fullMsg)

		aia := s6a.NewAuthenticationInformationAnswer()
		aia.SessionId = air.SessionId
		aia.AuthSessionState = air.AuthSessionState
		aia.OriginHost = models_base.DiameterIdentity("test-server.example.com")
		aia.OriginRealm = models_base.DiameterIdentity("example.com")
		aia.Header.HopByHopID = air.Header.HopByHopID
		aia.Header.EndToEndID = air.Header.EndToEndID
		resultCode := models_base.Unsigned32(2001)
		aia.ResultCode = &resultCode

		aiaBytes, _ := aia.Marshal()
		conn.Write(aiaBytes)
	})

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.GetListener().Addr().String()

	// Create client for S13
	clientS13 := newTestClient(t, addr)
	defer clientS13.Close()
	clientS13.sendCERWithS13(t)
	clientS13.receiveCEA(t)

	// Create client for S6a
	clientS6a := newTestClient(t, addr)
	defer clientS6a.Close()
	cer := base.NewCapabilitiesExchangeRequest()
	cer.OriginHost = models_base.DiameterIdentity("test-client.example.com")
	cer.OriginRealm = models_base.DiameterIdentity("example.com")
	cer.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("127.0.0.1")),
	}
	cer.VendorId = models_base.Unsigned32(10415)
	cer.ProductName = models_base.UTF8String("TestClient/1.0")
	cer.AuthApplicationId = []models_base.Unsigned32{16777251} // S6a
	cerBytes, _ := cer.Marshal()
	clientS6a.conn.Write(cerBytes)
	clientS6a.receiveCEA(t)

	// Send S13 messages
	numS13 := 5
	for i := 0; i < numS13; i++ {
		ecr := s13.NewMEIdentityCheckRequest()
		ecr.SessionId = models_base.UTF8String(fmt.Sprintf("test-client.example.com;%d;1", i))
		ecr.AuthSessionState = models_base.Enumerated(1)
		ecr.OriginHost = models_base.DiameterIdentity("test-client.example.com")
		ecr.OriginRealm = models_base.DiameterIdentity("example.com")
		ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
		ecr.TerminalInformation = &s13.TerminalInformation{
			Imei:            ptrUTF8String("123456789012345"),
			SoftwareVersion: ptrUTF8String("01"),
		}
		ecrBytes, _ := ecr.Marshal()
		clientS13.conn.Write(ecrBytes)
		clientS13.readMessage(t)
	}

	// Send S6a messages
	numS6a := 3
	for i := 0; i < numS6a; i++ {
		air := s6a.NewAuthenticationInformationRequest()
		air.SessionId = models_base.UTF8String(fmt.Sprintf("test-client.example.com;%d;1", i))
		air.AuthSessionState = models_base.Enumerated(1)
		air.OriginHost = models_base.DiameterIdentity("test-client.example.com")
		air.OriginRealm = models_base.DiameterIdentity("example.com")
		air.DestinationRealm = models_base.DiameterIdentity("example.com")
		air.UserName = models_base.UTF8String("452040000000023")
		air.VisitedPlmnId = models_base.OctetString("45204")

		air.Header.HopByHopID = uint32(0x1000 + i)
		air.Header.EndToEndID = uint32(0x2000 + i)

		airBytes, err := air.Marshal()
		if err != nil {
			t.Fatal("marshal air error", err)
		}
		clientS6a.conn.Write(airBytes)
		clientS6a.readMessage(t)
	}

	time.Sleep(200 * time.Millisecond)

	// Verify overall stats
	stats := server.GetStats()
	expectedTotal := uint64(numS13 + numS6a + 2) // 2 for cer
	if stats.MessagesReceived != expectedTotal {
		t.Errorf("Expected %d total messages received, got %d", expectedTotal, stats.MessagesReceived)
	}

	// Verify S13 stats
	s13Stats, exists := server.GetInterfaceStats(16777252)
	if !exists {
		t.Fatal("S13 interface stats not found")
	}
	if s13Stats.MessagesReceived != uint64(numS13) {
		t.Errorf("Expected %d S13 messages, got %d", numS13, s13Stats.MessagesReceived)
	}

	// Verify S6a stats
	s6aStats, exists := server.GetInterfaceStats(16777251)
	if !exists {
		t.Fatal("S6a interface stats not found")
	}
	if s6aStats.MessagesReceived != uint64(numS6a) {
		t.Errorf("Expected %d S6a messages, got %d", numS6a, s6aStats.MessagesReceived)
	}

	// Verify command-level stats
	ecrStats, exists := server.GetCommandStats(16777252, 324)
	if !exists || ecrStats.MessagesReceived != uint64(numS13) {
		t.Errorf("ECR command stats incorrect: exists=%v, recv=%d", exists, ecrStats.MessagesReceived)
	}

	airStats, exists := server.GetCommandStats(16777251, 318)
	if !exists || airStats.MessagesReceived != uint64(numS6a) {
		t.Errorf("AIR command stats incorrect: exists=%v, recv=%d", exists, airStats.MessagesReceived)
	}

	t.Logf("Total Stats: Recv=%d, Sent=%d", stats.MessagesReceived, stats.MessagesSent)
	t.Logf("S13 Stats: Recv=%d, Sent=%d", s13Stats.MessagesReceived, s13Stats.MessagesSent)
	t.Logf("S6a Stats: Recv=%d, Sent=%d", s6aStats.MessagesReceived, s6aStats.MessagesSent)
}

// TestServerBaseProtocolInterfaceStats tests stats tracking for base protocol (interface 0)
func TestServerBaseProtocolInterfaceStats(t *testing.T) {
	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	config.ConnectionConfig.HandleWatchdog = true

	server := NewServer(config, log)
	registerBaseProtocolHandlers(server, t)

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.GetListener().Addr().String()
	client := newTestClient(t, addr)
	defer client.Close()

	// Send CER
	client.sendCER(t)
	client.receiveCEA(t)

	// Send DWR
	dwr := base.NewDeviceWatchdogRequest()
	dwr.OriginHost = models_base.DiameterIdentity("test-client.example.com")
	dwr.OriginRealm = models_base.DiameterIdentity("example.com")
	dwr.Header.HopByHopID = 0x11223344
	dwr.Header.EndToEndID = 0x44332211
	dwrBytes, _ := dwr.Marshal()
	client.conn.Write(dwrBytes)
	client.readMessage(t)

	time.Sleep(200 * time.Millisecond)

	// Verify base protocol interface stats (Application ID 0)
	baseStats, exists := server.GetInterfaceStats(0)
	if !exists {
		t.Fatal("Base protocol interface stats not found")
	}

	// Should have CER (257) and DWR (280)
	if baseStats.MessagesReceived < 2 {
		t.Errorf("Expected at least 2 base protocol messages received, got %d", baseStats.MessagesReceived)
	}

	// Verify CER command stats
	cerStats, exists := baseStats.CommandStats[257]
	if !exists {
		t.Fatal("CER command stats not found")
	}
	if cerStats.MessagesReceived == 0 {
		t.Error("Expected CER messages received > 0")
	}

	// Verify DWR command stats
	dwrStats, exists := baseStats.CommandStats[280]
	if !exists {
		t.Fatal("DWR command stats not found")
	}
	if dwrStats.MessagesReceived == 0 {
		t.Error("Expected DWR messages received > 0")
	}

	t.Logf("Base Protocol Stats: Recv=%d, Sent=%d", baseStats.MessagesReceived, baseStats.MessagesSent)
	t.Logf("CER Command Stats: Recv=%d, Sent=%d", cerStats.MessagesReceived, cerStats.MessagesSent)
	t.Logf("DWR Command Stats: Recv=%d, Sent=%d", dwrStats.MessagesReceived, dwrStats.MessagesSent)
}
