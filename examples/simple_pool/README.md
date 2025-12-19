# Simple Connection Pool Example

A minimal example demonstrating connection pool creation and state monitoring.

## Purpose

This example shows:
- Creating a connection pool with minimal configuration
- Starting and stopping the pool
- Monitoring connection states
- Displaying pool statistics
- Checking pool health

## Running

```bash
cd examples/simple_pool
go run main.go
```

## Expected Output

```
Simple Connection Pool Example
================================
Creating connection pool...
Starting connection pool...
Waiting for connections to establish...
✓ At least one connection is active!

--- Pool State ---
Total Connections: 3
Active Connections: 3
Messages Sent: 6
Messages Received: 6
Bytes Sent: 240
Bytes Received: 240
Reconnections: 0

Connection States:
  127.0.0.1:3868-conn0: OPEN
  127.0.0.1:3868-conn1: OPEN
  127.0.0.1:3868-conn2: OPEN

✓ Pool is healthy
------------------
```

## What It Demonstrates

1. **Configuration**: Using default config with minimal customization
2. **Pool Creation**: Creating a connection pool with 3 connections
3. **State Monitoring**: Checking connection states and pool health
4. **Statistics**: Displaying message counts and connection metrics
5. **Graceful Shutdown**: Properly closing the pool

## No DRA Server?

If you don't have a DRA server running, the example will:
- Show connection attempts
- Display reconnection behavior
- Show unhealthy pool state

This is useful for understanding how the client handles unavailable servers.

## Next Steps

- See [s13_client](../s13_client/) for a complete production example
- Read [client documentation](../../client/README.md) for more details
- Review [design document](../../GATEWAY_CLIENT_DESIGN.md) for architecture
