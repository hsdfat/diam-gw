# Multi-DRA Client Testing Guide

Complete guide for testing the Diameter client with multiple DRA simulators featuring priority-based routing, automatic failover, and fail-back.

## Quick Start

### Containerized Setup (Recommended)

**One command:**
```bash
./podman-flow.sh
```

**Manual:**
```bash
podman-compose build
podman-compose up -d
podman-compose logs -f client

# Test failover
podman-compose stop dra-1 dra-2

# Test fail-back
podman-compose start dra-1 dra-2

# Cleanup
podman-compose down
```

### Host-Based Setup

```bash
# Build
make build-dra build-examples

# Start DRAs
./run-4-dras.sh start

# Run client (in another terminal)
./bin/multi-dra-test

# Test failover
./run-4-dras.sh kill-p1

# Test fail-back
./run-4-dras.sh start-p1

# Stop
./run-4-dras.sh stop
```

## Architecture

```
Client
  ├─ DRA-1 (Priority 1) 172.20.0.11:3868 ⭐ PRIMARY
  ├─ DRA-2 (Priority 1) 172.20.0.12:3868 ⭐ PRIMARY
  ├─ DRA-3 (Priority 2) 172.20.0.13:3868 (Standby)
  └─ DRA-4 (Priority 2) 172.20.0.14:3868 (Standby)

Connections: All 4 DRAs (CER/CEA + DWR/DWA)
Messages: Only active priority receives S13
Failover: Priority 1 → Priority 2 (all P1 down)
Fail-back: Priority 2 → Priority 1 (any P1 up)
```

## Features

- **Priority-Based Routing**: 2-tier priority system
- **Automatic Failover**: Switches to backup when all primaries fail
- **Automatic Fail-back**: Returns to primary when available
- **Health Monitoring**: DWR/DWA keepalives every 10s
- **Load Balancing**: Round-robin within same priority
- **Real-time Stats**: Status display every 10s

## Test Scenarios

### 1. Normal Operation
**Test**: Start all services
**Expected**: Messages sent to Priority 1 (DRA-1, DRA-2)

### 2. Single Primary Failure
**Test**: Stop DRA-1
**Expected**: Remain on Priority 1, all traffic to DRA-2

### 3. Complete Primary Failure (Failover)
**Test**: Stop DRA-1 AND DRA-2
**Expected**:
- Active Priority changes to 2
- Messages go to DRA-3 and DRA-4
- Failover count increments

### 4. Primary Recovery (Fail-back)
**Test**: Restart DRA-1 and DRA-2
**Expected**:
- Active Priority changes back to 1
- Messages return to DRA-1 and DRA-2
- DRA-3 and DRA-4 return to standby

### 5. Rolling Restart
**Test**: Restart DRAs one at a time
**Expected**: No complete failover, dynamic redistribution

## Configuration

### Container Environment
```yaml
DRA1_HOST=172.20.0.11
DRA1_PORT=3868
DRA2_HOST=172.20.0.12
DRA2_PORT=3868
DRA3_HOST=172.20.0.13
DRA3_PORT=3868
DRA4_HOST=172.20.0.14
DRA4_PORT=3868
```

### Go Client Configuration
```go
config := client.DefaultDRAPoolConfig()
config.HealthCheckInterval = 5 * time.Second
config.DRAs = []*client.DRAServerConfig{
    {Name: "DRA-1", Host: "...", Port: 3868, Priority: 1, Weight: 10},
    {Name: "DRA-2", Host: "...", Port: 3868, Priority: 1, Weight: 10},
    {Name: "DRA-3", Host: "...", Port: 3868, Priority: 2, Weight: 10},
    {Name: "DRA-4", Host: "...", Port: 3868, Priority: 2, Weight: 10},
}
```

## Monitoring

### View Logs
```bash
# Container
podman-compose logs -f client
podman-compose logs -f dra-1

# Host
tail -f logs/dra-1.log
```

### Statistics Output
```
════════════════════════════════════════════════════════════
DRA Pool Status
════════════════════════════════════════════════════════════
  Active Priority:      1 (PRIMARY)
  Total DRAs:           4
  Active DRAs:          2
  Messages Sent:        150
  Failover Count:       0

Individual DRA Status:
────────────────────────────────────────────────────────────
  [DRA-1] ⭐ ACTIVE
    Status:            🟢 HEALTHY
    Messages Sent:     75
    Messages Recv:     75

  [DRA-2] ⭐ ACTIVE
    Status:            🟢 HEALTHY
    Messages Sent:     75
    Messages Recv:     75

  [DRA-3]
    Status:            🟢 HEALTHY
    Messages Sent:     0

  [DRA-4]
    Status:            🟢 HEALTHY
    Messages Sent:     0
════════════════════════════════════════════════════════════
```

## Troubleshooting

### Container Issues

**"missing services [client]"**
→ Ensure `profiles` is removed from docker-compose.yml client service

**"health check failing"**
```bash
podman exec dra-1 nc -z localhost 3868
```

**"cannot connect"**
```bash
podman network inspect diam-gw_diameter_net
podman exec diameter-client ping 172.20.0.11
```

### Host Issues

**"address already in use"**
```bash
./run-4-dras.sh stop
sudo lsof -i :3868
```

**"connection refused"**
```bash
./run-4-dras.sh status
./run-4-dras.sh logs 3868
```

## Performance Tuning

**Health Check Interval:**
```go
config.HealthCheckInterval = 3 * time.Second  // Faster
config.HealthCheckInterval = 10 * time.Second // Slower
```

**Message Rate:**
```go
ticker := time.NewTicker(1 * time.Second)  // Faster
ticker := time.NewTicker(5 * time.Second)  // Slower
```

**Connections Per DRA:**
```go
config.ConnectionsPerDRA = 3  // More capacity
```

## Files

| File | Purpose |
|------|---------|
| [client/dra_pool.go](client/dra_pool.go) | Priority-based pool |
| [client/connection_pool.go](client/connection_pool.go) | Connection pooling |
| [client/connection.go](client/connection.go) | Single connection |
| [simulator/dra/](simulator/dra/) | DRA simulator |
| [docker-compose.yml](docker-compose.yml) | Container setup |
| [podman-flow.sh](podman-flow.sh) | Automated test |
| [run-4-dras.sh](run-4-dras.sh) | Host-based management |

## Prerequisites

**Container:**
- Podman or Docker
- podman-compose or docker-compose

**Host:**
- Go 1.21+
- Make
- netcat

## Production Notes

✅ **Ready:**
- Priority routing
- Automatic failover/fail-back
- Health monitoring
- Load balancing

❌ **TODO:**
- TLS support
- External config
- Metrics export
- Circuit breaker

---

**Updated**: 2025-12-12
