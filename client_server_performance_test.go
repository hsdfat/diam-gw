package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
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
	msg := make([]byte, size)
	if size < 20 {
		size = 20
		msg = make([]byte, 20)
	}

	msg[0] = 1
	binary.BigEndian.PutUint32([]byte{0, msg[1], msg[2], msg[3]}, uint32(size))
	msg[4] = 0x80
	binary.BigEndian.PutUint32([]byte{0, msg[5], msg[6], msg[7]}, 999)
	binary.BigEndian.PutUint32(msg[8:12], 16777252)
	binary.BigEndian.PutUint32(msg[12:16], 1)
	binary.BigEndian.PutUint32(msg[16:20], 1)

	if size > 20 {
		for i := 20; i < size; i++ {
			msg[i] = byte(i % 256)
		}
	}

	return msg
}

// TestPerformance_ClientServer_EndToEnd tests end-to-end performance with real client and server
func TestPerformance_ClientServer_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	// Start server
	log := logger.New("test-server", "error")
	serverConfig := server.DefaultServerConfig()
	serverConfig.ListenAddress = "127.0.0.1:0"
	serverConfig.MaxConnections = 1000
	serverConfig.RecvChannelSize = 10000

	diameterServer := server.NewServer(serverConfig, log)
	if err := diameterServer.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer diameterServer.Stop()

	serverAddr := diameterServer.GetListener().Addr().String()
	_, port, _ := net.SplitHostPort(serverAddr)

	// Start message echo handler
	go func() {
		for msgCtx := range diameterServer.Receive() {
			// Skip base protocol messages (CER, DWR, etc.)
			if len(msgCtx.Message) < 20 {
				continue
			}

			// Parse to check if it's a base protocol message
			msgInfo, err := client.ParseMessageHeader(msgCtx.Message)
			if err != nil || msgInfo.IsBaseProtocol() {
				continue
			}

			// Echo message back as response
			msg := make([]byte, len(msgCtx.Message))
			copy(msg, msgCtx.Message)
			msg[4] &^= 0x80 // Clear request bit

			// Send response
			if err := msgCtx.Connection.Send(msg); err != nil {
				// Connection might be closed, ignore error
				continue
			}
		}
	}()

	// Create client pool
	ctx := context.Background()
	clientConfig := client.DefaultConfig()
	clientConfig.Host = "127.0.0.1"
	clientConfig.Port = parsePort(port)
	clientConfig.OriginHost = "test-client.example.com"
	clientConfig.OriginRealm = "example.com"
	clientConfig.ConnectionCount = 5
	clientConfig.SendBufferSize = 1000
	clientConfig.RecvBufferSize = 1000

	pool, err := client.NewConnectionPool(ctx, clientConfig)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}

	if err := pool.Start(); err != nil {
		t.Fatalf("Failed to start pool: %v", err)
	}
	defer pool.Close()

	// Wait for connections to be ready
	if err := pool.WaitForConnection(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for connection: %v", err)
	}

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
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			metrics := runEndToEndTest(t, ctx, pool, scenario.concurrency,
				scenario.requestsPerClient, scenario.messageSize)

			t.Logf("Scenario: %s", scenario.name)
			t.Logf("  Total Requests: %d", metrics.TotalRequests)
			t.Logf("  Successful: %d", metrics.SuccessfulReqs)
			t.Logf("  Failed: %d", metrics.FailedReqs)
			t.Logf("  Throughput: %.2f req/s", metrics.RequestsPerSecond)
			t.Logf("  Avg Latency: %.2f ms", metrics.AvgLatencyMs)
			t.Logf("  P50 Latency: %d ms", metrics.P50LatencyMs)
			t.Logf("  P95 Latency: %d ms", metrics.P95LatencyMs)
			t.Logf("  P99 Latency: %d ms", metrics.P99LatencyMs)
			t.Logf("  Min Latency: %d ms", metrics.MinLatencyMs)
			t.Logf("  Max Latency: %d ms", metrics.MaxLatencyMs)

			// Performance assertions
			if metrics.SuccessfulReqs == 0 {
				t.Errorf("No successful requests")
			}
			if float64(metrics.SuccessfulReqs)/float64(metrics.TotalRequests) < 0.95 {
				t.Logf("WARNING: Success rate below 95%%: %.2f%%",
					float64(metrics.SuccessfulReqs)/float64(metrics.TotalRequests)*100)
			}
		})
	}
}

// TestPerformance_ClientServer_Latency tests latency under different load patterns
func TestPerformance_ClientServer_Latency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	// Start server
	log := logger.New("test-server", "error")
	serverConfig := server.DefaultServerConfig()
	serverConfig.ListenAddress = "127.0.0.1:0"
	serverConfig.MaxConnections = 1000

	diameterServer := server.NewServer(serverConfig, log)
	if err := diameterServer.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer diameterServer.Stop()

	serverAddr := diameterServer.GetListener().Addr().String()
	_, port, _ := net.SplitHostPort(serverAddr)

	// Start message echo handler
	go func() {
		for msgCtx := range diameterServer.Receive() {
			// Skip malformed messages
			if len(msgCtx.Message) < 20 {
				continue
			}

			// Quickly check ApplicationID to skip base protocol messages (CER/DWR/DPR)
			appID := binary.BigEndian.Uint32(msgCtx.Message[8:12])
			if appID == 0 {
				// Base protocol, let built-in handlers deal with it
				continue
			}

			// Echo application message back as an answer
			msg := make([]byte, len(msgCtx.Message))
			copy(msg, msgCtx.Message)
			msg[4] &^= 0x80 // clear request bit

			_ = msgCtx.Connection.Send(msg)
		}
	}()

	// Create client pool
	ctx := context.Background()
	clientConfig := client.DefaultConfig()
	clientConfig.Host = "127.0.0.1"
	clientConfig.Port = parsePort(port)
	clientConfig.OriginHost = "test-client.example.com"
	clientConfig.OriginRealm = "example.com"
	clientConfig.ConnectionCount = 5

	pool, err := client.NewConnectionPool(ctx, clientConfig)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}

	if err := pool.Start(); err != nil {
		t.Fatalf("Failed to start pool: %v", err)
	}
	defer pool.Close()

	if err := pool.WaitForConnection(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for connection: %v", err)
	}

	scenarios := []struct {
		name              string
		concurrency       int
		requestsPerClient int
		burstDelay        time.Duration
	}{
		{"SteadyLoad_Low", 10, 50, 0},
		{"SteadyLoad_Medium", 25, 50, 0},
		{"SteadyLoad_High", 50, 50, 0},
		{"BurstTraffic_Short", 50, 20, 50 * time.Millisecond},
		{"BurstTraffic_Long", 100, 10, 100 * time.Millisecond},
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

			// Latency SLAs
			if metrics.P95LatencyMs > 100 {
				t.Logf("WARNING: P95 latency exceeds 100ms threshold: %d ms", metrics.P95LatencyMs)
			}
			if metrics.P99LatencyMs > 200 {
				t.Logf("WARNING: P99 latency exceeds 200ms threshold: %d ms", metrics.P99LatencyMs)
			}
		})
	}
}

// BenchmarkClientServer_EndToEnd benchmarks end-to-end performance
func BenchmarkClientServer_EndToEnd(b *testing.B) {
	// Start server
	log := logger.New("test-server", "error")
	serverConfig := server.DefaultServerConfig()
	serverConfig.ListenAddress = "127.0.0.1:0"
	serverConfig.MaxConnections = 1000
	serverConfig.RecvChannelSize = 10000

	diameterServer := server.NewServer(serverConfig, log)
	if err := diameterServer.Start(); err != nil {
		b.Fatalf("Failed to start server: %v", err)
	}
	defer diameterServer.Stop()

	serverAddr := diameterServer.GetListener().Addr().String()
	_, port, _ := net.SplitHostPort(serverAddr)

	// Start message echo handler
	go func() {
		for msgCtx := range diameterServer.Receive() {
			msg := make([]byte, len(msgCtx.Message))
			copy(msg, msgCtx.Message)
			msg[4] &^= 0x80
			msgCtx.Connection.Send(msg)
		}
	}()

	// Create client pool
	ctx := context.Background()
	clientConfig := client.DefaultConfig()
	clientConfig.Host = "127.0.0.1"
	clientConfig.Port = parsePort(port)
	clientConfig.OriginHost = "test-client.example.com"
	clientConfig.OriginRealm = "example.com"
	clientConfig.ConnectionCount = 5

	pool, err := client.NewConnectionPool(ctx, clientConfig)
	if err != nil {
		b.Fatalf("Failed to create pool: %v", err)
	}

	if err := pool.Start(); err != nil {
		b.Fatalf("Failed to start pool: %v", err)
	}
	defer pool.Close()

	if err := pool.WaitForConnection(10 * time.Second); err != nil {
		b.Fatalf("Failed to wait for connection: %v", err)
	}

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

// Helper functions

func parsePort(portStr string) int {
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return port
}

func runEndToEndTest(t *testing.T, ctx context.Context, pool *client.ConnectionPool,
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

	// Start receiver goroutine
	recvDone := make(chan struct{})
	responseMap := make(map[uint32]chan time.Time)
	responseMu := sync.RWMutex{}
	testCtx, testCancel := context.WithTimeout(ctx, 30*time.Second)
	defer testCancel()

	go func() {
		defer close(recvDone)
		for {
			select {
			case <-testCtx.Done():
				return
			case resp, ok := <-pool.Receive():
				if !ok {
					return
				}
				if len(resp) >= 16 {
					h2hID := binary.BigEndian.Uint32(resp[12:16])
					responseMu.RLock()
					if ch, exists := responseMap[h2hID]; exists {
						select {
						case ch <- time.Now():
						default:
						}
					}
					responseMu.RUnlock()
				}
			}
		}
	}()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			for j := 0; j < requestsPerClient; j++ {
				// Create a copy of the message for each request
				requestMsg := make([]byte, len(msg))
				copy(requestMsg, msg)

				// Update hop-by-hop ID
				h2hID := uint32(clientID*10000 + j + 1)
				binary.BigEndian.PutUint32(requestMsg[12:16], h2hID)

				// Create response channel
				respCh := make(chan time.Time, 1)
				responseMu.Lock()
				responseMap[h2hID] = respCh
				responseMu.Unlock()

				reqStart := time.Now()
				err := pool.Send(requestMsg)

				if err != nil {
					atomic.AddInt64(&failCount, 1)
					responseMu.Lock()
					delete(responseMap, h2hID)
					responseMu.Unlock()
					continue
				}

				// Wait for response with timeout
				select {
				case respTime := <-respCh:
					latency := respTime.Sub(reqStart).Milliseconds()
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
				case <-time.After(2 * time.Second):
					atomic.AddInt64(&failCount, 1)
				case <-testCtx.Done():
					atomic.AddInt64(&failCount, 1)
					return
				}

				responseMu.Lock()
				delete(responseMap, h2hID)
				responseMu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	endTime := time.Now()

	// Cleanup receiver
	time.Sleep(100 * time.Millisecond)
	<-recvDone

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

func runLatencyTest(t *testing.T, ctx context.Context, pool *client.ConnectionPool,
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

	// Start receiver
	recvDone := make(chan struct{})
	responseMap := make(map[uint32]chan time.Time)
	responseMu := sync.RWMutex{}
	testCtx, testCancel := context.WithTimeout(ctx, 30*time.Second)
	defer testCancel()

	go func() {
		defer close(recvDone)
		for {
			select {
			case <-testCtx.Done():
				return
			case resp, ok := <-pool.Receive():
				if !ok {
					return
				}
				if len(resp) >= 16 {
					h2hID := binary.BigEndian.Uint32(resp[12:16])
					responseMu.RLock()
					if ch, exists := responseMap[h2hID]; exists {
						select {
						case ch <- time.Now():
						default:
						}
					}
					responseMu.RUnlock()
				}
			}
		}
	}()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			for j := 0; j < requestsPerClient; j++ {
				if burstDelay > 0 {
					time.Sleep(burstDelay)
				}

				// Create a copy of the message for each request
				requestMsg := make([]byte, len(msg))
				copy(requestMsg, msg)

				h2hID := uint32(clientID*10000 + j + 1)
				binary.BigEndian.PutUint32(requestMsg[12:16], h2hID)

				respCh := make(chan time.Time, 1)
				responseMu.Lock()
				responseMap[h2hID] = respCh
				responseMu.Unlock()

				reqStart := time.Now()
				err := pool.Send(requestMsg)

				if err != nil {
					atomic.AddInt64(&failCount, 1)
					responseMu.Lock()
					delete(responseMap, h2hID)
					responseMu.Unlock()
					continue
				}

				select {
				case respTime := <-respCh:
					latency := respTime.Sub(reqStart).Milliseconds()
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
				case <-time.After(2 * time.Second):
					atomic.AddInt64(&failCount, 1)
				case <-testCtx.Done():
					atomic.AddInt64(&failCount, 1)
					return
				}

				responseMu.Lock()
				delete(responseMap, h2hID)
				responseMu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	endTime := time.Now()

	time.Sleep(100 * time.Millisecond)
	<-recvDone

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
