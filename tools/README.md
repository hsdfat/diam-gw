# Development Tools

Utilities and tools for development, testing, and debugging of the Diameter Gateway.

## Available Tools

### Multi-DRA Manager (`run-4-dras.sh`)

Host-based tool for running and managing 4 DRA simulators with priority configuration for manual testing and development.

**Priority Configuration:**
- **Priority 1 (Primary)**: DRA-1 (port 3868), DRA-2 (port 3869)
- **Priority 2 (Backup)**: DRA-3 (port 3870), DRA-4 (port 3871)

### Usage

```bash
# From project root
./tools/run-4-dras.sh [command]

# Or from tools directory
cd tools
./run-4-dras.sh [command]
```

### Commands

#### Start all DRAs
```bash
./tools/run-4-dras.sh start
```
Starts all 4 DRA simulators with priority configuration. Creates PID files in `logs/` directory.

#### Stop all DRAs
```bash
./tools/run-4-dras.sh stop
```
Gracefully stops all running DRA simulators.

#### Restart all DRAs
```bash
./tools/run-4-dras.sh restart
```
Stops and restarts all DRA simulators.

#### Check status
```bash
./tools/run-4-dras.sh status
```
Shows current status of all DRA simulators (running/stopped, PIDs, ports).

#### View logs
```bash
./tools/run-4-dras.sh logs [1-4]
```
Tail logs for specific DRA (default: DRA-1).

Examples:
```bash
./tools/run-4-dras.sh logs 1    # View DRA-1 logs
./tools/run-4-dras.sh logs 3    # View DRA-3 logs
```

#### Test failover (kill Priority 1)
```bash
./tools/run-4-dras.sh kill-p1
```
Kills Priority 1 DRAs (DRA-1, DRA-2) to simulate failure and test failover to Priority 2.

#### Test fail-back (start Priority 1)
```bash
./tools/run-4-dras.sh start-p1
```
Restarts Priority 1 DRAs (DRA-1, DRA-2) to test fail-back from Priority 2.

### Example Testing Workflow

```bash
# 1. Build required binaries
make build-dra
make build

# 2. Start all 4 DRAs
./tools/run-4-dras.sh start

# 3. In another terminal, run test client
./bin/multi-dra-test

# 4. Test failover (in original terminal)
./tools/run-4-dras.sh kill-p1
# Observe client failover to Priority 2 DRAs

# 5. Test fail-back
./tools/run-4-dras.sh start-p1
# Observe client fail-back to Priority 1 DRAs

# 6. Check status
./tools/run-4-dras.sh status

# 7. Stop all when done
./tools/run-4-dras.sh stop
```

### Log Files

Logs are stored in `logs/` directory:
- `logs/dra-1.log` - DRA-1 logs
- `logs/dra-2.log` - DRA-2 logs
- `logs/dra-3.log` - DRA-3 logs
- `logs/dra-4.log` - DRA-4 logs
- `logs/dra-1.pid` - DRA-1 process ID
- (PID files for each DRA)

### Requirements

- `bin/dra-simulator` binary (built with `make build-dra`)
- Ports 3868-3871 available
- Sufficient permissions to bind to ports

### Troubleshooting

#### Ports in use
```bash
# Find processes using ports
lsof -i :3868
lsof -i :3869

# Kill specific process
kill -9 <PID>

# Or use the cleanup in the script
./tools/run-4-dras.sh stop
```

#### DRA fails to start
Check logs:
```bash
cat logs/dra-1.log
```

Rebuild binary:
```bash
make clean
make build-dra
```

#### Ctrl+C handling
The script traps SIGINT/SIGTERM and automatically stops all DRAs on exit.

### Comparison: Host-based vs Container-based Testing

| Feature | Host-based (`run-4-dras.sh`) | Container-based (`podman-flow.sh`) |
|---------|------------------------------|-------------------------------------|
| **Setup** | Requires built binaries | Builds containers automatically |
| **Isolation** | Processes on host | Containerized |
| **Speed** | Fast startup | Slower (container overhead) |
| **Use case** | Development, debugging | Integration testing, CI/CD |
| **Cleanup** | Kill processes | Remove containers |
| **Logs** | `logs/` directory | Container logs |
| **Network** | Localhost ports | Docker network |

### When to Use

**Use `run-4-dras.sh` when:**
- Developing and debugging client code
- Need quick iteration cycles
- Want to attach debugger to processes
- Testing on local machine

**Use `podman-flow.sh` when:**
- Running integration tests
- Validating complete system
- CI/CD pipelines
- Need isolated test environment

## Related Documentation

- [../tests/integration/README.md](../tests/integration/README.md) - Integration tests
- [../tests/dwr-failover/README.md](../tests/dwr-failover/README.md) - DWR failure tests
- [../examples/README.md](../examples/README.md) - Example applications
