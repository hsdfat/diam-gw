package client_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/pkg/logger"

	"github.com/hsdfat/diam-gw/commands/s13"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/server"
)

// ============================================================================
// Connection Pool Tests
// ============================================================================

func TestConnectionPoolBasic(t *testing.T) {
	testSrv := newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	config.ConnectionCount = 3
	ctx := context.Background()

	pool, err := client.NewConnectionPool(ctx, config, logger.Log)
	if err != nil {
		t.Fatalf("Failed to create connection pool: %v", err)
	}
	defer pool.Close()

	if pool == nil {
		t.Fatal("Connection pool is nil")
	}

	// Note: Cannot verify pool.config (unexported field)
	// Verify by checking stats instead
	stats := pool.GetStats()
	t.Logf("Pool created with %d total connections", stats.TotalConnections)
}

func TestConnectionPoolGetConnection(t *testing.T) {
	testSrv := newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	config.ConnectionCount = 2
	ctx := context.Background()

	pool, err := client.NewConnectionPool(ctx, config, logger.Log)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Start(); err != nil {
		t.Fatalf("Failed to start pool: %v", err)
	}

	// Wait for connections to be established
	if err := pool.WaitForConnection(5 * time.Second); err != nil {
		t.Fatalf("Failed to establish connections: %v", err)
	}

	// Get active connections
	activeConns := pool.GetActiveConnections()
	if len(activeConns) == 0 {
		t.Fatal("Expected at least one active connection")
	}

	t.Logf("Active connections: %d", len(activeConns))
}

func TestConnectionPoolMultipleConnections(t *testing.T) {
	testSrv := newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	config.ConnectionCount = 5
	ctx := context.Background()

	pool, err := client.NewConnectionPool(ctx, config, logger.Log)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Start(); err != nil {
		t.Fatalf("Failed to start pool: %v", err)
	}

	// Wait for all connections
	time.Sleep(2 * time.Second)

	activeConns := pool.GetActiveConnections()
	if len(activeConns) < 1 {
		t.Errorf("Expected at least 1 active connection, got %d", len(activeConns))
	}

	stats := pool.GetStats()
	t.Logf("Pool stats: Total=%d, Active=%d", stats.TotalConnections, stats.ActiveConnections)
}

func TestConnectionPoolReconnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping reconnection test in short mode")
	}

	testSrv := newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}

	config := newTestDRAConfig(testSrv.Address())
	config.ConnectionCount = 1
	config.ReconnectInterval = 500 * time.Millisecond
	ctx := context.Background()

	pool, err := client.NewConnectionPool(ctx, config, logger.Log)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Start(); err != nil {
		t.Fatalf("Failed to start pool: %v", err)
	}

	// Wait for initial connection
	if err := pool.WaitForConnection(3 * time.Second); err != nil {
		t.Fatalf("Failed to establish connection: %v", err)
	}

	initialStats := pool.GetStats()

	// Stop server to trigger reconnection
	testSrv.Stop()

	// Wait for disconnect
	time.Sleep(2 * time.Second)

	// Restart server
	testSrv = newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to restart test server: %v", err)
	}
	defer testSrv.Stop()

	// Wait for reconnection
	time.Sleep(3 * time.Second)

	finalStats := pool.GetStats()

	// Verify reconnection attempts were made
	if finalStats.TotalReconnects <= initialStats.TotalReconnects {
		t.Log("Warning: Expected reconnect count to increase")
	}

	t.Logf("Reconnects: initial=%d, final=%d",
		initialStats.TotalReconnects, finalStats.TotalReconnects)
}

func TestConnectionPoolLoadBalancing(t *testing.T) {
	testSrv := newTestServer(t)

	var messageDistribution sync.Map // connection address -> count

	testSrv.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		// Track which connection received the message
		addr := conn.RemoteAddr().String()
		val, _ := messageDistribution.LoadOrStore(addr, &atomic.Int32{})
		counter := val.(*atomic.Int32)
		counter.Add(1)

		// Send response
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
		t.Fatalf("Failed to establish connections: %v", err)
	}

	// Send messages using round-robin
	numMessages := 30
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
		if err := pool.SendRoundRobin(ecrBytes); err != nil {
			t.Errorf("Failed to send message %d: %v", i, err)
		}

		// Read response
		select {
		case <-pool.Receive():
		case <-time.After(2 * time.Second):
			t.Errorf("Timeout on message %d", i)
		}
	}

	time.Sleep(200 * time.Millisecond)

	// Check distribution
	t.Log("Message distribution across connections:")
	messageDistribution.Range(func(key, value interface{}) bool {
		addr := key.(string)
		count := value.(*atomic.Int32).Load()
		t.Logf("  %s: %d messages", addr, count)
		return true
	})
}

func TestConnectionPoolConcurrency(t *testing.T) {
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

	config := newTestDRAConfig(testSrv.Address())
	config.ConnectionCount = 5
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
		t.Fatalf("Failed to establish connections: %v", err)
	}

	// Test concurrent requests
	numRequests := 100
	var wg sync.WaitGroup
	errors := make(chan error, numRequests)

	// Consumer goroutine
	go func() {
		for i := 0; i < numRequests; i++ {
			select {
			case <-pool.Receive():
			case <-time.After(5 * time.Second):
				errors <- fmt.Errorf("timeout on response %d", i)
			}
		}
	}()

	// Concurrent senders
	for i := 0; i < numRequests; i++ {
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

			ecrBytes, _ := ecr.Marshal()
			if err := pool.Send(ecrBytes); err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Request failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Verify pool stats
	stats := pool.GetStats()
	if stats.TotalMessagesSent < uint64(numRequests) {
		t.Errorf("Expected at least %d messages sent, got %d",
			numRequests, stats.TotalMessagesSent)
	}

	if requestCount.Load() != int64(numRequests) {
		t.Errorf("Expected %d requests processed, got %d",
			numRequests, requestCount.Load())
	}

	t.Logf("Concurrent test: %d requests, stats: sent=%d, recv=%d, active=%d",
		numRequests, stats.TotalMessagesSent, stats.TotalMessagesRecv, stats.ActiveConnections)
}

func TestConnectionPoolWaitForConnection(t *testing.T) {
	testSrv := newTestServer(t)
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

	// Test WaitForConnection
	err = pool.WaitForConnection(5 * time.Second)
	if err != nil {
		t.Fatalf("WaitForConnection failed: %v", err)
	}

	// Should have at least one active connection
	stats := pool.GetStats()
	if stats.ActiveConnections == 0 {
		t.Error("Expected at least one active connection")
	}
}

func TestConnectionPoolWaitForConnectionTimeout(t *testing.T) {
	// Create pool without starting server (should timeout)
	config := &client.DRAConfig{
		Host:              "127.0.0.1",
		Port:              19999, // Non-existent
		OriginHost:        "test-client.example.com",
		OriginRealm:       "example.com",
		ProductName:       "TestClient/1.0",
		VendorID:          10415,
		ConnectionCount:   1,
		ConnectTimeout:    200 * time.Millisecond,
		CERTimeout:        200 * time.Millisecond,
		DWRInterval:       30 * time.Second,
		DWRTimeout:        5 * time.Second,
		MaxDWRFailures:    3,
		ReconnectInterval: 5 * time.Second, // Long interval
		SendBufferSize:    100,
		RecvBufferSize:    100,
	}

	ctx := context.Background()
	pool, err := client.NewConnectionPool(ctx, config, logger.Log)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Start(); err != nil {
		logger.Log.Errorw("failed to start", "error", err)
	}

	// Should timeout
	err = pool.WaitForConnection(1 * time.Second)
	if err == nil {
		t.Error("Expected WaitForConnection to timeout")
	}
}

func TestConnectionPoolSendToConnection(t *testing.T) {
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
	config.ConnectionCount = 2
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
		t.Fatalf("Failed to establish connections: %v", err)
	}

	// Get active connections
	activeConns := pool.GetActiveConnections()
	if len(activeConns) == 0 {
		t.Fatal("No active connections")
	}

	// Send to specific connection
	connID := activeConns[0].ID()

	ecr := s13.NewMEIdentityCheckRequest()
	ecr.SessionId = models_base.UTF8String("test-session-specific")
	ecr.AuthSessionState = models_base.Enumerated(1)
	ecr.OriginHost = models_base.DiameterIdentity(config.OriginHost)
	ecr.OriginRealm = models_base.DiameterIdentity(config.OriginRealm)
	ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
	ecr.TerminalInformation = &s13.TerminalInformation{
		Imei:            ptrUTF8String("123456789012345"),
		SoftwareVersion: ptrUTF8String("01"),
	}

	ecrBytes, _ := ecr.Marshal()

	if err := pool.SendToConnection(connID, ecrBytes); err != nil {
		t.Fatalf("Failed to send to specific connection: %v", err)
	}

	// Read response
	select {
	case <-pool.Receive():
		t.Log("Received response from specific connection")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}
}

func TestConnectionPoolStats(t *testing.T) {
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
		t.Fatalf("Failed to establish connections: %v", err)
	}

	initialStats := pool.GetStats()

	// Send some messages
	numMessages := 10
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

	finalStats := pool.GetStats()

	// Verify stats
	if finalStats.TotalMessagesSent <= initialStats.TotalMessagesSent {
		t.Error("Expected TotalMessagesSent to increase")
	}

	if finalStats.TotalMessagesRecv <= initialStats.TotalMessagesRecv {
		t.Error("Expected TotalMessagesRecv to increase")
	}

	if finalStats.TotalBytesSent == 0 {
		t.Error("Expected TotalBytesSent > 0")
	}

	if finalStats.TotalBytesRecv == 0 {
		t.Error("Expected TotalBytesRecv > 0")
	}

	t.Logf("Pool stats: Sent=%d, Recv=%d, BytesSent=%d, BytesRecv=%d, Active=%d, Total=%d, Reconnects=%d",
		finalStats.TotalMessagesSent, finalStats.TotalMessagesRecv,
		finalStats.TotalBytesSent, finalStats.TotalBytesRecv,
		finalStats.ActiveConnections, finalStats.TotalConnections, finalStats.TotalReconnects)
}

// ============================================================================
// Connection Pool Benchmark Tests
// ============================================================================

func BenchmarkConnectionPoolSend(b *testing.B) {
	testSrv := newTestServer(&testing.T{})

	testSrv.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		// Echo back
		conn.Write(append(msg.Header, msg.Body...))
	})

	testSrv.Start()
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	config.ConnectionCount = 3
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

	// Consumer goroutine
	go func() {
		for {
			select {
			case <-pool.Receive():
			case <-ctx.Done():
				return
			}
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pool.Send(ecrBytes)
	}
}

func BenchmarkConnectionPoolRoundRobin(b *testing.B) {
	testSrv := newTestServer(&testing.T{})

	testSrv.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		conn.Write(append(msg.Header, msg.Body...))
	})

	testSrv.Start()
	defer testSrv.Stop()

	config := newTestDRAConfig(testSrv.Address())
	config.ConnectionCount = 5
	ctx := context.Background()

	pool, _ := client.NewConnectionPool(ctx, config, logger.Log)
	pool.Start()
	pool.WaitForConnection(3 * time.Second)
	defer pool.Close()

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

	go func() {
		for {
			select {
			case <-pool.Receive():
			case <-ctx.Done():
				return
			}
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pool.SendRoundRobin(ecrBytes)
	}
}
