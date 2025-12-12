# Diameter Gateway Client

A production-ready Diameter protocol client for connecting to DRA (Diameter Routing Agent) servers with support for connection pooling, automatic heartbeat, health monitoring, and auto-reconnection.

## Features

- **Multiple Connections per DRA**: Maintains a configurable pool of TCP connections to each DRA for load distribution
- **Automatic Handshake**: Performs CER/CEA (Capabilities-Exchange) handshake on connection establishment
- **Heartbeat Mechanism**: Sends periodic DWR/DWA (Device-Watchdog) messages to maintain connection health
- **Health Monitoring**: Continuously monitors connection health and detects failures
- **Auto-Reconnection**: Automatically reconnects with exponential backoff when connections fail
- **Load Balancing**: Round-robin load balancing across connections in the pool
- **Concurrent-Safe**: Thread-safe operations for concurrent message sending/receiving
- **Graceful Shutdown**: Proper cleanup and graceful connection termination
- **Statistics**: Comprehensive connection and pool-level statistics

## Architecture

### Connection States

```
DISCONNECTED → CONNECTING → CER_SENT → OPEN → DWR_SENT → OPEN
                    ↓           ↓         ↓        ↓
                 FAILED ← - - - - - - - - - - - - ↓
                    ↓                              ↓
                RECONNECTING ← - - - - - - - - - -
```

- **DISCONNECTED**: Initial state, no connection
- **CONNECTING**: TCP connection in progress
- **CER_SENT**: CER sent, waiting for CEA
- **OPEN**: Connection established and ready to send/receive
- **DWR_SENT**: Watchdog request sent, waiting for DWA
- **FAILED**: Connection failed (transitioning to reconnect)
- **RECONNECTING**: Attempting to reconnect

### Components

1. **Connection** ([connection.go](connection.go)): Manages a single TCP connection with CER/CEA handshake, DWR/DWA heartbeat, and auto-reconnect
2. **ConnectionPool** ([connection_pool.go](connection_pool.go)): Manages multiple connections to a DRA with load balancing
3. **State Machine** ([state.go](state.go)): Connection state management
4. **Message Utilities** ([message.go](message.go)): Diameter message parsing and utilities
5. **Configuration** ([config.go](config.go)): Client configuration with validation
6. **Errors** ([errors.go](errors.go)): Custom error types

## Quick Start

### Basic Usage

```go
package main

import (
    "context"
    "log"
    "github.com/hsdfat8/diam-gw/client"
)

func main() {
    // Create configuration
    config := client.DefaultConfig()
    config.Host = "dra.example.com"
    config.Port = 3868
    config.OriginHost = "gateway.example.com"
    config.OriginRealm = "example.com"
    config.ConnectionCount = 5

    // Create and start connection pool
    ctx := context.Background()
    pool, err := client.NewConnectionPool(ctx, config)
    if err != nil {
        log.Fatal(err)
    }

    if err := pool.Start(); err != nil {
        log.Fatal(err)
    }
    defer pool.Close()

    // Send a message
    message := []byte{...} // Your Diameter message
    if err := pool.Send(message); err != nil {
        log.Printf("Send error: %v", err)
    }

    // Receive messages
    for msg := range pool.Receive() {
        log.Printf("Received %d bytes", len(msg))
        // Process message
    }
}
```

### Configuration

```go
config := &client.DRAConfig{
    // Connection settings
    Host:            "dra.example.com",
    Port:            3868,

    // Diameter identity
    OriginHost:      "gateway.example.com",
    OriginRealm:     "example.com",
    ProductName:     "My-Gateway",
    VendorID:        10415, // 3GPP

    // Connection pool
    ConnectionCount: 5,

    // Timeouts
    ConnectTimeout:  10 * time.Second,
    CERTimeout:      5 * time.Second,
    DWRInterval:     30 * time.Second,
    DWRTimeout:      10 * time.Second,

    // Reconnection
    ReconnectInterval: 5 * time.Second,
    MaxReconnectDelay: 5 * time.Minute,
    ReconnectBackoff:  1.5,

    // Buffer sizes
    SendBufferSize:  100,
    RecvBufferSize:  100,
}
```

### Default Configuration

The `DefaultConfig()` function returns sensible defaults:

```go
config := client.DefaultConfig()
// Port: 3868
// VendorID: 10415 (3GPP)
// ConnectionCount: 5
// ConnectTimeout: 10s
// CERTimeout: 5s
// DWRInterval: 30s
// DWRTimeout: 10s
// ReconnectInterval: 5s
// MaxReconnectDelay: 5m
// ReconnectBackoff: 1.5
```

## Advanced Usage

### Connection Pool Management

```go
// Get pool statistics
stats := pool.GetStats()
log.Printf("Active: %d/%d, Sent: %d, Recv: %d",
    stats.ActiveConnections, stats.TotalConnections,
    stats.TotalMessagesSent, stats.TotalMessagesRecv)

// Get connection states
states := pool.GetConnectionStates()
for connID, state := range states {
    log.Printf("%s: %s", connID, state)
}

// Check pool health
if pool.IsHealthy() {
    log.Println("Pool is healthy")
}

// Wait for connection
err := pool.WaitForConnection(30 * time.Second)
if err != nil {
    log.Printf("No connections available: %v", err)
}

// Send to specific connection
err := pool.SendToConnection("conn-id", message)
```

### Individual Connection Access

```go
// Get all connections
connections := pool.GetAllConnections()

// Get active connections only
activeConns := pool.GetActiveConnections()

// Get specific connection
conn := pool.GetConnection("conn-id")
if conn != nil {
    stats := conn.GetStats()
    log.Printf("Connection stats: sent=%d, recv=%d",
        stats.MessagesSent.Load(), stats.MessagesReceived.Load())
}
```

### Message Handling

```go
// Parse message header
info, err := client.ParseMessageHeader(data)
if err != nil {
    log.Printf("Parse error: %v", err)
    return
}

log.Printf("Received: %s", info.String())
log.Printf("Command Code: %d", info.CommandCode)
log.Printf("Is Request: %v", info.IsRequest)
log.Printf("Application ID: %d", info.ApplicationID)

// Check if base protocol message
if info.IsBaseProtocol() {
    // Handle CER/CEA, DWR/DWA, etc.
}
```

### Error Handling

```go
err := pool.Send(message)
if err != nil {
    switch e := err.(type) {
    case client.ErrPoolClosed:
        log.Println("Pool is closed")
    case client.ErrNoActiveConnections:
        log.Println("No active connections available")
    case client.ErrConnectionClosed:
        log.Printf("Connection %s is closed", e.ConnectionID)
    case client.ErrConnectionTimeout:
        log.Printf("%s timeout: %s", e.Operation, e.Timeout)
    default:
        log.Printf("Send error: %v", err)
    }
}
```

## Connection Lifecycle

### 1. Connection Establishment

```
1. TCP Connect
2. Send CER (Capabilities-Exchange-Request)
3. Receive CEA (Capabilities-Exchange-Answer)
4. Validate CEA result code
5. Transition to OPEN state
6. Start watchdog timer
```

### 2. Heartbeat

```
Every DWRInterval:
1. Send DWR (Device-Watchdog-Request)
2. Wait for DWA (Device-Watchdog-Answer)
3. Update activity timestamp
4. Transition back to OPEN state
```

### 3. Failure Detection

```
- TCP connection error
- Read/Write timeout
- DWR timeout exceeded
- Invalid message received
→ Transition to FAILED state
→ Close TCP connection
→ Trigger reconnection
```

### 4. Auto-Reconnection

```
1. Wait ReconnectInterval
2. Attempt TCP connect
3. If failed:
   - Increase backoff: interval *= ReconnectBackoff
   - Cap at MaxReconnectDelay
   - Retry
4. If successful:
   - Perform CER/CEA handshake
   - Restore to OPEN state
   - Reset backoff
```

## Statistics and Monitoring

### Pool Statistics

```go
stats := pool.GetStats()

type PoolStats struct {
    TotalConnections   int     // Total number of connections
    ActiveConnections  int     // Currently active connections
    TotalMessagesSent  uint64  // Aggregate messages sent
    TotalMessagesRecv  uint64  // Aggregate messages received
    TotalBytesSent     uint64  // Aggregate bytes sent
    TotalBytesRecv     uint64  // Aggregate bytes received
    TotalReconnects    uint32  // Total reconnection attempts
}
```

### Connection Statistics

```go
stats := conn.GetStats()

type ConnectionStats struct {
    MessagesSent     atomic.Uint64  // Messages sent
    MessagesReceived atomic.Uint64  // Messages received
    BytesSent        atomic.Uint64  // Bytes sent
    BytesReceived    atomic.Uint64  // Bytes received
    Reconnects       atomic.Uint32  // Reconnection count
    LastError        atomic.Value   // Last error (error type)
}
```

### Health Monitoring

The client automatically logs pool health every 30 seconds:

```
[Pool] Health: 5/5 active, sent=1234, recv=5678, reconnects=2
```

## Best Practices

### 1. Connection Count

- **Low traffic**: 2-3 connections per DRA
- **Medium traffic**: 5-10 connections per DRA
- **High traffic**: 10-20 connections per DRA

### 2. Timeout Configuration

```go
// Production recommendations
config.ConnectTimeout = 10 * time.Second  // TCP connect
config.CERTimeout = 5 * time.Second       // CER/CEA exchange
config.DWRInterval = 30 * time.Second     // Heartbeat interval
config.DWRTimeout = 10 * time.Second      // Watchdog timeout
```

### 3. Buffer Sizing

```go
// Size based on expected message rate
config.SendBufferSize = 100  // Can queue 100 messages per connection
config.RecvBufferSize = 100  // Can buffer 100 received messages per connection

// For high-throughput scenarios
config.SendBufferSize = 1000
config.RecvBufferSize = 1000
```

### 4. Graceful Shutdown

```go
// Handle shutdown signals
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

go func() {
    <-sigCh
    log.Println("Shutting down...")
    pool.Close()
    os.Exit(0)
}()
```

### 5. Context Management

```go
// Use context for cancellation
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

pool, err := client.NewConnectionPool(ctx, config)

// On shutdown
cancel()
pool.Close()
```

## Examples

See [examples/s13_gateway_client.go](../examples/s13_gateway_client.go) for a complete working example with:

- Configuration setup
- Connection pool creation and startup
- Message sending and receiving
- Statistics reporting
- Graceful shutdown

## Testing

### Unit Tests

```bash
cd client
go test -v
```

### Integration Tests

```bash
# Requires a running Diameter server
go test -v -tags=integration
```

### Load Tests

```bash
go test -v -bench=. -benchmem
```

## Troubleshooting

### No Active Connections

```go
err := pool.Send(message)
if err == client.ErrNoActiveConnections{} {
    // Check DRA connectivity
    // Review logs for connection errors
    // Verify DRA is accepting connections
}
```

### Frequent Reconnections

- Check network stability
- Verify DWR interval is appropriate
- Review DRA logs for errors
- Ensure CER/CEA parameters match DRA expectations

### High Memory Usage

- Reduce buffer sizes
- Decrease connection count
- Implement message batching
- Add flow control

### Performance Issues

- Increase connection count
- Use faster serialization
- Profile with pprof
- Check for lock contention

## References

- RFC 6733: Diameter Base Protocol
- 3GPP TS 29.272: Evolved Packet System (EPS) S13 interface
- [Design Document](../GATEWAY_CLIENT_DESIGN.md)

## License

Part of the Diameter Gateway project.
