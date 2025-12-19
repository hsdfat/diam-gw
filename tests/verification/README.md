# Verification Scripts

Scripts to verify correct setup and configuration of the Diameter Gateway components.

## Available Scripts

### Container Setup Verification (`verify-container-setup.sh`)

Verifies that all required container-related files are present and correctly configured.

**Usage:**
```bash
# From project root
./tests/verification/verify-container-setup.sh

# Or from this directory
./verify-container-setup.sh
```

**Checks:**
- ✓ Core container files (Dockerfiles, docker-compose.yml)
- ✓ Application code (multi_dra_test_container)
- ✓ Scripts are executable
- ✓ Documentation files exist

**Expected Output:**
```
╔══════════════════════════════════════════════════════════════╗
║        Container Setup Verification                          ║
╚══════════════════════════════════════════════════════════════╝

Core Container Files:
✓ Dockerfile.dra
✓ Dockerfile.client
✓ docker-compose.yml

...

╔══════════════════════════════════════════════════════════════╗
║                 Ready to Test!                                ║
╚══════════════════════════════════════════════════════════════╝
```

### Multi-DRA Setup Verification (`verify-multi-dra.sh`)

Verifies that all required components for multi-DRA testing are built and ready.

**Usage:**
```bash
# From project root
./tests/verification/verify-multi-dra.sh

# Or from this directory
./verify-multi-dra.sh
```

**Checks:**
- ✓ Binaries built (dra-simulator, multi-dra-test)
- ✓ Scripts executable (run-4-dras.sh)
- ✓ Source files present
- ✓ Documentation complete
- ✓ Code size validation (client/dra_pool.go >500 lines)

**Expected Output:**
```
╔══════════════════════════════════════════════════════════════╗
║          Multi-DRA Setup Verification                        ║
╚══════════════════════════════════════════════════════════════╝

Checking binaries...
✓ DRA simulator binary exists
✓ Multi-DRA test client binary exists

...

╔══════════════════════════════════════════════════════════════╗
║                  ✅ Verification Complete!                    ║
╚══════════════════════════════════════════════════════════════╝
```

## When to Use These Scripts

### verify-container-setup.sh
Use before running container-based tests to ensure:
- Docker/Podman environment is configured
- All container files are present
- Ready to run `podman-flow.sh` or manual container tests

### verify-multi-dra.sh
Use before running host-based multi-DRA tests to ensure:
- Binaries are built (`make build`)
- Ready to run `run-4-dras.sh` and multi-DRA test clients
- Development environment is complete

## Exit Codes

Both scripts:
- Exit with `0` on success (all checks pass)
- Exit with `1` on failure (any check fails)

This makes them suitable for CI/CD pipelines:
```bash
# In CI pipeline
./tests/verification/verify-container-setup.sh && ./test-integration.sh
```

## Related Documentation

- [../integration/README.md](../integration/README.md) - Integration tests
- [../dwr-failover/README.md](../dwr-failover/README.md) - DWR failure tests
- [../../tools/README.md](../../tools/README.md) - Development tools
