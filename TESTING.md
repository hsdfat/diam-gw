# Testing Guide - Diameter Gateway

Complete testing documentation for the Diameter Gateway project.

## Quick Start

```bash
# Run multi-application interface test (60 seconds)
./tests/multi-app/test-multi-app-podman.sh

# Run performance test (30 seconds, 100 req/s)
./tests/performance/test-performance-podman.sh --duration 30 --rate 100
```

## Test Categories

### 1. Multi-Application Interface Test
**Location**: `tests/multi-app/`
**Purpose**: Verify interface-based routing with multiple applications

**What it tests**:
- Interface discovery (S13, S6a, Gx)
- Interface-based message routing
- Connection affinity maintenance
- Multi-interface support (apps supporting multiple interfaces)

**Setup**:
- 1 DRA Simulator
- 1 Gateway
- 4 Applications with different interface support:
  - App1: S13 only
  - App2: S6a only
  - App3: S13 + S6a (multi-interface)
  - App4: Gx only

**Run**:
```bash
cd tests/multi-app
./test-multi-app-podman.sh
```

**Expected Result**:
```
✅ TEST PASSED

All validations successful:
  ✓ All applications connected
  ✓ Interface discovery working
  ✓ Interface-based routing operational
  ✓ No routing errors
  ✓ Connection affinity maintained
```

---

### 2. Performance Test
**Location**: `tests/performance/`
**Purpose**: Measure throughput and latency per interface under load

**What it measures**:
- Requests/second per interface (S13, S6a, Gx)
- Average latency
- P95/P99 latency percentiles
- Success rate
- Routing errors

**Setup**:
- 1 DRA (Load Generator)
- 1 Gateway
- 12 Applications (4 per interface)

**Run**:
```bash
cd tests/performance

# Quick test (30s, 100 req/s)
./test-performance-podman.sh --duration 30 --rate 100

# Standard test (60s, 1000 req/s)
./test-performance-podman.sh --duration 60 --rate 1000

# Stress test (5min, 5000 req/s)
./test-performance-podman.sh --stress
```

**Traffic Distribution**:
- S13: 40% (Equipment Check)
- S6a: 40% (Authentication)
- Gx: 20% (Policy Control)

**Performance Targets**:
| Level | Throughput | Latency P95 | Success Rate |
|-------|-----------|-------------|--------------|
| Minimum | 500 req/s | < 10ms | > 99% |
| Target | 2000 req/s | < 5ms | > 99.9% |
| Optimal | 5000+ req/s | < 3ms | > 99.99% |

---

### 3. DWR Failover Tests
**Location**: `tests/dwr-failover/`
**Purpose**: Test DWR (Device-Watchdog-Request) failure handling

**Run**:
```bash
./test-dwr.sh all
```

See [tests/dwr-failover/README.md](tests/dwr-failover/README.md) for details.

---

### 4. Integration Tests
**Location**: `tests/integration/`
**Purpose**: End-to-end automated integration testing

**Run**:
```bash
./test-integration.sh
```

See [tests/integration/README.md](tests/integration/README.md) for details.

---

## Test Files Organization

```
tests/
├── README.md                           # Overview of all tests
├── multi-app/                          # Multi-application interface tests
│   ├── test-multi-app-podman.sh       # Test script
│   └── docker-compose.multi-app-test.yml
├── performance/                        # Performance tests
│   ├── test-performance-podman.sh     # Test script
│   └── docker-compose.performance-test.yml
├── dwr-failover/                       # DWR failure threshold tests
│   ├── README.md
│   └── test-dwr-failover.sh
├── integration/                        # Integration tests
│   ├── README.md
│   └── podman-flow.sh
└── verification/                       # Setup verification scripts
    ├── verify-container-setup.sh
    └── verify-multi-dra.sh
```

## Viewing Test Results

### Multi-App Test Logs
```bash
# Gateway logs
podman logs gateway | grep "Interface routing"
podman logs gateway | grep "Gateway Metrics"

# Application logs
podman logs app1-s13
podman logs app2-s6a
podman logs app3-multi
podman logs app4-gx

# DRA logs
podman logs dra
```

### Performance Test Logs
```bash
# Gateway performance metrics
podman logs gateway-perf | grep "Messages"
podman logs gateway-perf | grep "Gateway Metrics" -A 20

# Per-interface statistics
podman logs gateway-perf | grep "Interface routing"

# DRA load generator
podman logs dra-perf
```

## Cleanup

```bash
# Stop multi-app test
podman-compose -f tests/multi-app/docker-compose.multi-app-test.yml down -v

# Stop performance test
podman-compose -f tests/performance/docker-compose.performance-test.yml down -v

# Clean all test containers
podman ps -a | grep -E "(dra|gateway|app)" | awk '{print $1}' | xargs podman rm -f
```

## Key Features Tested

### ✅ Multi-Application Support
- Multiple applications connecting simultaneously
- Each app advertises supported interfaces in CER
- Gateway discovers and tracks interface support

### ✅ Interface-Based Routing
- Messages routed based on interface (S13, S6a, Gx)
- Gateway maintains routing map per interface
- Round-robin across apps supporting same interface

### ✅ Connection Affinity
- Responses return on same connection as request
- H2H ID tracking ensures correct routing
- No affinity violations

### ✅ Scalability
- Tested with 4-12 applications
- Tested with 9-24 simultaneous connections
- Zero routing errors

### ✅ Performance
- Throughput measurement per interface
- Latency tracking (avg, P95, P99)
- Success rate monitoring

## Troubleshooting

### Tests fail to start
```bash
# Clean up any existing containers
podman-compose down -v
podman pod rm -f -a

# Rebuild images
podman rmi diam-gw-gateway diam-gw-dra diam-gw-app
```

### No traffic flowing
```bash
# Check if DRA is sending
podman logs dra | grep "MICR"

# Check if gateway is routing
podman logs gateway | grep "routing"

# Check if apps are responding
podman logs app1-s13 | grep "Handling MICR"
```

### Port conflicts
```bash
# Find what's using the ports
lsof -i :3868
lsof -i :3869

# Kill the process
kill -9 <PID>
```

## CI/CD Integration

```bash
#!/bin/bash
# Example CI/CD script

# Run multi-app test
if ! ./tests/multi-app/test-multi-app-podman.sh; then
    echo "Multi-app test failed"
    exit 1
fi

# Run performance test
if ! ./tests/performance/test-performance-podman.sh --duration 60 --rate 500; then
    echo "Performance test failed"
    exit 1
fi

echo "All tests passed"
exit 0
```

## Related Documentation

- [tests/README.md](tests/README.md) - Test suite overview
- [tests/dwr-failover/README.md](tests/dwr-failover/README.md) - DWR tests
- [tests/integration/README.md](tests/integration/README.md) - Integration tests
- [README.md](README.md) - Project main README

## Summary

The Diameter Gateway has comprehensive testing covering:

✅ **Functional Testing** - Multi-application, interface routing, connection affinity
✅ **Performance Testing** - Throughput, latency, scalability
✅ **Reliability Testing** - DWR failover, reconnection, error handling
✅ **Integration Testing** - End-to-end workflows

All tests are automated and can run in CI/CD pipelines.
