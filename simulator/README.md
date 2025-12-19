# Diameter Simulators

This directory contains Diameter protocol simulators for testing and development.

## Available Simulators

### DRA (Diameter Routing Agent) Simulator

A high-performance DRA simulator that handles:
- CER/CEA (Capabilities Exchange)
- DWR/DWA (Device Watchdog)
- DPR/DPA (Disconnect Peer)
- Session management
- Connection pooling
- Metrics and monitoring

**Location**: [`simulator/dra/`](dra/)

**Documentation**: See [DRA README](dra/README.md)

## Quick Start

### Build DRA Simulator

```bash
make build-dra
```

### Run DRA Simulator

```bash
./bin/dra-simulator -verbose
```

### Run with Test Client

```bash
# Terminal 1: Start DRA
./bin/dra-simulator -verbose

# Terminal 2: Run test client
./bin/test-with-dra
```

Or use the automated test script:

```bash
./test-dra.sh
```

## Use Cases

### 1. Unit Testing

Use the DRA simulator for unit tests:

```go
// Start DRA in test
dra := startTestDRA(t, "127.0.0.1:3868")
defer dra.Close()

// Test client connection
client := newDiameterClient("127.0.0.1:3868")
err := client.Connect()
assert.NoError(t, err)
```

### 2. Integration Testing

Test full application flows:

```bash
# Start DRA
./bin/dra-simulator -port 3868 -verbose

# Run your application
./my-diameter-app -dra-host 127.0.0.1 -dra-port 3868
```

### 3. Load Testing

Test system under load:

```bash
# Start DRA with high connection limit
./bin/dra-simulator -max-connections 10000 -verbose

# Run load test
./load-test-tool --connections 1000 --rate 10000
```

### 4. Development

Develop without needing a real DRA:

```bash
# Start DRA with verbose logging
./bin/dra-simulator -verbose -metrics-interval 5s

# Develop and test your client code
go run main.go
```

## Future Simulators

Planned simulators:

- **HSS (Home Subscriber Server)** - S6a interface simulator
- **EIR (Equipment Identity Register)** - S13 interface simulator
- **PCRF (Policy and Charging Rules Function)** - Gx interface simulator
- **OCS (Online Charging System)** - Gy interface simulator

## Architecture

```
simulator/
├── dra/              # DRA Simulator
│   ├── main.go       # Entry point & CLI
│   ├── dra.go        # Core DRA server
│   ├── config.go     # Configuration
│   ├── session.go    # Session management
│   ├── router.go     # Message routing
│   ├── metrics.go    # Metrics collection
│   └── README.md     # DRA documentation
└── README.md         # This file
```

## Contributing

To add a new simulator:

1. Create a new directory: `simulator/{name}/`
2. Implement core functionality:
   - `main.go` - CLI entry point
   - `{name}.go` - Core logic
   - `config.go` - Configuration
   - Connection handling
   - Protocol support
3. Add Makefile target:
   ```makefile
   build-{name}:
       go build -o bin/{name}-simulator simulator/{name}/*.go
   ```
4. Add documentation: `simulator/{name}/README.md`
5. Add tests: `simulator/{name}/*_test.go`

## Testing

Each simulator should include:

- Unit tests for core functionality
- Integration tests with client
- Performance benchmarks
- Example usage

Run tests:

```bash
# Test DRA simulator
go test ./simulator/dra/...

# Test with race detector
go test -race ./simulator/dra/...

# Benchmark
go test -bench=. ./simulator/dra/...
```

## Best Practices

1. **Configuration**: Support both CLI flags and config files
2. **Logging**: Use structured logging with levels
3. **Metrics**: Expose standard metrics (connections, messages, errors)
4. **Documentation**: Include examples and troubleshooting
5. **Testing**: Provide test clients and examples
6. **Performance**: Design for high throughput and low latency
7. **Reliability**: Handle errors gracefully, support reconnection

## Resources

- [RFC 6733 - Diameter Base Protocol](https://tools.ietf.org/html/rfc6733)
- [3GPP TS 29.272 - S6a Interface](https://www.3gpp.org/DynaReport/29272.htm)
- [3GPP TS 29.272 - S13 Interface](https://www.3gpp.org/DynaReport/29272.htm)
- [Testing Guide](../TESTING_WITH_DRA.md)

## License

Part of the diam-gw project.
