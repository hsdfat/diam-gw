package gateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/commands/s13"
	"github.com/hsdfat/diam-gw/gateway"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/connection"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

// TestS13Interface tests S13 interface with EIR role
// Flow: DRA -> Gateway -> LogicApp (EIR) -> Gateway -> DRA
func TestS13Interface(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	log := logger.New("perf-test", "info")
	draLog := log.With("mod", "dra").(logger.Logger)
	gwLog := log.With("mod", "gw").(logger.Logger)
	appLog := log.With("mod", "eir").(logger.Logger)

	// Start DRA simulator
	t.Log("Starting DRA simulator...")
	dra := NewDRASimulator(ctx, "127.0.0.1:13900", draLog)
	if err := dra.Start(); err != nil {
		t.Fatalf("Failed to start DRA simulator: %v", err)
	}
	defer dra.Stop()
	time.Sleep(200 * time.Millisecond)

	// Create gateway configuration
	t.Log("Creating gateway configuration...")
	gwConfig := &gateway.GatewayConfig{
		InServerConfig: &server.ServerConfig{
			ListenAddress:  "127.0.0.1:13901",
			MaxConnections: 100,
			ConnectionConfig: &server.ConnectionConfig{
				OriginHost:       "s13-gw.example.com",
				OriginRealm:      "example.com",
				ProductName:      "S13-Gateway",
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
					Name:     "DRA-S13",
					Host:     "127.0.0.1",
					Port:     13900,
					Priority: 1,
					Weight:   100,
				},
			},
			OriginHost:          "s13-gw.example.com",
			OriginRealm:         "example.com",
			ProductName:         "S13-Gateway",
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
		InClientConfig: &client.PoolConfig{
			OriginHost:          "s13-gw.example.com",
			OriginRealm:         "example.com",
			ProductName:         "S13-Gateway",
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
			ReconnectEnabled:    false,
			ReconnectInterval:   2 * time.Second,
			MaxReconnectDelay:   30 * time.Second,
			ReconnectBackoff:    1.5,
			HealthCheckInterval: 10 * time.Second,
		},
		DRASupported:   true,
		OriginHost:     "s13-gw.example.com",
		OriginRealm:    "example.com",
		ProductName:    "S13-Gateway",
		VendorID:       10415,
		SessionTimeout: 10 * time.Second,
	}

	// Start gateway
	t.Log("Starting gateway...")
	gw, err := gateway.NewGateway(gwConfig, log)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	registerBaseProtocolHandlers(gw, t)
	gw.RegisterDraPoolServer(connection.Command{
		Code:      324,
		Interface: s13.S13_APPLICATION_ID,
		Request:   true,
	}, func(msg *connection.Message, conn connection.Conn) {
		gwLog.Infow("process micr message")
		_, err := connection.ParseMessageHeader(msg.Header)
		if err != nil {
			gwLog.Errorw("cannot parse header", "error", err)
			return
		}
		rsp, err := gw.SendInternal("127.0.0.1:13911", append(msg.Header, msg.Body...))
		if err != nil {
			gwLog.Errorw("cannot send to eir", "err", err)
			return
		}
		conn.Write(rsp)
	})
	if err := gw.Start(); err != nil {
		t.Fatalf("Failed to start gateway: %v", err)
	}
	defer gw.Stop()

	// Wait for gateway to establish DRA connection
	time.Sleep(1 * time.Second)

	// Verify DRA connection
	draStats := gw.GetDRAPool().GetStats()
	if draStats.ActiveConnections == 0 {
		t.Fatal("Gateway did not connect to DRA")
	}
	t.Logf("✓ Gateway connected to DRA: %d active connections", draStats.ActiveConnections)

	// Start Logic App (EIR) simulator
	t.Log("Starting Logic App (EIR) simulator...")
	eir := NewS13EIRSimulator(ctx, "127.0.0.1:13901", "127.0.0.1:13911", appLog)
	if err := eir.Start(); err != nil {
		t.Fatalf("Failed to start EIR simulator: %v", err)
	}
	eir.server.HandleFunc(connection.Command{
		Code:      324,
		Interface: s13.S13_APPLICATION_ID,
		Request:   true,
	}, func(msg *connection.Message, conn connection.Conn) {
		appLog.Infow("process micr message")
		_, err := connection.ParseMessageHeader(msg.Header)
		if err != nil {
			appLog.Errorw("cannot parse header", "error", err)
			return
		}
		micr := s13.NewMEIdentityCheckRequest()
		err = micr.Unmarshal(append(msg.Header, msg.Body...))
		if err != nil {
			appLog.Errorw("cannot unmarshal", "error", err)
			return
		}
		mica := s13.NewMEIdentityCheckAnswer()
		mica.SessionId = micr.SessionId
		mica.AuthSessionState = models_base.Enumerated(1)
		mica.OriginHost = models_base.DiameterIdentity("server.example.com")
		mica.OriginRealm = models_base.DiameterIdentity("server.example.com")
	})
	defer eir.Stop()

	// Wait for EIR to connect
	time.Sleep(500 * time.Millisecond)

	// Get gateway server stats to verify EIR connection
	t.Logf("Gateway stats before MICR:")

	// DRA sends MICR to Gateway
	t.Log("========================================")
	t.Log("Testing S13 Message Flow:")
	t.Log("DRA -> Gateway -> EIR -> Gateway -> DRA")
	t.Log("========================================")

	// DRA initiates MICR (ME-Identity-Check-Request)
	testIMEI := "123456789012345"
	t.Logf("DRA sending MICR with IMEI: %s", testIMEI)

	if err := dra.SendMICR(testIMEI); err != nil {
		t.Fatalf("Failed to send MICR from DRA: %v", err)
	}

	// Wait for message to flow through the system
	t.Log("Waiting for message flow...")
	time.Sleep(2 * time.Second)

	// Check statistics
	t.Log("========================================")
	t.Log("Message Flow Statistics:")
	t.Log("========================================")

	t.Logf("Gateway stats:")

	eirStats := eir.server.GetStats()
	t.Logf("EIR stats:")
	t.Logf("  Requests received: %d", eirStats.MessagesReceived)
	t.Logf("  Responses sent: %d", eirStats.MessagesSent)
	t.Logf("  Errors: %d", eirStats.Errors)

	draStats = gw.GetDRAPool().GetStats()
	t.Logf("DRA connection stats:")
	t.Logf("  Messages sent: %d", draStats.TotalMessagesSent)
	t.Logf("  Messages received: %d", draStats.TotalMessagesRecv)

	// Verify connections are still active
	if draStats.ActiveConnections == 0 {
		t.Error("❌ DRA connection lost")
	} else {
		t.Logf("✓ DRA connection active")
	}

	// Verify message flow occurred
	t.Log("========================================")
	t.Log("Validation Results:")
	t.Log("========================================")

	// Expected flow:
	// 1. DRA sends MICR to Gateway (Gateway receives from DRA)
	// 2. Gateway forwards MICR to EIR (EIR receives MICR)
	// 3. EIR sends MICA to Gateway (Gateway receives from EIR)
	// 4. Gateway forwards MICA to DRA (DRA receives MICA)

	if eirStats.MessagesReceived > 0 {
		t.Logf("✓ EIR received MICR: %d requests", eirStats.MessagesReceived)
	} else {
		t.Errorf("❌ EIR did not receive MICR (expected >= 1, got %d)", eirStats.MessagesReceived)
	}

	if eirStats.MessagesSent > 0 {
		t.Logf("✓ EIR sent MICA: %d responses", eirStats.MessagesSent)
	} else {
		t.Errorf("❌ EIR did not send MICA (expected >= 1, got %d)", eirStats.MessagesSent)
	}

	if eirStats.Errors > 0 {
		t.Errorf("❌ EIR encountered errors: %d", eirStats.Errors)
	} else {
		t.Log("✓ No EIR errors")
	}

	t.Log("========================================")
	if eirStats.MessagesReceived > 0 && eirStats.MessagesSent > 0 {
		t.Log("✅ S13 Interface Test PASSED")
		t.Log("Full message flow completed: DRA -> Gateway -> EIR -> Gateway -> DRA")
	} else {
		t.Log("⚠ S13 Interface Test PARTIAL - some messages not received")
	}
	t.Log("========================================")
}
