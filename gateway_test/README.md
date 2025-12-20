# Diameter Gateway - Integration Tests

This package contains comprehensive integration tests for the Diameter Gateway, including simulators for DRA servers and Logic App clients.

## Components

### 1. Integration Tests ([integration_test.go](integration_test.go))

Complete end-to-end tests including:
- **TestGatewayConnectivity**: Basic connectivity test
- **TestGatewayPerformance**: Load testing with multiple clients
- **TestGatewayFailover**: DRA failover and failback testing
- **BenchmarkGatewayThroughput**: Performance benchmarking

### 2. DRA Simulator ([dra_simulator.go](dra_simulator.go))

A simulated DRA server that:
- Accepts Diameter connections
- Handles CER/CEA exchange
- Responds to DWR/DWA watchdog
- Echoes application requests as responses
- Tracks request/response statistics

### 3. Logic App Simulator ([logicapp_simulator.go](logicapp_simulator.go))

A simulated Logic Application client that:
- Connects to the gateway
- Performs CER/CEA exchange
- Sends Diameter requests
- Receives and validates responses
- Tracks request/response correlation

## Running Tests

### Run All Tests

```bash
cd gateway_test
go test -v
```

### Run Specific Test

```bash
# Connectivity test
go test -v -run TestGatewayConnectivity

# Performance test
go test -v -run TestGatewayPerformance

# Failover test
go test -v -run TestGatewayFailover
```

### Run Benchmark

```bash
go test -bench=. -benchmem
```

### Skip Long Tests

```bash
go test -v -short
```

## Test Scenarios

### Test 1: Connectivity

**Purpose**: Verify basic end-to-end communication

**Flow**:
```
1. Start DRA simulator on port 13868
2. Start Gateway on port 13867
3. Gateway connects to DRA (CER/CEA)
4. Logic App connects to Gateway (CER/CEA)
5. Logic App sends request
6. Gateway forwards to DRA
7. DRA sends response
8. Gateway forwards to Logic App
9. Verify H2H ID preserved
10. Verify statistics
```

**Expected Results**:
- Gateway connects to DRA successfully
- Logic App connects to Gateway successfully
- Request/response cycle completes
- H2H ID correctly mapped
- All statistics updated

### Test 2: Performance

**Purpose**: Measure gateway throughput and latency under load

**Configuration**:
- 10 concurrent Logic App clients
- 100 requests per client
- Total: 1,000 requests

**Metrics**:
- Total duration
- Success rate (should be >= 95%)
- Throughput (requests/second)
- Average latency (should be < 100ms typically)

**Expected Results**:
```
Performance test completed in ~X seconds
Successful: 1000 (100%)
Throughput: > 100 req/sec
Avg latency: < 100 ms
```

### Test 3: Failover

**Purpose**: Verify DRA failover and failback

**Flow**:
```
1. Start primary DRA (Priority 1) on port 13871
2. Start secondary DRA (Priority 2) on port 13872
3. Start Gateway with both DRAs
4. Verify active priority = 1 (primary)
5. Send request → goes to primary
6. Stop primary DRA
7. Wait for failover (15 seconds)
8. Verify active priority = 2 (secondary)
9. Send request → goes to secondary
```

**Expected Results**:
- Initial priority: 1 (primary active)
- After primary failure: priority 2 (failover)
- Requests route correctly after failover
- No message loss during failover

## Test Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Integration Test                      │
│                                                          │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────┐  │
│  │  Logic App   │───>│   Gateway    │───>│   DRA    │  │
│  │  Simulator   │<───│              │<───│Simulator │  │
│  └──────────────┘    └──────────────┘    └──────────┘  │
│       Port            Port 13867          Port 13868    │
│      Random                                             │
└─────────────────────────────────────────────────────────┘
```

## Port Allocation

| Test | Gateway Port | DRA Port(s) |
|------|--------------|-------------|
| Connectivity | 13867 | 13868 |
| Performance | 13870 | 13869 |
| Failover | 13873 | 13871, 13872 |
| Benchmark | 13875 | 13874 |

## Example Output

### Connectivity Test

```
=== RUN   TestGatewayConnectivity
INFO  DRA simulator started  address=127.0.0.1:13868
INFO  Starting Diameter Gateway  origin_host=test-gw.example.com
INFO  DRA pool started  active_priority=1
INFO  Gateway started successfully
INFO  Accepted connection  remote=127.0.0.1:xxxxx
INFO  Connected to gateway  address=127.0.0.1:13867
integration_test.go:xxx: Gateway connected to DRA: 1 active connections
integration_test.go:xxx: Logic App connected to gateway
integration_test.go:xxx: Received response from DRA via gateway
integration_test.go:xxx: Gateway stats: requests=1, responses=1, forwarded=1
integration_test.go:xxx: DRA stats: requests=1, responses=1
integration_test.go:xxx: ✓ Connectivity test passed
--- PASS: TestGatewayConnectivity (2.50s)
```

### Performance Test

```
=== RUN   TestGatewayPerformance
INFO  DRA simulator started
INFO  Gateway started successfully
integration_test.go:xxx: Starting performance test: 10 clients, 100 requests each
integration_test.go:xxx: Performance test completed in 5.234s
integration_test.go:xxx: Total requests: 1000
integration_test.go:xxx: Successful: 1000 (100.00%)
integration_test.go:xxx: Errors: 0
integration_test.go:xxx: Throughput: 191.05 req/sec
integration_test.go:xxx: Gateway stats:
integration_test.go:xxx:   Total requests: 1000
integration_test.go:xxx:   Total responses: 1000
integration_test.go:xxx:   Avg latency: 45.23 ms
integration_test.go:xxx: ✓ Performance test passed
--- PASS: TestGatewayPerformance (6.00s)
```

### Failover Test

```
=== RUN   TestGatewayFailover
INFO  DRA simulator started  address=127.0.0.1:13871
INFO  DRA simulator started  address=127.0.0.1:13872
INFO  Gateway started successfully
integration_test.go:xxx: Initial active priority: 1
integration_test.go:xxx: Request sent to primary DRA
INFO  Stopping DRA simulator
integration_test.go:xxx: Stopping primary DRA to trigger failover...
WARN  Failing over to lower priority  old_priority=1 new_priority=2
integration_test.go:xxx: Failover successful: active priority = 2
integration_test.go:xxx: Request sent after failover
integration_test.go:xxx: ✓ Failover test passed
--- PASS: TestGatewayFailover (18.50s)
```

### Benchmark

```
goos: darwin
goarch: amd64
pkg: github.com/hsdfat/diam-gw/gateway_test
BenchmarkGatewayThroughput-8   	    5000	    250000 ns/op	    1024 B/op	      15 allocs/op
integration_test.go:xxx: Throughput: 200.00 req/sec
integration_test.go:xxx: Avg latency: 42.50 ms
PASS
```

## Debugging Tests

### Enable Debug Logging

Edit test to use debug log level:

```go
log := logger.New("integration-test", "debug")
```

### Run Single Test with Verbose Output

```bash
go test -v -run TestGatewayConnectivity
```

### Check for Race Conditions

```bash
go test -race -v
```

### Profile CPU Usage

```bash
go test -cpuprofile=cpu.prof -bench=.
go tool pprof cpu.prof
```

### Profile Memory Usage

```bash
go test -memprofile=mem.prof -bench=.
go tool pprof mem.prof
```

## Common Issues

### Port Already in Use

**Error**: `listen tcp: bind: address already in use`

**Solution**: Change test ports or kill existing processes:
```bash
lsof -ti:13868 | xargs kill
```

### Timeout Errors

**Error**: `timeout waiting for response`

**Causes**:
- DRA simulator not started
- Gateway not connected to DRA
- Network issues

**Solution**:
- Check logs for connection errors
- Increase timeouts if system is slow
- Verify all components started successfully

### Failover Not Triggering

**Causes**:
- DWR timeout too long
- Health check interval too long
- Insufficient wait time

**Solution**:
- Reduce DWR interval in test config
- Reduce health check interval
- Increase wait time after stopping DRA

## Performance Tuning

### For Higher Throughput

Increase buffer sizes:
```go
SendChannelSize:  10000,
RecvChannelSize:  10000,
```

### For Lower Latency

Reduce timeouts:
```go
ReadTimeout:  5 * time.Second,
WriteTimeout: 2 * time.Second,
```

### For Many Concurrent Connections

Increase limits:
```go
MaxConnections: 10000,
```

## CI/CD Integration

### GitHub Actions

```yaml
- name: Run Integration Tests
  run: |
    cd gateway_test
    go test -v -timeout 5m
```

### Jenkins

```groovy
stage('Integration Tests') {
    steps {
        sh 'cd gateway_test && go test -v -timeout 5m'
    }
}
```

## Test Coverage

Generate coverage report:

```bash
go test -coverprofile=coverage.out
go tool cover -html=coverage.out
```

## Best Practices

1. **Isolation**: Each test uses unique ports to avoid conflicts
2. **Cleanup**: All tests properly clean up resources (defer)
3. **Timeouts**: All tests have timeouts to prevent hanging
4. **Logging**: Appropriate log levels for debugging
5. **Statistics**: Verify statistics to ensure correctness
6. **Error Handling**: Check error conditions thoroughly

## Contributing

When adding new tests:
1. Use unique port numbers
2. Include proper cleanup (defer)
3. Add context with timeout
4. Verify statistics
5. Document expected behavior
6. Add to this README

## License

Same as the parent project.
