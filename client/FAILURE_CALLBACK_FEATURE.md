# Connection Failure Callback Feature

## Overview

This feature adds immediate failure detection and cleanup to the Diameter client connection pool through a callback mechanism. When a connection failure is detected (e.g., remote disconnect, DWR timeout, network error), the pool is notified immediately rather than waiting for the periodic health check.

## Benefits

1. **Immediate Cleanup**: Dead connections are removed from the pool as soon as failure is detected, not after the next health check cycle (default: 30 seconds)
2. **Faster Failover**: Applications can react to connection failures immediately
3. **Resource Efficiency**: Failed connections are cleaned up promptly, freeing resources
4. **Better Observability**: Pool state accurately reflects connection health in real-time

## Architecture

### Components

```
Connection (connection.go)
    ├── onFailure callback field
    ├── SetOnFailure() - Register callback
    └── callOnFailure() - Trigger callback in goroutine

ManagedConnection (address_pool.go)
    └── onConnectionFailure() - Handle failure by cancelling context

AddressConnectionPool (address_pool.go)
    └── establishConnection() - Register callback during setup
```

### Flow Diagram

```
┌─────────────────────────────────────────────────────────────┐
│ Connection Failure Detection                                 │
│  - Read/Write Error                                          │
│  - DWR Timeout (MaxDWRFailures exceeded)                     │
│  - Network Disconnect                                        │
└──────────────────┬──────────────────────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────────────────────────┐
│ handleFailure() called                                       │
│  1. Set state to StateFailed                                 │
│  2. Close TCP connection                                     │
│  3. Call onFailure callback ◄── NEW FEATURE                  │
│  4. Attempt reconnect (if enabled)                           │
└──────────────────┬──────────────────────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────────────────────────┐
│ ManagedConnection.onConnectionFailure()                      │
│  - Logs failure event                                        │
│  - Cancels connection context                                │
└──────────────────┬──────────────────────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────────────────────────┐
│ Lifecycle Manager (goroutine)                                │
│  - Detects context cancellation                              │
│  - Calls pool.removeConnection()                             │
│  - Cleans up resources                                       │
└─────────────────────────────────────────────────────────────┘
```

## Implementation Details

### 1. Connection Struct Changes

**File**: [client/connection.go](connection.go)

Added fields:
```go
// Failure callback
onFailure   func(error)
onFailureMu sync.RWMutex
```

New methods:
```go
// SetOnFailure sets the failure callback
func (c *Connection) SetOnFailure(callback func(error))

// callOnFailure safely calls the failure callback if set
func (c *Connection) callOnFailure(err error)
```

Modified method:
```go
func (c *Connection) handleFailure(err error) {
    // ... existing code ...

    // Notify pool or other managers immediately
    c.callOnFailure(err)  // ◄── NEW

    // ... existing code ...
}
```

### 2. ManagedConnection Changes

**File**: [client/address_pool.go](address_pool.go)

New method:
```go
// onConnectionFailure is called when the underlying connection fails
// This allows immediate cleanup instead of waiting for health check
func (mc *ManagedConnection) onConnectionFailure(err error) {
    mc.pool.logger.Warnw("Connection failure detected",
        "remote_addr", mc.address,
        "error", err,
        "action", "immediate_cleanup")

    // Cancel the managed connection context to trigger cleanup
    mc.cancel()
}
```

### 3. Pool Setup Changes

**File**: [client/address_pool.go](address_pool.go)

Modified `establishConnection()` method:
```go
// Create the connection
conn := NewConnection(connCtx, connID, draConfig, p.logger)

// Create managed connection (before starting, so we can set callback)
mc := &ManagedConnection{
    conn:      conn,
    address:   remoteAddr,
    createdAt: time.Now(),
    pool:      p,
    ctx:       connCtx,
    cancel:    connCancel,
    handleFn:  p.handleFn,
}

// Set failure callback for immediate cleanup  ◄── NEW
conn.SetOnFailure(mc.onConnectionFailure)

// Start the connection (this performs CER/CEA)
if err := conn.Start(); err != nil {
    // ... handle error ...
}
```

## Failure Detection Mechanisms

The callback is triggered by multiple detection mechanisms:

### 1. Read Loop Errors
```go
// connection.go:505-542
func (c *Connection) startReadLoop() {
    // Detects:
    // - Connection closed by remote
    // - Network errors
    // - EOF on read
    if err != nil {
        c.handleFailure(err)  // ◄── Triggers callback
    }
}
```

### 2. Write Loop Errors
```go
// connection.go:610-645
func (c *Connection) startWriteLoop() {
    // Detects:
    // - Connection closed
    // - Write errors
    if err != nil {
        c.handleFailure(err)  // ◄── Triggers callback
    }
}
```

### 3. DWR/DWA Timeouts
```go
// connection.go:422-446
func (c *Connection) handleDWRFailure(err error) {
    failureCount := c.dwrFailureCount.Add(1)

    if int(failureCount) >= c.config.MaxDWRFailures {
        c.handleFailure(err)  // ◄── Triggers callback
    }
}
```

### 4. CloseNotify Events
```go
// connection.go:515-516
case <-c.conn.(connection.CloseNotifier).CloseNotify():
    c.handleFailure(fmt.Errorf("connect closed"))  // ◄── Triggers callback
```

## Configuration

No new configuration options are needed. The feature works with existing settings:

```go
config := &PoolConfig{
    // Connection health monitoring
    DWRInterval:         30 * time.Second,  // Heartbeat interval
    DWRTimeout:          10 * time.Second,  // Heartbeat timeout
    MaxDWRFailures:      3,                 // Failures before cleanup

    // Health check (still runs as backup)
    HealthCheckInterval: 30 * time.Second,  // Backup cleanup mechanism

    // Reconnection (connection-level, not pool-level)
    ReconnectEnabled:    false,             // Disable to remove from pool on failure
}
```

### Important: ReconnectEnabled Behavior

- **`ReconnectEnabled: true`**: Connection attempts automatic reconnection, stays in pool
- **`ReconnectEnabled: false`**: Connection is removed from pool on failure, recreated on next `Send()`

For address-based pools, **`ReconnectEnabled: false`** is recommended because:
1. Pool automatically creates new connections on demand
2. Allows connection to be fully cleaned up and recreated
3. Prevents stale connection states

## Testing

### Unit Tests

New test file: [address_pool_callback_test.go](address_pool_callback_test.go)

Tests include:
1. **TestConnection_OnFailureCallback**: Verifies callback is invoked
2. **TestConnection_MultipleCallbackCalls**: Tests multiple invocations
3. **TestConnection_NilCallback**: Ensures nil callback doesn't panic
4. **TestManagedConnection_FailureCallback**: Tests end-to-end cleanup

Run tests:
```bash
go test ./client -v -run "TestConnection.*Callback"
```

### Integration Test Scenario

```go
// Create pool
pool, _ := NewAddressConnectionPool(ctx, config, logger)

// Send message (creates connection automatically)
err := pool.Send(ctx, "192.168.1.100:3868", message)

// Simulate network failure (connection detects via read error)
// ↓ Callback fires immediately
// ↓ ManagedConnection.cancel() called
// ↓ Lifecycle manager removes from pool
// ↓ Next Send() creates new connection
```

## Comparison: Before vs After

### Before (Periodic Health Check Only)

```
T+0s:  Connection fails (remote disconnect)
       ├─ Connection stays in pool with !IsActive() state
       │
T+30s: Health check runs
       ├─ Detects !IsActive()
       └─ Removes from pool

Total Time to Cleanup: ~30 seconds (HealthCheckInterval)
```

### After (With Callback)

```
T+0s:  Connection fails (remote disconnect)
       ├─ Callback fires immediately
       ├─ Context cancelled
       └─ Lifecycle manager removes from pool

Total Time to Cleanup: ~milliseconds
```

## Thread Safety

All callback mechanisms are thread-safe:

1. **SetOnFailure()**: Uses `sync.RWMutex` to protect callback assignment
2. **callOnFailure()**: Executes callback in goroutine to prevent blocking
3. **onConnectionFailure()**: Only calls `cancel()`, which is goroutine-safe
4. **Pool operations**: Already protected by existing mutexes

## Backward Compatibility

✅ **Fully backward compatible**

- Existing code continues to work without changes
- Callback is optional (nil callback is handled gracefully)
- Health check still runs as backup safety net
- All existing tests pass

## Future Enhancements

Potential improvements:

1. **Configurable Callbacks**: Allow applications to register custom callbacks
2. **Failure Metrics**: Track callback invocations in pool metrics
3. **Callback Context**: Pass additional context (connection ID, remote address) to callbacks
4. **Failure Hooks**: Pre/post failure hooks for custom logic

## Example Usage

### Basic Usage (Automatic)

```go
// Create pool - callback is automatically registered
pool, err := NewAddressConnectionPool(ctx, config, logger)

// Send messages - failures trigger immediate cleanup
err = pool.Send(ctx, "192.168.1.100:3868", message)
```

### Advanced: Custom Callback

```go
// Create connection manually
conn := NewConnection(ctx, "my-conn", draConfig, logger)

// Register custom callback
conn.SetOnFailure(func(err error) {
    log.Printf("Connection failed: %v", err)
    // Custom failure handling
})

// Start connection
conn.Start()
```

## Files Modified

1. **[client/connection.go](connection.go)**
   - Added `onFailure` callback fields
   - Added `SetOnFailure()` method
   - Added `callOnFailure()` method
   - Modified `handleFailure()` to trigger callback

2. **[client/address_pool.go](address_pool.go)**
   - Added `onConnectionFailure()` to ManagedConnection
   - Modified `establishConnection()` to register callback

3. **[client/address_pool_callback_test.go](address_pool_callback_test.go)** (NEW)
   - Comprehensive test suite for callback feature

## Summary

This feature enhances the Diameter client's connection management by adding immediate failure detection and cleanup through callbacks. It provides:

- ⚡ **Immediate response** to connection failures
- 🔄 **Faster failover** for applications
- 🧹 **Cleaner resource management**
- 📊 **Better observability** of connection state
- ✅ **Full backward compatibility**

The implementation is minimal, non-intrusive, and leverages existing goroutine-safe patterns in the codebase.
