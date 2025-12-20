# Address-Based Diameter Client

A connection pool implementation for Diameter protocol that manages one TCP connection per remote address.

## Overview

The `AddressClient` provides a simple API for sending Diameter messages to multiple remote addresses. It automatically:

- Creates connections on-demand when sending to a new address
- Reuses existing connections when sending to known addresses
- Ensures only **one connection per remote address** at any time
- Handles concurrent requests safely (thread-safe)
- Performs Diameter capability exchange (CER/CEA) automatically
- Manages connection lifecycle with DWR/DWA keepalives
- Detects and cleans up broken connections
- Supports automatic reconnection with exponential backoff
- Provides comprehensive metrics and monitoring

## Key Features

### 1. **One Connection Per Address**
- Pool maintains at most one active connection per remote address
- Prevents duplicate connections to the same endpoint
- Efficient resource usage

### 2. **Automatic Connection Establishment**
- First message to a new address triggers connection creation
- TCP dial + CER/CEA exchange handled automatically
- Subsequent messages reuse the established connection

### 3. **Concurrent Caller Blocking**
- When multiple goroutines send to the same new address simultaneously
- Only one goroutine establishes the connection
- Other goroutines block and wait for establishment to complete
- All waiters are notified when ready (or if establishment fails)

### 4. **Connection Lifecycle Management**

#### CER/CEA Handshake
- Automatic Capabilities Exchange on connection establishment
- Configurable timeout for CER/CEA exchange
- Connection marked as OPEN only after successful CEA

#### DWR/DWA Keepalive
- Periodic Device Watchdog Requests to keep connection alive
- Configurable interval and timeout
- Tracks consecutive DWR failures
- Automatic reconnection after max failures threshold

#### Health Monitoring
- Periodic health checks of all connections
- Removes dead connections automatically
- Broken connections detected and cleaned up

#### Idle Timeout (Optional)
- Close connections that have been idle for too long
- Configurable idle timeout (0 = never close)
- New connection created on next send

#### Max Lifetime (Optional)
- Limit maximum lifetime of connections
- Configurable max lifetime (0 = unlimited)
- Forces connection refresh periodically

### 5. **Reconnection Logic**
- Automatic reconnection on connection failure
- Exponential backoff with configurable parameters:
  - Initial delay
  - Maximum delay
  - Backoff multiplier
- Can be disabled via configuration

### 6. **Thread Safety**
- All operations are thread-safe
- Multiple goroutines can send concurrently
- Internal locking prevents race conditions
- Safe concurrent access to connection pool

### 7. **Structured Logging**
- Comprehensive logging throughout lifecycle
- Uses structured logging (logw) for easy parsing
- Logs connection state transitions
- Logs errors and warnings with context

### 8. **Metrics and Monitoring**

#### Client-Level Metrics
- Total requests sent
- Total responses received
- Total errors
- Total timeouts

#### Pool-Level Metrics
- Active connections count
- Total connections created (lifetime)
- Failed establishment attempts
- Reconnect attempts
- Messages sent/received
- Bytes sent/received
- Connections closed
- Idle timeout closures
- Max lifetime expirations

#### Per-Connection Metrics
- Messages sent/received
- Bytes sent/received
- Reconnection count
- Last error
- Connection state

### 9. **Context Support**
- Context-aware operations
- Timeout support via context
- Cancellation support
- Graceful shutdown on context cancellation

### 10. **Configurable Timeouts**
- TCP dial timeout
- Send operation timeout
- CER/CEA exchange timeout
- DWR/DWA timeout
- Overall request timeout

## API Reference

### Creating a Client

```go
import (
    "context"
    "github.com/hsdfat/diam-gw/client"
    "github.com/hsdfat/diam-gw/pkg/logger"
)

// With default configuration
config := client.DefaultPoolConfig()
config.OriginHost = "client.example.com"
config.OriginRealm = "example.com"

log := logger.New("diameter-client", "info")
ctx := context.Background()

addressClient, err := client.NewAddressClient(ctx, config, log)
if err != nil {
    log.Fatalw("Failed to create client", "error", err)
}
defer addressClient.Close()
```

### Sending Messages

```go
// Basic send - uses default timeout
remoteAddr := "192.168.1.100:3868"  // host:port format
message := []byte("DIAMETER_MESSAGE") // Your Diameter message bytes

err := addressClient.Send(remoteAddr, message)
if err != nil {
    log.Errorw("Send failed", "error", err)
}

// Send with custom timeout
err = addressClient.SendWithTimeout(remoteAddr, message, 5*time.Second)

// Send with context (full control)
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

err = addressClient.SendWithContext(ctx, remoteAddr, message)
```

### Getting Statistics

```go
// Get comprehensive statistics
stats := addressClient.GetStats()

fmt.Printf("Total Requests: %d\n", stats.TotalRequests)
fmt.Printf("Active Connections: %d\n", stats.PoolMetrics.ActiveConnections)
fmt.Printf("Failed Establishments: %d\n", stats.PoolMetrics.FailedEstablishments)

// Get active connection count
activeCount := addressClient.GetActiveConnections()

// List all connected addresses
addresses := addressClient.ListConnections()
for _, addr := range addresses {
    fmt.Printf("Connected to: %s\n", addr)
}

// Get specific connection
if conn, exists := addressClient.GetConnection("192.168.1.100:3868"); exists {
    connStats := conn.GetStats()
    fmt.Printf("Messages sent: %d\n", connStats.MessagesSent.Load())
    fmt.Printf("Connection state: %s\n", conn.GetState())
}
```

## Configuration Reference

```go
type PoolConfig struct {
    // Diameter Identity (Required)
    OriginHost  string  // Local origin host (e.g., "gateway.example.com")
    OriginRealm string  // Local origin realm (e.g., "example.com")
    ProductName string  // Product name to advertise in CER
    VendorID    uint32  // Vendor ID (e.g., 10415 for 3GPP)

    // Timeouts
    DialTimeout    time.Duration  // TCP dial timeout (default: 10s)
    SendTimeout    time.Duration  // Send operation timeout (default: 5s)
    CERTimeout     time.Duration  // CER/CEA exchange timeout (default: 5s)
    DWRInterval    time.Duration  // Device watchdog interval (default: 30s)
    DWRTimeout     time.Duration  // Device watchdog timeout (default: 10s)
    MaxDWRFailures int            // Max DWR failures before reconnect (default: 3)

    // Reconnection Strategy
    ReconnectEnabled  bool          // Enable auto-reconnect (default: true)
    ReconnectInterval time.Duration // Initial reconnect delay (default: 5s)
    MaxReconnectDelay time.Duration // Max reconnect delay (default: 5m)
    ReconnectBackoff  float64       // Backoff multiplier (default: 1.5)

    // Application IDs
    AuthAppIDs []uint32  // Auth-Application-IDs for CER
    AcctAppIDs []uint32  // Acct-Application-IDs for CER

    // Connection Lifecycle
    IdleTimeout     time.Duration  // Close idle connections (0 = never)
    MaxConnLifetime time.Duration  // Max connection lifetime (0 = unlimited)

    // Buffer Sizes
    SendBufferSize int  // Send channel buffer (default: 100)
    RecvBufferSize int  // Receive channel buffer (default: 100)

    // Health Check
    HealthCheckInterval time.Duration  // Health check interval (default: 30s)
}
```

## Connection States

Connections progress through the following states:

1. **DISCONNECTED** - Initial state, not connected
2. **CONNECTING** - TCP connection in progress
3. **CER_SENT** - CER sent, waiting for CEA
4. **OPEN** - Fully established and ready
5. **DWR_SENT** - DWR sent, waiting for DWA
6. **FAILED** - Connection failed
7. **RECONNECTING** - Attempting to reconnect
8. **CLOSED** - Connection closed

Active states: **OPEN**, **DWR_SENT**

## Usage Examples

### Example 1: Basic Usage

```go
config := client.DefaultPoolConfig()
config.OriginHost = "client.example.com"
config.OriginRealm = "example.com"

ctx := context.Background()
addressClient, _ := client.NewAddressClient(ctx, config, logger.New("app", "info"))
defer addressClient.Close()

// Send to address - connection created automatically
addressClient.Send("192.168.1.100:3868", diameterMessage)

// Send to same address - reuses connection
addressClient.Send("192.168.1.100:3868", anotherMessage)

// Send to different address - creates new connection
addressClient.Send("192.168.1.101:3868", thirdMessage)
```

### Example 2: Concurrent Sends

```go
// Multiple goroutines sending concurrently
addresses := []string{
    "192.168.1.100:3868",
    "192.168.1.101:3868",
    "192.168.1.102:3868",
}

for _, addr := range addresses {
    go func(remoteAddr string) {
        for i := 0; i < 100; i++ {
            err := addressClient.Send(remoteAddr, message)
            // Handle error
        }
    }(addr)
}

// Pool ensures only one connection per address
// Thread-safe concurrent access
```

### Example 3: Production Configuration

```go
config := &client.PoolConfig{
    // Identity
    OriginHost:  "dra-gateway.telco.com",
    OriginRealm: "telco.com",
    ProductName: "Telco-DRA-Gateway",
    VendorID:    10415, // 3GPP

    // Timeouts
    DialTimeout: 10 * time.Second,
    SendTimeout: 5 * time.Second,
    CERTimeout:  5 * time.Second,
    DWRInterval: 30 * time.Second,
    DWRTimeout:  10 * time.Second,
    MaxDWRFailures: 3,

    // Reconnection with exponential backoff
    ReconnectEnabled:  true,
    ReconnectInterval: 5 * time.Second,
    MaxReconnectDelay: 5 * time.Minute,
    ReconnectBackoff:  2.0,  // Double delay on each retry

    // Connection lifecycle
    IdleTimeout:     30 * time.Minute,  // Close after 30 min idle
    MaxConnLifetime: 24 * time.Hour,    // Refresh daily

    // Application IDs (S13 interface)
    AuthAppIDs: []uint32{16777252},

    // Performance tuning
    SendBufferSize: 500,
    RecvBufferSize: 500,

    // Health monitoring
    HealthCheckInterval: 1 * time.Minute,
}

addressClient, err := client.NewAddressClient(ctx, config, log)
```

## Architecture

### Connection Establishment Flow

```
Client.Send(addr, msg)
    ↓
Pool.Send(ctx, addr, msg)
    ↓
[Check if connection exists for addr]
    ↓
    ├─ Yes → Use existing connection
    │         ↓
    │         conn.SendWithContext(ctx, msg)
    │
    └─ No → Establish new connection
            ↓
            [Check if another goroutine is establishing]
            ↓
            ├─ Yes → Wait on channel
            │         ↓
            │         (blocks until ready or error)
            │         ↓
            │         Use established connection
            │
            └─ No → We establish
                    ↓
                    1. TCP Dial
                    2. Start read/write/health goroutines
                    3. Send CER
                    4. Wait for CEA
                    5. Start DWR ticker
                    6. Add to pool
                    7. Notify waiters
                    ↓
                    conn.SendWithContext(ctx, msg)
```

### Connection Lifecycle

```
                    ┌─────────────┐
                    │ DISCONNECTED│
                    └──────┬──────┘
                           │ Start()
                           ↓
                    ┌─────────────┐
                    │ CONNECTING  │
                    └──────┬──────┘
                           │ TCP connected
                           ↓
                    ┌─────────────┐
                    │  CER_SENT   │
                    └──────┬──────┘
                           │ CEA received
                           ↓
            ┌─────────────────────────┐
            │         OPEN            │◄──┐
            └──────┬─────────┬────────┘   │
                   │         │ DWA received
                   │         ↓            │
                   │  ┌─────────────┐    │
                   │  │  DWR_SENT   │────┘
                   │  └─────────────┘
                   │  DWR timeout/error
                   ↓
            ┌─────────────┐
            │   FAILED    │
            └──────┬──────┘
                   │ reconnect
                   ↓
            ┌─────────────┐
            │RECONNECTING │──► (back to CONNECTING)
            └─────────────┘
```

## Thread Safety

The implementation is fully thread-safe:

- **Connection Map**: Protected by `sync.RWMutex`
- **Establishment Map**: Protected by `sync.Mutex`
- **Metrics**: Use `atomic` operations
- **Per-connection State**: Uses atomic values and mutexes
- **Channel Operations**: Channels are inherently thread-safe

Multiple goroutines can safely:
- Send to the same address concurrently
- Send to different addresses concurrently
- Query metrics while sending
- Close the client while operations are in progress

## Error Handling

The client returns specific error types:

- `ErrInvalidConfig`: Configuration validation failed
- `ErrConnectionClosed`: Connection is closed
- `ErrConnectionTimeout`: Operation timed out
- `ErrHandshakeFailed`: CER/CEA exchange failed
- `ErrPoolClosed`: Pool is closed
- `context.DeadlineExceeded`: Context timeout
- `context.Canceled`: Context cancelled

Always check errors and handle appropriately:

```go
err := addressClient.Send(addr, msg)
if err != nil {
    switch {
    case errors.Is(err, context.DeadlineExceeded):
        // Handle timeout
    case errors.Is(err, context.Canceled):
        // Handle cancellation
    default:
        // Handle other errors
    }
}
```

## Best Practices

1. **Reuse Client Instance**: Create one client and reuse it for all sends
2. **Use Contexts**: Always use context for timeouts and cancellation
3. **Monitor Metrics**: Regularly check metrics for health
4. **Tune Timeouts**: Adjust timeouts based on network characteristics
5. **Handle Errors**: Always check and handle returned errors
6. **Graceful Shutdown**: Call `Close()` on application shutdown
7. **Configure Lifecycle**: Set appropriate idle timeout and max lifetime
8. **Log Analysis**: Use structured logs for debugging

## Performance Considerations

1. **Connection Pooling**: One connection per address reduces overhead
2. **Goroutine Blocking**: Concurrent callers to new addresses block briefly during establishment
3. **Buffer Sizes**: Tune send/receive buffer sizes based on traffic
4. **Health Checks**: Adjust interval based on number of connections
5. **Reconnection**: Exponential backoff prevents thundering herd

## Migration from Old Client

If migrating from the old `Client` that connected to a single DRA:

**Old Code:**
```go
config := &ClientConfig{
    ServerAddress: "192.168.1.100:3868",
    // ...
}
client, _ := NewClient(config, log)
client.Start()
client.Send(message)
```

**New Code:**
```go
config := DefaultPoolConfig()
config.OriginHost = "client.example.com"
config.OriginRealm = "example.com"

addressClient, _ := NewAddressClient(ctx, config, log)
addressClient.Send("192.168.1.100:3868", message)
```

Key differences:
- No `Start()` method - connections created on-demand
- Pass remote address with each `Send()` call
- Supports multiple remote addresses automatically

## Troubleshooting

### Connection Not Establishing

Check:
1. Network connectivity to remote address
2. Firewall rules allow TCP traffic
3. Remote server is listening on specified port
4. CER timeout is sufficient
5. Logs for specific error messages

### Messages Not Sending

Check:
1. Connection state (`conn.GetState()`)
2. Send buffer not full (increase `SendBufferSize`)
3. Context timeout is sufficient
4. Error return values

### High Reconnection Rate

Check:
1. Network stability
2. DWR interval and timeout settings
3. MaxDWRFailures threshold
4. Remote server DWR/DWA handling

### Memory Growth

Check:
1. Connections are being closed properly
2. IdleTimeout and MaxConnLifetime configured
3. No connection leaks (monitor `ActiveConnections`)
4. Buffer sizes not too large

## License

See project LICENSE file.
