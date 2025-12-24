package server

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/commands/base"
	"github.com/hsdfat/diam-gw/models_base"
	"github.com/hsdfat/diam-gw/pkg/connection"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

func TestMiddlewareBasic(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var beforeCalled, afterCalled atomic.Bool
	var handlerCalled atomic.Bool

	// Create a simple middleware
	testMiddleware := func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			beforeCalled.Store(true)
			next(msg, conn)
			afterCalled.Store(true)
		}
	}

	srv.Use(testMiddleware)

	// Register a handler
	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			handlerCalled.Store(true)
		})

	// Start server
	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)
	addr := srv.GetListener().Addr().String()

	// Connect and send DWR
	client := newTestClient(t, addr)
	defer client.Close()

	client.sendCER(t)
	client.receiveCEA(t)

	sendDWR(t, client.conn)
	time.Sleep(100 * time.Millisecond)

	if !beforeCalled.Load() {
		t.Error("Middleware before handler was not called")
	}
	if !handlerCalled.Load() {
		t.Error("Handler was not called")
	}
	if !afterCalled.Load() {
		t.Error("Middleware after handler was not called")
	}
}

func TestMiddlewareChain(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var order []string
	orderMu := &atomic.Value{}
	orderMu.Store([]string{})

	addToOrder := func(s string) {
		current := orderMu.Load().([]string)
		orderMu.Store(append(current, s))
	}

	// Create three middlewares
	mw1 := func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			addToOrder("mw1-before")
			next(msg, conn)
			addToOrder("mw1-after")
		}
	}

	mw2 := func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			addToOrder("mw2-before")
			next(msg, conn)
			addToOrder("mw2-after")
		}
	}

	mw3 := func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			addToOrder("mw3-before")
			next(msg, conn)
			addToOrder("mw3-after")
		}
	}

	// Register middlewares
	srv.Use(mw1)
	srv.Use(mw2)
	srv.Use(mw3)

	// Register handler
	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			addToOrder("handler")
		})

	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)
	addr := srv.GetListener().Addr().String()

	client := newTestClient(t, addr)
	defer client.Close()

	client.sendCER(t)
	client.receiveCEA(t)

	// Reset order after CER/CEA (we only want to test DWR)
	orderMu.Store([]string{})

	sendDWR(t, client.conn)
	time.Sleep(100 * time.Millisecond)

	order = orderMu.Load().([]string)
	expected := []string{
		"mw1-before", "mw2-before", "mw3-before",
		"handler",
		"mw3-after", "mw2-after", "mw1-after",
	}

	if len(order) != len(expected) {
		t.Fatalf("Expected %d calls, got %d: %v", len(expected), len(order), order)
	}

	for i, exp := range expected {
		if order[i] != exp {
			t.Errorf("At position %d: expected %s, got %s", i, exp, order[i])
		}
	}
}

func TestLoggingMiddleware(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	srv.Use(LoggingMiddleware(srv))

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

	sendDWR(t, client.conn)
	time.Sleep(100 * time.Millisecond)
}

func TestRecoveryMiddleware(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var recovered atomic.Bool

	// Custom recovery middleware for testing
	recoveryMW := func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			defer func() {
				if r := recover(); r != nil {
					recovered.Store(true)
				}
			}()
			next(msg, conn)
		}
	}

	srv.Use(recoveryMW)

	// Register handler that panics
	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			panic("test panic")
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

	if !recovered.Load() {
		t.Error("Recovery middleware did not catch panic")
	}
}

func TestValidationMiddleware(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var validationPassed atomic.Bool

	srv.Use(ValidationMiddleware(srv))

	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			validationPassed.Store(true)
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

	if !validationPassed.Load() {
		t.Error("Valid message did not pass validation middleware")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var handlerCallCount atomic.Int32

	// Rate limit to 2 requests per second
	srv.Use(RateLimitMiddleware(srv, 2))

	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			handlerCallCount.Add(1)
		})

	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)
	addr := srv.GetListener().Addr().String()

	client := newTestClient(t, addr)
	defer client.Close()

	client.sendCER(t)
	client.receiveCEA(t)

	// Send 5 requests quickly
	for i := 0; i < 5; i++ {
		sendDWR(t, client.conn)
		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	// Should only process first 2 requests
	count := handlerCallCount.Load()
	if count > 2 {
		t.Errorf("Rate limit failed: expected max 2 calls, got %d", count)
	}
}

func TestConditionalMiddleware(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var mwCalled atomic.Bool

	// Middleware that only runs for large messages
	condition := func(msg *Message, conn Conn) bool {
		return msg.Length > 100
	}

	conditionalMW := ConditionalMiddleware(condition, func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			mwCalled.Store(true)
			next(msg, conn)
		}
	})

	srv.Use(conditionalMW)

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

	// DWR is small, should not trigger middleware
	sendDWR(t, client.conn)
	time.Sleep(100 * time.Millisecond)

	if mwCalled.Load() {
		t.Error("Conditional middleware should not have been called for small message")
	}
}

func TestInterfaceSpecificMiddleware(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var baseMWCalled, s13MWCalled atomic.Bool

	// Middleware only for base protocol (interface 0)
	baseMW := InterfaceSpecificMiddleware(0, func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			baseMWCalled.Store(true)
			next(msg, conn)
		}
	})

	// Middleware only for S13 (interface 16777252)
	s13MW := InterfaceSpecificMiddleware(16777252, func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			s13MWCalled.Store(true)
			next(msg, conn)
		}
	})

	srv.Use(baseMW)
	srv.Use(s13MW)

	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			// DWR handler
		})

	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)
	addr := srv.GetListener().Addr().String()

	client := newTestClient(t, addr)
	defer client.Close()

	client.sendCER(t)
	client.receiveCEA(t)

	// Send DWR (base protocol, interface 0)
	sendDWR(t, client.conn)
	time.Sleep(100 * time.Millisecond)

	if !baseMWCalled.Load() {
		t.Error("Base protocol middleware should have been called for DWR")
	}
	if s13MWCalled.Load() {
		t.Error("S13 middleware should not have been called for DWR")
	}
}

func TestCommandSpecificMiddleware(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var dwrMWCalled, cerMWCalled atomic.Bool

	// Middleware only for DWR (code 280)
	dwrMW := CommandSpecificMiddleware(280, func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			dwrMWCalled.Store(true)
			next(msg, conn)
		}
	})

	// Middleware only for CER (code 257)
	cerMW := CommandSpecificMiddleware(257, func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			cerMWCalled.Store(true)
			next(msg, conn)
		}
	})

	srv.Use(dwrMW)
	srv.Use(cerMW)

	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			// DWR handler
		})

	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)
	addr := srv.GetListener().Addr().String()

	client := newTestClient(t, addr)
	defer client.Close()

	client.sendCER(t)
	client.receiveCEA(t)

	// Send DWR
	sendDWR(t, client.conn)
	time.Sleep(100 * time.Millisecond)

	if !dwrMWCalled.Load() {
		t.Error("DWR middleware should have been called")
	}
	if cerMWCalled.Load() {
		t.Error("CER middleware should not have been called for DWR")
	}
}

func TestRequestOnlyMiddleware(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	var requestMWCalled atomic.Bool

	requestMW := RequestOnlyMiddleware(func(next Handler) Handler {
		return func(msg *Message, conn Conn) {
			requestMWCalled.Store(true)
			next(msg, conn)
		}
	})

	srv.Use(requestMW)

	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			// DWR handler
		})

	go srv.Start()
	defer srv.Stop()

	time.Sleep(100 * time.Millisecond)
	addr := srv.GetListener().Addr().String()

	client := newTestClient(t, addr)
	defer client.Close()

	client.sendCER(t)
	client.receiveCEA(t)

	// Send DWR (a request)
	sendDWR(t, client.conn)
	time.Sleep(100 * time.Millisecond)

	if !requestMWCalled.Load() {
		t.Error("Request-only middleware should have been called for DWR")
	}
}

func TestMetricsMiddleware(t *testing.T) {
	config := DefaultServerConfig()
	config.ListenAddress = "127.0.0.1:0"
	log := logger.New("test-server", "error")
	srv := NewServer(config, log)

	srv.Use(MetricsMiddleware(srv))

	srv.HandleFunc(connection.Command{Interface: 0, Code: 280, Request: true},
		func(msg *Message, conn Conn) {
			time.Sleep(10 * time.Millisecond) // Simulate work
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
	time.Sleep(200 * time.Millisecond)

	stats := srv.GetStats()
	if stats.MessagesReceived == 0 {
		t.Error("Metrics middleware should track received messages")
	}
}

// Helper functions
func sendDWR(t *testing.T, conn net.Conn) {
	dwr := base.NewDeviceWatchdogRequest()
	dwr.OriginHost = models_base.DiameterIdentity("test-client.example.com")
	dwr.OriginRealm = models_base.DiameterIdentity("example.com")

	dwrBytes, err := dwr.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal DWR: %v", err)
	}

	if _, err := conn.Write(dwrBytes); err != nil {
		t.Fatalf("Failed to send DWR: %v", err)
	}
}
