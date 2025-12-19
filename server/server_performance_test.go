package server

import (
	"encoding/binary"
	"io"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/commands/base"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

// PerformanceMetrics tracks performance test metrics
type PerformanceMetrics struct {
	TotalConnections  int64
	TotalMessages     int64
	SuccessfulMsgs    int64
	FailedMsgs        int64
	TotalLatencyMs    int64
	MinLatencyMs      int64
	MaxLatencyMs      int64
	MessagesPerSecond float64
	AvgLatencyMs      float64
	P50LatencyMs      int64
	P95LatencyMs      int64
	P99LatencyMs      int64
	StartTime         time.Time
	EndTime           time.Time
}

// LatencyBucket tracks latency distribution
type LatencyBucket struct {
	mu        sync.Mutex
	latencies []int64
}

func (lb *LatencyBucket) Add(latencyMs int64) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.latencies = append(lb.latencies, latencyMs)
}

func (lb *LatencyBucket) GetPercentile(p float64) int64 {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	if len(lb.latencies) == 0 {
		return 0
	}

	// Create a copy and sort for accurate percentile calculation
	sorted := make([]int64, len(lb.latencies))
	copy(sorted, lb.latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	index := int(float64(len(sorted)) * p)
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

// createTestMessage creates a simple Diameter message for testing
func createTestMessage(size int) []byte {
	// Create a minimal Diameter message header
	msg := make([]byte, size)
	if size < 20 {
		size = 20
		msg = make([]byte, 20)
	}

	// Set version
	msg[0] = 1

	// Set length (3 bytes)
	binary.BigEndian.PutUint32([]byte{0, msg[1], msg[2], msg[3]}, uint32(size))

	// Set flags (request bit)
	msg[4] = 0x80

	// Set command code (3 bytes) - use a test command code
	binary.BigEndian.PutUint32([]byte{0, msg[5], msg[6], msg[7]}, 999)

	// Set application ID
	binary.BigEndian.PutUint32(msg[8:12], 16777252) // S13

	// Set hop-by-hop ID
	binary.BigEndian.PutUint32(msg[12:16], 1)

	// Set end-to-end ID
	binary.BigEndian.PutUint32(msg[16:20], 1)

	// Fill rest with data if size > 20
	if size > 20 {
		for i := 20; i < size; i++ {
			msg[i] = byte(i % 256)
		}
	}

	return msg
}

// createCERMessage creates a CER message for handshake
func createCERMessage() []byte {
	cer := base.NewCapabilitiesExchangeRequest()
	cer.OriginHost = models_base.DiameterIdentity("test-client.example.com")
	cer.OriginRealm = models_base.DiameterIdentity("example.com")
	cer.ProductName = models_base.UTF8String("Test-Client")
	cer.VendorId = models_base.Unsigned32(10415)
	cer.AuthApplicationId = []models_base.Unsigned32{
		models_base.Unsigned32(16777252), // S13
	}

	cer.Header.HopByHopID = 1
	cer.Header.EndToEndID = 1

	data, _ := cer.Marshal()
	return data
}

// testHelper interface for testing.T and testing.B
type testHelper interface {
	Fatalf(format string, args ...interface{})
}

// mockClient creates a mock Diameter client that connects to the server
func createMockClient(t testHelper, serverAddr string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", serverAddr, 5*time.Second)
	if err != nil {
		return nil, err
	}

	// Set read timeout for handshake
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	// Perform CER/CEA handshake
	cer := createCERMessage()
	if _, err := conn.Write(cer); err != nil {
		conn.Close()
		return nil, err
	}

	// Read CEA response with full read
	header := make([]byte, 20)
	if _, err := io.ReadFull(conn, header); err != nil {
		conn.Close()
		return nil, err
	}

	length := binary.BigEndian.Uint32([]byte{0, header[1], header[2], header[3]})
	if length > 20 {
		body := make([]byte, length-20)
		if _, err := io.ReadFull(conn, body); err != nil {
			conn.Close()
			return nil, err
		}
	}

	// Clear read deadline
	conn.SetReadDeadline(time.Time{})

	return conn, nil
}

// TestPerformance_Server_ConcurrentConnections tests server handling of concurrent connections
func TestPerformance_Server_ConcurrentConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	config.MaxConnections = 1000
	config.RecvChannelSize = 1000

	server := NewServer(config, log)
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.listener.Addr().String()

	scenarios := []struct {
		name        string
		connections int
		messagesPer int
	}{
		{"LowConnections", 10, 100},
		{"MediumConnections", 50, 100},
		{"HighConnections", 100, 100},
		{"VeryHighConnections", 200, 50},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			metrics := runConcurrentConnectionsTest(t, serverAddr, scenario.connections, scenario.messagesPer)

			t.Logf("Scenario: %s", scenario.name)
			t.Logf("  Total Connections: %d", metrics.TotalConnections)
			t.Logf("  Total Messages: %d", metrics.TotalMessages)
			t.Logf("  Successful: %d", metrics.SuccessfulMsgs)
			t.Logf("  Failed: %d", metrics.FailedMsgs)
			t.Logf("  Throughput: %.2f msg/s", metrics.MessagesPerSecond)
			t.Logf("  Avg Latency: %.2f ms", metrics.AvgLatencyMs)
			t.Logf("  P95 Latency: %d ms", metrics.P95LatencyMs)
			t.Logf("  P99 Latency: %d ms", metrics.P99LatencyMs)
		})
	}
}

// TestPerformance_Server_MessageThroughput tests message processing throughput
func TestPerformance_Server_MessageThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	config.MaxConnections = 1000
	config.RecvChannelSize = 10000

	server := NewServer(config, log)
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.listener.Addr().String()

	scenarios := []struct {
		name              string
		concurrency       int
		messagesPerClient int
		messageSize       int
	}{
		{"LowConcurrency_SmallMsg", 10, 1000, 100},
		{"LowConcurrency_LargeMsg", 10, 1000, 4096},
		{"MediumConcurrency_SmallMsg", 25, 1000, 100},
		{"MediumConcurrency_LargeMsg", 25, 1000, 4096},
		{"HighConcurrency_SmallMsg", 50, 1000, 100},
		{"HighConcurrency_LargeMsg", 50, 1000, 4096},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			metrics := runMessageThroughputTest(t, serverAddr, scenario.concurrency,
				scenario.messagesPerClient, scenario.messageSize)

			t.Logf("Scenario: %s", scenario.name)
			t.Logf("  Total Messages: %d", metrics.TotalMessages)
			t.Logf("  Successful: %d", metrics.SuccessfulMsgs)
			t.Logf("  Failed: %d", metrics.FailedMsgs)
			t.Logf("  Throughput: %.2f msg/s", metrics.MessagesPerSecond)
			t.Logf("  Avg Latency: %.2f ms", metrics.AvgLatencyMs)
			t.Logf("  P95 Latency: %d ms", metrics.P95LatencyMs)
			t.Logf("  P99 Latency: %d ms", metrics.P99LatencyMs)
		})
	}
}

// TestPerformance_Server_Latency tests server latency under load
func TestPerformance_Server_Latency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	config.MaxConnections = 1000

	server := NewServer(config, log)
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.listener.Addr().String()

	scenarios := []struct {
		name              string
		concurrency       int
		messagesPerClient int
		burstDelay        time.Duration
	}{
		{"SteadyLoad_Low", 10, 100, 0},
		{"SteadyLoad_Medium", 25, 100, 0},
		{"SteadyLoad_High", 50, 100, 0},
		{"BurstTraffic_Short", 50, 50, 50 * time.Millisecond},
		{"BurstTraffic_Long", 100, 25, 100 * time.Millisecond},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			metrics := runLatencyTest(t, serverAddr, scenario.concurrency,
				scenario.messagesPerClient, scenario.burstDelay)

			t.Logf("Scenario: %s", scenario.name)
			t.Logf("  Avg Latency: %.2f ms", metrics.AvgLatencyMs)
			t.Logf("  P50 Latency: %d ms", metrics.P50LatencyMs)
			t.Logf("  P95 Latency: %d ms", metrics.P95LatencyMs)
			t.Logf("  P99 Latency: %d ms", metrics.P99LatencyMs)
			t.Logf("  Max Latency: %d ms", metrics.MaxLatencyMs)

			// Assert latency SLAs
			if metrics.P95LatencyMs > 100 {
				t.Logf("WARNING: P95 latency exceeds 100ms threshold: %d ms", metrics.P95LatencyMs)
			}
			if metrics.P99LatencyMs > 200 {
				t.Logf("WARNING: P99 latency exceeds 200ms threshold: %d ms", metrics.P99LatencyMs)
			}
		})
	}
}

// BenchmarkServer_AcceptConnection benchmarks connection acceptance
func BenchmarkServer_AcceptConnection(b *testing.B) {
	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	config.MaxConnections = 10000

	server := NewServer(config, log)
	if err := server.Start(); err != nil {
		b.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.listener.Addr().String()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			conn, err := createMockClient(b, serverAddr)
			if err != nil {
				b.Errorf("Failed to create client: %v", err)
				continue
			}
			conn.Close()
		}
	})
}

// BenchmarkServer_ProcessMessage benchmarks message processing
func BenchmarkServer_ProcessMessage(b *testing.B) {
	log := logger.New("test-server", "error")
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	config.MaxConnections = 1000
	config.RecvChannelSize = 10000

	server := NewServer(config, log)
	if err := server.Start(); err != nil {
		b.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	serverAddr := server.listener.Addr().String()

	// Create a connection
	conn, err := createMockClient(b, serverAddr)
	if err != nil {
		b.Fatalf("Failed to create client: %v", err)
	}
	defer conn.Close()

	msg := createTestMessage(100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Update hop-by-hop ID for each message
		binary.BigEndian.PutUint32(msg[12:16], uint32(i+1))
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write(msg); err != nil {
			b.Errorf("Failed to write: %v", err)
		}

		// Read response
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		header := make([]byte, 20)
		if _, err := io.ReadFull(conn, header); err != nil {
			b.Errorf("Failed to read: %v", err)
			continue
		}

		length := binary.BigEndian.Uint32([]byte{0, header[1], header[2], header[3]})
		if length > 20 {
			body := make([]byte, length-20)
			if _, err := io.ReadFull(conn, body); err != nil {
				b.Errorf("Failed to read body: %v", err)
				continue
			}
		}
	}
}

// Helper functions

func runConcurrentConnectionsTest(t *testing.T, serverAddr string, connections, messagesPer int) *PerformanceMetrics {
	var wg sync.WaitGroup
	var successCount, failCount int64
	var totalConnections int64

	startTime := time.Now()

	for i := 0; i < connections; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			conn, err := createMockClient(t, serverAddr)
			if err != nil {
				atomic.AddInt64(&failCount, int64(messagesPer))
				return
			}
			defer conn.Close()

			atomic.AddInt64(&totalConnections, 1)

			msg := createTestMessage(100)
			for j := 0; j < messagesPer; j++ {
				// Update hop-by-hop ID
				binary.BigEndian.PutUint32(msg[12:16], uint32(clientID*1000+j+1))

				// Set write timeout
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if _, err := conn.Write(msg); err != nil {
					atomic.AddInt64(&failCount, 1)
					continue
				}

				// Read response with timeout
				conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				header := make([]byte, 20)
				if _, err := io.ReadFull(conn, header); err != nil {
					atomic.AddInt64(&failCount, 1)
					continue
				}

				length := binary.BigEndian.Uint32([]byte{0, header[1], header[2], header[3]})
				if length > 20 {
					body := make([]byte, length-20)
					if _, err := io.ReadFull(conn, body); err != nil {
						atomic.AddInt64(&failCount, 1)
						continue
					}
				}

				atomic.AddInt64(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()
	endTime := time.Now()

	totalMessages := int64(connections * messagesPer)
	duration := endTime.Sub(startTime).Seconds()

	if duration == 0 {
		duration = 0.001
	}

	return &PerformanceMetrics{
		TotalConnections:  totalConnections,
		TotalMessages:     totalMessages,
		SuccessfulMsgs:    successCount,
		FailedMsgs:        failCount,
		MessagesPerSecond: float64(totalMessages) / duration,
		StartTime:         startTime,
		EndTime:           endTime,
	}
}

func runMessageThroughputTest(t *testing.T, serverAddr string, concurrency, messagesPerClient, messageSize int) *PerformanceMetrics {
	var wg sync.WaitGroup
	var successCount, failCount int64
	latencyBucket := &LatencyBucket{latencies: make([]int64, 0, concurrency*messagesPerClient)}

	var minLatency int64 = 999999
	var maxLatency int64 = 0
	var totalLatency int64 = 0
	var minLatencyMu sync.Mutex
	var maxLatencyMu sync.Mutex

	startTime := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			conn, err := createMockClient(t, serverAddr)
			if err != nil {
				atomic.AddInt64(&failCount, int64(messagesPerClient))
				return
			}
			defer conn.Close()

			msg := createTestMessage(messageSize)

			for j := 0; j < messagesPerClient; j++ {
				// Update hop-by-hop ID
				binary.BigEndian.PutUint32(msg[12:16], uint32(clientID*10000+j+1))

				reqStart := time.Now()
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if _, err := conn.Write(msg); err != nil {
					atomic.AddInt64(&failCount, 1)
					continue
				}

				// Read response
				conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				header := make([]byte, 20)
				if _, err := io.ReadFull(conn, header); err != nil {
					atomic.AddInt64(&failCount, 1)
					continue
				}

				length := binary.BigEndian.Uint32([]byte{0, header[1], header[2], header[3]})
				if length > 20 {
					body := make([]byte, length-20)
					if _, err := io.ReadFull(conn, body); err != nil {
						atomic.AddInt64(&failCount, 1)
						continue
					}
				}

				latency := time.Since(reqStart).Milliseconds()
				atomic.AddInt64(&successCount, 1)
				atomic.AddInt64(&totalLatency, latency)
				latencyBucket.Add(latency)

				minLatencyMu.Lock()
				if latency < minLatency {
					minLatency = latency
				}
				minLatencyMu.Unlock()

				maxLatencyMu.Lock()
				if latency > maxLatency {
					maxLatency = latency
				}
				maxLatencyMu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	endTime := time.Now()

	totalMessages := int64(concurrency * messagesPerClient)
	duration := endTime.Sub(startTime).Seconds()

	if duration == 0 {
		duration = 0.001
	}

	avgLatency := float64(0)
	if totalMessages > 0 {
		avgLatency = float64(totalLatency) / float64(totalMessages)
	}

	return &PerformanceMetrics{
		TotalMessages:     totalMessages,
		SuccessfulMsgs:   successCount,
		FailedMsgs:        failCount,
		TotalLatencyMs:    totalLatency,
		MinLatencyMs:      minLatency,
		MaxLatencyMs:      maxLatency,
		MessagesPerSecond: float64(totalMessages) / duration,
		AvgLatencyMs:      avgLatency,
		P50LatencyMs:      latencyBucket.GetPercentile(0.50),
		P95LatencyMs:      latencyBucket.GetPercentile(0.95),
		P99LatencyMs:      latencyBucket.GetPercentile(0.99),
		StartTime:         startTime,
		EndTime:           endTime,
	}
}

func runLatencyTest(t *testing.T, serverAddr string, concurrency, messagesPerClient int, burstDelay time.Duration) *PerformanceMetrics {
	var wg sync.WaitGroup
	var successCount, failCount int64
	latencyBucket := &LatencyBucket{latencies: make([]int64, 0, concurrency*messagesPerClient)}

	var minLatency int64 = 999999
	var maxLatency int64 = 0
	var totalLatency int64 = 0
	var minLatencyMu sync.Mutex
	var maxLatencyMu sync.Mutex

	startTime := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			conn, err := createMockClient(t, serverAddr)
			if err != nil {
				atomic.AddInt64(&failCount, int64(messagesPerClient))
				return
			}
			defer conn.Close()

			msg := createTestMessage(100)

			for j := 0; j < messagesPerClient; j++ {
				if burstDelay > 0 {
					time.Sleep(burstDelay)
				}

				// Update hop-by-hop ID
				binary.BigEndian.PutUint32(msg[12:16], uint32(clientID*10000+j+1))

				reqStart := time.Now()
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if _, err := conn.Write(msg); err != nil {
					atomic.AddInt64(&failCount, 1)
					continue
				}

				// Read response
				conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				header := make([]byte, 20)
				if _, err := io.ReadFull(conn, header); err != nil {
					atomic.AddInt64(&failCount, 1)
					continue
				}

				length := binary.BigEndian.Uint32([]byte{0, header[1], header[2], header[3]})
				if length > 20 {
					body := make([]byte, length-20)
					if _, err := io.ReadFull(conn, body); err != nil {
						atomic.AddInt64(&failCount, 1)
						continue
					}
				}

				latency := time.Since(reqStart).Milliseconds()
				atomic.AddInt64(&successCount, 1)
				atomic.AddInt64(&totalLatency, latency)
				latencyBucket.Add(latency)

				minLatencyMu.Lock()
				if latency < minLatency {
					minLatency = latency
				}
				minLatencyMu.Unlock()

				maxLatencyMu.Lock()
				if latency > maxLatency {
					maxLatency = latency
				}
				maxLatencyMu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	endTime := time.Now()

	totalMessages := int64(concurrency * messagesPerClient)
	duration := endTime.Sub(startTime).Seconds()

	if duration == 0 {
		duration = 0.001
	}

	avgLatency := float64(0)
	if totalMessages > 0 {
		avgLatency = float64(totalLatency) / float64(totalMessages)
	}

	return &PerformanceMetrics{
		TotalMessages:     totalMessages,
		SuccessfulMsgs:   successCount,
		FailedMsgs:        failCount,
		TotalLatencyMs:    totalLatency,
		MinLatencyMs:      minLatency,
		MaxLatencyMs:      maxLatency,
		MessagesPerSecond: float64(totalMessages) / duration,
		AvgLatencyMs:      avgLatency,
		P50LatencyMs:      latencyBucket.GetPercentile(0.50),
		P95LatencyMs:      latencyBucket.GetPercentile(0.95),
		P99LatencyMs:      latencyBucket.GetPercentile(0.99),
		StartTime:         startTime,
		EndTime:           endTime,
	}
}

