# Diameter Gateway Configuration

The Diameter Gateway uses a hierarchical configuration system with the following priority order (highest to lowest):

1. **Environment variables** (prefixed with `DIAMGW_`)
2. **Config file specified by path** (optional)
3. **config.yaml** in standard paths (`.`, `./config`, `/etc/diam-gw`)
4. **config.default.yaml** as fallback
5. **Hardcoded defaults**

## Configuration Files

- `config.default.yaml` - Default configuration with all available options and their default values
- `config.yaml` - Production/deployment configuration (overrides defaults)

## Configuration Sections

### Gateway
Core gateway identity and behavior settings:
- `originHost`: Diameter Origin-Host AVP
- `originRealm`: Diameter Origin-Realm AVP
- `productName`: Product name for CER
- `vendorID`: 3GPP Vendor ID (default: 10415)
- `sessionTimeout`: Session expiration timeout
- `enableReqLog`: Enable request message logging
- `enableRespLog`: Enable response message logging
- `statsInterval`: Statistics reporting interval

### Server
Inbound Diameter server configuration:
- `listenAddr`: Listen address (default: 0.0.0.0:3868)
- `maxConnections`: Maximum concurrent connections
- `readTimeout`: Connection read timeout
- `writeTimeout`: Connection write timeout
- `watchdogInterval`: DWR interval
- `watchdogTimeout`: DWR timeout
- `maxMessageSize`: Maximum Diameter message size
- `sendChannelSize`: Send buffer channel size
- `recvChannelSize`: Receive buffer channel size
- `handleWatchdog`: Enable watchdog handling

### DRA Pool
Outbound DRA (Diameter Routing Agent) pool configuration:
- `supported`: Enable DRA support
- `connectionsPerDRA`: Number of connections per DRA
- `connectTimeout`: Connection timeout
- `cerTimeout`: CER timeout
- `dwrInterval`: Device-Watchdog-Request interval
- `dwrTimeout`: DWR timeout
- `maxDWRFailures`: Max DWR failures before reconnect
- `healthCheckInterval`: Health check interval
- `reconnectInterval`: Reconnect interval
- `maxReconnectDelay`: Maximum reconnect delay
- `reconnectBackoff`: Reconnect backoff multiplier
- `sendBufferSize`: Send buffer size
- `recvBufferSize`: Receive buffer size
- `dras`: List of DRA servers
  - `name`: DRA server name
  - `host`: DRA hostname/IP
  - `port`: DRA port
  - `priority`: Priority (lower = higher priority)
  - `weight`: Load balancing weight

### Client
Outbound client pool configuration for forwarding:
- `dialTimeout`: Dial timeout
- `sendTimeout`: Send timeout
- `cerTimeout`: CER timeout
- `dwrInterval`: DWR interval
- `dwrTimeout`: DWR timeout
- `maxDWRFailures`: Max DWR failures
- `authAppIDs`: Supported Auth-Application-IDs (S6a: 16777251, S13: 16777252)
- `sendBufferSize`: Send buffer size
- `recvBufferSize`: Receive buffer size
- `reconnectEnabled`: Enable automatic reconnection
- `reconnectInterval`: Reconnect interval
- `maxReconnectDelay`: Maximum reconnect delay
- `reconnectBackoff`: Reconnect backoff multiplier
- `healthCheckInterval`: Health check interval

### Logging
Logging configuration:
- `level`: Log level (debug, info, warn, error)
- `format`: Log format (json, text)

### Metrics
Metrics exposition configuration:
- `enabled`: Enable metrics
- `port`: Metrics HTTP port
- `path`: Metrics HTTP path

### Governance
Service discovery and governance configuration:
- `enabled`: Enable governance registration
- `url`: Governance manager URL
- `serviceName`: Service name for registration
- `podName`: Pod name (optional, auto-detected)
- `subscriptions`: List of service groups to subscribe to
- `failOnError`: Panic if registration fails

## Environment Variables

All configuration values can be overridden using environment variables with the `DIAMGW_` prefix.

Examples:
```bash
DIAMGW_GATEWAY_ORIGINHOST=diam-gw.telco.com
DIAMGW_SERVER_LISTENADDR=0.0.0.0:3868
DIAMGW_LOGGING_LEVEL=debug
DIAMGW_DRAPOOL_SUPPORTED=true
```

For nested arrays like DRAs, use index-based notation:
```bash
DIAMGW_DRAPOOL_DRAS_0_NAME=DRA-1
DIAMGW_DRAPOOL_DRAS_0_HOST=dra1.example.com
DIAMGW_DRAPOOL_DRAS_0_PORT=3869
DIAMGW_DRAPOOL_DRAS_1_NAME=DRA-2
DIAMGW_DRAPOOL_DRAS_1_HOST=dra2.example.com
DIAMGW_DRAPOOL_DRAS_1_PORT=3870
```

## Usage

The gateway will automatically search for configuration files in the following order:
1. `config.yaml` in current directory
2. `config.yaml` in `./config` directory
3. `config.yaml` in `/etc/diam-gw` directory
4. `config.default.yaml` as fallback

No command-line flags are needed. Simply run:
```bash
./gateway
```

The configuration file being used will be printed on startup.
