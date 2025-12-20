# Diameter Gateway

A high-performance Diameter Gateway microservice that acts as a proxy between Logic Applications and DRA (Diameter Routing Agent) servers.

## Architecture

```
Logic App ⇄ Gateway ⇄ DRA
```

The gateway provides:
- **Inbound Server**: Accepts connections from Logic Applications
- **Outbound DRA Pool**: Manages multiple DRA connections with priority-based routing
- **Session Tracking**: Maps requests to responses using Hop-by-Hop IDs
- **Automatic Failover**: Priority-based DRA selection with automatic failback

## Features

### DRA Connectivity

- **Priority-based routing**: Multiple DRAs organized by priority levels (1 = highest)
- **Single connection per DRA**: Only one active TCP connection to each DRA at a time
- **Connection establishment sequence**: TCP dial → CER → CEA
- **Health monitoring**: Periodic DWR/DWA exchanges
- **Automatic reconnection**: Exponential backoff on connection failures
- **Priority failover**: Automatically switches to lower priority DRAs if higher priority fails
- **Automatic failback**: Returns to higher priority DRAs when they recover

### Inbound Server (from Logic App)

- **Multiple concurrent connections**: Accepts connections from multiple Logic Apps
- **CER/CEA exchange**: Automatic capabilities exchange
- **DWR/DWA handling**: Built-in watchdog support
- **Request forwarding**: Routes requests to active DRA connections
- **Response routing**: Returns DRA responses to originating Logic App

### Message Handling

- **Thread-safe**: Concurrent request processing with goroutines
- **Hop-by-Hop ID mapping**: Preserves request/response correlation
  - Logic App sends request with H2H ID `X`
  - Gateway generates new H2H ID `Y` for DRA
  - DRA responds with H2H ID `Y`
  - Gateway restores original H2H ID `X` when forwarding to Logic App
- **End-to-End ID preservation**: E2E IDs are preserved throughout
- **Session timeout**: Automatic cleanup of expired sessions

### Statistics & Monitoring

- Request/Response counters
- Active session tracking
- Error counters (timeouts, routing failures)
- Average latency calculation
- Per-DRA statistics
- Failover counts

## Configuration

### Gateway Identity

```go
config := &gateway.GatewayConfig{
    OriginHost:  "diameter-gw.example.com",
    OriginRealm: "example.com",
    ProductName: "Diameter-Gateway",
    VendorID:    10415, // 3GPP
}
```

### Server Configuration (Inbound)

```go
config.ServerConfig = &server.ServerConfig{
    ListenAddress:  "0.0.0.0:3868",
    MaxConnections: 1000,
    ConnectionConfig: &server.ConnectionConfig{
        ReadTimeout:      30 * time.Second,
        WriteTimeout:     10 * time.Second,
        WatchdogInterval: 30 * time.Second,
        WatchdogTimeout:  10 * time.Second,
        HandleWatchdog:   true,
    },
}
```

### DRA Pool Configuration (Outbound)

```go
config.DRAPoolConfig = &client.DRAPoolConfig{
    DRAs: []*client.DRAServerConfig{
        {
            Name:     "DRA-1",
            Host:     "10.0.1.100",
            Port:     3868,
            Priority: 1, // Primary
            Weight:   100,
        },
        {
            Name:     "DRA-2",
            Host:     "10.0.2.100",
            Port:     3868,
            Priority: 2, // Secondary
            Weight:   100,
        },
    },
    ConnectionsPerDRA:   1,
    DWRInterval:         30 * time.Second,
    DWRTimeout:          10 * time.Second,
    MaxDWRFailures:      3,
    HealthCheckInterval: 10 * time.Second,
    ReconnectInterval:   5 * time.Second,
}
```

## Usage

### Building

```bash
cd cmd/gateway
go build -o diameter-gateway
```

### Running

```bash
# Default configuration
./diameter-gateway

# Custom configuration
./diameter-gateway \
  -listen 0.0.0.0:3868 \
  -origin-host diameter-gw.example.com \
  -origin-realm example.com \
  -dra1-host 10.0.1.100 \
  -dra1-port 3868 \
  -dra2-host 10.0.2.100 \
  -dra2-port 3868 \
  -log debug \
  -log-requests \
  -stats-interval 30s
```

### Command-line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-listen` | `0.0.0.0:3868` | Gateway listen address |
| `-origin-host` | `diameter-gw.example.com` | Gateway Origin-Host |
| `-origin-realm` | `example.com` | Gateway Origin-Realm |
| `-product` | `Diameter-Gateway` | Product name |
| `-dra1-host` | `127.0.0.1` | Primary DRA host |
| `-dra1-port` | `3869` | Primary DRA port |
| `-dra2-host` | `127.0.0.1` | Secondary DRA host |
| `-dra2-port` | `3870` | Secondary DRA port |
| `-max-connections` | `1000` | Maximum inbound connections |
| `-conns-per-dra` | `1` | Connections per DRA |
| `-session-timeout` | `30s` | Session timeout |
| `-log` | `info` | Log level (debug/info/warn/error) |
| `-log-requests` | `false` | Enable request logging |
| `-log-responses` | `false` | Enable response logging |
| `-stats-interval` | `60s` | Statistics logging interval |

### Programmatic Usage

```go
package main

import (
    "github.com/hsdfat/diam-gw/gateway"
    "github.com/hsdfat/diam-gw/pkg/logger"
)

func main() {
    log := logger.New("my-gateway", "info")

    config := gateway.DefaultGatewayConfig()
    config.OriginHost = "my-gw.example.com"
    config.ServerConfig.ListenAddress = "0.0.0.0:3868"

    // Configure DRAs
    config.DRAPoolConfig.DRAs = []*client.DRAServerConfig{
        {Name: "DRA-1", Host: "10.0.1.100", Port: 3868, Priority: 1},
        {Name: "DRA-2", Host: "10.0.2.100", Port: 3868, Priority: 2},
    }

    gw, err := gateway.NewGateway(config, log)
    if err != nil {
        log.Fatalw("Failed to create gateway", "error", err)
    }

    if err := gw.Start(); err != nil {
        log.Fatalw("Failed to start gateway", "error", err)
    }

    // Gateway is running...

    // Graceful shutdown
    gw.Stop()
}
```

## Message Flow

### Request Flow (Logic App → Gateway → DRA)

1. Logic App sends Diameter request to Gateway
   - Message has original H2H ID: `0x12345678`
   - Message has E2E ID: `0xABCDEF01`

2. Gateway receives request
   - Creates session tracking entry
   - Generates new H2H ID for DRA: `0x00000001`
   - Preserves E2E ID: `0xABCDEF01`

3. Gateway forwards to DRA
   - Modified H2H ID: `0x00000001`
   - Original E2E ID: `0xABCDEF01`

### Response Flow (DRA → Gateway → Logic App)

1. DRA sends response to Gateway
   - H2H ID: `0x00000001` (matches gateway's request)
   - E2E ID: `0xABCDEF01`

2. Gateway receives response
   - Looks up session by H2H ID `0x00000001`
   - Finds original H2H ID: `0x12345678`
   - Restores original identifiers

3. Gateway forwards to Logic App
   - Restored H2H ID: `0x12345678`
   - Original E2E ID: `0xABCDEF01`

### DRA-initiated Requests (DRA → Gateway → Logic App)

The gateway also supports DRA-initiated requests (e.g., push notifications):

1. DRA sends request to Gateway
2. Gateway forwards to appropriate Logic App connection
3. Logic App sends response back to Gateway
4. Gateway forwards response to DRA

## Connection States

### DRA Connection States

```
Disconnected → Connecting → CER Sent → Open
                                ↓
                            DWR Sent → Open
                                ↓
                            Failed → Reconnecting
```

### Server Connection States

```
Connecting → CER Received → CEA Sent → Open
                                         ↓
                                    DWR/DWA exchanges
```

## Priority-based Failover

The gateway supports automatic failover and failback:

1. **Normal Operation**: Uses Priority 1 DRAs
2. **Failover**: If all Priority 1 DRAs fail, switches to Priority 2
3. **Failback**: When Priority 1 DRAs recover, automatically switches back

Example:
```
DRA-1 (Priority 1, Primary)    → Active
DRA-2 (Priority 1, Primary)    → Active (load balanced)
DRA-3 (Priority 2, Secondary)  → Standby

[DRA-1 and DRA-2 fail]

DRA-1 (Priority 1, Primary)    → Down
DRA-2 (Priority 1, Primary)    → Down
DRA-3 (Priority 2, Secondary)  → Active (failover)

[DRA-1 recovers]

DRA-1 (Priority 1, Primary)    → Active (failback)
DRA-2 (Priority 1, Primary)    → Down
DRA-3 (Priority 2, Secondary)  → Standby
```

## Monitoring

### Statistics Output

```
=== Gateway Statistics ===
Gateway:
  total_requests: 10000
  total_responses: 9998
  active_sessions: 2
  total_errors: 2
  avg_latency_ms: 12.34

Forwarding:
  to_dra: 10000
  from_dra: 9998
  timeout_errors: 1
  routing_errors: 1

Sessions:
  created: 10000
  completed: 9998
  expired: 2

DRA Pool:
  active_priority: 1
  total_dras: 2
  active_dras: 2
  total_connections: 2
  active_connections: 2
  failover_count: 0

DRA Messages:
  sent: 10000
  received: 9998
```

### Health Checks

The gateway performs automatic health checks:
- **DWR/DWA**: Sent every 30 seconds (configurable)
- **Failure threshold**: 3 consecutive failures trigger reconnection
- **Health check interval**: 10 seconds (configurable)

## Error Handling

### Timeout Handling

- **Session timeout**: Default 30 seconds
- **Expired sessions**: Automatically cleaned up
- **Statistics**: `timeout_errors` counter incremented

### Connection Failures

- **Automatic reconnection**: Exponential backoff (1.5x multiplier)
- **Max reconnect delay**: 5 minutes
- **Priority failover**: Switches to next priority level

### Message Errors

- **Routing errors**: Incremented when DRA pool is unavailable
- **Parse errors**: Logged and counted in `total_errors`

## Thread Safety

All components are thread-safe:
- **Session map**: Protected by `sync.RWMutex`
- **Statistics**: Uses `atomic.Uint64` for lock-free updates
- **Connection pools**: Concurrent-safe implementation
- **Server connections**: Each handled in separate goroutine

## Performance Considerations

- **Goroutines**: Each Logic App connection has dedicated goroutines
- **Buffered channels**: Reduces blocking on message passing
- **Connection pooling**: Reuses DRA connections
- **Lock-free statistics**: Uses atomic operations where possible
- **Efficient message copying**: Minimal allocations for message forwarding

## Graceful Shutdown

The gateway supports graceful shutdown:

1. Stop accepting new connections
2. Complete in-flight requests
3. Close DRA connections (sends DPR/DPA)
4. Close server connections
5. Wait for all goroutines to finish

```go
// Triggered by SIGINT or SIGTERM
gw.Stop()
```

## Logging

Log levels: `debug`, `info`, `warn`, `error`

### Debug Logging
- Request/Response details
- Session creation/deletion
- Connection state changes

### Info Logging
- Gateway start/stop
- DRA pool status
- Statistics reports

### Warn Logging
- Session timeouts
- Connection failures
- Failover events

### Error Logging
- Configuration errors
- Fatal connection errors
- Message parsing failures

## Requirements

- Go 1.21 or later
- Existing Diameter server package
- Existing Diameter client package with DRA pool support

## License

Same as the parent project.
