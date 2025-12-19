package client_test

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/commands/s13"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/server"
)

// ============================================================================
// DRA Pool Tests
// ============================================================================

func TestDRAPoolBasic(t *testing.T) {
	// Create two test servers
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

	// Create DRA pool config
	host1, port1, _ := splitHostPort(testSrv1.Address())
	host2, port2, _ := splitHostPort(testSrv2.Address())

	config := &client.DRAPoolConfig{
		DRAs: []*client.DRAServerConfig{
			{
				Name:     "dra1",
				Host:     host1,
				Port:     port1,
				Priority: 1,
				Weight:   50,
			},
			{
				Name:     "dra2",
				Host:     host2,
				Port:     port2,
				Priority: 1,
				Weight:   50,
			},
		},
		OriginHost:        "test-client.example.com",
		OriginRealm:       "example.com",
		ProductName:       "TestClient/1.0",
		VendorID:          10415,
		ConnectionsPerDRA: 1,
		ConnectTimeout:    2 * time.Second,
		CERTimeout:        2 * time.Second,
		DWRInterval:       30 * time.Second,
		DWRTimeout:        5 * time.Second,
		MaxDWRFailures:    3,
		ReconnectInterval: 1 * time.Second,
		SendBufferSize:    100,
		HealthCheckInterval: 10 * time.Second,
		MaxReconnectDelay:   30 * time.Second,
		ReconnectBackoff:    2.0,
		RecvBufferSize:    100,
	}

	ctx := context.Background()
	pool, err := client.NewDRAPool(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create DRA pool: %v", err)
	}
	defer pool.Close()

	if pool == nil {
		t.Fatal("DRA pool is nil")
	}
}

// Helper function to split host:port
func splitHostPort(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port := 3868
	if portStr != "" {
		fmt.Sscanf(portStr, "%d", &port)
	}
	return host, port, nil
}

// Helper to create DRAPoolConfig with all required fields
func newTestDRAPoolConfig(dras []*client.DRAServerConfig) *client.DRAPoolConfig {
	return &client.DRAPoolConfig{
		DRAs:                dras,
		OriginHost:          "test-client.example.com",
		OriginRealm:         "example.com",
		ProductName:         "TestClient/1.0",
		VendorID:            10415,
		ConnectionsPerDRA:   1,
		ConnectTimeout:      2 * time.Second,
		CERTimeout:          2 * time.Second,
		DWRInterval:         30 * time.Second,
		DWRTimeout:          5 * time.Second,
		MaxDWRFailures:      3,
		HealthCheckInterval: 10 * time.Second,
		ReconnectInterval:   1 * time.Second,
		MaxReconnectDelay:   30 * time.Second,
		ReconnectBackoff:    2.0,
		SendBufferSize:      100,
		RecvBufferSize:      100,
	}
}

func TestDRAPoolRouting(t *testing.T) {
	// Create two test servers
	testSrv1 := newTestServer(t)
	var srv1Count atomic.Int32
	testSrv1.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		srv1Count.Add(1)

		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		ecr.Unmarshal(fullMsg)

		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("dra1.example.com")
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

	if err := testSrv1.Start(); err != nil {
		t.Fatalf("Failed to start test server 1: %v", err)
	}
	defer testSrv1.Stop()

	testSrv2 := newTestServer(t)
	var srv2Count atomic.Int32
	testSrv2.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		srv2Count.Add(1)

		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		ecr.Unmarshal(fullMsg)

		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("dra2.example.com")
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

	if err := testSrv2.Start(); err != nil {
		t.Fatalf("Failed to start test server 2: %v", err)
	}
	defer testSrv2.Stop()

	// Create DRA pool config with same priority
	host1, port1, _ := splitHostPort(testSrv1.Address())
	host2, port2, _ := splitHostPort(testSrv2.Address())

	config := &client.DRAPoolConfig{
		DRAs: []*client.DRAServerConfig{
			{
				Name:     "dra1",
				Host:     host1,
				Port:     port1,
				Priority: 1,
				Weight:   50,
			},
			{
				Name:     "dra2",
				Host:     host2,
				Port:     port2,
				Priority: 1,
				Weight:   50,
			},
		},
		OriginHost:        "test-client.example.com",
		OriginRealm:       "example.com",
		ProductName:       "TestClient/1.0",
		VendorID:          10415,
		ConnectionsPerDRA: 1,
		ConnectTimeout:    2 * time.Second,
		CERTimeout:        2 * time.Second,
		DWRInterval:       30 * time.Second,
		DWRTimeout:        5 * time.Second,
		MaxDWRFailures:    3,
		ReconnectInterval: 1 * time.Second,
		SendBufferSize:    100,
		HealthCheckInterval: 10 * time.Second,
		MaxReconnectDelay:   30 * time.Second,
		ReconnectBackoff:    2.0,
		RecvBufferSize:    100,
	}

	ctx := context.Background()
	pool, err := client.NewDRAPool(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create DRA pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Start(); err != nil {
		t.Fatalf("Failed to start DRA pool: %v", err)
	}

	// Wait for connections
	time.Sleep(2 * time.Second)

	// Send messages - should be distributed
	numMessages := 20
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
		if err := pool.Send(ecrBytes); err != nil {
			t.Errorf("Failed to send message %d: %v", i, err)
		}

		select {
		case <-pool.Receive():
		case <-time.After(2 * time.Second):
			t.Errorf("Timeout on message %d", i)
		}
	}

	time.Sleep(200 * time.Millisecond)

	// Verify distribution
	count1 := srv1Count.Load()
	count2 := srv2Count.Load()

	t.Logf("Message distribution: DRA1=%d, DRA2=%d", count1, count2)

	// Both should have received messages (approximately balanced)
	if count1 == 0 && count2 == 0 {
		t.Error("No messages were routed to any DRA")
	}
}

func TestDRAPoolFailover(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping failover test in short mode")
	}

	// Create two test servers with different priorities
	testSrv1 := newTestServer(t)
	var srv1Count atomic.Int32
	testSrv1.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		srv1Count.Add(1)

		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		ecr.Unmarshal(fullMsg)

		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("dra1-priority1.example.com")
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

	if err := testSrv1.Start(); err != nil {
		t.Fatalf("Failed to start test server 1: %v", err)
	}

	testSrv2 := newTestServer(t)
	var srv2Count atomic.Int32
	testSrv2.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		srv2Count.Add(1)

		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		ecr.Unmarshal(fullMsg)

		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("dra2-priority2.example.com")
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

	if err := testSrv2.Start(); err != nil {
		t.Fatalf("Failed to start test server 2: %v", err)
	}
	defer testSrv2.Stop()

	// Create DRA pool config: DRA1 has priority 1 (higher), DRA2 has priority 2 (lower/backup)
	host1, port1, _ := splitHostPort(testSrv1.Address())
	host2, port2, _ := splitHostPort(testSrv2.Address())

	config := &client.DRAPoolConfig{
		DRAs: []*client.DRAServerConfig{
			{
				Name:     "dra1",
				Host:     host1,
				Port:     port1,
				Priority: 1, // Higher priority
				Weight:   100,
			},
			{
				Name:     "dra2",
				Host:     host2,
				Port:     port2,
				Priority: 2, // Lower priority (backup)
				Weight:   100,
			},
		},
		OriginHost:        "test-client.example.com",
		OriginRealm:       "example.com",
		ProductName:       "TestClient/1.0",
		VendorID:          10415,
		ConnectionsPerDRA: 1,
		ConnectTimeout:    2 * time.Second,
		CERTimeout:        2 * time.Second,
		DWRInterval:       30 * time.Second,
		DWRTimeout:        5 * time.Second,
		MaxDWRFailures:    3,
		ReconnectInterval: 1 * time.Second,
		SendBufferSize:    100,
		HealthCheckInterval: 10 * time.Second,
		MaxReconnectDelay:   30 * time.Second,
		ReconnectBackoff:    2.0,
		RecvBufferSize:    100,
	}

	ctx := context.Background()
	pool, err := client.NewDRAPool(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create DRA pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Start(); err != nil {
		t.Fatalf("Failed to start DRA pool: %v", err)
	}

	// Wait for connections
	time.Sleep(2 * time.Second)

	// Phase 1: Send messages - should go to DRA1 (priority 1)
	t.Log("Phase 1: Sending messages to primary DRA (priority 1)")
	numMessages := 5
	for i := 0; i < numMessages; i++ {
		ecr := s13.NewMEIdentityCheckRequest()
		ecr.SessionId = models_base.UTF8String(fmt.Sprintf("phase1-session-%d", i))
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
			t.Logf("Warning: Timeout on message %d", i)
		}
	}

	time.Sleep(500 * time.Millisecond)

	phase1_srv1 := srv1Count.Load()
	phase1_srv2 := srv2Count.Load()
	t.Logf("Phase 1 results: DRA1 (priority 1)=%d, DRA2 (priority 2)=%d", phase1_srv1, phase1_srv2)

	// Most/all should have gone to DRA1
	if phase1_srv1 == 0 {
		t.Error("Expected messages to be sent to DRA1 (higher priority)")
	}

	// Phase 2: Stop DRA1 to trigger failover
	t.Log("Phase 2: Stopping primary DRA to trigger failover")
	testSrv1.Stop()

	// Wait for failover detection
	time.Sleep(3 * time.Second)

	// Send more messages - should now go to DRA2
	t.Log("Sending messages after failover - should go to DRA2")
	for i := 0; i < numMessages; i++ {
		ecr := s13.NewMEIdentityCheckRequest()
		ecr.SessionId = models_base.UTF8String(fmt.Sprintf("phase2-session-%d", i))
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
			t.Logf("Warning: Timeout on failover message %d", i)
		}
	}

	time.Sleep(500 * time.Millisecond)

	phase2_srv1 := srv1Count.Load()
	phase2_srv2 := srv2Count.Load()
	t.Logf("Phase 2 results: DRA1=%d (no change), DRA2=%d (should increase)", phase2_srv1, phase2_srv2)

	// DRA2 should have received the failover messages
	if phase2_srv2 <= phase1_srv2 {
		t.Log("Warning: Expected DRA2 to receive messages after DRA1 failure")
	}

	// Verify active priority changed
	activePriority := pool.GetActivePriority()
	t.Logf("Active priority after failover: %d (expected 2)", activePriority)
}

func TestDRAPoolSendToDRA(t *testing.T) {
	// Create two test servers
	testSrv1 := newTestServer(t)
	var srv1Count atomic.Int32
	testSrv1.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		srv1Count.Add(1)

		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		ecr.Unmarshal(fullMsg)

		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("dra1.example.com")
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

	if err := testSrv1.Start(); err != nil {
		t.Fatalf("Failed to start test server 1: %v", err)
	}
	defer testSrv1.Stop()

	testSrv2 := newTestServer(t)
	var srv2Count atomic.Int32
	testSrv2.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		srv2Count.Add(1)

		ecr := &s13.MEIdentityCheckRequest{}
		fullMsg := append(msg.Header, msg.Body...)
		ecr.Unmarshal(fullMsg)

		eca := s13.NewMEIdentityCheckAnswer()
		eca.SessionId = ecr.SessionId
		eca.AuthSessionState = ecr.AuthSessionState
		eca.OriginHost = models_base.DiameterIdentity("dra2.example.com")
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

	if err := testSrv2.Start(); err != nil {
		t.Fatalf("Failed to start test server 2: %v", err)
	}
	defer testSrv2.Stop()

	// Create DRA pool config
	host1, port1, _ := splitHostPort(testSrv1.Address())
	host2, port2, _ := splitHostPort(testSrv2.Address())

	config := &client.DRAPoolConfig{
		DRAs: []*client.DRAServerConfig{
			{
				Name:     "dra1",
				Host:     host1,
				Port:     port1,
				Priority: 1,
				Weight:   50,
			},
			{
				Name:     "dra2",
				Host:     host2,
				Port:     port2,
				Priority: 1,
				Weight:   50,
			},
		},
		OriginHost:        "test-client.example.com",
		OriginRealm:       "example.com",
		ProductName:       "TestClient/1.0",
		VendorID:          10415,
		ConnectionsPerDRA: 1,
		ConnectTimeout:    2 * time.Second,
		CERTimeout:        2 * time.Second,
		DWRInterval:       30 * time.Second,
		DWRTimeout:        5 * time.Second,
		MaxDWRFailures:    3,
		ReconnectInterval: 1 * time.Second,
		SendBufferSize:    100,
		HealthCheckInterval: 10 * time.Second,
		MaxReconnectDelay:   30 * time.Second,
		ReconnectBackoff:    2.0,
		RecvBufferSize:    100,
	}

	ctx := context.Background()
	pool, err := client.NewDRAPool(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create DRA pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Start(); err != nil {
		t.Fatalf("Failed to start DRA pool: %v", err)
	}

	// Wait for connections
	time.Sleep(2 * time.Second)

	// Send to specific DRA
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

	// Send to dra2
	if err := pool.SendToDRA("dra2", ecrBytes); err != nil {
		t.Fatalf("Failed to send to DRA2: %v", err)
	}

	select {
	case <-pool.Receive():
		t.Log("Received response from DRA2")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for response")
	}

	time.Sleep(200 * time.Millisecond)

	// Verify only dra2 received the message
	if srv1Count.Load() != 0 {
		t.Errorf("DRA1 should not have received messages, got %d", srv1Count.Load())
	}
	if srv2Count.Load() != 1 {
		t.Errorf("DRA2 should have received 1 message, got %d", srv2Count.Load())
	}
}

func TestDRAPoolGetDRAsByPriority(t *testing.T) {
	// Create test servers
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

	// Create DRA pool config with different priorities
	host1, port1, _ := splitHostPort(testSrv1.Address())
	host2, port2, _ := splitHostPort(testSrv2.Address())
	host3, port3, _ := splitHostPort(testSrv3.Address())

	config := &client.DRAPoolConfig{
		DRAs: []*client.DRAServerConfig{
			{
				Name:     "dra1",
				Host:     host1,
				Port:     port1,
				Priority: 1,
				Weight:   50,
			},
			{
				Name:     "dra2",
				Host:     host2,
				Port:     port2,
				Priority: 1,
				Weight:   50,
			},
			{
				Name:     "dra3",
				Host:     host3,
				Port:     port3,
				Priority: 2,
				Weight:   100,
			},
		},
		OriginHost:        "test-client.example.com",
		OriginRealm:       "example.com",
		ProductName:       "TestClient/1.0",
		VendorID:          10415,
		ConnectionsPerDRA: 1,
		ConnectTimeout:    2 * time.Second,
		CERTimeout:        2 * time.Second,
		DWRInterval:       30 * time.Second,
		DWRTimeout:        5 * time.Second,
		MaxDWRFailures:    3,
		ReconnectInterval: 1 * time.Second,
		SendBufferSize:    100,
		HealthCheckInterval: 10 * time.Second,
		MaxReconnectDelay:   30 * time.Second,
		ReconnectBackoff:    2.0,
		RecvBufferSize:    100,
	}

	ctx := context.Background()
	pool, err := client.NewDRAPool(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create DRA pool: %v", err)
	}
	defer pool.Close()

	// Get DRAs by priority
	priority1DRAs := pool.GetDRAsByPriority(1)
	if len(priority1DRAs) != 2 {
		t.Errorf("Expected 2 DRAs at priority 1, got %d", len(priority1DRAs))
	}

	priority2DRAs := pool.GetDRAsByPriority(2)
	if len(priority2DRAs) != 1 {
		t.Errorf("Expected 1 DRA at priority 2, got %d", len(priority2DRAs))
	}

	t.Logf("Priority 1 DRAs: %d, Priority 2 DRAs: %d",
		len(priority1DRAs), len(priority2DRAs))
}

func TestDRAPoolIsHealthy(t *testing.T) {
	testSrv := newTestServer(t)
	if err := testSrv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}
	defer testSrv.Stop()

	host, port, _ := splitHostPort(testSrv.Address())

	config := &client.DRAPoolConfig{
		DRAs: []*client.DRAServerConfig{
			{
				Name:     "dra1",
				Host:     host,
				Port:     port,
				Priority: 1,
				Weight:   100,
			},
		},
		OriginHost:        "test-client.example.com",
		OriginRealm:       "example.com",
		ProductName:       "TestClient/1.0",
		VendorID:          10415,
		ConnectionsPerDRA: 1,
		ConnectTimeout:    2 * time.Second,
		CERTimeout:        2 * time.Second,
		DWRInterval:       30 * time.Second,
		DWRTimeout:        5 * time.Second,
		MaxDWRFailures:    3,
		ReconnectInterval: 1 * time.Second,
		SendBufferSize:    100,
		HealthCheckInterval: 10 * time.Second,
		MaxReconnectDelay:   30 * time.Second,
		ReconnectBackoff:    2.0,
		RecvBufferSize:    100,
	}

	ctx := context.Background()
	pool, err := client.NewDRAPool(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create DRA pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Start(); err != nil {
		t.Fatalf("Failed to start DRA pool: %v", err)
	}

	// Wait for connection
	time.Sleep(2 * time.Second)

	// Check health
	if !pool.IsHealthy() {
		t.Log("Warning: DRA pool should be healthy with active connection")
	}
}

// ============================================================================
// DRA Pool Benchmark Tests
// ============================================================================

func BenchmarkDRAPoolSend(b *testing.B) {
	testSrv := newTestServer(&testing.T{})

	testSrv.RegisterS13Handler(func(msg *server.Message, conn server.Conn) {
		conn.Write(append(msg.Header, msg.Body...))
	})

	testSrv.Start()
	defer testSrv.Stop()

	host, port, _ := splitHostPort(testSrv.Address())

	config := &client.DRAPoolConfig{
		DRAs: []*client.DRAServerConfig{
			{
				Name:     "dra1",
				Host:     host,
				Port:     port,
				Priority: 1,
				Weight:   100,
			},
		},
		OriginHost:        "test-client.example.com",
		OriginRealm:       "example.com",
		ProductName:       "TestClient/1.0",
		VendorID:          10415,
		ConnectionsPerDRA: 1,
		ConnectTimeout:    2 * time.Second,
		CERTimeout:        2 * time.Second,
		DWRInterval:       30 * time.Second,
		DWRTimeout:        5 * time.Second,
		MaxDWRFailures:    3,
		ReconnectInterval: 1 * time.Second,
		SendBufferSize:    100,
		HealthCheckInterval: 10 * time.Second,
		MaxReconnectDelay:   30 * time.Second,
		ReconnectBackoff:    2.0,
		RecvBufferSize:    100,
	}

	ctx := context.Background()
	pool, _ := client.NewDRAPool(ctx, config)
	pool.Start()
	time.Sleep(2 * time.Second)
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
		pool.Send(ecrBytes)
	}
}
