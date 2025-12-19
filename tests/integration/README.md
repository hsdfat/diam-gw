# Integration Tests

Automated end-to-end integration tests for the Diameter Gateway.

## Available Tests

### Priority-Based Failover Flow (`podman-flow.sh`)

Comprehensive automated test demonstrating the complete priority-based failover and fail-back workflow using containers.

**What it tests:**
- Building DRA simulator and client container images
- Starting 4 DRAs (2 at Priority 1, 2 at Priority 2)
- Client connections to all DRAs
- Message routing to Priority 1 DRAs only
- Automatic failover when Priority 1 DRAs fail
- Automatic fail-back when Priority 1 DRAs recover

**Usage:**

```bash
# From project root
./test-integration.sh

# Or from this directory
./podman-flow.sh
```

**Requirements:**
- `podman-compose` or `docker-compose` installed
- `docker-compose.yml` in project root
- Built container images (script will build if needed)

**Test Flow:**

1. **Build Phase**: Builds DRA simulator and client container images
2. **Startup Phase**: Starts all 4 DRA containers
3. **Normal Operation**: Client sends messages to Priority 1 DRAs
4. **Failover Test**: Stops Priority 1 DRAs, verifies failover to Priority 2
5. **Fail-back Test**: Restarts Priority 1 DRAs, verifies fail-back
6. **Statistics**: Displays final statistics and summary

**Duration:** ~2-3 minutes

**Interactive Options:**
- Choose to keep containers running for manual inspection
- View real-time logs during test execution

## Logs

Test output is displayed in real-time. Container logs can be viewed:

```bash
# While test is running
podman-compose logs -f client
podman-compose logs -f dra-1

# After test completes (if containers kept running)
podman-compose logs client
```

## Troubleshooting

### Containers already exist
```bash
podman-compose down -v
podman system prune -f
```

### Script fails to find docker-compose.yml
Run from the project root directory where `docker-compose.yml` is located.

### Build failures
```bash
# Rebuild images from scratch
podman-compose build --no-cache
```

## Related Documentation

- [../../README.md](../../README.md) - Project overview
- [../dwr-failover/README.md](../dwr-failover/README.md) - DWR failure threshold tests
- [../../client/README.md](../../client/README.md) - Client library documentation
