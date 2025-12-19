# Simple DRA Simulator

A lightweight Diameter Routing Agent (DRA) simulator built using the server package. This simulator implements basic Diameter protocol behavior for testing and development purposes.

## Features

- **TCP Listener**: Accepts incoming Diameter connections on a configurable address
- **Single Connection Support**: Handles one active connection at a time (configurable via max-connections in code)
- **Base Protocol Messages**:
  - CER/CEA (Capabilities Exchange Request/Answer)
  - DWR/DWA (Device Watchdog Request/Answer)
  - DPR/DPA (Disconnect Peer Request/Answer)
- **Connection Lifecycle**:
  - Accepts connection
  - Receives CER and responds with CEA
  - Marks connection as established
  - Handles periodic DWR and responds with DWA
- **Configurable Identity**: Custom Origin-Host, Origin-Realm, Product-Name, Vendor-ID
- **Statistics Reporting**: Periodic statistics with message counts, bytes transferred, and error counts
- **Graceful Shutdown**: Clean connection closure and resource cleanup
- **Comprehensive Logging**: Structured logging with configurable log levels

## Usage

### Build

```bash
cd simulator/simple-dra
go build -o dra-simulator
```

### Run with Default Settings

```bash
./dra-simulator
```

This will start the DRA listening on `0.0.0.0:3868` with default identity settings.

### Command-Line Options

```bash
./dra-simulator [OPTIONS]

Options:
  -listen string
        Listen address (host:port) (default "0.0.0.0:3868")

  -origin-host string
        Origin-Host identity (default "dra-simulator.example.com")

  -origin-realm string
        Origin-Realm identity (default "example.com")

  -product-name string
        Product name (default "DRA-Simulator")

  -vendor-id uint
        Vendor ID (10415 for 3GPP) (default 10415)

  -dwr-interval duration
        Device Watchdog Request interval (default 30s)

  -log-level string
        Log level (debug, info, warn, error) (default "info")

  -stats-interval duration
        Statistics reporting interval (0 to disable) (default 60s)
```

### Examples

#### Run on custom port with debug logging

```bash
./dra-simulator -listen "0.0.0.0:13868" -log-level debug
```

#### Run with custom identity

```bash
./dra-simulator \
  -origin-host "my-dra.telco.com" \
  -origin-realm "telco.com" \
  -product-name "My-DRA" \
  -vendor-id 12345
```

#### Run with custom watchdog interval and statistics

```bash
./dra-simulator \
  -dwr-interval 15s \
  -stats-interval 30s \
  -log-level info
```

#### Disable statistics reporting

```bash
./dra-simulator -stats-interval 0
```

## Architecture

The DRA simulator is built on top of the robust `server` package and provides:

1. **Server Package Integration**: Uses the production-ready server implementation
2. **Handler-Based Architecture**: Registers handlers for specific Diameter commands
3. **Automatic Message Processing**: The server handles message reading/writing and protocol parsing
4. **Statistics Tracking**: Comprehensive metrics at server, interface, and command levels

### Message Flow

```
Client Connection → TCP Accept
                 ↓
             CER Received → handleCER() → CEA Sent
                 ↓
          Connection Established
                 ↓
         DWR Received (periodic) → handleDWR() → DWA Sent
                 ↓
         DPR Received (optional) → handleDPR() → DPA Sent → Connection Closed
```

## Configuration

### Default Configuration

- **Listen Address**: `0.0.0.0:3868`
- **Origin-Host**: `dra-simulator.example.com`
- **Origin-Realm**: `example.com`
- **Product-Name**: `DRA-Simulator`
- **Vendor-ID**: `10415` (3GPP)
- **DWR Interval**: `30s`
- **Read Timeout**: `60s`
- **Write Timeout**: `30s`
- **Max Connections**: `100`
- **Log Level**: `info`

### Server Configuration

The simulator uses the following server configuration:

```go
ServerConfig{
    ListenAddress:  "0.0.0.0:3868",
    MaxConnections: 100,
    ConnectionConfig: {
        ReadTimeout:      60 * time.Second,
        WriteTimeout:     30 * time.Second,
        WatchdogInterval: 30 * time.Second,
        WatchdogTimeout:  10 * time.Second,
        MaxMessageSize:   65535,
        SendChannelSize:  100,
        RecvChannelSize:  100,
        OriginHost:       "dra-simulator.example.com",
        OriginRealm:      "example.com",
        ProductName:      "DRA-Simulator",
        VendorID:         10415,
        HandleWatchdog:   false,
    },
    RecvChannelSize: 1000,
}
```

## Testing

### Test with Diameter Client

You can test the DRA simulator with any Diameter client. Example using the application simulator:

```bash
# Terminal 1: Start DRA simulator
./dra-simulator -log-level debug

# Terminal 2: Run application simulator in client mode
cd ../app
./app-simulator -mode client -remote-addr "localhost:3868"
```

### Expected Behavior

1. **Connection Establishment**:
   - Client connects to DRA
   - Client sends CER
   - DRA responds with CEA containing success result code (2001)
   - Connection marked as established

2. **Watchdog Exchange**:
   - Client periodically sends DWR (every 30s by default)
   - DRA responds with DWA containing success result code (2001)

3. **Graceful Shutdown**:
   - Client sends DPR (optional)
   - DRA responds with DPA
   - Connection closes cleanly

## Statistics Output

The DRA periodically reports statistics (every 60s by default):

```
=== DRA Statistics ===
Connections: total=5 active=2
Messages: total=250 received=125 sent=125
Data: bytes_received=12500 bytes_sent=12500
Errors: count=0

Interface Stats: interface=Base Protocol app_id=0 messages_received=125 messages_sent=125
  Command Stats: command=CER/CEA code=257 received=2 sent=2
  Command Stats: command=DWR/DWA code=280 received=123 sent=123
=====================
```

## Logging

The simulator uses structured logging with the following fields:

- **Timestamp**: ISO 8601 format
- **Level**: DEBUG, INFO, WARN, ERROR
- **Message**: Log message
- **Fields**: Additional context (remote address, message details, etc.)

### Log Levels

- **DEBUG**: Detailed protocol messages (DWR/DWA, message parsing)
- **INFO**: Connection events, CER/CEA, statistics, lifecycle events
- **WARN**: Abnormal conditions, connection limits
- **ERROR**: Errors in message processing, network errors

## Shutdown

The simulator handles graceful shutdown on receiving `SIGINT` (Ctrl+C) or `SIGTERM`:

1. Stops accepting new connections
2. Closes active connections
3. Waits for message processing to complete
4. Prints final statistics
5. Exits cleanly

## Implementation Details

### Handlers

#### CER Handler
- Unmarshals CER message
- Extracts Origin-Host, Origin-Realm, Product-Name
- Creates CEA with success result code (2001)
- Copies supported Application IDs from CER
- Sets Hop-by-Hop-ID and End-to-End-ID for correlation
- Sends CEA response

#### DWR Handler
- Unmarshals DWR message
- Creates DWA with success result code (2001)
- Sets Hop-by-Hop-ID and End-to-End-ID for correlation
- Sends DWA response

#### DPR Handler
- Unmarshals DPR message
- Logs disconnect cause
- Creates DPA with success result code (2001)
- Sends DPA response
- Closes connection after 100ms delay

### Result Codes

- **2001**: DIAMETER_SUCCESS - Request was successfully processed

### Diameter Command Codes

- **257**: CER/CEA - Capabilities Exchange
- **280**: DWR/DWA - Device Watchdog
- **282**: DPR/DPA - Disconnect Peer

## Extending the Simulator

To add support for additional Diameter messages:

1. Register a handler in `registerHandlers()`:
```go
d.server.HandleFunc(server.Command{Interface: appID, Code: cmdCode}, d.handleYourMessage)
```

2. Implement the handler:
```go
func (d *DRASimulator) handleYourMessage(msg *server.Message, conn server.Conn) {
    // Unmarshal request
    // Process message
    // Create response
    // Send response
}
```

## Troubleshooting

### Connection Refused
- Check if port is already in use: `netstat -an | grep 3868`
- Verify firewall settings allow incoming connections
- Try different port: `-listen "0.0.0.0:13868"`

### No Messages Received
- Check client is connecting to correct address
- Verify log level shows message reception: `-log-level debug`
- Check network connectivity: `telnet localhost 3868`

### High Error Count
- Check logs for specific error messages
- Verify message format is valid Diameter
- Ensure client and DRA use compatible protocol versions

## License

This simulator is part of the diam-gw project.
