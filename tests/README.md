# Test Suites

Comprehensive testing framework for the Diameter Gateway.

## Quick Start

```bash
# From project root

# List and run DWR failure threshold tests
./test-dwr.sh list
./test-dwr.sh all

# Run integration tests
./test-integration.sh

# Verify setup
./tests/verification/verify-container-setup.sh
./tests/verification/verify-multi-dra.sh
```

## Test Categories

### 1. DWR Failover Tests (`dwr-failover/`)

Tests for the DWR (Device-Watchdog-Request) failure threshold feature.

**Quick Access:**
```bash
./test-dwr.sh list    # List all tests
./test-dwr.sh all     # Run all tests
./test-dwr.sh test1   # Run specific test
```

**What it tests:**
- DWR/DWA heartbeat mechanism
- Configurable failure tolerance (MaxDWRFailures)
- Failure counter behavior
- Reconnection after threshold exceeded
- Priority failover with DWR failures

**Documentation:** [dwr-failover/README.md](dwr-failover/README.md)

---

### 2. Integration Tests (`integration/`)

End-to-end automated integration tests using containers.

**Quick Access:**
```bash
./test-integration.sh
```

**What it tests:**
- Complete priority-based failover workflow
- Multi-DRA connections and routing
- Automatic failover when Priority 1 fails
- Automatic fail-back when Priority 1 recovers
- Message routing to active priority only

**Duration:** ~2-3 minutes
**Documentation:** [integration/README.md](integration/README.md)

---

### 3. Verification Scripts (`verification/`)

Pre-flight checks to verify correct setup and configuration.

**Quick Access:**
```bash
# Container setup verification
./tests/verification/verify-container-setup.sh

# Multi-DRA setup verification
./tests/verification/verify-multi-dra.sh
```

**What it verifies:**
- Required files exist
- Binaries are built
- Scripts are executable
- Documentation is complete
- Environment is ready for testing

**Documentation:** [verification/README.md](verification/README.md)

---

## Test Organization

```
tests/
├── README.md                           # This file
├── dwr-failover/                       # DWR failure threshold tests
│   ├── README.md                       # Detailed DWR test documentation
│   ├── test-dwr-failover.sh           # Main test suite (4 scenarios)
│   ├── test-scenario-*.sh             # Custom scenario templates
│   ├── docker-compose-dwr-test.yml    # Docker compose for DWR tests
│   └── test-self-check.sh             # Self-verification
├── integration/                        # Integration tests
│   ├── README.md                       # Integration test documentation
│   └── podman-flow.sh                 # Automated failover flow test
└── verification/                       # Verification scripts
    ├── README.md                       # Verification documentation
    ├── verify-container-setup.sh      # Container setup checks
    └── verify-multi-dra.sh            # Multi-DRA setup checks
```

## Wrapper Scripts (Project Root)

For convenience, wrapper scripts are provided in the project root:

- **`test-dwr.sh`** - Wrapper for DWR failover tests
  ```bash
  ./test-dwr.sh list    # List available tests
  ./test-dwr.sh all     # Run all DWR tests
  ```

- **`test-integration.sh`** - Wrapper for integration tests
  ```bash
  ./test-integration.sh # Run integration test
  ```

## Testing Workflow

### Initial Setup Verification

```bash
# 1. Verify container setup
./tests/verification/verify-container-setup.sh

# 2. Verify multi-DRA setup
./tests/verification/verify-multi-dra.sh
```

### Running Tests

```bash
# 3. Run DWR failure threshold tests
./test-dwr.sh all

# 4. Run integration tests
./test-integration.sh
```

### Development Testing

For interactive development and debugging, use the DRA management tool:

```bash
# Start 4 DRAs on host
./tools/run-4-dras.sh start

# Run your client
./bin/multi-dra-test

# Test failover manually
./tools/run-4-dras.sh kill-p1

# Test fail-back
./tools/run-4-dras.sh start-p1

# Stop when done
./tools/run-4-dras.sh stop
```

See [../tools/README.md](../tools/README.md) for details.

## Test Matrix

| Test Type | Duration | Automation | Use Case |
|-----------|----------|------------|----------|
| **DWR Failover** | 30s-2min | Automated | DWR threshold validation |
| **Integration** | 2-3min | Automated | Full system validation |
| **Verification** | <10s | Automated | Pre-flight checks |
| **Manual (tools)** | Variable | Manual | Development/Debugging |

## Continuous Integration

All automated tests can be run in CI/CD pipelines:

```bash
# CI/CD example
./tests/verification/verify-container-setup.sh && \
./test-integration.sh && \
./test-dwr.sh all
```

Exit codes:
- `0` - All tests passed
- `1` - Test failed or error occurred

## Logs

Test logs are organized by test type:

```
logs/
├── dwr-test/          # DWR failover test logs
├── scenarios/         # Custom scenario logs
└── (container logs)   # Via podman-compose logs
```

View logs:
```bash
# DWR test logs
ls -lt logs/dwr-test/
tail -f logs/dwr-test/test_*.log

# Scenario logs
ls -lt logs/scenarios/
tail -f logs/scenarios/*.log

# Container logs (if running)
podman-compose logs -f client
```

## Adding New Tests

### Custom DWR Scenario

```bash
cd tests/dwr-failover
cp test-scenario-template.sh test-scenario-my-test.sh
chmod +x test-scenario-my-test.sh
# Edit and customize
./test-scenario-my-test.sh
```

### New Test Category

Create new directory under `tests/`:

```bash
mkdir tests/my-test-category
cd tests/my-test-category
# Add test scripts and README.md
```

Update this file to document the new test category.

## Related Documentation

- **[../TESTING.md](../TESTING.md)** - General testing guide
- **[../README.md](../README.md)** - Project overview
- **[../tools/README.md](../tools/README.md)** - Development tools
- **[dwr-failover/README.md](dwr-failover/README.md)** - DWR test details
- **[integration/README.md](integration/README.md)** - Integration test details
- **[verification/README.md](verification/README.md)** - Verification details

## Troubleshooting

### Tests won't start
```bash
# Check if containers/processes are already running
podman-compose ps
pkill -f dra-simulator

# Clean up
podman-compose down -v
./tools/run-4-dras.sh stop
```

### Port conflicts
```bash
# Find what's using the ports
lsof -i :3868

# Kill specific process
kill -9 <PID>
```

### Container build failures
```bash
# Rebuild from scratch
podman-compose build --no-cache
```

For more troubleshooting, see individual test READMEs.
