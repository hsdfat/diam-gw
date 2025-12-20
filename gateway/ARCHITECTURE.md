# Diameter Gateway - Architecture Documentation

## Overview

The Diameter Gateway is a high-performance proxy that sits between Logic Applications and DRA (Diameter Routing Agent) servers, providing intelligent routing, failover, and session management.

## System Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Logic Applications                        │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐             │
│  │  Logic App  │  │  Logic App  │  │  Logic App  │   ...       │
│  │     #1      │  │     #2      │  │     #3      │             │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘             │
└─────────┼─────────────────┼─────────────────┼───────────────────┘
          │                 │                 │
          │  CER/CEA        │  CER/CEA        │  CER/CEA
          │  DWR/DWA        │  DWR/DWA        │  DWR/DWA
          │  App Requests   │  App Requests   │  App Requests
          └─────────────────┴─────────────────┘
                            │
          ┌─────────────────▼─────────────────┐
          │                                   │
          │      Diameter Gateway Server      │
          │    (Inbound from Logic Apps)      │
          │                                   │
          │  • Accept connections             │
          │  • CER/CEA exchange               │
          │  • DWR/DWA handling               │
          │  • Connection tracking            │
          │                                   │
          └─────────────────┬─────────────────┘
                            │
          ┌─────────────────▼─────────────────┐
          │                                   │
          │       Gateway Core Engine         │
          │                                   │
          │  • Session Tracking               │
          │  • Hop-by-Hop ID Mapping          │
          │  • Request Routing                │
          │  • Response Correlation           │
          │  • Statistics Collection          │
          │                                   │
          └─────────────────┬─────────────────┘
                            │
          ┌─────────────────▼─────────────────┐
          │                                   │
          │      DRA Pool Manager             │
          │   (Outbound to DRA Servers)       │
          │                                   │
          │  • Priority-based Routing         │
          │  • Health Monitoring              │
          │  • Automatic Failover             │
          │  • Connection Management          │
          │                                   │
          └─────────────┬───────────┬─────────┘
                        │           │
          ┌─────────────▼───┐   ┌───▼─────────────┐
          │  Priority 1     │   │  Priority 2     │
          │  DRA Pool       │   │  DRA Pool       │
          │                 │   │  (Standby)      │
          │  ┌───────────┐  │   │  ┌───────────┐  │
          │  │  DRA-1    │  │   │  │  DRA-3    │  │
          │  │ Primary   │  │   │  │ Secondary │  │
          │  └─────┬─────┘  │   │  └─────┬─────┘  │
          │        │        │   │        │        │
          │  ┌─────▼─────┐  │   │  ┌─────▼─────┐  │
          │  │  DRA-2    │  │   │  │  DRA-4    │  │
          │  │ Primary   │  │   │  │ Secondary │  │
          │  └───────────┘  │   │  └───────────┘  │
          └─────────────────┘   └─────────────────┘
```

## Component Architecture

### 1. Gateway Server (Inbound)

**Purpose**: Accept and manage connections from Logic Applications

**Components**:
```
server.Server
    ├── Listener (TCP)
    ├── Connection Pool
    │   ├── Connection #1 (Logic App #1)
    │   │   ├── Read Loop
    │   │   ├── Write Loop
    │   │   └── Process Loop
    │   ├── Connection #2 (Logic App #2)
    │   └── Connection #N
    └── Statistics Tracker
```

**Responsibilities**:
- Listen on configured port (default 3868)
- Accept TCP connections from Logic Apps
- Handle CER/CEA exchange per connection
- Manage DWR/DWA watchdog per connection
- Forward application requests to Gateway Core
- Track connection statistics

### 2. Gateway Core Engine

**Purpose**: Route requests and track sessions

**Data Structures**:
```go
type Gateway struct {
    sessions   map[uint32]*Session  // H2H ID -> Session
    sessionsMu sync.RWMutex

    stats GatewayStats

    server  *server.Server
    draPool *client.DRAPool
}

type Session struct {
    LogicAppConnection  *server.Connection
    OriginalHopByHopID  uint32
    OriginalEndToEndID  uint32
    DRAHopByHopID       uint32
    CreatedAt           time.Time
}
```

**Workflow**:
1. Receive request from Logic App connection
2. Parse message header (extract H2H, E2E, Command, AppID)
3. Generate new H2H ID for DRA
4. Create session entry
5. Modify message (replace H2H ID)
6. Forward to DRA pool
7. Wait for DRA response (separate goroutine)
8. Lookup session by DRA H2H ID
9. Restore original H2H and E2E IDs
10. Forward response to Logic App connection
11. Delete session entry

### 3. DRA Pool Manager (Outbound)

**Purpose**: Manage connections to multiple DRAs with priority-based routing

**Architecture**:
```
DRAPool
    ├── Priority Groups
    │   ├── Priority 1 (Active)
    │   │   ├── DRA-1 ConnectionPool
    │   │   │   ├── Connection #1
    │   │   │   │   ├── Read Loop
    │   │   │   │   ├── Write Loop
    │   │   │   │   └── Watchdog Loop
    │   │   │   └── Connection #N
    │   │   └── DRA-2 ConnectionPool
    │   │       └── ...
    │   └── Priority 2 (Standby)
    │       └── DRA-3 ConnectionPool
    │           └── ...
    ├── Health Monitor
    ├── Priority Manager
    └── Message Aggregator
```

**Features**:
- **Priority-based Routing**: Use highest priority available
- **Load Balancing**: Round-robin within same priority
- **Health Monitoring**: Periodic checks via DWR/DWA
- **Automatic Failover**: Switch to lower priority on failures
- **Automatic Failback**: Return to higher priority when recovered

## Message Flow Diagrams

### Request Flow (Logic App → Gateway → DRA)

```
Logic App         Gateway Server      Gateway Core       DRA Pool         DRA
    │                   │                   │               │              │
    │  Diameter Req     │                   │               │              │
    │  H2H: 0x1234      │                   │               │              │
    │  E2E: 0xABCD      │                   │               │              │
    ├──────────────────>│                   │               │              │
    │                   │                   │               │              │
    │                   │  Forward Request  │               │              │
    │                   ├──────────────────>│               │              │
    │                   │                   │               │              │
    │                   │                   │ Create Session│              │
    │                   │                   │ H2H: 0x1234   │              │
    │                   │                   │ -> 0x0001     │              │
    │                   │                   │               │              │
    │                   │                   │ Modified Req  │              │
    │                   │                   │ H2H: 0x0001   │              │
    │                   │                   │ E2E: 0xABCD   │              │
    │                   │                   ├──────────────>│              │
    │                   │                   │               │              │
    │                   │                   │               │ Forward      │
    │                   │                   │               ├─────────────>│
    │                   │                   │               │              │
```

### Response Flow (DRA → Gateway → Logic App)

```
Logic App         Gateway Server      Gateway Core       DRA Pool         DRA
    │                   │                   │               │              │
    │                   │                   │               │  Diameter Ans│
    │                   │                   │               │  H2H: 0x0001 │
    │                   │                   │               │  E2E: 0xABCD │
    │                   │                   │               │<─────────────┤
    │                   │                   │               │              │
    │                   │                   │ DRA Response  │              │
    │                   │                   │<──────────────┤              │
    │                   │                   │               │              │
    │                   │                   │ Lookup Session│              │
    │                   │                   │ H2H: 0x0001   │              │
    │                   │                   │ -> 0x1234     │              │
    │                   │                   │               │              │
    │                   │ Restored Response │               │              │
    │                   │ H2H: 0x1234       │               │              │
    │                   │ E2E: 0xABCD       │               │              │
    │                   │<──────────────────┤               │              │
    │                   │                   │               │              │
    │  Diameter Ans     │                   │               │              │
    │  H2H: 0x1234      │                   │               │              │
    │  E2E: 0xABCD      │                   │               │              │
    │<──────────────────┤                   │               │              │
    │                   │                   │               │              │
```

### Priority Failover Sequence

```
Time    Priority 1          Priority 2          Action
─────────────────────────────────────────────────────────
t0      DRA-1 [UP]          DRA-3 [STANDBY]     Normal
        DRA-2 [UP]          DRA-4 [STANDBY]     operation

t1      DRA-1 [DOWN]        DRA-3 [STANDBY]     DRA-1 fails
        DRA-2 [UP]          DRA-4 [STANDBY]     Use DRA-2

t2      DRA-1 [DOWN]        DRA-3 [ACTIVE]      Both P1 down
        DRA-2 [DOWN]        DRA-4 [ACTIVE]      FAILOVER to P2

t3      DRA-1 [UP]          DRA-3 [STANDBY]     DRA-1 recovers
        DRA-2 [DOWN]        DRA-4 [STANDBY]     FAILBACK to P1

t4      DRA-1 [UP]          DRA-3 [STANDBY]     DRA-2 recovers
        DRA-2 [UP]          DRA-4 [STANDBY]     Normal operation
```

## Thread Model

### Goroutine Architecture

```
main()
  │
  ├─> server.Start() [goroutine: server listener]
  │     │
  │     ├─> conn.serve() [goroutine per Logic App connection]
  │     │     ├─> readLoop()   [goroutine]
  │     │     ├─> writeLoop()  [goroutine]
  │     │     └─> processLoop() [goroutine]
  │     │
  │     └─> ... (multiple connections)
  │
  ├─> gateway.monitorLogicAppConnections() [goroutine]
  │     └─> startConnectionHandler() [goroutine per connection]
  │
  ├─> gateway.processDRAResponses() [goroutine]
  │
  ├─> gateway.sessionCleanup() [goroutine]
  │
  └─> draPool.Start()
        ├─> connectionPool.Start() [per DRA]
        │     ├─> connection.Start() [per connection]
        │     │     ├─> readLoop()     [goroutine]
        │     │     ├─> writeLoop()    [goroutine]
        │     │     └─> watchdogLoop() [goroutine]
        │     │
        │     └─> messageAggregator() [goroutine]
        │
        ├─> startMessageAggregator() [goroutine]
        ├─> startHealthMonitor()    [goroutine]
        └─> startPriorityManager()  [goroutine]
```

**Total Goroutines** (typical deployment):
- 1 main goroutine
- 1 server listener
- N × 3 goroutines per Logic App connection (N = number of Logic Apps)
- M × 3 goroutines per DRA connection (M = number of DRAs)
- 4 gateway management goroutines
- 3 DRA pool management goroutines

**Example**:
- 10 Logic Apps = 30 goroutines
- 4 DRAs with 1 conn each = 12 goroutines
- Gateway overhead = 7 goroutines
- **Total ≈ 49 goroutines**

### Concurrency Safety

**Thread-Safe Components**:
- Session map: `sync.RWMutex`
- Statistics: `atomic.Uint64`
- Connection pools: Internal locking
- DRA pool priority: `atomic.Int32`

**Lock-Free Operations**:
- Counter increments (statistics)
- H2H ID generation
- Priority level reads

## State Management

### Connection States

```
Server Connection (Logic App):
    Connecting → CER Received → CEA Sent → Open
                                              ↓
                                         (Running)
                                              ↓
                                         DPR Received → DPA Sent → Closed

DRA Connection:
    Disconnected → Connecting → CER Sent → CEA Received → Open
                                                             ↓
                                                        (Running)
                                                             ↓
                                                    DWR Sent → DWA Received
                                                             ↓
                                                    (Health Check Failed)
                                                             ↓
                                                         Failed
                                                             ↓
                                                      Reconnecting
```

### Session States

```
Session Lifecycle:
    Created → Active → Completed
              │
              └──> Expired (timeout)
```

## Data Structures

### Session Mapping

```
Sessions Map: map[uint32]*Session
Key: DRA Hop-by-Hop ID (generated by gateway)
Value: {
    LogicAppConnection:  *server.Connection
    OriginalHopByHopID:  uint32  // From Logic App
    OriginalEndToEndID:  uint32  // Preserved
    DRAHopByHopID:       uint32  // Generated for DRA
    CreatedAt:           time.Time
    ResponseAt:          time.Time
}

Lookup Time: O(1)
Insert Time: O(1)
Delete Time: O(1)
Memory: ~200 bytes per session
```

### Priority Groups

```
Priority Groups: map[int][]*DRAServerConfig
Key: Priority level (1 = highest)
Value: List of DRA servers at that priority

Example:
{
    1: [DRA-1, DRA-2],      // Primary
    2: [DRA-3, DRA-4],      // Secondary
    3: [DRA-5]              // Tertiary
}
```

## Performance Characteristics

### Throughput

- **Theoretical Max**: ~100,000 requests/second (single instance)
- **Typical**: 10,000-50,000 requests/second
- **Limiting Factors**:
  - Session map lock contention
  - Network I/O
  - DRA response time

### Latency

- **Gateway Overhead**: < 1ms (session lookup + ID replacement)
- **Total Latency**: Dominated by DRA response time
- **Typical End-to-End**: 10-50ms

### Memory Usage

- **Base**: ~50 MB (Go runtime + code)
- **Per Connection**: ~10 KB
- **Per Session**: ~200 bytes
- **Example**:
  - 1000 Logic App connections = 10 MB
  - 10,000 active sessions = 2 MB
  - **Total ≈ 62 MB**

### CPU Usage

- **Idle**: < 1%
- **Under Load**: 10-30% (depends on request rate)
- **Goroutine Context Switching**: Minimal (Go scheduler)

## Scalability

### Horizontal Scaling

The gateway can be deployed in multiple instances:

```
         Load Balancer
              │
    ┌─────────┼─────────┐
    │         │         │
Gateway-1  Gateway-2  Gateway-3
    │         │         │
    └─────────┼─────────┘
              │
          DRA Pool
```

**Considerations**:
- Each gateway maintains independent sessions
- No state sharing between gateways
- Logic Apps can connect to any gateway instance

### Vertical Scaling

**CPU**: Can utilize multiple cores (Go scheduler)
**Memory**: Scales linearly with sessions and connections
**Network**: Limited by NIC bandwidth

## Error Handling

### Error Categories

1. **Connection Errors**
   - TCP connection failures
   - CER/CEA exchange failures
   - Handled by: Reconnection logic

2. **Protocol Errors**
   - Invalid message format
   - Unexpected message types
   - Handled by: Error logging, connection close

3. **Routing Errors**
   - No DRA available
   - All DRAs down
   - Handled by: Priority failover

4. **Timeout Errors**
   - Session timeout
   - Request timeout
   - Handled by: Session cleanup, error response

### Recovery Mechanisms

- **Connection Loss**: Automatic reconnection with backoff
- **DRA Failure**: Priority-based failover
- **Session Timeout**: Cleanup and error statistics
- **Message Corruption**: Drop message, log error

## Monitoring & Observability

### Key Metrics

**Gateway Metrics**:
- `total_requests`: Total requests received
- `total_responses`: Total responses sent
- `active_sessions`: Current active sessions
- `avg_latency_ms`: Average request latency
- `total_errors`: Total errors

**DRA Pool Metrics**:
- `active_priority`: Current active priority level
- `active_dras`: Number of healthy DRAs
- `failover_count`: Number of failovers
- `total_connections`: Total DRA connections

### Health Checks

1. **DRA Health**: DWR/DWA every 30s
2. **Connection Health**: Active session count
3. **Gateway Health**: Error rate monitoring

## Security Considerations

### Network Security

- TLS support for encrypted communication
- IP-based access control (firewall)
- Port restrictions

### Protocol Security

- Message validation
- Length checks
- AVP validation

### Operational Security

- Log sanitization (no sensitive data)
- Secure configuration management
- Access control to gateway management

## Future Enhancements

1. **Dynamic Configuration**: Reload config without restart
2. **Advanced Routing**: Content-based routing by AVPs
3. **Session Persistence**: Redis-backed session storage
4. **Metrics Export**: Prometheus/Grafana integration
5. **Admin API**: REST API for management
6. **Rate Limiting**: Per-connection rate limits
7. **Circuit Breaker**: Automatic DRA circuit breaking
8. **Request Replay**: Replay failed requests

## References

- RFC 6733: Diameter Base Protocol
- RFC 3588: Diameter Base Protocol (obsoleted)
- 3GPP TS 29.272: S6a/S6d Interface
- 3GPP TS 29.272: S13 Interface
