# DWR Failure Threshold Testing

Comprehensive testing framework for the DWR (Device-Watchdog-Request) failure threshold feature.

## Quick Start

```bash
# From project root
cd tests/dwr-failover

# List all available tests with descriptions
./test-dwr-failover.sh list

# Run all automated tests
./test-dwr-failover.sh all

# Run specific test
./test-dwr-failover.sh test1    # Normal operation
./test-dwr-failover.sh test2    # Single failure (below threshold)
./test-dwr-failover.sh test3    # Multiple failures (exceeds threshold)
./test-dwr-failover.sh test4    # Priority failover
```

## Overview

The DWR failure threshold feature allows configurable tolerance for consecutive DWR/DWA failures before triggering connection reconnection.

### Configuration Parameters

| Parameter | Description | Default | Config Field |
|-----------|-------------|---------|--------------|
| `DWR_INTERVAL` | Interval between DWR messages | 10s | `DWRInterval` |
| `DWR_TIMEOUT` | Timeout waiting for DWA response | 5s | `DWRTimeout` |
| `MAX_DWR_FAILURES` | Max consecutive failures before reconnect | 3 | `MaxDWRFailures` |

### How It Works

**Old Behavior:**
```
DWR timeout → Immediate reconnection
```

**New Behavior:**
```
DWR timeout #1 → Counter: 1/3 (continue)
DWR timeout #2 → Counter: 2/3 (continue)
DWR timeout #3 → Counter: 3/3 (reconnect!)
DWA success   → Counter: 0/3 (reset)
```

## Test Files

| File | Purpose |
|------|---------|
| `docker-compose-dwr-test.yml` | Docker compose with DWR configurations |
| `test-dwr-failover.sh` | Main automated test suite (4 scenarios) |
| `test-scenario-template.sh` | Template for creating custom tests |
| `test-scenario-gradual-degradation.sh` | Example: Gradual failure test |
| `test-scenario-rapid-recovery.sh` | Example: Recovery behavior test |
| `test-self-check.sh` | Self-check and validation script |

## Test Scenarios

### Test 1: Normal Operation ✅

**Purpose:** Verify baseline operation with all DRAs healthy

**Expected:**
- All connections established (CER/CEA)
- DWR/DWA exchanges every `DWR_INTERVAL`
- Messages sent to Priority 1 DRAs only
- Failure counter remains at 0

```bash
./test-dwr-failover.sh test1
```

### Test 2: Single DWR Failure ⚠️

**Purpose:** Verify that single failures don't trigger reconnection

**Expected:**
- 1 DWR failure logged
- Failure counter: 1 (< MAX_DWR_FAILURES)
- Connection maintained
- Failure counter resets to 0 on next successful DWA

```bash
./test-dwr-failover.sh test2
```

### Test 3: Multiple DWR Failures 🔴

**Purpose:** Verify reconnection after threshold exceeded

**Expected:**
- MAX_DWR_FAILURES consecutive failures
- Reconnection triggered after threshold
- Connection re-established
- Failure counter reset to 0

```bash
./test-dwr-failover.sh test3
```

### Test 4: Priority Failover 🔄

**Purpose:** Test failover behavior combined with DWR failures

**Expected:**
- Failover to Priority 2 after P1 failures
- Fail-back to Priority 1 when available
- DWR failure counters work correctly across priorities

```bash
./test-dwr-failover.sh test4
```

## Environment Variables

Override defaults with environment variables:

```bash
# Test with aggressive DWR settings
DWR_INTERVAL=5s \
DWR_TIMEOUT=2s \
MAX_DWR_FAILURES=2 \
./test-dwr-failover.sh all

# Use podman-compose instead of docker-compose
COMPOSE_CMD=podman-compose ./test-dwr-failover.sh all

# Change compose file
COMPOSE_FILE=my-compose.yml ./test-dwr-failover.sh test3
```

## Creating Custom Tests

### Step 1: Copy Template

```bash
cp test-scenario-template.sh test-scenario-my-test.sh
chmod +x test-scenario-my-test.sh
```

### Step 2: Edit Configuration

```bash
SCENARIO_NAME="My Custom Test"
SCENARIO_DESCRIPTION="What I'm testing"
TEST_DURATION="90"
```

### Step 3: Customize Test Logic

```bash
run_test_scenario() {
    start_services
    run_client_background "my-client" 120

    # Your test actions
    pause_service "dra-1" 30
    stop_service "dra-2"
    start_service "dra-2"

    wait_client "my-client"
}
```

### Step 4: Run

```bash
./test-scenario-my-test.sh
```

## Interpreting Results

### Success Indicators ✅

```
✓ All DRAs are healthy
Connection established successfully
DWA received successfully
Reset DWR failure counter
```

### Warning Indicators ⚠️

```
DWR timeout
DWR failure count: 1/3
DWR failure count: 2/3
```

### Error/Action Indicators 🔴

```
✗ Failed to start
DWR failure threshold exceeded
exceeded max DWR failures (3)
Handling failure
Reconnection attempt
```

## Logs

Test logs are saved in:
```
../../logs/dwr-test/       # Main test suite logs
../../logs/scenarios/      # Custom scenario logs
```

View logs:
```bash
# Test script logs
ls -lt ../../logs/dwr-test/
tail -f ../../logs/dwr-test/test_*.log

# Scenario logs
ls -lt ../../logs/scenarios/
tail -f ../../logs/scenarios/*.log
```

## Debugging

### View Real-Time Logs

```bash
# All services
$COMPOSE_CMD -f docker-compose-dwr-test.yml logs -f

# Specific service
$COMPOSE_CMD -f docker-compose-dwr-test.yml logs -f dra-1
$COMPOSE_CMD -f docker-compose-dwr-test.yml logs -f client-dwr-test
```

### Check Service Status

```bash
$COMPOSE_CMD -f docker-compose-dwr-test.yml ps
```

### Manual Service Control

```bash
# Start services
$COMPOSE_CMD -f docker-compose-dwr-test.yml up -d

# Pause a DRA (simulates network issue)
$COMPOSE_CMD -f docker-compose-dwr-test.yml pause dra-1
$COMPOSE_CMD -f docker-compose-dwr-test.yml unpause dra-1

# Stop a DRA (simulates crash)
$COMPOSE_CMD -f docker-compose-dwr-test.yml stop dra-1
$COMPOSE_CMD -f docker-compose-dwr-test.yml start dra-1

# Clean up
$COMPOSE_CMD -f docker-compose-dwr-test.yml down
```

## Troubleshooting

### Services Won't Start

```bash
# Check if ports are in use
lsof -i :3868

# Clean up old containers
$COMPOSE_CMD -f docker-compose-dwr-test.yml down -v
docker system prune -f  # or: podman system prune -f

# Rebuild images
$COMPOSE_CMD -f docker-compose-dwr-test.yml build --no-cache
```

### Tests Hang

```bash
# Kill stuck containers
docker ps | grep dwr-test | awk '{print $1}' | xargs docker kill
# or for podman:
podman ps | grep dwr-test | awk '{print $1}' | xargs podman kill

# Force cleanup
$COMPOSE_CMD -f docker-compose-dwr-test.yml down -v
```

### Need Verbose Output

```bash
./test-dwr-failover.sh test3 2>&1 | tee debug.log
```

## Configuration Examples

### Aggressive Failure Detection

Quick detection, low tolerance:

```bash
DWR_INTERVAL=5s
DWR_TIMEOUT=2s
MAX_DWR_FAILURES=1
```

Use when: High availability required, fast failover needed

### Conservative Failure Detection

Slow detection, high tolerance:

```bash
DWR_INTERVAL=60s
DWR_TIMEOUT=20s
MAX_DWR_FAILURES=5
```

Use when: Network is unstable, avoid false positives

### Balanced (Default)

```bash
DWR_INTERVAL=10s
DWR_TIMEOUT=5s
MAX_DWR_FAILURES=3
```

Use when: General purpose deployment

## Related Documentation

- [../../client/README.md](../../client/README.md) - Client library documentation
- [../../TESTING.md](../../TESTING.md) - General testing guide
- [../../README.md](../../README.md) - Project overview

## Related Code

- `../../client/config.go:25` - MaxDWRFailures configuration
- `../../client/connection.go:44-46` - Failure counter implementation
- `../../client/connection.go:391-415` - handleDWRFailure() function
- `../../client/dra_pool.go:42` - DRAPoolConfig.MaxDWRFailures
- `../../examples/multi_dra_test_container/main.go` - Test client implementation
