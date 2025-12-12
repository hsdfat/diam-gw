# Diameter Gateway Examples Guide

Complete guide to the Diameter Gateway client examples with step-by-step tutorials.

## Directory Structure

```
examples/
├── README.md              # Overview and quick reference
├── EXAMPLES_GUIDE.md      # This file - detailed guide
│
├── simple_pool/           # Beginner: Basic connection pool
│   ├── main.go           # 102 lines - minimal pool example
│   └── README.md         # Setup and usage instructions
│
├── s13_client/           # Production: Full S13 gateway
│   ├── main.go           # 194 lines - complete example
│   └── README.md         # Production deployment guide
│
└── basic/                # Legacy: Protocol fundamentals
    ├── simple_cer_cea.go # 200 lines - basic handshake
    └── README.md         # Protocol learning guide
```

## Learning Path

### Step 1: Start with Simple Pool (15 minutes)

**Goal**: Understand connection pool basics

```bash
cd examples/simple_pool
go run main.go
```

**What you'll learn**:
- Creating a connection pool
- Monitoring connection states
- Reading statistics
- Pool health checking

**Key concepts**:
```go
// 1. Create config
config := client.DefaultConfig()
config.Host = "127.0.0.1"

// 2. Create pool
pool, _ := client.NewConnectionPool(ctx, config)

// 3. Start connections
pool.Start()

// 4. Monitor state
stats := pool.GetStats()
states := pool.GetConnectionStates()
```

### Step 2: Explore S13 Client (30 minutes)

**Goal**: Understand production-ready implementation

```bash
cd examples/s13_client
go run main.go
```

**What you'll learn**:
- Complete connection lifecycle
- Message sending/receiving
- S13 protocol handling
- Statistics reporting
- Graceful shutdown

**Key concepts**:
```go
// 1. Setup message handlers
go receiveMessages(pool)
go reportStats(pool)

// 2. Send S13 messages
ecr := s13.NewMEIdentityCheckRequest()
// ... configure message
data, _ := ecr.Marshal()
pool.Send(data)

// 3. Handle responses
for msg := range pool.Receive() {
    info, _ := client.ParseMessageHeader(msg)
    handleS13Message(msg, info)
}

// 4. Graceful shutdown
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, os.Interrupt)
<-sigCh
pool.Close()
```

### Step 3: Review Basic Example (Optional)

**Goal**: Understand low-level protocol operations

```bash
cd examples/basic
go run simple_cer_cea.go
```

**What you'll learn**:
- Raw Diameter message structure
- Manual CER/CEA handshake
- Low-level TCP operations

## Example Comparison

| Feature | simple_pool | s13_client | basic |
|---------|-------------|------------|-------|
| **Purpose** | Learning | Production | Protocol study |
| **Complexity** | Low | High | Low |
| **Lines of Code** | 102 | 194 | 200 |
| **Dependencies** | client pkg | client pkg | base only |
| | | | |
| **Features** | | | |
| Connection Pool | ✅ | ✅ | ❌ |
| CER/CEA | ✅ Auto | ✅ Auto | ✅ Manual |
| DWR/DWA | ✅ Auto | ✅ Auto | ❌ |
| Auto-reconnect | ✅ | ✅ | ❌ |
| Load Balancing | ✅ | ✅ | ❌ |
| S13 Messages | ❌ | ✅ | ❌ |
| Statistics | ✅ Basic | ✅ Full | ❌ |
| Signal Handling | ❌ | ✅ | ❌ |
| Production Ready | ❌ | ✅ | ❌ |

## Hands-On Tutorials

### Tutorial 1: Running Without DRA Server

All examples work without a real DRA to demonstrate retry logic.

**Steps**:
1. Run any example:
   ```bash
   go run simple_pool/main.go
   ```

2. Observe the output:
   ```
   [conn0] Starting connection to 127.0.0.1:3868
   [conn0] State transition: DISCONNECTED -> CONNECTING
   [conn0] Failed to connect: connection refused
   [conn0] State transition: CONNECTING -> FAILED
   [conn0] Reconnection attempt 1
   [conn0] State transition: FAILED -> RECONNECTING
   ```

3. Watch exponential backoff:
   - Attempt 1: 5s delay
   - Attempt 2: 7.5s delay
   - Attempt 3: 11.25s delay
   - ... up to 5min max

**Learning**: Understand resilience and retry behavior

### Tutorial 2: Simulating Connection Failures

Test auto-reconnection with a real DRA.

**Steps**:
1. Start example with DRA running
2. Verify connections are OPEN
3. Stop DRA server
4. Watch connections transition to FAILED
5. Observe reconnection attempts
6. Start DRA again
7. See connections restore to OPEN

**Learning**: Verify automatic failure recovery

### Tutorial 3: Load Balancing Verification

See round-robin distribution in action.

**Steps**:
1. Run s13_client example
2. Send multiple messages
3. Check logs for connection IDs:
   ```
   [conn0] Sending message
   [conn1] Sending message
   [conn2] Sending message
   [conn0] Sending message  # Round-robin back to conn0
   ```

**Learning**: Understand load distribution

### Tutorial 4: Statistics Monitoring

Monitor pool health in real-time.

**Steps**:
1. Run simple_pool example
2. Observe statistics every 5 seconds:
   ```
   --- Pool State ---
   Total Connections: 3
   Active Connections: 3
   Messages Sent: 6        # CER messages
   Messages Received: 6    # CEA messages
   Reconnections: 0
   ```

3. Correlate with DWR/DWA:
   - Every 30s, messages increase by (connections × 2)
   - 3 connections: +6 messages every 30s

**Learning**: Understand metrics and monitoring

## Customization Guide

### Modify Connection Count

**File**: `simple_pool/main.go` or `s13_client/main.go`

```go
// Change from 3 to 10 connections
config.ConnectionCount = 10
```

**When to use**:
- Low traffic: 2-3 connections
- Medium traffic: 5-10 connections
- High traffic: 10-20 connections

### Adjust Heartbeat Interval

```go
config.DWRInterval = 60 * time.Second  // Change from 30s to 60s
config.DWRTimeout = 20 * time.Second   // Must be < DWRInterval
```

**When to use**:
- Reduce DRA load: Increase interval
- Faster failure detection: Decrease interval

### Change Reconnection Strategy

```go
config.ReconnectInterval = 10 * time.Second  // Initial delay
config.MaxReconnectDelay = 10 * time.Minute  // Max delay cap
config.ReconnectBackoff = 2.0                // Backoff multiplier
```

**Backoff examples**:
- Conservative: `ReconnectBackoff = 1.5` (default)
- Aggressive: `ReconnectBackoff = 2.0`
- Linear: `ReconnectBackoff = 1.0`

### Add Custom Message Handlers

**File**: `s13_client/main.go`

```go
func handleS13Message(data []byte, info *client.MessageInfo) {
    switch info.CommandCode {
    case s13.CommandCodeMEIDENTITYCHECKANSWER:
        handleECA(data)

    // Add new handler
    case YOUR_COMMAND_CODE:
        handleYourMessage(data)

    default:
        log.Printf("Unhandled command: %d", info.CommandCode)
    }
}

func handleYourMessage(data []byte) {
    // Your custom handling logic
}
```

## Production Deployment Checklist

Based on the s13_client example:

### Configuration
- [ ] Update DRA host/port
- [ ] Set correct OriginHost/OriginRealm
- [ ] Tune ConnectionCount for expected load
- [ ] Configure timeouts appropriately
- [ ] Set buffer sizes based on message rate

### Security
- [ ] Add TLS support if required
- [ ] Secure credential management
- [ ] Network isolation/firewalls
- [ ] Access control lists

### Monitoring
- [ ] Add metrics export (Prometheus)
- [ ] Health check endpoint
- [ ] Logging configuration
- [ ] Alert rules for failures

### Testing
- [ ] Load testing with expected traffic
- [ ] Failover testing
- [ ] Memory leak detection
- [ ] Performance profiling

### Operations
- [ ] Deployment automation
- [ ] Graceful restart capability
- [ ] Log rotation
- [ ] Backup connection configuration

## Troubleshooting

### Example Won't Build

```bash
# Ensure you're in the right directory
cd examples/simple_pool

# Check Go module
go mod tidy

# Build with verbose output
go build -v main.go
```

### Connection Refused

**Symptoms**: All connections show FAILED state

**Causes**:
1. No DRA server running
2. Wrong host/port
3. Firewall blocking

**Solutions**:
```bash
# Test connectivity
nc -zv 127.0.0.1 3868

# Check DRA logs
# Review firewall rules
```

### High Memory Usage

**Symptoms**: Memory grows over time

**Causes**:
1. Message buffers too large
2. Not closing connections
3. Goroutine leaks

**Solutions**:
```go
// Reduce buffer sizes
config.SendBufferSize = 50
config.RecvBufferSize = 50

// Ensure cleanup
defer pool.Close()
```

### Messages Not Received

**Symptoms**: Send works but no responses

**Causes**:
1. Wrong application ID
2. DRA routing misconfigured
3. Message format error

**Solutions**:
- Verify ApplicationID matches DRA config
- Check DRA routing tables
- Validate message format with wireshark

## Advanced Topics

### Custom Statistics Exporter

```go
// Add Prometheus metrics
import "github.com/prometheus/client_golang/prometheus"

var (
    messagesSent = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "diameter_messages_sent_total",
            Help: "Total messages sent",
        },
    )
)

func exportStats(pool *client.ConnectionPool) {
    stats := pool.GetStats()
    messagesSent.Add(float64(stats.TotalMessagesSent))
}
```

### Connection Pool Sharding

For very high traffic, shard across multiple pools:

```go
type PoolShard struct {
    pools []*client.ConnectionPool
    next  atomic.Uint32
}

func (s *PoolShard) Send(msg []byte) error {
    idx := s.next.Add(1) % uint32(len(s.pools))
    return s.pools[idx].Send(msg)
}
```

### Request/Response Correlation

Track pending requests:

```go
type PendingRequest struct {
    SentTime time.Time
    RespCh   chan []byte
}

var pending = sync.Map{} // map[EndToEndID]*PendingRequest

func sendRequest(pool *client.ConnectionPool, req []byte, e2eID uint32) {
    pending.Store(e2eID, &PendingRequest{
        SentTime: time.Now(),
        RespCh:   make(chan []byte, 1),
    })
    pool.Send(req)
}

func handleResponse(data []byte, e2eID uint32) {
    if val, ok := pending.LoadAndDelete(e2eID); ok {
        req := val.(*PendingRequest)
        latency := time.Since(req.SentTime)
        log.Printf("Response latency: %v", latency)
        req.RespCh <- data
    }
}
```

## Additional Resources

- [Client Package Documentation](../client/README.md)
- [Architecture Design](../GATEWAY_CLIENT_DESIGN.md)
- [Implementation Summary](../IMPLEMENTATION_SUMMARY.md)
- RFC 6733: Diameter Base Protocol
- 3GPP TS 29.272: S13 Interface Specification

## Getting Help

1. Check example READMEs for specific guidance
2. Review client package documentation
3. Examine test files for usage patterns
4. Enable debug logging:
   ```go
   log.SetFlags(log.LstdFlags | log.Lshortfile)
   ```

## Contributing Examples

To add a new example:

1. Create directory: `examples/my_example/`
2. Add `main.go` with comments
3. Add `README.md` with:
   - Purpose
   - Usage instructions
   - What it demonstrates
4. Update `examples/README.md`
5. Test thoroughly
6. Submit PR with description

Example template: See `simple_pool/` for structure.
