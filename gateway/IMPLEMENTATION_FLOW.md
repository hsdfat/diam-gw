# Diameter Gateway - Implementation Flow

## Request Processing Flow

### Step-by-Step Execution

```
┌──────────────────────────────────────────────────────────────┐
│ Step 1: Logic App Connects                                   │
└──────────────────────────────────────────────────────────────┘
    │
    ▼
Server accepts connection (server.go:246)
    │
    ▼
Create server.Connection (server/connection.go:92)
    │
    ▼
Start connection goroutines (connection.go:123):
    - readLoop()    [goroutine 1]
    - writeLoop()   [goroutine 2]  
    - processLoop() [goroutine 3]
    │
    ▼
CER/CEA exchange (connection.go:411-494)
    │
    ▼
Connection State = Open

┌──────────────────────────────────────────────────────────────┐
│ Step 2: Gateway Discovers Connection                         │
└──────────────────────────────────────────────────────────────┘
    │
    ▼
monitorLogicAppConnections() scans (gateway.go:178)
    │
    ▼
Finds new connection via server.GetAllConnections()
    │
    ▼
startConnectionHandler() [goroutine 4] (gateway.go:206)
    │
    ▼
Listens to connection.Receive() channel

┌──────────────────────────────────────────────────────────────┐
│ Step 3: Logic App Sends Request                              │
└──────────────────────────────────────────────────────────────┘
    │
    ▼
Message arrives at server connection readLoop
    │
    ▼
Forwarded to processLoop (server/connection.go:335)
    │
    ▼
handleMessage() checks command code (connection.go:352)
    │
    ├─> Base protocol (CER/DWR/DPR) → handled locally
    │
    └─> Application request → forward to receiveChan
            │
            ▼
        Gateway receives on connection handler (gateway.go:225)

┌──────────────────────────────────────────────────────────────┐
│ Step 4: Gateway Processes Request                            │
└──────────────────────────────────────────────────────────────┘
    │
    ▼
handleLogicAppRequest() called (gateway.go:244)
    │
    ▼
Parse message header (line 251):
    - Extract H2H ID (original)
    - Extract E2E ID  
    - Extract Command Code
    - Extract Application ID
    │
    ▼
Generate new H2H ID for DRA (line 286):
    newHopByHopID := gw.generateHopByHopID()
    │
    ▼
Create Session (line 289):
    Session {
        LogicAppConnection:  conn
        OriginalHopByHopID:  0x12345678
        OriginalEndToEndID:  0xABCDEF01
        DRAHopByHopID:       0x00000001
        CreatedAt:           time.Now()
    }
    │
    ▼
Store session in map (line 298):
    gw.sessions[newHopByHopID] = session
    │
    ▼
Modify message (line 307):
    Replace H2H ID: 0x12345678 → 0x00000001
    Keep E2E ID:    0xABCDEF01
    │
    ▼
Forward to DRA pool (line 313):
    gw.draPool.Send(newMsg)

┌──────────────────────────────────────────────────────────────┐
│ Step 5: DRA Pool Routes to Active DRA                        │
└──────────────────────────────────────────────────────────────┘
    │
    ▼
DRAPool.Send() called (client/dra_pool.go:224)
    │
    ▼
Get current priority level (line 225):
    currentPriority := 1 (or 2 if failed over)
    │
    ▼
Get DRAs at current priority (line 229)
    │
    ▼
Select healthy DRA (line 237):
    for each DRA at priority:
        if pool.IsHealthy():
            pool.Send(data)
            return
    │
    ▼
Message sent to DRA connection (client/connection.go)

┌──────────────────────────────────────────────────────────────┐
│ Step 6: DRA Processes and Responds                           │
└──────────────────────────────────────────────────────────────┘
    │
    ▼
DRA receives request (H2H: 0x00000001, E2E: 0xABCDEF01)
    │
    ▼
DRA processes business logic
    │
    ▼
DRA sends response (H2H: 0x00000001, E2E: 0xABCDEF01)

┌──────────────────────────────────────────────────────────────┐
│ Step 7: Gateway Receives DRA Response                        │
└──────────────────────────────────────────────────────────────┘
    │
    ▼
DRA connection readLoop receives response
    │
    ▼
Forwarded to DRA pool receive channel
    │
    ▼
Aggregated to gateway's DRA pool channel
    │
    ▼
processDRAResponses() receives (gateway.go:330)

┌──────────────────────────────────────────────────────────────┐
│ Step 8: Gateway Processes DRA Response                       │
└──────────────────────────────────────────────────────────────┘
    │
    ▼
handleDRAResponse() called (gateway.go:350)
    │
    ▼
Parse message header (line 356):
    - Extract H2H ID (0x00000001)
    │
    ▼
Lookup session (line 366):
    session := gw.sessions[0x00000001]
    │
    ├─> Not found → error, drop message
    │
    └─> Found → continue
            │
            ▼
        Calculate latency (line 375):
            latencyMs = ResponseAt - CreatedAt
            │
            ▼
        Restore original IDs (line 391):
            H2H: 0x00000001 → 0x12345678
            E2E: unchanged    0xABCDEF01
            │
            ▼
        Forward to Logic App (line 398):
            session.LogicAppConnection.Send(restoredMsg)
            │
            ▼
        Clean up session (line 409):
            delete(gw.sessions, 0x00000001)

┌──────────────────────────────────────────────────────────────┐
│ Step 9: Logic App Receives Response                          │
└──────────────────────────────────────────────────────────────┘
    │
    ▼
Server connection writeLoop sends message
    │
    ▼
TCP write to Logic App
    │
    ▼
Logic App receives response:
    H2H: 0x12345678 (original)
    E2E: 0xABCDEF01 (original)
    │
    ▼
✅ Request/Response cycle complete
```

## Concurrent Operations

### Multiple Requests in Flight

```
Time    Logic App 1         Logic App 2         Gateway             DRA
────────────────────────────────────────────────────────────────────────
t0      Request A           -                   -                   -
        H2H: 0x1111                             
                            
t1      -                   -                   Session A created   -
                                                H2H: 0x1111→0x0001
                                                
t2      -                   -                   Forward to DRA      Request A
                                                                    H2H: 0x0001
                                                
t3      -                   Request B           -                   -
                            H2H: 0x2222
                            
t4      -                   -                   Session B created   Processing A
                                                H2H: 0x2222→0x0002
                                                
t5      -                   -                   Forward to DRA      Request B
                                                                    H2H: 0x0002
                                                
t6      -                   -                   -                   Response A
                                                                    H2H: 0x0001
                                                
t7      -                   -                   Lookup Session A    -
                                                Restore H2H: 0x1111
                                                
t8      Response A          -                   -                   -
        H2H: 0x1111
        
t9      -                   -                   Delete Session A    Response B
                                                                    H2H: 0x0002
                                                
t10     -                   -                   Lookup Session B    -
                                                Restore H2H: 0x2222
                                                
t11     -                   Response B          -                   -
                            H2H: 0x2222
                            
t12     -                   -                   Delete Session B    -
```

## Failover Sequence

```
┌──────────────────────────────────────────────────────────────┐
│ Normal Operation: Priority 1 Active                          │
└──────────────────────────────────────────────────────────────┘
    │
    DRA-1 (Priority 1) ← Active
    DRA-2 (Priority 2) ← Standby
    │
    ▼
Request arrives
    │
    ▼
Route to DRA-1
    │
    ▼
✅ Success

┌──────────────────────────────────────────────────────────────┐
│ DRA-1 Failure Detected                                       │
└──────────────────────────────────────────────────────────────┘
    │
    ▼
DWR/DWA fails 3 times (client/connection.go watchdogLoop)
    │
    ▼
Connection state → Failed
    │
    ▼
Reconnection starts (backoff)
    │
    ▼
Pool marks DRA-1 unhealthy
    │
    ▼
Priority manager checks (dra_pool.go:342)
    │
    ▼
No healthy DRAs at Priority 1
    │
    ▼
Find next priority with healthy DRAs
    │
    ▼
Switch to Priority 2 (line 369)
    │
    ▼
Log: "Failing over to lower priority"
    │
    ▼
activePriority = 2
    │
    ▼
New requests → DRA-2

┌──────────────────────────────────────────────────────────────┐
│ DRA-1 Recovery                                                │
└──────────────────────────────────────────────────────────────┘
    │
    ▼
Reconnection succeeds
    │
    ▼
CER/CEA exchange
    │
    ▼
DWR/DWA successful
    │
    ▼
Pool marks DRA-1 healthy
    │
    ▼
Priority manager checks (every 5s)
    │
    ▼
Finds Priority 1 healthy (line 350)
    │
    ▼
Switch to Priority 1 (line 357)
    │
    ▼
Log: "Failing back to higher priority"
    │
    ▼
activePriority = 1
    │
    ▼
New requests → DRA-1
```

## Code Path Matrix

| Component | File | Line | Goroutines | Locks |
|-----------|------|------|------------|-------|
| Server Listener | server/server.go | 246 | 1 | None |
| Connection Handler | server/connection.go | 123 | 3 per conn | None |
| Gateway Monitor | gateway/gateway.go | 178 | 1 | None |
| Connection Handler | gateway/gateway.go | 206 | 1 per conn | None |
| Request Handler | gateway/gateway.go | 244 | inline | RWMutex |
| DRA Response Handler | gateway/gateway.go | 330 | 1 | RWMutex |
| Session Cleanup | gateway/gateway.go | 426 | 1 | RWMutex |
| DRA Pool | client/dra_pool.go | 224 | 3 | RWMutex |
| DRA Connection | client/connection.go | var | 3 per conn | Mutex |

## Performance Path

### Critical Path (Request → Response)

1. **Receive from Logic App**: O(1) channel read
2. **Parse header**: O(1) byte operations
3. **Generate H2H ID**: O(1) atomic increment
4. **Create session**: O(1) map insert with lock
5. **Modify message**: O(1) byte copy + replace
6. **Route to DRA**: O(1) priority lookup
7. **Send to DRA**: O(1) channel write
8. **Receive from DRA**: O(1) channel read
9. **Lookup session**: O(1) map lookup with lock
10. **Restore IDs**: O(1) byte replace
11. **Send to Logic App**: O(1) channel write
12. **Delete session**: O(1) map delete with lock

**Total Overhead**: < 1ms typically

## Error Paths

```
Request Received
    │
    ├─> Parse Error → Log + Drop
    │
    ├─> Session Create Error → Error Response
    │
    ├─> DRA Send Error → Cleanup + Error
    │
    ├─> DRA Timeout → Session Cleanup
    │
    ├─> Response Parse Error → Log + Drop
    │
    └─> Session Not Found → Log + Drop
```

## Memory Lifecycle

```
Request Arrives
    │
    ▼
Allocate:
    - Message buffer (size: message length)
    - Session struct (~200 bytes)
    - Map entry (~50 bytes)
    │
    ▼
Total: ~message_length + 250 bytes
    │
    ▼
Response Received
    │
    ▼
Free:
    - Message buffer (GC)
    - Session struct (deleted from map)
    - Map entry (deleted)
    │
    ▼
Memory released (GC)
```

## Thread Safety

### Protected Resources

```
gw.sessions (map):
    - Protected by: sessionsMu (RWMutex)
    - Writers: handleLogicAppRequest(), cleanupExpiredSessions()
    - Readers: handleDRAResponse()
    
gw.stats (counters):
    - Protected by: atomic operations
    - Writers: all handlers
    - Readers: GetStats()
    
draPool.activePriority (int32):
    - Protected by: atomic operations
    - Writers: checkAndUpdatePriority()
    - Readers: Send()
```

## Key Function Call Graph

```
main()
├─> NewGateway()
│   ├─> server.NewServer()
│   └─> client.NewDRAPool()
│
├─> gateway.Start()
│   ├─> draPool.Start()
│   │   ├─> pool.Start() [for each DRA]
│   │   │   └─> connection.Start() [for each conn]
│   │   ├─> startMessageAggregator()
│   │   ├─> startHealthMonitor()
│   │   └─> startPriorityManager()
│   │
│   ├─> server.Start()
│   │   └─> accept loop → newConn() → serve()
│   │
│   ├─> monitorLogicAppConnections()
│   ├─> processDRAResponses()
│   └─> sessionCleanup()
│
└─> gateway.Stop()
    ├─> server.Stop()
    └─> draPool.Close()
```

This implementation flow provides a complete view of how the gateway processes requests from receipt to response delivery.
