package gateway_test

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/commands/base"
	"github.com/hsdfat/diam-gw/commands/s6a"
	"github.com/hsdfat/diam-gw/gateway"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/connection"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

// registerBaseProtocolHandlers registers handlers for base Diameter protocol messages
func registerBaseProtocolHandlers(gateway *gateway.Gateway, t *testing.T) {
	// CER/CEA handler
	gateway.RegisterInServer(connection.Command{Interface: 0, Code: 257, Request: true}, func(msg *connection.Message, conn connection.Conn) {
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
	gateway.RegisterInServer(connection.Command{Interface: 0, Code: 280, Request: true}, func(msg *connection.Message, conn connection.Conn) {
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
	gateway.RegisterInServer(connection.Command{Interface: 0, Code: 282, Request: true}, func(msg *connection.Message, conn connection.Conn) {
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

// TestGatewayConnectivity tests basic connectivity between Logic App, Gateway, and DRA
func TestGatewayConnectivity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log := logger.New("integration-test", "debug")
	draLog := log.With("mod", "dra").(logger.Logger)
	gwLog := log.With("mod", "gw").(logger.Logger)
	appLog := log.With("mod", "app").(logger.Logger)

	// Start DRA simulator
	dra := NewDRASimulator(ctx, "127.0.0.1:13868", draLog)
	if err := dra.Start(); err != nil {
		t.Fatalf("Failed to start DRA simulator: %v", err)
	}
	defer dra.Stop()

	// Wait for DRA to be ready
	time.Sleep(200 * time.Millisecond)

	// Create gateway configuration
	gwConfig := &gateway.GatewayConfig{
		InServerConfig: &server.ServerConfig{
			ListenAddress:  "127.0.0.1:13867",
			MaxConnections: 100,
			ConnectionConfig: &server.ConnectionConfig{
				OriginHost:       "test-gw.example.com",
				OriginRealm:      "example.com",
				ProductName:      "Test-Gateway",
				VendorID:         10415,
				ReadTimeout:      10 * time.Second,
				WriteTimeout:     5 * time.Second,
				WatchdogInterval: 30 * time.Second,
				WatchdogTimeout:  10 * time.Second,
				MaxMessageSize:   65535,
				SendChannelSize:  100,
				RecvChannelSize:  100,
				HandleWatchdog:   true,
			},
			RecvChannelSize: 100,
		},
		DRAPoolConfig: &client.DRAPoolConfig{
			DRAs: []*client.DRAServerConfig{
				{
					Name:     "DRA-SIM",
					Host:     "127.0.0.1",
					Port:     13868,
					Priority: 1,
					Weight:   100,
				},
			},
			OriginHost:          "test-gw.example.com",
			OriginRealm:         "example.com",
			ProductName:         "Test-Gateway",
			VendorID:            10415,
			ConnectionsPerDRA:   1,
			ConnectTimeout:      5 * time.Second,
			CERTimeout:          5 * time.Second,
			DWRInterval:         30 * time.Second,
			DWRTimeout:          10 * time.Second,
			MaxDWRFailures:      3,
			HealthCheckInterval: 5 * time.Second,
			ReconnectInterval:   2 * time.Second,
			MaxReconnectDelay:   30 * time.Second,
			ReconnectBackoff:    1.5,
			SendBufferSize:      100,
			RecvBufferSize:      100,
		},
		DRASupported:   true,
		OriginHost:     "test-gw.example.com",
		OriginRealm:    "example.com",
		ProductName:    "Test-Gateway",
		VendorID:       10415,
		SessionTimeout: 10 * time.Second,
	}

	// Start gateway
	gw, err := gateway.NewGateway(gwConfig, gwLog)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	registerBaseProtocolHandlers(gw, t)

	if err := gw.Start(); err != nil {
		t.Fatalf("Failed to start gateway: %v", err)
	}
	defer gw.Stop()

	// Wait for gateway to establish DRA connection
	time.Sleep(500 * time.Millisecond)

	// Verify DRA connection
	draStats := gw.GetDRAPool().GetStats()
	if draStats.ActiveConnections == 0 {
		t.Fatal("Gateway did not connect to DRA")
	}
	t.Logf("Gateway connected to DRA: %d active connections", draStats.ActiveConnections)

	// Create Logic App client
	logicApp := NewLogicAppSimulator(ctx, 0, "127.0.0.1:13867", appLog)
	if err := logicApp.Connect(); err != nil {
		t.Fatalf("Failed to connect Logic App to gateway: %v", err)
	}
	defer logicApp.Close()

	t.Logf("Logic App connected to gateway")

	// Send test request
	response, err := logicApp.SendRequest(316, 16777251)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}

	if response == nil {
		t.Fatal("Did not receive response")
	}

	t.Logf("Received response from DRA via gateway")

	// Verify statistics
	stats := gw.GetStats()
	if stats.InServer.MessagesReceived == 0 {
		t.Error("Gateway did not track requests")
	}
	if stats.DraPool.TotalMessagesSent == 0 {
		t.Error("Gateway did not track responses")
	}
	if stats.DraPool.TotalMessagesRecv == 0 {
		t.Error("Gateway did not forward to DRA")
	}

	t.Logf("Gateway stats: requests=%d, responses=%d, forwarded=%d",
		stats.InServer.MessagesReceived, stats.DraPool.TotalMessagesRecv, stats.DraPool.TotalMessagesRecv)

	// Verify DRA received the request
	if dra.GetRequestCount() == 0 {
		t.Error("DRA did not receive request")
	}

	t.Logf("DRA stats: requests=%d, responses=%d", dra.GetRequestCount(), dra.GetResponseCount())

	// Test successful
	t.Log("✓ Connectivity test passed")
}

// TestGatewayPerformance tests gateway performance under load
func TestGatewayPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}
	log := logger.New("perf-test", "error")
	draLog := log.With("mod", "dra").(logger.Logger)
	gwLog := log.With("mod", "gw").(logger.Logger)
	appLog := log.With("mod", "app").(logger.Logger)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Start DRA simulator
	dra := NewDRASimulator(ctx, "127.0.0.1:13869", draLog)
	if err := dra.Start(); err != nil {
		t.Fatalf("Failed to start DRA simulator: %v", err)
	}
	dra.server.HandleFunc(connection.Command{
		Code:      316,
		Interface: 16777251,
		Request:   true,
	}, func(msg *connection.Message, conn connection.Conn) {
		msgInfo, err := connection.ParseMessageHeader(msg.Header)
		if err != nil {
			draLog.Errorw("cannot parse header", "error", err)
			return
		}
		ulr := &s6a.UpdateLocationRequest{}
		err = ulr.Unmarshal(append(msg.Header, msg.Body...))
		if err != nil {
			draLog.Errorw("cannot unmarshal msg", "msg", msgInfo.String())
			return
		}

		ula := s6a.NewUpdateLocationAnswer()
		ula.SessionId = models_base.UTF8String("client.example.com;1234567890;1")
		ula.AuthSessionState = models_base.Enumerated(1)
		ula.OriginHost = models_base.DiameterIdentity("server.example.com")
		ula.OriginRealm = models_base.DiameterIdentity("server.example.com")

		// Set header identifiers for comparison
		ula.Header.HopByHopID = ulr.Header.HopByHopID
		ula.Header.EndToEndID = ulr.Header.EndToEndID

		b, err := ula.Marshal()
		if err != nil {
			draLog.Errorw("cannot marshal msg", "msg", msgInfo.String())
		}
		conn.Write(b)
	})
	defer dra.Stop()

	time.Sleep(200 * time.Millisecond)

	// Create gateway
	gwConfig := &gateway.GatewayConfig{
		InServerConfig: &server.ServerConfig{
			ListenAddress:  "127.0.0.1:13870",
			MaxConnections: 1000,
			ConnectionConfig: &server.ConnectionConfig{
				OriginHost:       "perf-gw.example.com",
				OriginRealm:      "example.com",
				ProductName:      "Perf-Gateway",
				VendorID:         10415,
				ReadTimeout:      10 * time.Second,
				WriteTimeout:     5 * time.Second,
				WatchdogInterval: 30 * time.Second,
				WatchdogTimeout:  10 * time.Second,
				MaxMessageSize:   65535,
				SendChannelSize:  1000,
				RecvChannelSize:  1000,
				HandleWatchdog:   true,
			},
			RecvChannelSize: 1000,
		},
		DRAPoolConfig: &client.DRAPoolConfig{
			DRAs: []*client.DRAServerConfig{
				{
					Name:     "DRA-PERF",
					Host:     "127.0.0.1",
					Port:     13869,
					Priority: 1,
					Weight:   100,
				},
			},
			OriginHost:          "perf-gw.example.com",
			OriginRealm:         "example.com",
			ProductName:         "Perf-Gateway",
			VendorID:            10415,
			ConnectionsPerDRA:   1,
			ConnectTimeout:      5 * time.Second,
			CERTimeout:          5 * time.Second,
			DWRInterval:         30 * time.Second,
			DWRTimeout:          10 * time.Second,
			MaxDWRFailures:      3,
			HealthCheckInterval: 5 * time.Second,
			ReconnectInterval:   2 * time.Second,
			MaxReconnectDelay:   30 * time.Second,
			ReconnectBackoff:    1.5,
			SendBufferSize:      1000,
			RecvBufferSize:      1000,
		},
		DRASupported:   true,
		OriginHost:     "perf-gw.example.com",
		OriginRealm:    "example.com",
		ProductName:    "Perf-Gateway",
		VendorID:       10415,
		SessionTimeout: 10 * time.Second,
	}

	gw, err := gateway.NewGateway(gwConfig, gwLog)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	registerBaseProtocolHandlers(gw, t)
	gw.RegisterInServer(connection.Command{
		Code:      316,
		Interface: 16777251,
		Request:   true,
	}, func(msg *connection.Message, conn connection.Conn) {
		msgInfo, err := connection.ParseMessageHeader(msg.Header)
		if err != nil {
			gwLog.Errorw("cannot parse header", "error", err)
			return
		}
		rsp := make(chan *connection.Message, 1)
		gw.StoreSession(msgInfo.HopByHopID, &gateway.Session{
			Conn:         conn,
			CreatedAt:    time.Now(),
			ResponseChan: rsp,
		})
		gw.GetDRAPool().Send(append(msg.Header, msg.Body...))
		select {
		case rMsg := <-rsp:
			conn.Write(append(rMsg.Header, rMsg.Body...))
		case <-time.After(time.Second):
			gwLog.Errorw("timeout to receive rsp", "msg", msgInfo.String())
		}
	})

	gw.RegisterDraPoolServer(connection.Command{
		Code:      316,
		Interface: 16777251,
		Request:   false,
	}, func(msg *connection.Message, conn connection.Conn) {
		msgInfo, err := connection.ParseMessageHeader(msg.Header)
		if err != nil {
			gwLog.Errorw("cannot parse header", "error", err)
			return
		}
		session, err := gw.FindSession(msgInfo.HopByHopID)
		if err != nil {
			gwLog.Debugw("cannot found session", "msg", msgInfo.String())
			return
		}
		session.ResponseChan <- msg
	})
	if err := gw.Start(); err != nil {
		t.Fatalf("Failed to start gateway: %v", err)
	}
	defer gw.Stop()

	time.Sleep(500 * time.Millisecond)

	// Performance test parameters
	const (
		numClients        = 10
		requestsPerClient = 100
		totalRequests     = numClients * requestsPerClient
	)

	t.Logf("Starting performance test: %d clients, %d requests each, %d total",
		numClients, requestsPerClient, totalRequests)

	// Start time
	startTime := time.Now()

	// Create multiple Logic App clients
	var wg sync.WaitGroup
	var successCount atomic.Uint64
	var errorCount atomic.Uint64

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			// Create client
			logicApp := NewLogicAppSimulator(ctx, uint32(i)*10000, "127.0.0.1:13870", appLog)
			if err := logicApp.Connect(); err != nil {
				t.Errorf("Client %d failed to connect: %v", clientID, err)
				errorCount.Add(uint64(requestsPerClient))
				return
			}
			// defer logicApp.Close()

			// Send requests
			for range requestsPerClient {
				_, err := logicApp.SendRequest(316, 16777251)
				if err != nil {
					errorCount.Add(1)
				} else {
					successCount.Add(1)
				}
			}
		}(i)
	}

	// Wait for all clients to complete
	wg.Wait()
	duration := time.Since(startTime)

	time.Sleep(3 * time.Second)
	// End time

	// Calculate statistics
	successRate := float64(successCount.Load()) / float64(totalRequests) * 100
	throughput := float64(successCount.Load()) / duration.Seconds()

	t.Logf("Performance test completed in %v", duration)
	t.Logf("Total requests: %d", totalRequests)
	t.Logf("Successful: %d (%.2f%%)", successCount.Load(), successRate)
	t.Logf("Errors: %d", errorCount.Load())
	t.Logf("Throughput: %.2f req/sec", throughput)

	// Get gateway statistics
	stats := gw.GetStats()
	t.Logf("Gateway stats:")

	// Verify performance expectations
	if successRate < 95.0 {
		t.Errorf("Success rate too low: %.2f%% (expected >= 95%%)", successRate)
	}

	if throughput < 100 {
		t.Logf("Warning: Throughput below 100 req/sec: %.2f", throughput)
	}

	if stats.AverageLatencyMs > 100 {
		t.Logf("Warning: Average latency above 100ms: %.2f", stats.AverageLatencyMs)
	}

	// Test successful
	t.Log("✓ Performance test passed")
}

// TestGatewayFailover tests DRA failover functionality
func TestGatewayFailover(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	log := logger.New("failover-test", "debug")

	// Start two DRA simulators (primary and secondary)
	draPrimary := NewDRASimulator(ctx, "127.0.0.1:13871", log)
	if err := draPrimary.Start(); err != nil {
		t.Fatalf("Failed to start primary DRA: %v", err)
	}
	defer draPrimary.Stop()

	draSecondary := NewDRASimulator(ctx, "127.0.0.1:13872", log)
	if err := draSecondary.Start(); err != nil {
		t.Fatalf("Failed to start secondary DRA: %v", err)
	}
	defer draSecondary.Stop()

	time.Sleep(200 * time.Millisecond)

	// Create gateway with two DRAs
	gwConfig := &gateway.GatewayConfig{
		InServerConfig: &server.ServerConfig{
			ListenAddress:  "127.0.0.1:13873",
			MaxConnections: 100,
			ConnectionConfig: &server.ConnectionConfig{
				OriginHost:       "failover-gw.example.com",
				OriginRealm:      "example.com",
				ProductName:      "Failover-Gateway",
				VendorID:         10415,
				ReadTimeout:      10 * time.Second,
				WriteTimeout:     5 * time.Second,
				WatchdogInterval: 5 * time.Second,
				WatchdogTimeout:  2 * time.Second,
				MaxMessageSize:   65535,
				SendChannelSize:  100,
				RecvChannelSize:  100,
				HandleWatchdog:   true,
			},
			RecvChannelSize: 100,
		},
		DRAPoolConfig: &client.DRAPoolConfig{
			DRAs: []*client.DRAServerConfig{
				{
					Name:     "DRA-PRIMARY",
					Host:     "127.0.0.1",
					Port:     13871,
					Priority: 1, // Higher priority
					Weight:   100,
				},
				{
					Name:     "DRA-SECONDARY",
					Host:     "127.0.0.1",
					Port:     13872,
					Priority: 2, // Lower priority
					Weight:   100,
				},
			},
			OriginHost:          "failover-gw.example.com",
			OriginRealm:         "example.com",
			ProductName:         "Failover-Gateway",
			VendorID:            10415,
			ConnectionsPerDRA:   1,
			ConnectTimeout:      5 * time.Second,
			CERTimeout:          5 * time.Second,
			DWRInterval:         5 * time.Second,
			DWRTimeout:          2 * time.Second,
			MaxDWRFailures:      2,
			HealthCheckInterval: 2 * time.Second,
			ReconnectInterval:   2 * time.Second,
			MaxReconnectDelay:   10 * time.Second,
			ReconnectBackoff:    1.5,
			SendBufferSize:      100,
			RecvBufferSize:      100,
		},
		OriginHost:     "failover-gw.example.com",
		OriginRealm:    "example.com",
		ProductName:    "Failover-Gateway",
		VendorID:       10415,
		SessionTimeout: 10 * time.Second,
	}

	gw, err := gateway.NewGateway(gwConfig, log)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}

	if err := gw.Start(); err != nil {
		t.Fatalf("Failed to start gateway: %v", err)
	}
	defer gw.Stop()

	time.Sleep(1 * time.Second)

	// Verify initial priority is 1
	activePriority := gw.GetDRAPool().GetActivePriority()
	if activePriority != 1 {
		t.Errorf("Expected active priority 1, got %d", activePriority)
	}
	t.Logf("Initial active priority: %d", activePriority)

	// Create Logic App client
	logicApp := NewLogicAppSimulator(ctx, 0, "127.0.0.1:13873", log)
	if err := logicApp.Connect(); err != nil {
		t.Fatalf("Failed to connect Logic App: %v", err)
	}
	defer logicApp.Close()

	// Send request to primary
	_, err = logicApp.SendRequest(316, 16777251)
	if err != nil {
		t.Fatalf("Failed to send request to primary: %v", err)
	}
	t.Logf("Request sent to primary DRA")

	// Verify primary received it
	if draPrimary.GetRequestCount() == 0 {
		t.Error("Primary DRA did not receive request")
	}

	// Stop primary DRA to trigger failover
	t.Log("Stopping primary DRA to trigger failover...")
	draPrimary.Stop()

	// Wait for failover to occur
	time.Sleep(15 * time.Second)

	// Verify failover to priority 2
	activePriority = gw.GetDRAPool().GetActivePriority()
	if activePriority != 2 {
		t.Errorf("Expected failover to priority 2, got %d", activePriority)
	}
	t.Logf("Failover successful: active priority = %d", activePriority)

	// Send request to secondary
	_, err = logicApp.SendRequest(316, 16777251)
	if err != nil {
		t.Fatalf("Failed to send request after failover: %v", err)
	}
	t.Logf("Request sent after failover")

	// Verify secondary received it
	time.Sleep(500 * time.Millisecond)
	if draSecondary.GetRequestCount() == 0 {
		t.Error("Secondary DRA did not receive request")
	}

	t.Log("✓ Failover test passed")
}

// BenchmarkGatewayThroughput benchmarks gateway throughput
func BenchmarkGatewayThroughput(b *testing.B) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	log := logger.New("bench", "error")

	// Start DRA simulator
	dra := NewDRASimulator(ctx, "127.0.0.1:13874", log)
	if err := dra.Start(); err != nil {
		b.Fatalf("Failed to start DRA: %v", err)
	}
	defer dra.Stop()

	time.Sleep(200 * time.Millisecond)

	// Create gateway
	gwConfig := &gateway.GatewayConfig{
		InServerConfig: &server.ServerConfig{
			ListenAddress:  "127.0.0.1:13875",
			MaxConnections: 10000,
			ConnectionConfig: &server.ConnectionConfig{
				OriginHost:       "bench-gw.example.com",
				OriginRealm:      "example.com",
				ProductName:      "Bench-Gateway",
				VendorID:         10415,
				ReadTimeout:      30 * time.Second,
				WriteTimeout:     10 * time.Second,
				WatchdogInterval: 30 * time.Second,
				WatchdogTimeout:  10 * time.Second,
				MaxMessageSize:   65535,
				SendChannelSize:  10000,
				RecvChannelSize:  10000,
				HandleWatchdog:   true,
			},
			RecvChannelSize: 10000,
		},
		DRAPoolConfig: &client.DRAPoolConfig{
			DRAs: []*client.DRAServerConfig{
				{Name: "DRA-BENCH", Host: "127.0.0.1", Port: 13874, Priority: 1, Weight: 100},
			},
			OriginHost:          "bench-gw.example.com",
			OriginRealm:         "example.com",
			ProductName:         "Bench-Gateway",
			VendorID:            10415,
			ConnectionsPerDRA:   1,
			ConnectTimeout:      5 * time.Second,
			CERTimeout:          5 * time.Second,
			DWRInterval:         30 * time.Second,
			DWRTimeout:          10 * time.Second,
			MaxDWRFailures:      3,
			HealthCheckInterval: 10 * time.Second,
			ReconnectInterval:   2 * time.Second,
			MaxReconnectDelay:   30 * time.Second,
			ReconnectBackoff:    1.5,
			SendBufferSize:      10000,
			RecvBufferSize:      10000,
		},
		OriginHost:     "bench-gw.example.com",
		OriginRealm:    "example.com",
		ProductName:    "Bench-Gateway",
		VendorID:       10415,
		SessionTimeout: 30 * time.Second,
	}

	gw, err := gateway.NewGateway(gwConfig, log)
	if err != nil {
		b.Fatalf("Failed to create gateway: %v", err)
	}

	if err := gw.Start(); err != nil {
		b.Fatalf("Failed to start gateway: %v", err)
	}
	defer gw.Stop()

	time.Sleep(500 * time.Millisecond)

	// Create Logic App client
	logicApp := NewLogicAppSimulator(ctx, 0, "127.0.0.1:13875", log)
	if err := logicApp.Connect(); err != nil {
		b.Fatalf("Failed to connect: %v", err)
	}
	defer logicApp.Close()

	// Benchmark
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := logicApp.SendRequest(316, 16777251)
		if err != nil {
			b.Errorf("Request failed: %v", err)
		}
	}
	b.StopTimer()

	// Report stats
	stats := gw.GetStats()
	b.Logf("Throughput: %.2f req/sec", float64(b.N)/b.Elapsed().Seconds())
	b.Logf("Avg latency: %.2f ms", stats.AverageLatencyMs)
}

// Note: Helper functions moved to helpers.go
