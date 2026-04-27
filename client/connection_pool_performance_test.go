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

	"github.com/hsdfat/go-zlog/logger"
)

// PerformanceMetrics captures the results of a load test run.
type PerformanceMetrics struct {
	TotalRequests  int64
	SuccessfulReqs int64
	FailedReqs     int64
	Throughput     float64 // req/s
	Min            time.Duration
	Mean           time.Duration
	P50            time.Duration
	P95            time.Duration
	P99            time.Duration
	Max            time.Duration
}

// latencyBucket accumulates per-request durations for percentile calculation.
type latencyBucket struct {
	mu        sync.Mutex
	latencies []time.Duration
}

func newLatencyBucket(cap int) *latencyBucket {
	return &latencyBucket{latencies: make([]time.Duration, 0, cap)}
}

func (lb *latencyBucket) add(d time.Duration) {
	lb.mu.Lock()
	lb.latencies = append(lb.latencies, d)
	lb.mu.Unlock()
}

func (lb *latencyBucket) percentile(p float64) time.Duration {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	if len(lb.latencies) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(lb.latencies))
	copy(sorted, lb.latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func (lb *latencyBucket) stats() (min, mean, max time.Duration) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	if len(lb.latencies) == 0 {
		return
	}
	min = lb.latencies[0]
	max = lb.latencies[0]
	var sum time.Duration
	for _, d := range lb.latencies {
		sum += d
		if d < min {
			min = d
		}
		if d > max {
			max = d
		}
	}
	mean = sum / time.Duration(len(lb.latencies))
	return
}

// createTestMessage builds a valid minimal Diameter message of the given size.
// The message has a proper 3-byte length field, request flag, command code 999,
// and Application-ID 16777252 (S13) — enough that a framing-aware server can
// read it without closing the connection.
func createTestMessage(size int) []byte {
	if size < 20 {
		size = 20
	}
	msg := make([]byte, size)
	msg[0] = 1 // Version
	// 3-byte big-endian length (bytes 1-3)
	msg[1] = byte(size >> 16)
	msg[2] = byte(size >> 8)
	msg[3] = byte(size)
	msg[4] = 0x80 // Flags: R(equest) bit set
	// 3-byte big-endian command code 999 (bytes 5-7)
	msg[5] = byte(999 >> 16)
	msg[6] = byte(999 >> 8)
	msg[7] = byte(999 & 0xFF)
	binary.BigEndian.PutUint32(msg[8:12], 16777252)  // Application-ID (S13)
	binary.BigEndian.PutUint32(msg[12:16], 1)        // Hop-by-Hop ID
	binary.BigEndian.PutUint32(msg[16:20], 1)        // End-to-End ID
	for i := 20; i < size; i++ {
		msg[i] = byte(i % 256)
	}
	return msg
}

// testHelper is satisfied by both *testing.T and *testing.B.
type testHelper interface {
	Fatalf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

// startMockServer runs a minimal Diameter echo server on addr ("host:0" for
// any free port). It performs CER/CEA and then echoes every subsequent message
// back as an answer (request bit cleared). Returns the listener and a cleanup
// function.
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
				mockServerHandleConn(c, stop)
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

func mockServerHandleConn(c net.Conn, stop <-chan struct{}) {
	// CER/CEA handshake
	if err := c.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return
	}

	header := make([]byte, 20)
	if _, err := io.ReadFull(c, header); err != nil {
		return
	}
	length := uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
	if length < 20 || length > 65535 {
		return
	}
	cerData := make([]byte, length)
	copy(cerData[:20], header)
	if length > 20 {
		if _, err := io.ReadFull(c, cerData[20:]); err != nil {
			return
		}
	}

	// Minimal CEA: 20-byte header + 12-byte Result-Code AVP = 32 bytes
	const ceaSize = 32
	cea := make([]byte, ceaSize)
	cea[0] = 1
	cea[1] = byte(ceaSize >> 16)
	cea[2] = byte(ceaSize >> 8)
	cea[3] = byte(ceaSize)
	cea[4] = 0x00 // Answer
	cea[5] = byte(257 >> 16)
	cea[6] = byte(257 >> 8)
	cea[7] = byte(257 & 0xFF)
	binary.BigEndian.PutUint32(cea[8:12], 0)
	copy(cea[12:16], cerData[12:16]) // HbH
	copy(cea[16:20], cerData[16:20]) // E2E
	// Result-Code AVP 268, M-bit, length 12, value 2001
	binary.BigEndian.PutUint32(cea[20:24], 268)
	cea[24] = 0x40
	cea[25] = 0
	cea[26] = 0
	cea[27] = 12
	binary.BigEndian.PutUint32(cea[28:32], 2001)

	if _, err := c.Write(cea); err != nil {
		return
	}
	c.SetReadDeadline(time.Time{})

	// Echo loop
	msgHeader := make([]byte, 20)
	for {
		select {
		case <-stop:
			return
		default:
		}

		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.ReadFull(c, msgHeader); err != nil {
			return
		}
		msgLen := uint32(msgHeader[1])<<16 | uint32(msgHeader[2])<<8 | uint32(msgHeader[3])
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
		msg[4] &^= 0x80 // clear R-bit → answer
		if _, err := c.Write(msg); err != nil {
			return
		}
	}
}

func parsePort(portStr string) int {
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return port
}

// poolForTest builds and starts a ConnectionPool pointing at the mock server.
// Callers must defer pool.Close().
func poolForTest(t testHelper, ctx context.Context, listener net.Listener, connCount, sendBuf int) *ConnectionPool {
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	cfg := DefaultConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = parsePort(port)
	cfg.OriginHost = "test-client.example.com"
	cfg.OriginRealm = "example.com"
	cfg.ConnectionCount = connCount
	cfg.SendBufferSize = sendBuf
	cfg.RecvBufferSize = sendBuf

	pool, err := NewConnectionPool(ctx, cfg, logger.NewLogger())
	if err != nil {
		t.Fatalf("NewConnectionPool: %v", err)
	}
	if err := pool.Start(); err != nil {
		t.Fatalf("pool.Start: %v", err)
	}
	return pool
}

// runLoadTest sends concurrency×requests messages using pool.Send(), collects
// send-side latency, and returns aggregated metrics. thinkTime injects a delay
// between sends within each goroutine (0 = no delay).
func runLoadTest(t *testing.T, ctx context.Context, pool *ConnectionPool,
	concurrency, requests int, msgSize int, thinkTime time.Duration) *PerformanceMetrics {

	total := concurrency * requests
	bucket := newLatencyBucket(total)
	var successes, failures atomic.Int64

	var wg sync.WaitGroup
	start := time.Now()

	msg := createTestMessage(msgSize)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < requests; j++ {
				if thinkTime > 0 {
					time.Sleep(thinkTime)
				}
				t0 := time.Now()
				err := pool.Send(msg)
				bucket.add(time.Since(t0))
				if err != nil {
					failures.Add(1)
				} else {
					successes.Add(1)
				}
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	min, mean, max := bucket.stats()
	n := int64(total)
	return &PerformanceMetrics{
		TotalRequests:  n,
		SuccessfulReqs: successes.Load(),
		FailedReqs:     failures.Load(),
		Throughput:     float64(n) / elapsed.Seconds(),
		Min:            min,
		Mean:           mean,
		P50:            bucket.percentile(0.50),
		P95:            bucket.percentile(0.95),
		P99:            bucket.percentile(0.99),
		Max:            max,
	}
}

func logMetrics(t *testing.T, label string, m *PerformanceMetrics) {
	t.Helper()
	t.Logf("%s: total=%d ok=%d fail=%d tput=%.0f req/s",
		label, m.TotalRequests, m.SuccessfulReqs, m.FailedReqs, m.Throughput)
	t.Logf("  latency: min=%s p50=%s p95=%s p99=%s max=%s mean=%s",
		m.Min, m.P50, m.P95, m.P99, m.Max, m.Mean)
}

// TestPerformance_ConnectionPool_Throughput measures send-side throughput of
// ConnectionPool.Send() across message sizes and concurrency levels.
func TestPerformance_ConnectionPool_Throughput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}

	ctx := context.Background()
	listener, cleanup := startMockServer(t, "127.0.0.1:0")
	defer cleanup()

	scenarios := []struct {
		name        string
		concurrency int
		requests    int
		msgSize     int
	}{
		{"low-conc/small-msg", 5, 200, 100},
		{"low-conc/large-msg", 5, 200, 1000},
		{"med-conc/small-msg", 25, 200, 100},
		{"med-conc/large-msg", 25, 200, 1000},
		{"high-conc/small-msg", 50, 200, 100},
		{"high-conc/large-msg", 50, 200, 1000},
	}

	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			pool := poolForTest(t, ctx, listener, 5, sc.concurrency*sc.requests+100)
			defer pool.Close()
			time.Sleep(500 * time.Millisecond)

			m := runLoadTest(t, ctx, pool, sc.concurrency, sc.requests, sc.msgSize, 0)
			logMetrics(t, sc.name, m)
		})
	}
}

// TestPerformance_ConnectionPool_Latency measures ConnectionPool.Send() latency
// under various concurrency and burst patterns.
func TestPerformance_ConnectionPool_Latency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}

	ctx := context.Background()
	listener, cleanup := startMockServer(t, "127.0.0.1:0")
	defer cleanup()

	pool := poolForTest(t, ctx, listener, 5, 2000)
	defer pool.Close()
	time.Sleep(500 * time.Millisecond)

	scenarios := []struct {
		name        string
		concurrency int
		requests    int
		thinkTime   time.Duration
	}{
		{"steady/low", 10, 100, 0},
		{"steady/medium", 25, 100, 0},
		{"steady/high", 50, 100, 0},
		{"burst/100ms-gap", 50, 40, 100 * time.Millisecond},
		{"burst/200ms-gap", 100, 20, 200 * time.Millisecond},
	}

	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			m := runLoadTest(t, ctx, pool, sc.concurrency, sc.requests, 100, sc.thinkTime)
			logMetrics(t, sc.name, m)
			if m.P95 > 10*time.Millisecond {
				t.Logf("WARN: p95 send latency %s exceeds 10ms", m.P95)
			}
		})
	}
}

// BenchmarkConnectionPool_Send measures the steady-state send throughput of a
// 4-connection pool. Run with: go test -bench=BenchmarkConnectionPool_Send -benchtime=5s
func BenchmarkConnectionPool_Send(b *testing.B) {
	ctx := context.Background()
	listener, cleanup := startMockServer(b, "127.0.0.1:0")
	defer cleanup()

	pool := poolForTest(b, ctx, listener, 4, b.N+1000)
	defer pool.Close()
	time.Sleep(500 * time.Millisecond)

	msg := createTestMessage(100)
	b.SetBytes(int64(len(msg)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := pool.Send(msg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConnectionPool_SendParallel is the parallel variant of the send
// benchmark; it saturates the pool from multiple goroutines simultaneously.
// Run with: go test -bench=BenchmarkConnectionPool_SendParallel -cpu=1,4,8
func BenchmarkConnectionPool_SendParallel(b *testing.B) {
	ctx := context.Background()
	listener, cleanup := startMockServer(b, "127.0.0.1:0")
	defer cleanup()

	pool := poolForTest(b, ctx, listener, 4, b.N+10000)
	defer pool.Close()
	time.Sleep(500 * time.Millisecond)

	msg := createTestMessage(100)
	b.SetBytes(int64(len(msg)))
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := pool.Send(msg); err != nil {
				b.Error(err)
			}
		}
	})
}
