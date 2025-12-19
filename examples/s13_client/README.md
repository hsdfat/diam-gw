# S13 Gateway Client Example

Complete production-ready example of using the Diameter Gateway client for S13 interface (EIR - Equipment Identity Register).

## Overview

This example demonstrates:
- Connection pool setup and management
- Automatic CER/CEA handshake
- DWR/DWA heartbeat
- Sending ME-Identity-Check-Request (ECR)
- Receiving ME-Identity-Check-Answer (ECA)
- Statistics reporting
- Graceful shutdown

## Configuration

Edit the configuration in `main.go`:

```go
config := client.DefaultConfig()
config.Host = "127.0.0.1"        // DRA hostname or IP
config.Port = 3868               // Diameter port
config.OriginHost = "gateway.example.com"
config.OriginRealm = "example.com"
config.ConnectionCount = 5       // Number of connections
```

## Building

```bash
go build -o s13-gateway main.go
```

## Running

### Basic Run
```bash
./s13-gateway
```

### With Custom Configuration
Edit the config values in `main.go` before building.

## Expected Output

```
Starting connection pool...
Waiting for connections to be ready...
[127.0.0.1:3868-conn0] Starting connection to 127.0.0.1:3868
[127.0.0.1:3868-conn1] Starting connection to 127.0.0.1:3868
...
[127.0.0.1:3868-conn0] TCP connection established
[127.0.0.1:3868-conn0] Performing CER/CEA handshake
[127.0.0.1:3868-conn0] CEA received successfully: DIAMETER_SUCCESS
[127.0.0.1:3868-conn0] Connection established successfully
...
Connection pool is ready!
Starting message receiver...
Sending example ME-Identity-Check-Request...
ECR sent successfully (xxx bytes)
```

## Testing Without DRA

If you don't have a DRA server available, you can:

1. Comment out the `pool.Start()` line to skip connection
2. Use a mock/stub DRA server
3. Test individual components with unit tests

## Features Demonstrated

### 1. Connection Pool Management
- Multiple connections per DRA
- Automatic connection establishment
- Health monitoring

### 2. Message Handling
- Sending S13 ECR messages
- Receiving and parsing ECA messages
- Equipment status checking

### 3. Statistics Reporting
- Periodic connection stats
- Message counts
- Reconnection tracking

### 4. Graceful Shutdown
- Signal handling (Ctrl+C)
- Clean connection closure
- Resource cleanup

## Message Flow

```
Application
    ↓
Send ECR → ConnectionPool → Round-robin → Connection → TCP → DRA
                                             ↓
                                          CER/CEA
                                          DWR/DWA
                                             ↓
Receive ECA ← ConnectionPool ← Aggregator ← Connection ← TCP ← DRA
    ↓
Handle Response
```

## Customization

### Adding Custom Message Handlers

```go
func handleS13Message(data []byte, info *client.MessageInfo) {
    switch info.CommandCode {
    case s13.CommandCodeMEIDENTITYCHECKANSWER:
        if !info.IsRequest {
            handleECA(data)
        }
    // Add more S13 message handlers here
    default:
        log.Printf("Unhandled S13 command code: %d", info.CommandCode)
    }
}
```

### Adjusting Statistics Interval

```go
// Change from 10s to 30s
ticker := time.NewTicker(30 * time.Second)
```

### Modifying Connection Pool Size

```go
config.ConnectionCount = 10  // Increase to 10 connections
```

## Troubleshooting

### Connection Failures
- Verify DRA host and port
- Check network connectivity
- Review firewall rules
- Ensure DRA accepts connections from your origin host/realm

### No Messages Received
- Verify S13 application ID (16777252)
- Check message format
- Review DRA logs
- Ensure routing is configured on DRA

### High Reconnection Count
- Check network stability
- Verify DRA keepalive settings
- Adjust DWR interval if needed

## Production Deployment

Before deploying to production:

1. **Security**
   - Add TLS support if required
   - Secure credential management
   - Network isolation

2. **Monitoring**
   - Add Prometheus metrics
   - Health check endpoints
   - Alert on connection failures

3. **Performance**
   - Load testing
   - Tune connection pool size
   - Profile memory usage

4. **Reliability**
   - Test failover scenarios
   - Verify auto-reconnection
   - Test graceful shutdown

## References

- [Client Package Documentation](../../client/README.md)
- [Design Document](../../GATEWAY_CLIENT_DESIGN.md)
- 3GPP TS 29.272: S13 Interface Specification
- RFC 6733: Diameter Base Protocol
