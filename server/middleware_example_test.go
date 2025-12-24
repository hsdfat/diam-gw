package server_test

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/hsdfat/diam-gw/pkg/connection"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

// Example_middlewareBasicUsage demonstrates basic middleware usage
func Example_middlewareBasicUsage() {
	// Create server
	config := server.DefaultServerConfig()
	log := logger.New("example-server", "info")
	s := server.NewServer(config, log)

	// Add global middlewares - these will run for ALL handlers
	s.Use(server.RecoveryMiddleware(s))      // Recover from panics
	s.Use(server.LoggingMiddleware(s))       // Log all requests
	s.Use(server.ValidationMiddleware(s))    // Validate message structure
	s.Use(server.MetricsMiddleware(s))       // Track metrics

	// Register a handler
	s.HandleFunc(connection.Command{Interface: 16777251, Code: 316, Request: true},
		func(msg *server.Message, conn server.Conn) {
			// Handle Update-Location-Request (S6a)
			fmt.Println("Handling ULR")
		})

	// All handlers will now be wrapped with:
	// Recovery -> Logging -> Validation -> Metrics -> Handler
}

// Example_middlewareRateLimiting demonstrates rate limiting middleware
func Example_middlewareRateLimiting() {
	config := server.DefaultServerConfig()
	log := logger.New("example-server", "info")
	s := server.NewServer(config, log)

	// Apply rate limiting: max 100 requests per second
	s.Use(server.RateLimitMiddleware(s, 100))

	// Register handlers
	s.HandleFunc(connection.Command{Interface: 16777251, Code: 316, Request: true},
		func(msg *server.Message, conn server.Conn) {
			fmt.Println("Handling ULR")
		})

	// Requests exceeding 100/second will be rejected
}

// Example_middlewareErrorCapture demonstrates error capturing middleware
func Example_middlewareErrorCapture() {
	config := server.DefaultServerConfig()
	log := logger.New("example-server", "info")
	s := server.NewServer(config, log)

	// Create custom error capturing middleware
	var errorCount atomic.Uint64
	errorCaptureMiddleware := func(next server.Handler) server.Handler {
		return func(msg *server.Message, conn server.Conn) {
			defer func() {
				if r := recover(); r != nil {
					errorCount.Add(1)
					log.Errorw("Error captured",
						"error", r,
						"total_errors", errorCount.Load(),
						"remote_addr", conn.RemoteAddr())

					// Optionally send error response to client
					// sendErrorResponse(msg, conn)
				}
			}()

			next(msg, conn)
		}
	}

	s.Use(errorCaptureMiddleware)

	// Register handlers
	s.HandleFunc(connection.Command{Interface: 16777251, Code: 316, Request: true},
		func(msg *server.Message, conn server.Conn) {
			// This handler might panic
			// The error will be captured by middleware
		})

	fmt.Printf("Total errors captured: %d\n", errorCount.Load())
}

// Example_middlewareConditional demonstrates conditional middleware
func Example_middlewareConditional() {
	config := server.DefaultServerConfig()
	log := logger.New("example-server", "info")
	s := server.NewServer(config, log)

	// Apply logging only to S6a interface (16777251)
	s6aLogging := server.InterfaceSpecificMiddleware(16777251,
		server.LoggingMiddleware(s))
	s.Use(s6aLogging)

	// Apply timeout only to specific command (ULR = 316)
	ulrTimeout := server.CommandSpecificMiddleware(316,
		server.TimeoutMiddleware(s, 5*time.Second))
	s.Use(ulrTimeout)

	// Apply validation only to requests (not answers)
	requestValidation := server.RequestOnlyMiddleware(
		server.ValidationMiddleware(s))
	s.Use(requestValidation)

	s.HandleFunc(connection.Command{Interface: 16777251, Code: 316, Request: true},
		func(msg *server.Message, conn server.Conn) {
			fmt.Println("Handling ULR with conditional middlewares")
		})
}

// Example_middlewareChaining demonstrates chaining multiple middlewares
func Example_middlewareChaining() {
	config := server.DefaultServerConfig()
	log := logger.New("example-server", "info")
	s := server.NewServer(config, log)

	// Chain multiple middlewares together
	authAndLogging := server.ChainMiddleware(
		server.LoggingMiddleware(s),
		server.ValidationMiddleware(s),
		server.MetricsMiddleware(s),
	)

	s.Use(authAndLogging)

	s.HandleFunc(connection.Command{Interface: 16777251, Code: 316, Request: true},
		func(msg *server.Message, conn server.Conn) {
			fmt.Println("Handling request with chained middlewares")
		})
}

// Example_middlewareCustom demonstrates creating a custom middleware
func Example_middlewareCustom() {
	config := server.DefaultServerConfig()
	log := logger.New("example-server", "info")
	s := server.NewServer(config, log)

	// Custom middleware for message size tracking
	var totalBytes atomic.Uint64
	messageSizeTracker := func(next server.Handler) server.Handler {
		return func(msg *server.Message, conn server.Conn) {
			// Before handler
			totalBytes.Add(uint64(msg.Length))
			log.Infow("Message size tracked",
				"size", msg.Length,
				"total_bytes", totalBytes.Load())

			// Execute handler
			next(msg, conn)

			// After handler (if needed)
		}
	}

	s.Use(messageSizeTracker)

	s.HandleFunc(connection.Command{Interface: 16777251, Code: 316, Request: true},
		func(msg *server.Message, conn server.Conn) {
			fmt.Println("Handler executed")
		})

	fmt.Printf("Total bytes processed: %d\n", totalBytes.Load())
}

// Example_middlewareAuthentication demonstrates authentication middleware
func Example_middlewareAuthentication() {
	config := server.DefaultServerConfig()
	log := logger.New("example-server", "info")
	s := server.NewServer(config, log)

	// Custom authentication middleware
	authMiddleware := func(next server.Handler) server.Handler {
		return func(msg *server.Message, conn server.Conn) {
			// Check if connection has valid credentials
			// This is a simplified example
			remoteAddr := conn.RemoteAddr().String()

			// Validate remote address or check connection context
			if remoteAddr == "" {
				log.Warnw("Authentication failed - no remote address")
				return
			}

			// You could also check specific AVPs in the message
			// or maintain an authentication state per connection

			log.Infow("Authentication successful", "remote_addr", remoteAddr)
			next(msg, conn)
		}
	}

	s.Use(authMiddleware)

	s.HandleFunc(connection.Command{Interface: 16777251, Code: 316, Request: true},
		func(msg *server.Message, conn server.Conn) {
			fmt.Println("Handler executed after authentication")
		})
}

// Example_middlewareMessageFiltering demonstrates filtering messages
func Example_middlewareMessageFiltering() {
	config := server.DefaultServerConfig()
	log := logger.New("example-server", "info")
	s := server.NewServer(config, log)

	// Only allow specific command codes
	allowedCommands := map[int]bool{
		257: true, // CER
		280: true, // DWR
		316: true, // ULR
		321: true, // AIR
	}

	s.Use(server.CommandFilterMiddleware(s, allowedCommands))

	// All other command codes will be rejected

	s.HandleFunc(connection.Command{Interface: 16777251, Code: 316, Request: true},
		func(msg *server.Message, conn server.Conn) {
			fmt.Println("Allowed command processed")
		})
}

// Example_middlewarePerformanceMonitoring demonstrates performance monitoring
func Example_middlewarePerformanceMonitoring() {
	config := server.DefaultServerConfig()
	log := logger.New("example-server", "info")
	s := server.NewServer(config, log)

	// Performance monitoring middleware
	type PerfStats struct {
		count      atomic.Uint64
		totalTime  atomic.Uint64
		minTime    atomic.Uint64
		maxTime    atomic.Uint64
	}
	stats := &PerfStats{}
	stats.minTime.Store(^uint64(0)) // Max uint64

	perfMonitor := func(next server.Handler) server.Handler {
		return func(msg *server.Message, conn server.Conn) {
			start := time.Now()

			next(msg, conn)

			duration := time.Since(start).Microseconds()
			stats.count.Add(1)
			stats.totalTime.Add(uint64(duration))

			// Update min/max
			for {
				oldMin := stats.minTime.Load()
				if uint64(duration) >= oldMin {
					break
				}
				if stats.minTime.CompareAndSwap(oldMin, uint64(duration)) {
					break
				}
			}

			for {
				oldMax := stats.maxTime.Load()
				if uint64(duration) <= oldMax {
					break
				}
				if stats.maxTime.CompareAndSwap(oldMax, uint64(duration)) {
					break
				}
			}

			// Log every 1000 requests
			if stats.count.Load()%1000 == 0 {
				count := stats.count.Load()
				total := stats.totalTime.Load()
				avg := total / count
				log.Infow("Performance stats",
					"count", count,
					"avg_us", avg,
					"min_us", stats.minTime.Load(),
					"max_us", stats.maxTime.Load())
			}
		}
	}

	s.Use(perfMonitor)

	s.HandleFunc(connection.Command{Interface: 16777251, Code: 316, Request: true},
		func(msg *server.Message, conn server.Conn) {
			fmt.Println("Handler with performance monitoring")
		})
}
