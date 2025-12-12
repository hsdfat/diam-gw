# Diameter Gateway Client Design - S13 Interface

## Overview

This document outlines the design for the Diameter Gateway client component that connects to DRA (Diameter Routing Agent) servers. The gateway will manage multiple TCP connections per DRA, handle Diameter protocol handshakes (CER/CEA), maintain heartbeats (DWR/DWA), and automatically recover from connection failures.

## Architecture

### Component Structure

```
client/
├── dra_client.go          # Main DRA client interface
├── connection.go          # Single TCP connection management
├── connection_pool.go     # Connection pool per DRA
├── state.go              # Connection state machine
├── health.go             # Health monitoring & auto-reconnect
├── message.go            # Message handling utilities
└── client_test.go        # Unit tests
```

## Key Components

### 1. Connection States

```
DISCONNECTED → CONNECTING → CER_SENT → OPEN → DWR_SENT → OPEN
                    ↓           ↓         ↓        ↓
                 FAILED ← - - - - - - - - - - - - ↓
                    ↓                              ↓
                RECONNECTING ← - - - - - - - - - -
```

States:
- `DISCONNECTED`: Initial state, no connection
- `CONNECTING`: TCP connection in progress
- `CER_SENT`: CER sent, waiting for CEA
- `OPEN`: Connection established and ready
- `DWR_SENT`: Watchdog request sent, waiting for DWA
- `FAILED`: Connection failed (temporary state before reconnect)
- `RECONNECTING`: Attempting to reconnect

### 2. DRA Configuration

```go
type DRAConfig struct {
    Host              string        // DRA hostname or IP
    Port              int           // DRA port
    OriginHost        string        // Local origin host
    OriginRealm       string        // Local origin realm
    ProductName       string        // Product name
    VendorID          uint32        // Vendor ID (3GPP = 10415)
    ConnectionCount   int           // Number of connections per DRA

    // Timeouts
    ConnectTimeout    time.Duration // TCP connect timeout
    CERTimeout        time.Duration // CER/CEA timeout
    DWRInterval       time.Duration // Watchdog interval
    DWRTimeout        time.Duration // Watchdog timeout

    // Reconnection
    ReconnectInterval time.Duration // Base reconnect interval
    MaxReconnectDelay time.Duration // Max backoff delay
    ReconnectBackoff  float64       // Backoff multiplier
}
```

### 3. Connection Pool

Each DRA will have a pool of TCP connections:

```go
type ConnectionPool struct {
    config      *DRAConfig
    connections []*Connection

    // Load balancing
    nextConnIdx atomic.Uint32

    // State tracking
    mu          sync.RWMutex
    activeCount int

    // Lifecycle
    ctx         context.Context
    cancel      context.CancelFunc
    wg          sync.WaitGroup
}
```

**Features:**
- Round-robin load balancing across connections
- Dynamic active connection count tracking
- Graceful shutdown support
- Concurrent-safe operations

### 4. Single Connection

```go
type Connection struct {
    id          string
    config      *DRAConfig
    conn        net.Conn

    // State management
    state       atomic.Value // ConnectionState
    stateMu     sync.Mutex

    // Diameter protocol
    hopByHopID  atomic.Uint32
    endToEndID  atomic.Uint32

    // Health monitoring
    lastActivity time.Time
    activityMu   sync.RWMutex
    dwrTicker    *time.Ticker

    // Message channels
    sendCh      chan []byte
    recvCh      chan []byte
    errorCh     chan error

    // Lifecycle
    ctx         context.Context
    cancel      context.CancelFunc
    wg          sync.WaitGroup
}
```

### 5. Message Flow

#### Connection Establishment

```
Client                          DRA
  |                              |
  |--- TCP Connect ------------->|
  |                              |
  |<-- TCP Connected ------------|
  |                              |
  |--- CER --------------------->|
  |                              |
  |<-- CEA ----------------------|
  |                              |
  | (Connection OPEN)            |
```

#### Heartbeat Flow

```
Client                          DRA
  |                              |
  | (Every DWRInterval)          |
  |--- DWR --------------------->|
  |                              |
  |<-- DWA ----------------------|
  |                              |
  | (Update lastActivity)        |
```

#### Connection Failure & Recovery

```
Client                          DRA
  |                              |
  | (Timeout or Error)           |
  |--- Connection Lost --------->|
  |                              |
  | (State: FAILED)              |
  | (Wait ReconnectInterval)     |
  |                              |
  |--- TCP Connect ------------->|
  | (State: RECONNECTING)        |
  |                              |
  |<-- TCP Connected ------------|
  |                              |
  |--- CER --------------------->|
  |                              |
  |<-- CEA ----------------------|
  |                              |
  | (State: OPEN)                |
```

## Implementation Details

### Connection State Machine

```go
type ConnectionState int

const (
    StateDisconnected ConnectionState = iota
    StateConnecting
    StateCERSent
    StateOpen
    StateDWRSent
    StateFailed
    StateReconnecting
)

func (c *Connection) setState(newState ConnectionState) {
    c.stateMu.Lock()
    defer c.stateMu.Unlock()

    oldState := c.getState()
    c.state.Store(newState)

    // Log state transition
    log.Printf("[%s] State transition: %v -> %v", c.id, oldState, newState)
}
```

### CER/CEA Handshake

```go
func (c *Connection) sendCER() error {
    cer := base.NewCapabilitiesExchangeRequest()

    // Set required fields
    cer.OriginHost = models_base.DiameterIdentity(c.config.OriginHost)
    cer.OriginRealm = models_base.DiameterIdentity(c.config.OriginRealm)
    cer.HostIpAddress = []models_base.Address{
        models_base.Address(getLocalIP()),
    }
    cer.VendorId = models_base.Unsigned32(c.config.VendorID)
    cer.ProductName = models_base.UTF8String(c.config.ProductName)

    // Set Auth-Application-Id for S13
    cer.AuthApplicationId = []models_base.Unsigned32{
        models_base.Unsigned32(s13.S13_APPLICATION_ID),
    }

    // Set IDs
    cer.Header.HopByHopID = c.nextHopByHopID()
    cer.Header.EndToEndID = c.nextEndToEndID()

    // Marshal and send
    data, err := cer.Marshal()
    if err != nil {
        return fmt.Errorf("failed to marshal CER: %w", err)
    }

    c.setState(StateCERSent)
    return c.send(data)
}

func (c *Connection) handleCEA(data []byte) error {
    cea := &base.CapabilitiesExchangeAnswer{}
    if err := cea.Unmarshal(data); err != nil {
        return fmt.Errorf("failed to unmarshal CEA: %w", err)
    }

    // Check result code
    if cea.ResultCode != 2001 { // DIAMETER_SUCCESS
        return fmt.Errorf("CEA failed with result code: %d", cea.ResultCode)
    }

    c.setState(StateOpen)
    c.updateActivity()

    // Start watchdog timer
    c.startWatchdog()

    return nil
}
```

### DWR/DWA Heartbeat

```go
func (c *Connection) startWatchdog() {
    c.dwrTicker = time.NewTicker(c.config.DWRInterval)

    c.wg.Add(1)
    go func() {
        defer c.wg.Done()

        for {
            select {
            case <-c.ctx.Done():
                return
            case <-c.dwrTicker.C:
                if err := c.sendDWR(); err != nil {
                    log.Printf("[%s] Failed to send DWR: %v", c.id, err)
                    c.handleFailure(err)
                    return
                }
            }
        }
    }()
}

func (c *Connection) sendDWR() error {
    dwr := base.NewDeviceWatchdogRequest()

    dwr.OriginHost = models_base.DiameterIdentity(c.config.OriginHost)
    dwr.OriginRealm = models_base.DiameterIdentity(c.config.OriginRealm)

    dwr.Header.HopByHopID = c.nextHopByHopID()
    dwr.Header.EndToEndID = c.nextEndToEndID()

    data, err := dwr.Marshal()
    if err != nil {
        return fmt.Errorf("failed to marshal DWR: %w", err)
    }

    c.setState(StateDWRSent)
    return c.sendWithTimeout(data, c.config.DWRTimeout)
}

func (c *Connection) handleDWA(data []byte) error {
    dwa := &base.DeviceWatchdogAnswer{}
    if err := dwa.Unmarshal(data); err != nil {
        return fmt.Errorf("failed to unmarshal DWA: %w", err)
    }

    c.setState(StateOpen)
    c.updateActivity()

    return nil
}
```

### Health Monitoring & Auto-Reconnect

```go
func (c *Connection) monitorHealth() {
    c.wg.Add(1)
    go func() {
        defer c.wg.Done()

        ticker := time.NewTicker(1 * time.Second)
        defer ticker.Stop()

        for {
            select {
            case <-c.ctx.Done():
                return
            case <-ticker.C:
                if c.getState() == StateDWRSent {
                    // Check if DWA timeout exceeded
                    if time.Since(c.getLastActivity()) > c.config.DWRTimeout {
                        log.Printf("[%s] DWR timeout, connection lost", c.id)
                        c.handleFailure(fmt.Errorf("DWR timeout"))
                        return
                    }
                }
            }
        }
    }()
}

func (c *Connection) handleFailure(err error) {
    log.Printf("[%s] Connection failed: %v", c.id, err)

    c.setState(StateFailed)
    c.close()

    // Start reconnection process
    c.reconnect()
}

func (c *Connection) reconnect() {
    backoff := c.config.ReconnectInterval
    attempts := 0

    for {
        select {
        case <-c.ctx.Done():
            return
        case <-time.After(backoff):
            attempts++
            log.Printf("[%s] Reconnection attempt %d", c.id, attempts)

            c.setState(StateReconnecting)

            if err := c.connect(); err != nil {
                log.Printf("[%s] Reconnection failed: %v", c.id, err)

                // Exponential backoff
                backoff = time.Duration(float64(backoff) * c.config.ReconnectBackoff)
                if backoff > c.config.MaxReconnectDelay {
                    backoff = c.config.MaxReconnectDelay
                }
                continue
            }

            // Reconnection successful
            log.Printf("[%s] Reconnected successfully", c.id)
            return
        }
    }
}
```

### Message Reading & Dispatching

```go
func (c *Connection) readLoop() {
    c.wg.Add(1)
    go func() {
        defer c.wg.Done()

        for {
            select {
            case <-c.ctx.Done():
                return
            default:
                // Read Diameter header (20 bytes)
                header := make([]byte, 20)
                if _, err := io.ReadFull(c.conn, header); err != nil {
                    c.handleFailure(fmt.Errorf("read header failed: %w", err))
                    return
                }

                // Parse message length
                length := binary.BigEndian.Uint32([]byte{0, header[1], header[2], header[3]})

                // Read full message
                message := make([]byte, length)
                copy(message[:20], header)

                if length > 20 {
                    if _, err := io.ReadFull(c.conn, message[20:]); err != nil {
                        c.handleFailure(fmt.Errorf("read body failed: %w", err))
                        return
                    }
                }

                // Dispatch message
                if err := c.handleMessage(message); err != nil {
                    log.Printf("[%s] Failed to handle message: %v", c.id, err)
                }
            }
        }
    }()
}

func (c *Connection) handleMessage(data []byte) error {
    if len(data) < 20 {
        return fmt.Errorf("message too short")
    }

    // Parse command code
    commandCode := binary.BigEndian.Uint32([]byte{0, data[5], data[6], data[7]})
    flags := data[4]
    isRequest := (flags & 0x80) != 0

    switch commandCode {
    case 257: // CER/CEA
        if !isRequest {
            return c.handleCEA(data)
        }
    case 280: // DWR/DWA
        if !isRequest {
            return c.handleDWA(data)
        } else {
            return c.handleDWR(data)
        }
    default:
        // Forward to application handler
        c.recvCh <- data
    }

    return nil
}
```

## Default Configuration Values

```go
var DefaultConfig = DRAConfig{
    ConnectionCount:   5,
    ConnectTimeout:    10 * time.Second,
    CERTimeout:        5 * time.Second,
    DWRInterval:       30 * time.Second,
    DWRTimeout:        10 * time.Second,
    ReconnectInterval: 5 * time.Second,
    MaxReconnectDelay: 5 * time.Minute,
    ReconnectBackoff:  1.5,
    VendorID:          10415, // 3GPP
}
```

## Usage Example

```go
package main

import (
    "context"
    "log"
    "github.com/hsdfat8/diam-gw/client"
)

func main() {
    config := client.DRAConfig{
        Host:              "dra.example.com",
        Port:              3868,
        OriginHost:        "gateway.example.com",
        OriginRealm:       "example.com",
        ProductName:       "Diameter-GW-S13",
        VendorID:          10415,
        ConnectionCount:   5,
        DWRInterval:       30 * time.Second,
        ReconnectInterval: 5 * time.Second,
    }

    ctx := context.Background()
    pool := client.NewConnectionPool(ctx, &config)

    if err := pool.Start(); err != nil {
        log.Fatalf("Failed to start connection pool: %v", err)
    }

    // Send a message
    message := []byte{...} // Your S13 message
    if err := pool.Send(message); err != nil {
        log.Printf("Failed to send message: %v", err)
    }

    // Receive messages
    for msg := range pool.Receive() {
        log.Printf("Received message: %d bytes", len(msg))
    }

    // Graceful shutdown
    pool.Close()
}
```

## Testing Strategy

1. **Unit Tests**
   - State machine transitions
   - CER/CEA handshake
   - DWR/DWA heartbeat
   - Message marshaling/unmarshaling

2. **Integration Tests**
   - Connection pool management
   - Multiple concurrent connections
   - Load balancing verification

3. **Failure Tests**
   - TCP connection drop
   - DWR timeout
   - Auto-reconnection
   - Exponential backoff

4. **Load Tests**
   - High message throughput
   - Connection pool saturation
   - Memory leak detection

## Monitoring & Metrics

Key metrics to track:

- Active connections per DRA
- Connection state distribution
- Message send/receive rate
- Failed connection attempts
- Average reconnection time
- DWR/DWA latency

## Security Considerations

1. **TLS Support** (future enhancement)
   - Optional TLS encryption
   - Certificate validation

2. **Authentication**
   - Origin-Host verification
   - Realm-based routing

3. **Rate Limiting**
   - Message rate per connection
   - Prevent resource exhaustion

## Next Steps

1. Implement base connection management
2. Add CER/CEA handshake
3. Implement DWR/DWA heartbeat
4. Add health monitoring & reconnection
5. Implement connection pool
6. Add comprehensive tests
7. Create usage examples
