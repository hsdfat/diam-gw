# Server Middleware Guide

This guide explains how to use middleware in the Diameter server package.

## Overview

Middleware is a function that wraps a handler and can execute code before and/or after the handler runs. Middlewares are useful for:

- Logging requests and responses
- Authentication and authorization
- Rate limiting
- Error handling and recovery
- Metrics collection
- Message validation
- Timeout enforcement

## Middleware Type

```go
type Middleware func(Handler) Handler
```

A middleware takes a `Handler` and returns a new `Handler` that wraps the original.

## Basic Usage

### 1. Register Global Middleware

Global middlewares apply to **all handlers** registered on the server:

```go
server := server.NewServer(config, logger)

// Add middlewares - they execute in order
server.Use(server.RecoveryMiddleware(server))
server.Use(server.LoggingMiddleware(server))
server.Use(server.ValidationMiddleware(server))

// Register handlers - all will be wrapped with the middlewares above
server.HandleFunc(command, handler)
```

### 2. Execution Order

Middlewares are executed in the order they are registered (FIFO):

```go
server.Use(middleware1)  // Runs first (outermost)
server.Use(middleware2)  // Runs second
server.Use(middleware3)  // Runs third (innermost)

// Execution flow:
// middleware1 (before) -> middleware2 (before) -> middleware3 (before)
//     -> HANDLER
// middleware3 (after) -> middleware2 (after) -> middleware1 (after)
```

## Built-in Middlewares

### Recovery Middleware

Recovers from panics in handlers and logs them:

```go
server.Use(server.RecoveryMiddleware(server))
```

### Logging Middleware

Logs request details and processing time:

```go
server.Use(server.LoggingMiddleware(server))
```

Example output:
```
Request received: remote_addr=192.168.1.10:3868, interface=16777251, code=316
Request processed: remote_addr=192.168.1.10:3868, duration_ms=15
```

### Validation Middleware

Validates message structure (length, header, command code):

```go
server.Use(server.ValidationMiddleware(server))
```

### Metrics Middleware

Tracks request metrics (duration, size):

```go
server.Use(server.MetricsMiddleware(server))
```

### Rate Limiting Middleware

Limits requests per second (simple implementation):

```go
// Allow max 100 requests per second
server.Use(server.RateLimitMiddleware(server, 100))
```

### Timeout Middleware

Enforces handler execution timeout:

```go
// 5 second timeout for all handlers
server.Use(server.TimeoutMiddleware(server, 5*time.Second))
```

### Command Filter Middleware

Only allows specific command codes:

```go
allowed := map[int]bool{
    257: true,  // CER
    280: true,  // DWR
    316: true,  // ULR
    321: true,  // AIR
}
server.Use(server.CommandFilterMiddleware(server, allowed))
```

## Conditional Middlewares

Apply middleware only when certain conditions are met:

### Interface-Specific Middleware

Apply middleware only for specific Diameter interfaces:

```go
// Only log S6a messages (interface 16777251)
s6aLogging := server.InterfaceSpecificMiddleware(16777251,
    server.LoggingMiddleware(server))
server.Use(s6aLogging)
```

### Command-Specific Middleware

Apply middleware only for specific commands:

```go
// Only timeout ULR requests (command code 316)
ulrTimeout := server.CommandSpecificMiddleware(316,
    server.TimeoutMiddleware(server, 5*time.Second))
server.Use(ulrTimeout)
```

### Request/Answer-Specific Middleware

Apply middleware only for requests or answers:

```go
// Validate only requests
requestValidation := server.RequestOnlyMiddleware(
    server.ValidationMiddleware(server))
server.Use(requestValidation)

// Log only answers
answerLogging := server.AnswerOnlyMiddleware(
    server.LoggingMiddleware(server))
server.Use(answerLogging)
```

### Custom Conditional Middleware

Create your own conditions:

```go
condition := func(msg *server.Message, conn server.Conn) bool {
    // Only apply middleware if message is larger than 1000 bytes
    return msg.Length > 1000
}

largeMsgMiddleware := server.ConditionalMiddleware(condition,
    server.LoggingMiddleware(server))
server.Use(largeMsgMiddleware)
```

## Chaining Middlewares

Chain multiple middlewares together:

```go
// Create a middleware chain
chain := server.ChainMiddleware(
    server.RecoveryMiddleware(server),
    server.LoggingMiddleware(server),
    server.ValidationMiddleware(server),
)

server.Use(chain)
```

## Creating Custom Middlewares

### Basic Custom Middleware

```go
customMiddleware := func(next server.Handler) server.Handler {
    return func(msg *server.Message, conn server.Conn) {
        // Code before handler
        logger.Info("Before handler")

        // Call the next handler
        next(msg, conn)

        // Code after handler
        logger.Info("After handler")
    }
}

server.Use(customMiddleware)
```

### Error Capturing Middleware

```go
var errorCount atomic.Uint64

errorCapture := func(next server.Handler) server.Handler {
    return func(msg *server.Message, conn server.Conn) {
        defer func() {
            if r := recover(); r != nil {
                errorCount.Add(1)
                logger.Errorw("Error captured",
                    "error", r,
                    "total_errors", errorCount.Load(),
                    "remote_addr", conn.RemoteAddr())
            }
        }()
        next(msg, conn)
    }
}

server.Use(errorCapture)
```

### Authentication Middleware

```go
authMiddleware := func(next server.Handler) server.Handler {
    return func(msg *server.Message, conn server.Conn) {
        // Check authentication
        if !isAuthenticated(conn) {
            logger.Warn("Unauthorized request from", conn.RemoteAddr())
            // Optionally send error response
            return
        }

        // Continue to next handler
        next(msg, conn)
    }
}

server.Use(authMiddleware)
```

### Message Size Tracking Middleware

```go
var totalBytes atomic.Uint64

sizeTracker := func(next server.Handler) server.Handler {
    return func(msg *server.Message, conn server.Conn) {
        totalBytes.Add(uint64(msg.Length))

        logger.Infow("Message processed",
            "size", msg.Length,
            "total_bytes", totalBytes.Load())

        next(msg, conn)
    }
}

server.Use(sizeTracker)
```

### Performance Monitoring Middleware

```go
type PerfStats struct {
    count     atomic.Uint64
    totalTime atomic.Uint64
    minTime   atomic.Uint64
    maxTime   atomic.Uint64
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

        // Update min/max atomically
        for {
            oldMin := stats.minTime.Load()
            if uint64(duration) >= oldMin {
                break
            }
            if stats.minTime.CompareAndSwap(oldMin, uint64(duration)) {
                break
            }
        }

        // Log stats periodically
        if stats.count.Load()%1000 == 0 {
            count := stats.count.Load()
            total := stats.totalTime.Load()
            avg := total / count
            logger.Infow("Performance stats",
                "count", count,
                "avg_us", avg,
                "min_us", stats.minTime.Load(),
                "max_us", stats.maxTime.Load())
        }
    }
}

server.Use(perfMonitor)
```

## Complete Example

Here's a complete example showing how to set up a server with multiple middlewares:

```go
package main

import (
    "github.com/hsdfat/diam-gw/pkg/connection"
    "github.com/hsdfat/diam-gw/pkg/logger"
    "github.com/hsdfat/diam-gw/server"
)

func main() {
    // Create server
    config := server.DefaultServerConfig()
    log := logger.New("diameter-server", "info")
    srv := server.NewServer(config, log)

    // Add global middlewares (apply to all handlers)
    srv.Use(server.RecoveryMiddleware(srv))        // 1. Recover from panics
    srv.Use(server.LoggingMiddleware(srv))         // 2. Log all requests
    srv.Use(server.ValidationMiddleware(srv))      // 3. Validate messages
    srv.Use(server.RateLimitMiddleware(srv, 1000)) // 4. Rate limit to 1000 req/s

    // Add conditional middleware (only for specific cases)
    s6aTimeout := server.InterfaceSpecificMiddleware(16777251,
        server.TimeoutMiddleware(srv, 5*time.Second))
    srv.Use(s6aTimeout)

    // Register handlers
    srv.HandleFunc(
        connection.Command{Interface: 16777251, Code: 316, Request: true},
        handleUpdateLocationRequest,
    )

    srv.HandleFunc(
        connection.Command{Interface: 16777251, Code: 321, Request: true},
        handleAuthenticationInformationRequest,
    )

    // Start server
    if err := srv.Start(); err != nil {
        log.Fatalf("Failed to start server: %v", err)
    }
}

func handleUpdateLocationRequest(msg *server.Message, conn server.Conn) {
    // Handler logic
    // All middlewares will run before this function
}

func handleAuthenticationInformationRequest(msg *server.Message, conn server.Conn) {
    // Handler logic
}
```

## Best Practices

1. **Order Matters**: Place recovery middleware first to catch all panics
2. **Keep Middlewares Focused**: Each middleware should do one thing well
3. **Use Conditional Middlewares**: Apply expensive operations only when needed
4. **Thread Safety**: Use atomic operations or proper locking for shared state
5. **Performance**: Be mindful of middleware overhead on the critical path
6. **Error Handling**: Always handle errors gracefully in middleware
7. **Logging**: Use appropriate log levels (Debug for verbose, Warn for issues)

## Common Patterns

### Middleware Stack for Production

```go
// Recommended middleware stack for production
srv.Use(server.RecoveryMiddleware(srv))      // Must be first
srv.Use(server.LoggingMiddleware(srv))       // Early logging
srv.Use(server.ValidationMiddleware(srv))    // Validate before processing
srv.Use(server.RateLimitMiddleware(srv, 1000))
srv.Use(server.MetricsMiddleware(srv))       // Track all requests
srv.Use(customAuthMiddleware)                 // Your auth logic
```

### Development/Debugging Stack

```go
// Verbose logging for debugging
srv.Use(server.RecoveryMiddleware(srv))
srv.Use(server.LoggingMiddleware(srv))
srv.Use(server.ValidationMiddleware(srv))
srv.Use(detailedDebugMiddleware)
```

### High-Performance Stack

```go
// Minimal middleware for maximum performance
srv.Use(server.RecoveryMiddleware(srv))      // Safety first
srv.Use(server.ValidationMiddleware(srv))    // Basic validation only
// No logging, no metrics in hot path
```

## Troubleshooting

### Middleware Not Executing

- Ensure `Use()` is called **before** `Start()`
- Check that handlers are registered with `HandleFunc()`

### Performance Issues

- Profile your middleware to identify bottlenecks
- Use conditional middlewares to avoid unnecessary work
- Consider async processing for expensive operations

### Memory Leaks

- Ensure goroutines spawned in middleware have proper cleanup
- Use timeouts for any blocking operations
- Clean up resources in defer blocks

## See Also

- [middleware_example_test.go](middleware_example_test.go) - Comprehensive examples
- [middleware.go](middleware.go) - Built-in middleware implementations
- [server.go](server.go) - Server and middleware types
