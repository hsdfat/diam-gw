package server

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/pkg/connection"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

// TestMiddlewareIntegration_ErrorCapture tests error capturing middleware
func TestMiddlewareIntegration_ErrorCapture(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var errorsCaptured atomic.Uint64
	var handlerCalls atomic.Uint64

	// Error capture middleware
	errorCapture := func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			defer func() {
				if r := recover(); r != nil {
					errorsCaptured.Add(1)
					t.Logf("Captured error: %v", r)
				}
			}()
			next(msg, conn)
		}
	}

	srv.Use(errorCapture)

	// Handler that panics
	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			handlerCalls.Add(1)
			panic("intentional panic for testing")
		})

	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)
	addr := srv.GetListener().Addr().String()

	client := newTestClient(t, addr)
	defer client.Close()

	client.sendCER(t)
	client.receiveCEA(t)

	// Send multiple DWR requests
	for i := 0; i < 3; i++ {
		sendDWR(t, client.conn)
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	if handlerCalls.Load() != 3 {
		t.Errorf("Expected 3 handler calls, got %d", handlerCalls.Load())
	}

	if errorsCaptured.Load() != 3 {
		t.Errorf("Expected 3 errors captured, got %d", errorsCaptured.Load())
	}

	t.Logf("Successfully captured %d errors from %d handler calls",
		errorsCaptured.Load(), handlerCalls.Load())
}

// TestMiddlewareIntegration_RateLimitingDetailed tests rate limiting with detailed tracking
func TestMiddlewareIntegration_RateLimitingDetailed(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var allowed atomic.Uint64
	var rejected atomic.Uint64

	// Custom rate limiting middleware with tracking
	rateLimiter := func(maxPerSecond int) Middleware {
		var (
			lastReset     = time.Now()
			requestCount  = 0
		)

		return func(next Handler) Handler {
			return func(msg *Message, conn Conn) {
				now := time.Now()

				// Reset counter every second
				if now.Sub(lastReset) >= time.Second {
					lastReset = now
					requestCount = 0
				}

				requestCount++
				if requestCount > maxPerSecond {
					rejected.Add(1)
					t.Logf("Request %d rejected (rate limit: %d/sec)", requestCount, maxPerSecond)
					return
				}

				allowed.Add(1)
				next(msg, conn)
			}
		}
	}

	srv.Use(rateLimiter(3))

	var handlerCalls atomic.Uint64
	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			handlerCalls.Add(1)
		})

	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)
	addr := srv.GetListener().Addr().String()

	client := newTestClient(t, addr)
	defer client.Close()

	client.sendCER(t)
	client.receiveCEA(t)
	time.Sleep(100 * time.Millisecond)

	// Reset counters after CER (CER consumed one slot)
	allowed.Store(0)
	rejected.Store(0)
	handlerCalls.Store(0)

	// Send 10 requests rapidly
	for i := 0; i < 10; i++ {
		sendDWR(t, client.conn)
		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	t.Logf("Allowed: %d, Rejected: %d, Handler calls: %d",
		allowed.Load(), rejected.Load(), handlerCalls.Load())

	if allowed.Load() > 3 {
		t.Errorf("Rate limit failed: allowed %d requests, limit was 3", allowed.Load())
	}

	if handlerCalls.Load() != allowed.Load() {
		t.Errorf("Handler calls (%d) should match allowed requests (%d)",
			handlerCalls.Load(), allowed.Load())
	}

	if rejected.Load() != 10-allowed.Load() {
		t.Errorf("Expected %d rejected requests, got %d",
			10-allowed.Load(), rejected.Load())
	}
}

// TestMiddlewareIntegration_MessageSizeTracking tests tracking message sizes
func TestMiddlewareIntegration_MessageSizeTracking(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var totalBytes atomic.Uint64
	var messageCount atomic.Uint64
	var minSize atomic.Uint64
	var maxSize atomic.Uint64
	minSize.Store(^uint64(0)) // Max uint64

	// Message size tracking middleware
	sizeTracker := func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			size := uint64(msg.Length)
			totalBytes.Add(size)
			messageCount.Add(1)

			// Update min
			for {
				oldMin := minSize.Load()
				if size >= oldMin {
					break
				}
				if minSize.CompareAndSwap(oldMin, size) {
					break
				}
			}

			// Update max
			for {
				oldMax := maxSize.Load()
				if size <= oldMax {
					break
				}
				if maxSize.CompareAndSwap(oldMax, size) {
					break
				}
			}

			next(msg, conn)
		}
	}

	srv.Use(sizeTracker)

	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			// Handler
		})

	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)
	addr := srv.GetListener().Addr().String()

	client := newTestClient(t, addr)
	defer client.Close()

	client.sendCER(t)
	client.receiveCEA(t)
	time.Sleep(50 * time.Millisecond)

	// Reset counters after CER
	totalBytes.Store(0)
	messageCount.Store(0)
	minSize.Store(^uint64(0))
	maxSize.Store(0)

	// Send multiple DWR requests
	for i := 0; i < 5; i++ {
		sendDWR(t, client.conn)
		time.Sleep(20 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	count := messageCount.Load()
	total := totalBytes.Load()
	min := minSize.Load()
	max := maxSize.Load()
	avg := total / count

	t.Logf("Message size stats: count=%d, total=%d bytes, avg=%d bytes, min=%d, max=%d",
		count, total, avg, min, max)

	if count != 5 {
		t.Errorf("Expected 5 messages, got %d", count)
	}

	if total == 0 {
		t.Error("Total bytes should not be zero")
	}

	if min == 0 || min == ^uint64(0) {
		t.Error("Min size not properly set")
	}

	if max == 0 {
		t.Error("Max size not properly set")
	}

	if avg == 0 {
		t.Error("Average size should not be zero")
	}
}

// TestMiddlewareIntegration_TimingAnalysis tests processing time analysis
func TestMiddlewareIntegration_TimingAnalysis(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var totalDuration atomic.Uint64
	var messageCount atomic.Uint64
	var slowMessages atomic.Uint64

	// Timing analysis middleware
	timingAnalyzer := func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			start := time.Now()

			next(msg, conn)

			duration := time.Since(start)
			totalDuration.Add(uint64(duration.Microseconds()))
			messageCount.Add(1)

			// Track slow messages (>10ms)
			if duration > 10*time.Millisecond {
				slowMessages.Add(1)
				t.Logf("Slow message detected: %v", duration)
			}
		}
	}

	srv.Use(timingAnalyzer)

	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			// Simulate some processing
			time.Sleep(5 * time.Millisecond)
		})

	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)
	addr := srv.GetListener().Addr().String()

	client := newTestClient(t, addr)
	defer client.Close()

	client.sendCER(t)
	client.receiveCEA(t)
	time.Sleep(50 * time.Millisecond)

	// Reset counters after CER
	totalDuration.Store(0)
	messageCount.Store(0)
	slowMessages.Store(0)

	// Send requests
	for i := 0; i < 5; i++ {
		sendDWR(t, client.conn)
		time.Sleep(20 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	count := messageCount.Load()
	total := totalDuration.Load()
	slow := slowMessages.Load()
	avg := total / count

	t.Logf("Timing analysis: count=%d, avg=%d μs, slow_messages=%d",
		count, avg, slow)

	if count != 5 {
		t.Errorf("Expected 5 messages, got %d", count)
	}

	if avg == 0 {
		t.Error("Average duration should not be zero")
	}
}

// TestMiddlewareIntegration_MultipleMiddlewaresCombined tests combining multiple middlewares
func TestMiddlewareIntegration_MultipleMiddlewaresCombined(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var loggingCalls atomic.Uint64
	var validationCalls atomic.Uint64
	var metricsCalls atomic.Uint64
	var handlerCalls atomic.Uint64

	// Logging middleware
	loggingMW := func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			loggingCalls.Add(1)
			t.Logf("Logging: processing message from %s", conn.RemoteAddr())
			next(msg, conn)
		}
	}

	// Validation middleware
	validationMW := func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			validationCalls.Add(1)
			if msg.Length < 20 {
				t.Error("Validation failed: message too short")
				return
			}
			next(msg, conn)
		}
	}

	// Metrics middleware
	metricsMW := func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			metricsCalls.Add(1)
			next(msg, conn)
		}
	}

	// Register all middlewares
	srv.Use(loggingMW)
	srv.Use(validationMW)
	srv.Use(metricsMW)

	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			handlerCalls.Add(1)
		})

	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)
	addr := srv.GetListener().Addr().String()

	client := newTestClient(t, addr)
	defer client.Close()

	client.sendCER(t)
	client.receiveCEA(t)
	time.Sleep(50 * time.Millisecond)

	// Reset counters after CER/CEA
	loggingCalls.Store(0)
	validationCalls.Store(0)
	metricsCalls.Store(0)
	handlerCalls.Store(0)

	// Send requests
	for i := 0; i < 3; i++ {
		sendDWR(t, client.conn)
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	t.Logf("Middleware calls - Logging: %d, Validation: %d, Metrics: %d, Handler: %d",
		loggingCalls.Load(), validationCalls.Load(), metricsCalls.Load(), handlerCalls.Load())

	// All middlewares should be called for each message
	expected := uint64(3)
	if loggingCalls.Load() != expected {
		t.Errorf("Expected %d logging calls, got %d", expected, loggingCalls.Load())
	}
	if validationCalls.Load() != expected {
		t.Errorf("Expected %d validation calls, got %d", expected, validationCalls.Load())
	}
	if metricsCalls.Load() != expected {
		t.Errorf("Expected %d metrics calls, got %d", expected, metricsCalls.Load())
	}
	if handlerCalls.Load() != expected {
		t.Errorf("Expected %d handler calls, got %d", expected, handlerCalls.Load())
	}
}

// TestMiddlewareIntegration_ConditionalByCommandCode tests command-specific middleware
func TestMiddlewareIntegration_ConditionalByCommandCode(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var dwrMiddlewareCalls atomic.Uint64
	var cerMiddlewareCalls atomic.Uint64
	var dwrHandlerCalls atomic.Uint64
	var cerHandlerCalls atomic.Uint64

	// DWR-specific middleware (code 280)
	dwrMW := CommandSpecificMiddleware(280, func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			dwrMiddlewareCalls.Add(1)
			t.Log("DWR middleware executed")
			next(msg, conn)
		}
	})

	// CER-specific middleware (code 257)
	cerMW := CommandSpecificMiddleware(257, func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			cerMiddlewareCalls.Add(1)
			t.Log("CER middleware executed")
			next(msg, conn)
		}
	})

	srv.Use(dwrMW)
	srv.Use(cerMW)

	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			dwrHandlerCalls.Add(1)
		})

	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)
	addr := srv.GetListener().Addr().String()

	client := newTestClient(t, addr)
	defer client.Close()

	// Send CER (should trigger CER middleware but not DWR middleware)
	client.sendCER(t)
	client.receiveCEA(t)
	time.Sleep(50 * time.Millisecond)

	// Send DWR (should trigger DWR middleware but not CER middleware)
	for i := 0; i < 3; i++ {
		sendDWR(t, client.conn)
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	t.Logf("CER middleware calls: %d, DWR middleware calls: %d",
		cerMiddlewareCalls.Load(), dwrMiddlewareCalls.Load())
	t.Logf("CER handler calls: %d (default), DWR handler calls: %d",
		cerHandlerCalls.Load(), dwrHandlerCalls.Load())

	// CER middleware should be called once (for CER)
	if cerMiddlewareCalls.Load() != 1 {
		t.Errorf("Expected 1 CER middleware call, got %d", cerMiddlewareCalls.Load())
	}

	// DWR middleware should be called 3 times (for 3 DWRs)
	if dwrMiddlewareCalls.Load() != 3 {
		t.Errorf("Expected 3 DWR middleware calls, got %d", dwrMiddlewareCalls.Load())
	}

	// DWR handler should be called 3 times
	if dwrHandlerCalls.Load() != 3 {
		t.Errorf("Expected 3 DWR handler calls, got %d", dwrHandlerCalls.Load())
	}
}

// TestMiddlewareIntegration_ContextPropagation tests context propagation through middleware
func TestMiddlewareIntegration_ContextPropagation(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var contextChecked atomic.Bool

	// Middleware that checks connection context
	contextMW := func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			ctx := conn.Context()
			if ctx != nil {
				contextChecked.Store(true)
				t.Log("Context available in middleware")
			}
			next(msg, conn)
		}
	}

	srv.Use(contextMW)

	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			ctx := conn.Context()
			if ctx == nil {
				t.Error("Context should not be nil in handler")
			}
		})

	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)
	addr := srv.GetListener().Addr().String()

	client := newTestClient(t, addr)
	defer client.Close()

	client.sendCER(t)
	client.receiveCEA(t)

	sendDWR(t, client.conn)
	time.Sleep(100 * time.Millisecond)

	if !contextChecked.Load() {
		t.Error("Context was not checked in middleware")
	}
}

// TestMiddlewareIntegration_NoMiddleware tests that handlers work without middleware
func TestMiddlewareIntegration_NoMiddleware(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	// Don't add any middleware

	var handlerCalls atomic.Uint64
	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			handlerCalls.Add(1)
		})

	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)
	addr := srv.GetListener().Addr().String()

	client := newTestClient(t, addr)
	defer client.Close()

	client.sendCER(t)
	client.receiveCEA(t)

	sendDWR(t, client.conn)
	time.Sleep(100 * time.Millisecond)

	if handlerCalls.Load() != 1 {
		t.Errorf("Expected 1 handler call without middleware, got %d", handlerCalls.Load())
	}

	t.Log("Handler works correctly without any middleware")
}
