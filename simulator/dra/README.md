# DRA Simulator

A high-performance Diameter Routing Agent (DRA) simulator for testing Diameter client implementations.

## Features

- **Full Diameter Protocol Support**
  - CER/CEA (Capabilities Exchange)
  - DWR/DWA (Device Watchdog)
  - DPR/DPA (Disconnect Peer)
  - Session management

- **High Performance**
  - Concurrent connection handling
  - Configurable connection limits
  - Non-blocking I/O

- **Monitoring & Metrics**
  - Real-time statistics
  - Connection tracking
  - Message counters
  - Bandwidth monitoring

- **Flexible Configuration**
  - Configurable timeouts
  - Custom Diameter identity
  - Optional message routing
  - Backend server support

## Building

```bash
# Build DRA simulator
make build-dra

# The binary will be created at: bin/dra-simulator
```

## Usage

### Basic Usage

Start a DRA simulator with default settings:

```bash
./bin/dra-simulator
```

This starts a DRA on `0.0.0.0:3868` with default configuration.

### Custom Configuration

```bash
./bin/dra-simulator \
  -host 127.0.0.1 \
  -port 3868 \
  -origin-host dra.example.com \
  -origin-realm example.com \
  -verbose
```

### Command Line Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `-host` | string | `0.0.0.0` | DRA listening host |
| `-port` | int | `3868` | DRA listening port |
| `-origin-host` | string | `dra.example.com` | Diameter Origin-Host |
| `-origin-realm` | string | `example.com` | Diameter Origin-Realm |
| `-product-name` | string | `DRA-Simulator/1.0` | Product name advertised in CEA |
| `-vendor-id` | uint | `10415` | Vendor ID (3GPP=10415) |
| `-max-connections` | int | `1000` | Maximum concurrent connections |
| `-read-timeout` | duration | `30s` | Connection read timeout |
| `-write-timeout` | duration | `30s` | Connection write timeout |
| `-dwr-interval` | duration | `30s` | Device Watchdog Request interval |
| `-metrics` | bool | `true` | Enable metrics collection |
| `-metrics-interval` | duration | `10s` | Metrics reporting interval |
| `-routing` | bool | `false` | Enable message routing |
| `-backends` | string | `""` | Backend servers (comma-separated host:port) |
| `-verbose` | bool | `false` | Enable verbose logging |

## Examples

### Example 1: Basic DRA for Testing

```bash
./bin/dra-simulator -verbose
```

Output:
```
╔════════════════════════════════════════════════════════════╗
║         Diameter Routing Agent (DRA) Simulator            ║
╚════════════════════════════════════════════════════════════╝

Configuration:
  Listen Address:      0.0.0.0:3868
  Origin-Host:         dra.example.com
  Origin-Realm:        example.com
  Product-Name:        DRA-Simulator/1.0
  Vendor-ID:           10415
  Max Connections:     1000
  Read Timeout:        30s
  Write Timeout:       30s
  DWR Interval:        30s
  Metrics Enabled:     true
  Metrics Interval:    10s
  Routing Enabled:     false

DRA listening on 0.0.0.0:3868
```

### Example 2: DRA with Routing

```bash
./bin/dra-simulator \
  -routing \
  -backends "backend1.example.com:3868,backend2.example.com:3868" \
  -verbose
```

This starts a DRA that can route messages to backend servers.

### Example 3: Custom Diameter Identity

```bash
./bin/dra-simulator \
  -origin-host my-dra.company.com \
  -origin-realm company.com \
  -port 3868
```

## Testing with Diameter Client

You can test the DRA with the included client examples:

### Terminal 1: Start DRA
```bash
./bin/dra-simulator -verbose
```

### Terminal 2: Run S13 Client
```bash
./bin/s13-client -dra-host 127.0.0.1 -dra-port 3868
```

Or use the connection pool example:
```bash
./bin/simple-pool
```

## Metrics

When metrics are enabled, the DRA reports statistics at regular intervals:

```
=== DRA Metrics ===
Connections:
  total: 10
  active: 5
Messages:
  total: 1250
  received: 625
  sent: 625
  routed: 0
Base Protocol:
  cer_received: 5
  cea_sent: 5
  dwr_received: 50
  dwa_sent: 50
  dwr_sent: 50
  dwa_received: 50
Data:
  bytes_received: 125000
  bytes_sent: 125000
Sessions:
  active: 15
Errors: 0
===================
```

## Architecture

### Components

1. **Main Server (`dra.go`)**
   - TCP listener
   - Connection manager
   - Message dispatcher

2. **Client Connection (`dra.go`)**
   - Per-connection state
   - Read/write loops
   - Protocol handlers

3. **Session Manager (`session.go`)**
   - Session tracking
   - Activity monitoring
   - Cleanup routines

4. **Router (`router.go`)**
   - Backend selection
   - Round-robin routing
   - Health checking

5. **Metrics (`metrics.go`)**
   - Statistics collection
   - Periodic reporting

### Message Flow

```
Client --> [TCP] --> DRA --> CER/CEA Handshake --> Active Connection
                      |
                      +--> DWR/DWA Keepalive
                      |
                      +--> Application Messages --> [Router] --> Backend
```

## Protocol Support

### Implemented

- ✅ CER/CEA (Capabilities Exchange)
- ✅ DWR/DWA (Device Watchdog) - bidirectional
- ✅ DPR/DPA (Disconnect Peer)
- ✅ Session management
- ✅ Connection state machine
- ✅ Metrics and statistics

### To Be Implemented

- ⏳ Full message routing
- ⏳ Load balancing strategies
- ⏳ Realm-based routing
- ⏳ Error handling and retries
- ⏳ TLS/SCTP support

## Performance

The DRA simulator is designed for high performance:

- Non-blocking I/O
- Goroutine-per-connection model
- Lock-free statistics (atomic operations)
- Efficient message parsing
- Configurable buffer sizes

Expected performance:
- **Connections**: 1000+ concurrent connections
- **Throughput**: 10,000+ messages/second
- **Latency**: < 1ms message processing

## Troubleshooting

### Issue: Connection refused
**Solution**: Ensure the DRA is running and the port is not blocked by firewall.

### Issue: CER timeout
**Solution**: Check that Origin-Host and Origin-Realm match between client and expectations.

### Issue: High memory usage
**Solution**: Reduce `-max-connections` or enable session cleanup.

### Issue: Messages not being routed
**Solution**: Enable routing with `-routing` and specify `-backends`.

## Development

### Adding New Features

1. Edit the appropriate file in `simulator/dra/`
2. Rebuild: `make build-dra`
3. Test with client examples

### Debugging

Enable verbose logging:
```bash
./bin/dra-simulator -verbose
```

This will show detailed information about:
- Connection events
- Message reception/transmission
- Protocol state changes
- Errors and warnings

## License

Part of the diam-gw project.
