package server

import (
	"fmt"
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

// TestServerPerformanceMultipleInterfacesConcurrent tests concurrent performance across multiple interfaces
// This test validates that the server can handle multiple Diameter interfaces (S13, S6a, Base) concurrently
// and that statistics are accurately tracked per interface and per command
func TestServerPerformanceMultipleInterfacesConcurrent(t *testing.T) {
	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	config.MaxConnections = 200

	server := NewServer(config, log)
	registerBaseProtocolHandlers(server, t)

	// Register S13 ECR handler
	server.HandleFunc(Command{Interface: 16777252, Code: 324}, func(msg *Message, conn Conn) {
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

	// Register S6a AIR handler
	server.HandleFunc(Command{Interface: 16777251, Code: 318}, func(msg *Message, conn Conn) {
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

	// Get initial stats
	initialStats := server.GetStats()

	// Test configuration
	numWorkers := 10         // Number of concurrent workers per interface
	messagesPerWorker := 100 // Messages each worker will send

	var wg sync.WaitGroup
	var s13Sent, s13Received atomic.Uint64
	var s6aSent, s6aReceived atomic.Uint64
	var dwrSent, dwrReceived atomic.Uint64

	startTime := time.Now()

	// Launch S13 workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			client := newTestClient(t, addr)
			defer client.Close()

			// Send CER with S13 support
			client.sendCERWithS13(t)
			client.receiveCEA(t)

			// Send S13 ECR messages
			for j := 0; j < messagesPerWorker; j++ {
				ecr := s13.NewMEIdentityCheckRequest()
				ecr.SessionId = models_base.UTF8String(fmt.Sprintf("s13-worker-%d-%d", workerID, j))
				ecr.AuthSessionState = models_base.Enumerated(1)
				ecr.OriginHost = models_base.DiameterIdentity("test-client.example.com")
				ecr.OriginRealm = models_base.DiameterIdentity("example.com")
				ecr.DestinationRealm = models_base.DiameterIdentity("example.com")
				ecr.TerminalInformation = &s13.TerminalInformation{
					Imei:            ptrUTF8String("123456789012345"),
					SoftwareVersion: ptrUTF8String("01"),
				}
				ecr.Header.HopByHopID = uint32(workerID*10000 + j)
				ecr.Header.EndToEndID = uint32(workerID*10000 + j + 50000)

				ecrBytes, err := ecr.Marshal()
				if err != nil {
					t.Errorf("Failed to marshal ECR: %v", err)
					return
				}

				if _, err := client.conn.Write(ecrBytes); err != nil {
					t.Errorf("Failed to send ECR: %v", err)
					return
				}
				s13Sent.Add(1)

				// Read ECA
				client.readMessage(t)
				s13Received.Add(1)
			}
		}(i)
	}

	// Launch S6a workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			client := newTestClient(t, addr)
			defer client.Close()

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

			cerBytes, _ := cer.Marshal()
			client.conn.Write(cerBytes)
			client.receiveCEA(t)

			// Send S6a AIR messages
			for j := 0; j < messagesPerWorker; j++ {
				air := s6a.NewAuthenticationInformationRequest()
				air.SessionId = models_base.UTF8String(fmt.Sprintf("s6a-worker-%d-%d", workerID, j))
				air.AuthSessionState = models_base.Enumerated(1)
				air.OriginHost = models_base.DiameterIdentity("test-client.example.com")
				air.OriginRealm = models_base.DiameterIdentity("example.com")
				air.DestinationRealm = models_base.DiameterIdentity("example.com")
				air.UserName = models_base.UTF8String("452040000000023")
				air.VisitedPlmnId = models_base.OctetString("45204")
				air.Header.HopByHopID = uint32(workerID*10000 + j + 100000)
				air.Header.EndToEndID = uint32(workerID*10000 + j + 200000)

				airBytes, err := air.Marshal()
				if err != nil {
					t.Errorf("Failed to marshal AIR: %v", err)
					return
				}

				if _, err := client.conn.Write(airBytes); err != nil {
					t.Errorf("Failed to send AIR: %v", err)
					return
				}
				s6aSent.Add(1)

				// Read AIA
				client.readMessage(t)
				s6aReceived.Add(1)
			}
		}(i)
	}

	// Launch DWR workers (base protocol)
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			client := newTestClient(t, addr)
			defer client.Close()

			// Send CER
			client.sendCER(t)
			client.receiveCEA(t)

			// Send DWR messages
			for j := 0; j < messagesPerWorker; j++ {
				dwr := base.NewDeviceWatchdogRequest()
				dwr.OriginHost = models_base.DiameterIdentity("test-client.example.com")
				dwr.OriginRealm = models_base.DiameterIdentity("example.com")
				dwr.Header.HopByHopID = uint32(workerID*10000 + j + 300000)
				dwr.Header.EndToEndID = uint32(workerID*10000 + j + 400000)

				dwrBytes, err := dwr.Marshal()
				if err != nil {
					t.Errorf("Failed to marshal DWR: %v", err)
					return
				}

				if _, err := client.conn.Write(dwrBytes); err != nil {
					t.Errorf("Failed to send DWR: %v", err)
					return
				}
				dwrSent.Add(1)

				// Read DWA
				client.readMessage(t)
				dwrReceived.Add(1)
			}
		}(i)
	}

	// Wait for all workers to complete
	wg.Wait()
	duration := time.Since(startTime)

	// Wait for stats to settle
	time.Sleep(500 * time.Millisecond)

	// Get final stats
	finalStats := server.GetStats()

	// Calculate expected counts
	expectedS13 := uint64(numWorkers * messagesPerWorker)
	expectedS6a := uint64(numWorkers * messagesPerWorker)
	expectedDWR := uint64(numWorkers * messagesPerWorker)
	expectedCER := uint64(numWorkers * 3) // 3 groups of workers sent CER
	expectedTotal := expectedS13 + expectedS6a + expectedDWR + expectedCER

	// Verify overall stats
	totalReceived := finalStats.MessagesReceived - initialStats.MessagesReceived
	totalSent := finalStats.MessagesSent - initialStats.MessagesSent

	if totalReceived < expectedTotal {
		t.Errorf("Expected at least %d total messages received, got %d", expectedTotal, totalReceived)
	}
	if totalSent < expectedTotal {
		t.Errorf("Expected at least %d total messages sent, got %d", expectedTotal, totalSent)
	}

	// Verify S13 interface stats
	s13Stats, exists := server.GetInterfaceStats(16777252)
	if !exists {
		t.Fatal("S13 interface stats not found")
	}
	if s13Stats.MessagesReceived < expectedS13 {
		t.Errorf("S13: Expected at least %d messages received, got %d", expectedS13, s13Stats.MessagesReceived)
	}
	if s13Stats.MessagesSent < expectedS13 {
		t.Errorf("S13: Expected at least %d messages sent, got %d", expectedS13, s13Stats.MessagesSent)
	}

	// Verify S13 ECR command stats
	ecrStats, exists := s13Stats.CommandStats[324]
	if !exists {
		t.Fatal("S13 ECR command stats not found")
	}
	if ecrStats.MessagesReceived < expectedS13 {
		t.Errorf("S13 ECR: Expected at least %d messages received, got %d", expectedS13, ecrStats.MessagesReceived)
	}

	// Verify S6a interface stats
	s6aStats, exists := server.GetInterfaceStats(16777251)
	if !exists {
		t.Fatal("S6a interface stats not found")
	}
	if s6aStats.MessagesReceived < expectedS6a {
		t.Errorf("S6a: Expected at least %d messages received, got %d", expectedS6a, s6aStats.MessagesReceived)
	}
	if s6aStats.MessagesSent < expectedS6a {
		t.Errorf("S6a: Expected at least %d messages sent, got %d", expectedS6a, s6aStats.MessagesSent)
	}

	// Verify S6a AIR command stats
	airStats, exists := s6aStats.CommandStats[318]
	if !exists {
		t.Fatal("S6a AIR command stats not found")
	}
	if airStats.MessagesReceived < expectedS6a {
		t.Errorf("S6a AIR: Expected at least %d messages received, got %d", expectedS6a, airStats.MessagesReceived)
	}

	// Verify base protocol (interface 0) stats
	baseStats, exists := server.GetInterfaceStats(0)
	if !exists {
		t.Fatal("Base protocol interface stats not found")
	}
	if baseStats.MessagesReceived < expectedDWR+expectedCER {
		t.Errorf("Base: Expected at least %d messages received, got %d", expectedDWR+expectedCER, baseStats.MessagesReceived)
	}

	// Verify DWR command stats
	dwrStats, exists := baseStats.CommandStats[280]
	if !exists {
		t.Fatal("DWR command stats not found")
	}
	if dwrStats.MessagesReceived < expectedDWR {
		t.Errorf("DWR: Expected at least %d messages received, got %d", expectedDWR, dwrStats.MessagesReceived)
	}

	// Verify CER command stats
	cerStats, exists := baseStats.CommandStats[257]
	if !exists {
		t.Fatal("CER command stats not found")
	}
	if cerStats.MessagesReceived < expectedCER {
		t.Errorf("CER: Expected at least %d messages received, got %d", expectedCER, cerStats.MessagesReceived)
	}

	// Calculate throughput
	totalMessages := s13Sent.Load() + s6aSent.Load() + dwrSent.Load()
	throughput := float64(totalMessages) / duration.Seconds()

	// Verify client-side counters match
	if s13Sent.Load() != expectedS13 {
		t.Errorf("S13: Sent %d messages, expected %d", s13Sent.Load(), expectedS13)
	}
	if s13Received.Load() != expectedS13 {
		t.Errorf("S13: Received %d responses, expected %d", s13Received.Load(), expectedS13)
	}
	if s6aSent.Load() != expectedS6a {
		t.Errorf("S6a: Sent %d messages, expected %d", s6aSent.Load(), expectedS6a)
	}
	if s6aReceived.Load() != expectedS6a {
		t.Errorf("S6a: Received %d responses, expected %d", s6aReceived.Load(), expectedS6a)
	}
	if dwrSent.Load() != expectedDWR {
		t.Errorf("DWR: Sent %d messages, expected %d", dwrSent.Load(), expectedDWR)
	}
	if dwrReceived.Load() != expectedDWR {
		t.Errorf("DWR: Received %d responses, expected %d", dwrReceived.Load(), expectedDWR)
	}

	// Verify no errors occurred
	if finalStats.Errors > initialStats.Errors {
		t.Errorf("Errors occurred during test: %d", finalStats.Errors-initialStats.Errors)
	}

	// Verify bytes are tracked
	if s13Stats.BytesReceived == 0 {
		t.Error("S13: Expected BytesReceived > 0")
	}
	if s13Stats.BytesSent == 0 {
		t.Error("S13: Expected BytesSent > 0")
	}
	if s6aStats.BytesReceived == 0 {
		t.Error("S6a: Expected BytesReceived > 0")
	}
	if s6aStats.BytesSent == 0 {
		t.Error("S6a: Expected BytesSent > 0")
	}
	if baseStats.BytesReceived == 0 {
		t.Error("Base: Expected BytesReceived > 0")
	}
	if baseStats.BytesSent == 0 {
		t.Error("Base: Expected BytesSent > 0")
	}

	// Log performance results
	t.Logf("=== Concurrent Multi-Interface Performance Test Results ===")
	t.Logf("Duration: %v", duration)
	t.Logf("Workers per interface: %d", numWorkers)
	t.Logf("Messages per worker: %d", messagesPerWorker)
	t.Logf("Total messages sent: %d", totalMessages)
	t.Logf("Overall throughput: %.2f msg/sec", throughput)
	t.Logf("")
	t.Logf("S13 Interface:")
	t.Logf("  Messages Received: %d", s13Stats.MessagesReceived)
	t.Logf("  Messages Sent: %d", s13Stats.MessagesSent)
	t.Logf("  Bytes Received: %d", s13Stats.BytesReceived)
	t.Logf("  Bytes Sent: %d", s13Stats.BytesSent)
	t.Logf("  ECR Command: Recv=%d, Sent=%d", ecrStats.MessagesReceived, ecrStats.MessagesSent)
	t.Logf("")
	t.Logf("S6a Interface:")
	t.Logf("  Messages Received: %d", s6aStats.MessagesReceived)
	t.Logf("  Messages Sent: %d", s6aStats.MessagesSent)
	t.Logf("  Bytes Received: %d", s6aStats.BytesReceived)
	t.Logf("  Bytes Sent: %d", s6aStats.BytesSent)
	t.Logf("  AIR Command: Recv=%d, Sent=%d", airStats.MessagesReceived, airStats.MessagesSent)
	t.Logf("")
	t.Logf("Base Protocol Interface:")
	t.Logf("  Messages Received: %d", baseStats.MessagesReceived)
	t.Logf("  Messages Sent: %d", baseStats.MessagesSent)
	t.Logf("  Bytes Received: %d", baseStats.BytesReceived)
	t.Logf("  Bytes Sent: %d", baseStats.BytesSent)
	t.Logf("  CER Command: Recv=%d, Sent=%d", cerStats.MessagesReceived, cerStats.MessagesSent)
	t.Logf("  DWR Command: Recv=%d, Sent=%d", dwrStats.MessagesReceived, dwrStats.MessagesSent)
	t.Logf("")
	t.Logf("Overall Server Stats:")
	t.Logf("  Total Connections: %d", finalStats.TotalConnections)
	t.Logf("  Total Messages: %d", totalReceived)
	t.Logf("  Total Bytes Received: %d", finalStats.TotalBytesReceived-initialStats.TotalBytesReceived)
	t.Logf("  Total Bytes Sent: %d", finalStats.TotalBytesSent-initialStats.TotalBytesSent)
	t.Logf("  Errors: %d", finalStats.Errors-initialStats.Errors)
}
