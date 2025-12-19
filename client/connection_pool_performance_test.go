package client

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// PerformanceMetrics tracks performance test metrics
type PerformanceMetrics struct {
	TotalRequests     int64
	SuccessfulReqs    int64
	FailedReqs        int64
	TotalLatencyMs    int64
	MinLatencyMs      int64
	MaxLatencyMs      int64
	RequestsPerSecond float64
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

// testHelper interface for testing.T and testing.B
type testHelper interface {
	Fatalf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

// mockServer creates a simple mock Diameter server for testing
func startMockServer(t testHelper, addr string) (net.Listener, func()) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to start mock server: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}

			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-stop:
					return
				default:
					continue
				}
			}

			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()

				// Set read deadline for handshake
				if err := c.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
					return
				}

				// Handle CER/CEA handshake - read full message
				header := make([]byte, 20)
				if _, err := io.ReadFull(c, header); err != nil {
					return
				}

				length := binary.BigEndian.Uint32([]byte{0, header[1], header[2], header[3]})
				if length < 20 || length > 65535 {
					return
				}

				// Read full CER message
				cerData := make([]byte, length)
				copy(cerData[:20], header)
				if length > 20 {
					if _, err := io.ReadFull(c, cerData[20:]); err != nil {
						return
					}
				}

				// Build a minimal valid CEA response
				// Start with 20-byte header + Result-Code AVP (12 bytes) = 32 bytes minimum
				ceaSize := 32
				cea := make([]byte, ceaSize)

				// Header (20 bytes)
				cea[0] = 1 // Version
				// Length (3 bytes at positions 1-3) - write 32 as 3 bytes
				cea[1] = byte((ceaSize >> 16) & 0xFF)
				cea[2] = byte((ceaSize >> 8) & 0xFF)
				cea[3] = byte(ceaSize & 0xFF)
				cea[4] = 0x00 // Flags: Answer (not Request)
				// Command Code (3 bytes at 5-7) - 257 for CEA
				cea[5] = byte((257 >> 16) & 0xFF)
				cea[6] = byte((257 >> 8) & 0xFF)
				cea[7] = byte(257 & 0xFF)
				// Application ID
				binary.BigEndian.PutUint32(cea[8:12], 0)
				// Hop-by-hop ID (copy from CER)
				copy(cea[12:16], cerData[12:16])
				// End-to-end ID (copy from CER)
				copy(cea[16:20], cerData[16:20])

				// Add Result-Code AVP (12 bytes): Code=268, Flags=0x40 (M-bit), Length=12, Value=2001 (SUCCESS)
				binary.BigEndian.PutUint32(cea[20:24], 268) // AVP Code
				cea[24] = 0x40                            // Flags (M-bit)
				// AVP Length (3 bytes at 25-27) = 12
				cea[25] = byte((12 >> 16) & 0xFF)
				cea[26] = byte((12 >> 8) & 0xFF)
				cea[27] = byte(12 & 0xFF)
				binary.BigEndian.PutUint32(cea[28:32], 2001) // Result-Code value (DIAMETER_SUCCESS)

				// Write the CEA
				if _, err := c.Write(cea); err != nil {
					fmt.Printf("Failed to write CEA: %v\n", err)
					return
				}

				// Clear read deadline
				c.SetReadDeadline(time.Time{})

				// Echo messages back
				for {
					select {
					case <-stop:
						return
					default:
					}

					c.SetReadDeadline(time.Now().Add(5 * time.Second))
					msgHeader := make([]byte, 20)
					if _, err := io.ReadFull(c, msgHeader); err != nil {
						select {
						case <-stop:
							return
						default:
							return
						}
					}

					msgLen := binary.BigEndian.Uint32([]byte{0, msgHeader[1], msgHeader[2], msgHeader[3]})
					if msgLen < 20 || msgLen > 65535 {
						return
					}

					msg := make([]byte, msgLen)
					copy(msg[:20], msgHeader)

					if msgLen > 20 {
						if _, err := io.ReadFull(c, msg[20:]); err != nil {
							return
						}
					}

					// Echo back as response
					msg[4] &^= 0x80 // Clear request bit
					if _, err := c.Write(msg); err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	cleanup := func() {
		close(stop)
		listener.Close()
		wg.Wait()
	}

	return listener, cleanup
}

// TestPerformance_ConnectionPool_Throughput tests connection pool throughput
func TestPerformance_ConnectionPool_Throughput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	ctx := context.Background()
	serverAddr := "127.0.0.1:0"
	listener, cleanup := startMockServer(t, serverAddr)
	defer cleanup()

	_, port, _ := net.SplitHostPort(listener.Addr().String())

	config := DefaultConfig()
	config.Host = "127.0.0.1"
	config.Port = parsePort(port)
	config.OriginHost = "test-client.example.com"
	config.OriginRealm = "example.com"
	config.ConnectionCount = 5
	config.SendBufferSize = 1000
	config.RecvBufferSize = 1000

	scenarios := []struct {
		name              string
		concurrency       int
		requestsPerClient int
		messageSize       int
	}{
		{"LowConcurrency_SmallMsg", 5, 100, 100},
		{"LowConcurrency_LargeMsg", 5, 100, 1000},
		{"MediumConcurrency_SmallMsg", 25, 100, 100},
		{"MediumConcurrency_LargeMsg", 25, 100, 1000},
		{"HighConcurrency_SmallMsg", 50, 100, 100},
		{"HighConcurrency_LargeMsg", 50, 100, 1000},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			pool, err := NewConnectionPool(ctx, config)
			if err != nil {
				t.Fatalf("Failed to create pool: %v", err)
			}

			if err := pool.Start(); err != nil {
				t.Fatalf("Failed to start pool: %v", err)
			}
			defer pool.Close()

			// Wait for connections to be ready
			time.Sleep(2 * time.Second)

			metrics := runThroughputTest(t, ctx, pool, scenario.concurrency,
				scenario.requestsPerClient, scenario.messageSize)

			t.Logf("Scenario: %s", scenario.name)
			t.Logf("  Total Requests: %d", metrics.TotalRequests)
			t.Logf("  Successful: %d", metrics.SuccessfulReqs)
			t.Logf("  Failed: %d", metrics.FailedReqs)
			t.Logf("  Throughput: %.2f req/s", metrics.RequestsPerSecond)
			t.Logf("  Avg Latency: %.2f ms", metrics.AvgLatencyMs)
			t.Logf("  P95 Latency: %d ms", metrics.P95LatencyMs)
			t.Logf("  P99 Latency: %d ms", metrics.P99LatencyMs)
		})
	}
}

// TestPerformance_ConnectionPool_Latency tests connection pool latency
func TestPerformance_ConnectionPool_Latency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	ctx := context.Background()
	serverAddr := "127.0.0.1:0"
	listener, cleanup := startMockServer(t, serverAddr)
	defer cleanup()

	_, port, _ := net.SplitHostPort(listener.Addr().String())

	config := DefaultConfig()
	config.Host = "127.0.0.1"
	config.Port = parsePort(port)
	config.OriginHost = "test-client.example.com"
	config.OriginRealm = "example.com"
	config.ConnectionCount = 5

	pool, err := NewConnectionPool(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}

	if err := pool.Start(); err != nil {
		t.Fatalf("Failed to start pool: %v", err)
	}
	defer pool.Close()

	// Wait for connections
	time.Sleep(2 * time.Second)

	scenarios := []struct {
		name              string
		concurrency       int
		requestsPerClient int
		burstDelay        time.Duration
	}{
		{"SteadyLoad_Low", 10, 50, 0},
		{"SteadyLoad_Medium", 25, 50, 0},
		{"SteadyLoad_High", 50, 50, 0},
		{"BurstTraffic_Short", 50, 20, 100 * time.Millisecond},
		{"BurstTraffic_Long", 100, 10, 200 * time.Millisecond},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			metrics := runLatencyTest(t, ctx, pool, scenario.concurrency,
				scenario.requestsPerClient, scenario.burstDelay)

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

// BenchmarkConnectionPool_Send benchmarks message sending through connection pool
func BenchmarkConnectionPool_Send(b *testing.B) {
	ctx := context.Background()
	serverAddr := "127.0.0.1:0"
	listener, cleanup := startMockServer(b, serverAddr)
	defer cleanup()

	_, port, _ := net.SplitHostPort(listener.Addr().String())

	config := DefaultConfig()
	config.Host = "127.0.0.1"
	config.Port = parsePort(port)
	config.OriginHost = "test-client.example.com"
	config.OriginRealm = "example.com"
	config.ConnectionCount = 5

	pool, err := NewConnectionPool(ctx, config)
	if err != nil {
		b.Fatalf("Failed to create pool: %v", err)
	}

	if err := pool.Start(); err != nil {
		b.Fatalf("Failed to start pool: %v", err)
	}
	defer pool.Close()

	// Wait for connections
	time.Sleep(2 * time.Second)

	msg := createTestMessage(100)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := pool.Send(msg); err != nil {
				b.Errorf("Send failed: %v", err)
			}
		}
	})
}

// BenchmarkConnectionPool_SendLarge benchmarks sending large messages
func BenchmarkConnectionPool_SendLarge(b *testing.B) {
	ctx := context.Background()
	serverAddr := "127.0.0.1:0"
	listener, cleanup := startMockServer(b, serverAddr)
	defer cleanup()

	_, port, _ := net.SplitHostPort(listener.Addr().String())

	config := DefaultConfig()
	config.Host = "127.0.0.1"
	config.Port = parsePort(port)
	config.OriginHost = "test-client.example.com"
	config.OriginRealm = "example.com"
	config.ConnectionCount = 5

	pool, err := NewConnectionPool(ctx, config)
	if err != nil {
		b.Fatalf("Failed to create pool: %v", err)
	}

	if err := pool.Start(); err != nil {
		b.Fatalf("Failed to start pool: %v", err)
	}
	defer pool.Close()

	time.Sleep(2 * time.Second)

	msg := createTestMessage(4096)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := pool.Send(msg); err != nil {
				b.Errorf("Send failed: %v", err)
			}
		}
	})
}

// Helper functions

func parsePort(portStr string) int {
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return port
}

func runThroughputTest(t *testing.T, ctx context.Context, pool *ConnectionPool,
	concurrency, requestsPerClient, messageSize int) *PerformanceMetrics {

	var wg sync.WaitGroup
	var successCount, failCount int64
	latencyBucket := &LatencyBucket{latencies: make([]int64, 0, concurrency*requestsPerClient)}

	var minLatency int64 = 999999
	var maxLatency int64 = 0
	var totalLatency int64 = 0
	var minLatencyMu sync.Mutex
	var maxLatencyMu sync.Mutex

	msg := createTestMessage(messageSize)
	startTime := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			for j := 0; j < requestsPerClient; j++ {
				reqStart := time.Now()
				err := pool.Send(msg)
				latency := time.Since(reqStart).Milliseconds()

				if err != nil {
					atomic.AddInt64(&failCount, 1)
				} else {
					atomic.AddInt64(&successCount, 1)
				}

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

	totalRequests := int64(concurrency * requestsPerClient)
	duration := endTime.Sub(startTime).Seconds()

	if duration == 0 {
		duration = 0.001
	}

	avgLatency := float64(0)
	if totalRequests > 0 {
		avgLatency = float64(totalLatency) / float64(totalRequests)
	}

	return &PerformanceMetrics{
		TotalRequests:     totalRequests,
		SuccessfulReqs:    successCount,
		FailedReqs:        failCount,
		TotalLatencyMs:    totalLatency,
		MinLatencyMs:      minLatency,
		MaxLatencyMs:      maxLatency,
		RequestsPerSecond: float64(totalRequests) / duration,
		AvgLatencyMs:      avgLatency,
		P50LatencyMs:      latencyBucket.GetPercentile(0.50),
		P95LatencyMs:      latencyBucket.GetPercentile(0.95),
		P99LatencyMs:      latencyBucket.GetPercentile(0.99),
		StartTime:         startTime,
		EndTime:           endTime,
	}
}

func runLatencyTest(t *testing.T, ctx context.Context, pool *ConnectionPool,
	concurrency, requestsPerClient int, burstDelay time.Duration) *PerformanceMetrics {

	var wg sync.WaitGroup
	var successCount, failCount int64
	latencyBucket := &LatencyBucket{latencies: make([]int64, 0, concurrency*requestsPerClient)}

	var minLatency int64 = 999999
	var maxLatency int64 = 0
	var totalLatency int64 = 0
	var minLatencyMu sync.Mutex
	var maxLatencyMu sync.Mutex

	msg := createTestMessage(100)
	startTime := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			for j := 0; j < requestsPerClient; j++ {
				if burstDelay > 0 {
					time.Sleep(burstDelay)
				}

				reqStart := time.Now()
				err := pool.Send(msg)
				latency := time.Since(reqStart).Milliseconds()

				if err != nil {
					atomic.AddInt64(&failCount, 1)
				} else {
					atomic.AddInt64(&successCount, 1)
				}

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

	totalRequests := int64(concurrency * requestsPerClient)
	duration := endTime.Sub(startTime).Seconds()

	if duration == 0 {
		duration = 0.001
	}

	avgLatency := float64(0)
	if totalRequests > 0 {
		avgLatency = float64(totalLatency) / float64(totalRequests)
	}

	return &PerformanceMetrics{
		TotalRequests:     totalRequests,
		SuccessfulReqs:    successCount,
		FailedReqs:        failCount,
		TotalLatencyMs:    totalLatency,
		MinLatencyMs:      minLatency,
		MaxLatencyMs:      maxLatency,
		RequestsPerSecond: float64(totalRequests) / duration,
		AvgLatencyMs:      avgLatency,
		P50LatencyMs:      latencyBucket.GetPercentile(0.50),
		P95LatencyMs:      latencyBucket.GetPercentile(0.95),
		P99LatencyMs:      latencyBucket.GetPercentile(0.99),
		StartTime:         startTime,
		EndTime:           endTime,
	}
}

