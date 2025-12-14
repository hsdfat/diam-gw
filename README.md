# Diameter Gateway - Client & Code Generator

[![CI/CD](https://github.com/hsdfat8/diam-gw/actions/workflows/gateway-ci.yml/badge.svg)](https://github.com/hsdfat8/diam-gw/actions/workflows/gateway-ci.yml)
[![Performance](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/hsdfat8/diam-gw/main/.github/badges/performance.json)](https://github.com/hsdfat8/diam-gw/actions/workflows/gateway-ci.yml)
[![Grade](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/hsdfat8/diam-gw/main/.github/badges/grade.json)](https://github.com/hsdfat8/diam-gw/actions/workflows/gateway-ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/hsdfat8/diam-gw)](https://goreportcard.com/report/github.com/hsdfat8/diam-gw)

A comprehensive Diameter protocol implementation in Go with code generation tools, production-ready client with priority-based routing, and DRA simulator for testing.

## Overview

This project provides:
- **Code Generator**: Proto-like syntax for Diameter commands and AVPs
- **Client Library**: Production-ready client with multi-DRA support, priority-based routing, and automatic failover
- **DRA Simulator**: Full-featured Diameter Routing Agent for testing
- **Type-Safe**: Generated Go structs with Marshal/Unmarshal methods
- **Complete Protocol**: Base protocol (CER/CEA, DWR/DWA, DPR/DPA, etc.) and S13 interface

## Quick Start

### Testing & Performance

The gateway includes comprehensive testing and performance benchmarking:

```bash
# Multi-application interface test
cd tests/multi-app
./test-multi-app-podman.sh

# Single performance test (1000 req/s baseline)
cd tests/performance
./test-performance-podman.sh --duration 60 --rate 1000

# Find maximum throughput (quick ~5 min)
./quick-benchmark.sh

# Detailed maximum throughput benchmark (~15-30 min)
./benchmark-max-throughput.sh

# Profile at specific rate to find bottlenecks
./profile-gateway.sh --rate 5000 --duration 60
```

**Performance Testing:**
- ✅ **Real Load Generation**: S13 (MICR) and S6a (AIR) actual Diameter requests
- ✅ **Accurate Metrics**: Application messages only (excludes protocol overhead)
- ✅ **Automated CI/CD**: Performance regression testing on every commit
- ✅ **Grade System**: A (≥95%), B (80-95%), C (<80%), F (errors)

See **[BENCHMARKING.md](tests/performance/BENCHMARKING.md)** for complete benchmarking guide.

### Using the Client

```go
import "github.com/hsdfat8/diam-gw/client"

// Configure DRA pool with priorities
config := client.DefaultDRAPoolConfig()
config.DRAs = []*client.DRAServerConfig{
    {Name: "DRA-1", Host: "10.0.0.1", Port: 3868, Priority: 1},
    {Name: "DRA-2", Host: "10.0.0.2", Port: 3868, Priority: 1},
    {Name: "DRA-3", Host: "10.0.0.3", Port: 3868, Priority: 2},
}

// Create and start pool
pool, _ := client.NewDRAPool(ctx, config)
pool.Start()

// Send message (automatically routes to active priority)
pool.Send(messageData)
```

## Features

### Client Library

- **Priority-Based Routing**: Multi-tier priority system with automatic failover/fail-back
- **Connection Pooling**: Multiple connections per DRA
- **Health Monitoring**: Automatic health checks via DWR/DWA
- **Load Balancing**: Round-robin within same priority level
- **Automatic Reconnection**: Handles connection failures gracefully
- **Thread-Safe**: Concurrent message sending

### DRA Simulator

- **Full Protocol Support**: CER/CEA, DWR/DWA, DPR/DPA, S13 commands
- **Concurrent Connections**: Handles multiple clients
- **Health Checks**: Automatic DWR/DWA keepalives
- **Statistics**: Real-time metrics and logging
- **Configurable**: Command-line flags for all parameters

### Code Generator

- **Proto-Like Syntax**: Familiar .proto format for Diameter definitions
- **Auto-Generate**: Creates Go structs with full serialization
- **Type-Safe**: Compile-time checking for Diameter messages
- **Round-Trip**: Marshal/Unmarshal with length calculation
- **Extensible**: Support for custom AVPs and commands

## Project Structure

```
diam-gw/
├── client/                    # Client library
│   ├── dra_pool.go           # Priority-based DRA pool
│   ├── connection_pool.go    # Connection pooling per DRA
│   └── connection.go         # Single connection management
├── simulator/dra/            # DRA simulator
│   ├── dra.go               # Main DRA implementation
│   ├── load_generator.go    # Performance test load generator
│   ├── session.go           # Session management
│   └── router.go            # Message routing
├── codegen/                  # Code generator
│   ├── parser.go            # Proto file parser
│   └── generator.go         # Code generator
├── commands/                 # Generated commands
│   ├── base/                # Base protocol (CER, DWR, etc.)
│   └── s13/                 # S13 interface (MEICR, MEICA)
├── models_base/             # Diameter data types
├── examples/                # Example applications
│   ├── multi_dra_test/      # Host-based multi-DRA test
│   └── multi_dra_test_container/ # Container multi-DRA test
├── tests/                   # Test suites
│   ├── multi-app/           # Multi-application interface tests
│   ├── performance/         # Performance tests
│   ├── dwr-failover/        # DWR failure threshold testing
│   ├── integration/         # Integration tests
│   └── verification/        # Setup verification scripts
├── tools/                   # Development tools
│   └── run-4-dras.sh        # Host-based DRA management
├── docker-compose.yml       # Container orchestration
├── test-integration.sh      # Wrapper for integration tests
└── test-dwr.sh             # Wrapper for DWR tests
```

## Installation

```bash
git clone https://github.com/hsdfat8/diam-gw.git
cd diam-gw
go mod download
```

## Usage

### Testing with Multiple DRAs

See **[TESTING.md](TESTING.md)** for:
- Containerized setup with Docker/Podman
- Host-based setup
- Test scenarios (normal, failover, fail-back)
- Monitoring and troubleshooting
- Performance tuning

### Client Library Examples

**Basic connection:**
```go
// Single DRA
conn, _ := client.NewConnection(ctx, &client.ConnectionConfig{
    Host:        "10.0.0.1",
    Port:        3868,
    OriginHost:  "client.example.com",
    OriginRealm: "example.com",
})
conn.Start()
conn.Send(messageData)
```

**Multi-DRA with priority:**
```go
// Multiple DRAs with failover
pool, _ := client.NewDRAPool(ctx, &client.DRAPoolConfig{
    OriginHost:  "client.example.com",
    OriginRealm: "example.com",
    DRAs: []*client.DRAServerConfig{
        {Name: "Primary-1", Host: "10.0.0.1", Port: 3868, Priority: 1},
        {Name: "Primary-2", Host: "10.0.0.2", Port: 3868, Priority: 1},
        {Name: "Backup-1", Host: "10.0.0.3", Port: 3868, Priority: 2},
    },
    HealthCheckInterval: 5 * time.Second,
    ConnectionsPerDRA:   2,
})
pool.Start()

// Send automatically routes to active priority with load balancing
pool.Send(messageData)

// Get statistics
stats := pool.GetStats()
fmt.Printf("Active Priority: %d, Messages Sent: %d\n",
    stats.CurrentPriority, stats.TotalMessagesSent)
```

### DRA Simulator

**Start simulator:**
```bash
# Build
go build -o bin/dra-simulator simulator/dra/*.go

# Run
./bin/dra-simulator \
  -host 0.0.0.0 \
  -port 3868 \
  -origin-host dra.example.com \
  -origin-realm example.com \
  -verbose
```

**Docker:**
```bash
docker build -f Dockerfile.dra -t dra-simulator .
docker run -p 3868:3868 dra-simulator
```

### Code Generator

**Define protocol:**
```proto
syntax = "diameter1";
package diameter.base;

avp Origin-Host {
  code = 264;
  type = DiameterIdentity;
  must = true;
}

command Capabilities-Exchange-Request {
  code = 257;
  application_id = 0;
  request = true;

  fixed required Origin-Host origin_host = 1;
  fixed required Origin-Realm origin_realm = 2;
}
```

**Generate code:**
```bash
go run cmd/diameter-codegen/main.go \
  -proto proto/diameter.proto \
  -output commands/base/diameter.pb.go \
  -package base
```

**Use generated code:**
```go
cer := base.NewCapabilitiesExchangeRequest()
cer.OriginHost = models_base.DiameterIdentity("client.example.com")
cer.OriginRealm = models_base.DiameterIdentity("example.com")

data, _ := cer.Marshal()
// Send data over network
```

## Testing Architecture

```
Client
  ├─ DRA-1 (Priority 1) ⭐ Primary
  ├─ DRA-2 (Priority 1) ⭐ Primary
  ├─ DRA-3 (Priority 2) Standby
  └─ DRA-4 (Priority 2) Standby

• All 4 connections maintained (CER/CEA, DWR/DWA)
• Messages sent only to active priority
• Automatic failover: All Priority 1 down → Priority 2
• Automatic fail-back: Any Priority 1 up → Priority 1
```

## Building

```bash
# Build all
make build

# Build DRA simulator
make build-dra

# Build examples
make build-examples

# Run tests
make test
```

## CI/CD Pipeline

The project includes comprehensive GitHub Actions workflows for automated testing and performance monitoring:

### Automated Testing
- **Build & Unit Tests**: Go tests with race detection and coverage reporting
- **Multi-App Test**: Interface-based routing validation with 4 applications
- **Performance Test**: Throughput and latency testing with 12 applications
- **Automatic Reports**: Performance metrics posted to PRs

### Performance Monitoring
- Real-time performance badges showing throughput and grade
- Configurable test parameters (duration, rate)
- Automated performance regression detection
- Historical performance tracking

See **[.github/CICD.md](.github/CICD.md)** for complete CI/CD documentation.

### Manual Workflow Triggers
```bash
# Run performance test with custom parameters
gh workflow run gateway-ci.yml \
  -f performance_duration=120 \
  -f performance_rate=2000
```

## Documentation

### Testing & Performance
- **[TESTING.md](TESTING.md)** - Complete testing guide (multi-app, performance, DWR, integration)
- **[BENCHMARKING.md](tests/performance/BENCHMARKING.md)** - Comprehensive benchmarking guide
- **[MAXIMUM_THROUGHPUT_TESTING.md](MAXIMUM_THROUGHPUT_TESTING.md)** - Quick reference for finding max capacity
- **[PERFORMANCE_TEST_FIXES.md](PERFORMANCE_TEST_FIXES.md)** - Recent performance improvements
- **[tests/README.md](tests/README.md)** - Test suite overview

### CI/CD
- **[.github/CICD.md](.github/CICD.md)** - CI/CD pipeline documentation
- Automated performance testing on every commit
- Real-time performance badges and grade tracking
- Performance regression detection
- Historical performance data tracking

### Components
- **[client/README.md](client/README.md)** - Client library documentation
- **[simulator/dra/README.md](simulator/dra/README.md)** - DRA simulator documentation
- **[examples/README.md](examples/README.md)** - Example applications
- **[tools/README.md](tools/README.md)** - Development tools

## Supported Protocols

### Diameter Base (RFC 6733)
- CER/CEA - Capabilities Exchange
- DWR/DWA - Device Watchdog
- DPR/DPA - Disconnect Peer
- RAR/RAA - Re-Auth
- STR/STA - Session Termination
- ASR/ASA - Abort Session
- ACR/ACA - Accounting

### 3GPP S13 Interface
- ME-Identity-Check-Request/Answer (MEICR/MEICA)

## Requirements

- Go 1.21 or later
- For containers: Podman or Docker
- For testing: netcat

## References

- RFC 6733: Diameter Base Protocol
- 3GPP TS 29.272: S13 Interface
- [diameter_base_commands.txt](diameter_base_commands.txt) - Command descriptions
- [rfc3588_diameter_summary.txt](rfc3588_diameter_summary.txt) - Protocol summary

## License

Part of the Diameter Gateway implementation.

---

**Quick Links:**
- [Testing Guide](TESTING.md) - Multi-app, integration, DWR tests
- [Benchmarking Guide](tests/performance/BENCHMARKING.md) - Find maximum throughput
- [Performance Fixes](PERFORMANCE_TEST_FIXES.md) - Recent improvements & load generator
- [Client Library](client/) - Production-ready Diameter client
- [DRA Simulator](simulator/dra/) - Full-featured testing DRA
- [Examples](examples/) - Sample applications
- [CI/CD Pipeline](.github/CICD.md) - Automated testing & deployment
