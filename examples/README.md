# Diameter Gateway Examples

Collection of examples demonstrating various aspects of the Diameter Gateway client.

## Examples by Complexity

### 1. Simple Pool (`simple_pool/`)
**Complexity**: ⭐ Beginner

Minimal example showing:
- Connection pool creation
- State monitoring
- Basic statistics

**Best for**: Understanding the basics

```bash
cd simple_pool
go run main.go
```

### 2. S13 Client (`s13_client/`)
**Complexity**: ⭐⭐⭐ Production

Complete S13 gateway implementation with:
- Full connection pool setup
- S13 message handling (ECR/ECA)
- Statistics reporting
- Graceful shutdown
- Signal handling

**Best for**: Production deployment reference

```bash
cd s13_client
go build -o s13-gateway main.go
./s13-gateway
```

### 3. Basic (`basic/`)
**Complexity**: ⭐ Legacy

Legacy examples predating the client package:
- Simple CER/CEA handshake
- Low-level protocol operations

**Best for**: Understanding protocol fundamentals

```bash
cd basic
go run simple_cer_cea.go
```

## Quick Start

### Running Without DRA Server

All examples can run without a real DRA server to demonstrate:
- Connection retry behavior
- Reconnection logic
- Error handling
- State management

### Running With DRA Server

1. Update the configuration in each example:
   ```go
   config.Host = "your-dra-host.example.com"
   config.Port = 3868
   config.OriginHost = "your-gateway.example.com"
   config.OriginRealm = "your-realm.com"
   ```

2. Run the example:
   ```bash
   go run main.go
   ```

## Example Progression

Recommended learning path:

```
simple_pool     →     s13_client     →     Production
(Learn basics)     (Learn features)     (Deploy)
```

## Features Demonstrated

| Feature | simple_pool | s13_client | basic |
|---------|-------------|------------|-------|
| Connection Pool | ✓ | ✓ | ✗ |
| CER/CEA | ✓ | ✓ | ✓ |
| DWR/DWA | ✓ | ✓ | ✗ |
| Auto-reconnect | ✓ | ✓ | ✗ |
| Load Balancing | ✓ | ✓ | ✗ |
| S13 Messages | ✗ | ✓ | ✗ |
| Statistics | ✓ | ✓ | ✗ |
| Graceful Shutdown | ✓ | ✓ | ✗ |
| Signal Handling | ✗ | ✓ | ✗ |

## Common Configuration

All examples use similar configuration:

```go
config := client.DefaultConfig()
config.Host = "127.0.0.1"              // DRA address
config.Port = 3868                     // Diameter port
config.OriginHost = "gateway.example.com"
config.OriginRealm = "example.com"
config.ConnectionCount = 5             // Connection pool size
config.DWRInterval = 30 * time.Second  // Heartbeat interval
```

## Testing Tips

### 1. Test Connection Handling
```bash
# Start example
go run simple_pool/main.go

# While running, stop/start your DRA to see reconnection
```

### 2. Test Load Balancing
```bash
# Send multiple messages and watch them distribute
# across connections (see s13_client example)
```

### 3. Test Graceful Shutdown
```bash
# Press Ctrl+C and observe clean shutdown
go run s13_client/main.go
^C
```

## Building Examples

### Build All
```bash
# From examples directory
go build -o bin/simple-pool simple_pool/main.go
go build -o bin/s13-gateway s13_client/main.go
```

### Build with Optimizations
```bash
go build -ldflags="-s -w" -o s13-gateway s13_client/main.go
```

## Troubleshooting

### "connection refused"
- DRA server not running
- Wrong host/port
- Firewall blocking

**Solution**: Examples work without DRA to show retry behavior

### "no active connections"
- All connections failed to establish
- DRA rejecting connections
- Network issues

**Solution**: Check DRA logs and network connectivity

### High CPU usage
- Too many reconnection attempts
- Increase `ReconnectInterval`

**Solution**: Adjust backoff settings in config

## Resources

- [Client Package Documentation](../client/README.md)
- [Design Document](../GATEWAY_CLIENT_DESIGN.md)
- [Implementation Summary](../IMPLEMENTATION_SUMMARY.md)
- RFC 6733: Diameter Base Protocol
- 3GPP TS 29.272: S13 Interface

## Contributing

To add a new example:

1. Create a new directory: `examples/my_example/`
2. Add `main.go` and `README.md`
3. Update this main README
4. Ensure it builds and runs
5. Document what it demonstrates

## License

Part of the Diameter Gateway project.
