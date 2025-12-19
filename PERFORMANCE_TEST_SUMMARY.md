# Performance Test Summary

## Overview

Performance tests have been created for both the `client` and `server` packages in the Diameter Gateway project. These tests measure throughput, latency, and scalability under various load conditions.

## Test Files

### 1. Client Package Tests (`client/connection_pool_performance_test.go`)

- **TestPerformance_ConnectionPool_Throughput**: Tests connection pool throughput with different concurrency levels and message sizes
- **TestPerformance_ConnectionPool_Latency**: Tests latency under steady load and burst traffic patterns
- **BenchmarkConnectionPool_Send**: Benchmark for message sending performance
- **BenchmarkConnectionPool_SendLarge**: Benchmark for large message (4KB) sending

### 2. Server Package Tests (`server/server_performance_test.go`)

- **TestPerformance_Server_ConcurrentConnections**: Tests server handling of concurrent connections
- **TestPerformance_Server_MessageThroughput**: Tests message processing throughput
- **TestPerformance_Server_Latency**: Tests server latency under different load patterns
- **BenchmarkServer_AcceptConnection**: Benchmark for connection acceptance
- **BenchmarkServer_ProcessMessage**: Benchmark for message processing

### 3. Combined Client-Server Tests (`client_server_performance_test.go`)

- **TestPerformance_ClientServer_EndToEnd**: End-to-end performance test using real client and server
- **TestPerformance_ClientServer_Latency**: End-to-end latency testing
- **BenchmarkClientServer_EndToEnd**: End-to-end benchmark

## Running Tests

### Run all performance tests:
```bash
cd diam-gw
go test -v -run TestPerformance -timeout 10m
```

### Run benchmarks:
```bash
cd diam-gw
go test -bench=. -benchmem -timeout 10m
```

### Run specific test:
```bash
go test -v -run TestPerformance_ClientServer_EndToEnd -timeout 5m
```

## Performance Optimizations

### 1. Connection Pool Optimizations

**Current Implementation:**
- Connection pool uses round-robin load balancing
- Staggered connection startup to avoid thundering herd
- Configurable buffer sizes for send/receive channels

**Optimizations Applied:**
- ✅ Proper CER/CEA handshake handling with Host-IP-Address
- ✅ Full message reading using `io.ReadFull` for reliability
- ✅ Proper timeout handling for read operations
- ✅ Connection state management with atomic operations

**Recommended Optimizations:**
1. **Connection Reuse**: Implement connection warm-up before tests
2. **Buffer Sizing**: Tune buffer sizes based on message rate:
   - Low traffic: 100-500
   - Medium traffic: 500-2000
   - High traffic: 2000-10000
3. **Connection Count**: Optimal connection count depends on load:
   - Low: 2-5 connections
   - Medium: 5-10 connections
   - High: 10-20 connections

### 2. Server Optimizations

**Current Implementation:**
- Per-connection goroutines for read/write/process
- Channel-based message passing
- Configurable channel buffer sizes

**Optimizations Applied:**
- ✅ Host-IP-Address added to CEA responses
- ✅ Proper message validation and error handling
- ✅ Efficient connection management with sync.RWMutex

**Recommended Optimizations:**
1. **Worker Pool**: Consider worker pool pattern for message processing instead of per-connection goroutines
2. **Batch Processing**: Implement message batching for high-throughput scenarios
3. **Connection Limits**: Monitor and enforce connection limits more efficiently
4. **Memory Pool**: Use sync.Pool for message buffers to reduce allocations

### 3. Message Handling Optimizations

**Current Implementation:**
- Individual message processing
- Response matching via hop-by-hop ID maps

**Recommended Optimizations:**
1. **Message Batching**: Batch multiple messages in single write operation
2. **Zero-Copy**: Use buffer pools to avoid allocations
3. **Response Caching**: Cache common responses for faster processing
4. **Compression**: Consider message compression for large payloads

## Performance Metrics

### Key Metrics Tracked:

1. **Throughput**: Requests/Messages per second
2. **Latency**: 
   - Average latency
   - P50 (median)
   - P95 (95th percentile)
   - P99 (99th percentile)
   - Min/Max latency
3. **Success Rate**: Percentage of successful requests
4. **Connection Metrics**: Active connections, total connections

### Performance Targets:

- **P95 Latency**: < 100ms (warning if exceeded)
- **P99 Latency**: < 200ms (warning if exceeded)
- **Success Rate**: > 95%
- **Throughput**: Varies by scenario (measured in req/s)

## Test Scenarios

### Throughput Tests:
- Low concurrency (5 clients) with small/large messages
- Medium concurrency (25 clients) with small/large messages
- High concurrency (50 clients) with small/large messages

### Latency Tests:
- Steady load (low/medium/high)
- Burst traffic (short/long intervals)

### Connection Tests:
- Low connections (10)
- Medium connections (50)
- High connections (100)
- Very high connections (200)

## Known Issues and Fixes

### Fixed Issues:
1. ✅ Mock server CER/CEA handshake - Fixed to properly read full messages
2. ✅ Server CEA Host-IP-Address - Added to CEA response
3. ✅ Message reading - Changed to use `io.ReadFull` for reliability

### Remaining Considerations:
1. Test timeout handling - Some tests may need longer timeouts for high-load scenarios
2. Resource cleanup - Ensure proper cleanup of goroutines and connections
3. Error handling - Improve error handling in test helpers

## Future Enhancements

1. **Load Testing**: Add sustained load tests (60+ seconds)
2. **Stress Testing**: Test system limits and breaking points
3. **Memory Profiling**: Add memory profiling to detect leaks
4. **CPU Profiling**: Add CPU profiling to identify bottlenecks
5. **Distributed Testing**: Test with multiple servers/clients
6. **Real-world Scenarios**: Test with actual Diameter application messages (S13, S6a)

## Usage Examples

### Example: Run end-to-end performance test
```bash
cd diam-gw
go test -v -run TestPerformance_ClientServer_EndToEnd -timeout 5m
```

### Example: Run benchmark
```bash
cd diam-gw
go test -bench=BenchmarkClientServer_EndToEnd -benchmem -timeout 5m
```

### Example: Run with specific concurrency
```bash
go test -v -run TestPerformance_ClientServer_EndToEnd/LowConcurrency_SmallMsg
```

## Performance Tuning Guide

### For High Throughput:
1. Increase connection count (10-20)
2. Increase buffer sizes (2000-10000)
3. Use larger message batches
4. Tune goroutine limits

### For Low Latency:
1. Reduce connection count (2-5)
2. Use smaller buffer sizes (100-500)
3. Optimize message processing path
4. Reduce context switching

### For High Concurrency:
1. Increase server max connections
2. Optimize connection acceptance
3. Use connection pooling
4. Implement rate limiting

## Conclusion

The performance test suite provides comprehensive coverage of client and server performance characteristics. The tests can be used to:
- Validate performance improvements
- Identify bottlenecks
- Set performance baselines
- Monitor performance regressions

Regular performance testing is recommended as part of the CI/CD pipeline to ensure consistent performance characteristics.

