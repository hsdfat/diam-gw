# Diameter Gateway - Quick Start Guide

This guide will help you get the Diameter Gateway up and running quickly.

## Prerequisites

- Go 1.21 or later
- Access to DRA servers
- Network connectivity on port 3868 (default Diameter port)

## Quick Setup

### 1. Build the Gateway

```bash
cd cmd/gateway
go build -o diameter-gateway
```

### 2. Basic Configuration

The gateway requires at minimum:
- **Listen address** for Logic App connections
- **DRA endpoints** with priority levels
- **Gateway identity** (Origin-Host, Origin-Realm)

### 3. Run with Default Settings

```bash
./diameter-gateway \
  -listen 0.0.0.0:3868 \
  -dra1-host 10.0.1.100 \
  -dra1-port 3868 \
  -dra2-host 10.0.2.100 \
  -dra2-port 3868
```

This starts the gateway with:
- Listening on `0.0.0.0:3868`
- Primary DRA at `10.0.1.100:3868` (Priority 1)
- Secondary DRA at `10.0.2.100:3868` (Priority 2)

## Configuration Examples

### Example 1: Development Setup (Local Testing)

```bash
./diameter-gateway \
  -listen 127.0.0.1:3868 \
  -dra1-host 127.0.0.1 \
  -dra1-port 3869 \
  -dra2-host 127.0.0.1 \
  -dra2-port 3870 \
  -log debug \
  -log-requests \
  -log-responses
```

### Example 2: Production Setup (Active-Standby)

```bash
./diameter-gateway \
  -listen 0.0.0.0:3868 \
  -origin-host diameter-gw-prod.example.com \
  -origin-realm example.com \
  -dra1-host 10.0.1.100 \
  -dra1-port 3868 \
  -dra2-host 10.0.2.100 \
  -dra2-port 3868 \
  -max-connections 5000 \
  -conns-per-dra 1 \
  -session-timeout 60s \
  -log info \
  -stats-interval 30s
```

### Example 3: High Availability Setup

```bash
./diameter-gateway \
  -listen 0.0.0.0:3868 \
  -origin-host diameter-gw-ha.example.com \
  -origin-realm example.com \
  -dra1-host 10.0.1.100 \
  -dra1-port 3868 \
  -dra2-host 10.0.1.101 \
  -dra2-port 3868 \
  -max-connections 10000 \
  -conns-per-dra 2 \
  -log warn
```

## Verify Gateway is Running

### Check Logs

You should see output like:

```
INFO  Starting Diameter Gateway  origin_host=diameter-gw.example.com
INFO  DRA pool started  active_priority=1
INFO  Gateway started successfully  server_address=0.0.0.0:3868
```

### Test with a Client

Use the provided test client or a Diameter testing tool:

```bash
# Connect to gateway
telnet localhost 3868

# Or use the DRA simulator
cd simulator/simple-dra
go run main.go -mode client -target localhost:3868
```

### Check Statistics

Statistics are logged periodically:

```
=== Gateway Statistics ===
Gateway:
  total_requests: 1000
  total_responses: 998
  active_sessions: 2
  avg_latency_ms: 12.34

DRA Pool:
  active_priority: 1
  active_dras: 2
  failover_count: 0
```

## Common Issues

### Gateway won't start

**Problem**: `Failed to listen on 0.0.0.0:3868: address already in use`

**Solution**: Port 3868 is already in use. Either:
- Stop the other process using port 3868
- Use a different port: `-listen 0.0.0.0:3869`

### Cannot connect to DRA

**Problem**: `Failed to start DRA pool: connection refused`

**Solution**:
- Verify DRA is running: `telnet <dra-host> <dra-port>`
- Check firewall rules
- Verify correct host and port configuration

### Session timeouts

**Problem**: `Cleaned up expired sessions count=50`

**Solution**:
- Increase session timeout: `-session-timeout 60s`
- Check DRA response time
- Verify network connectivity

### High latency

**Problem**: `avg_latency_ms: 500.00`

**Solution**:
- Check network latency to DRA: `ping <dra-host>`
- Increase connections per DRA: `-conns-per-dra 2`
- Check DRA performance

## Testing the Gateway

### 1. Test DRA Connectivity

```bash
# Primary DRA should be active
tail -f /var/log/diameter-gateway.log | grep "DRA pool started"
```

### 2. Test Failover

```bash
# Stop primary DRA, gateway should failover to secondary
# Watch logs for: "Failing over to lower priority"
```

### 3. Test Failback

```bash
# Restart primary DRA, gateway should failback
# Watch logs for: "Failing back to higher priority"
```

### 4. Load Testing

Use the provided load generator:

```bash
cd simulator/simple-dra
go run main.go -mode load -target localhost:3868 -rate 100
```

## Production Deployment

### Systemd Service

Create `/etc/systemd/system/diameter-gateway.service`:

```ini
[Unit]
Description=Diameter Gateway
After=network.target

[Service]
Type=simple
User=diameter
WorkingDirectory=/opt/diameter-gateway
ExecStart=/opt/diameter-gateway/diameter-gateway \
  -listen 0.0.0.0:3868 \
  -origin-host diameter-gw.example.com \
  -origin-realm example.com \
  -dra1-host 10.0.1.100 \
  -dra1-port 3868 \
  -dra2-host 10.0.2.100 \
  -dra2-port 3868 \
  -log info
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl enable diameter-gateway
sudo systemctl start diameter-gateway
sudo systemctl status diameter-gateway
```

### Docker Deployment

Create `Dockerfile`:

```dockerfile
FROM golang:1.21 AS builder
WORKDIR /build
COPY . .
RUN cd cmd/gateway && go build -o diameter-gateway

FROM debian:bookworm-slim
COPY --from=builder /build/cmd/gateway/diameter-gateway /usr/local/bin/
EXPOSE 3868
ENTRYPOINT ["diameter-gateway"]
```

Build and run:

```bash
docker build -t diameter-gateway .

docker run -d \
  --name diameter-gateway \
  -p 3868:3868 \
  diameter-gateway \
  -listen 0.0.0.0:3868 \
  -dra1-host 10.0.1.100 \
  -dra1-port 3868 \
  -dra2-host 10.0.2.100 \
  -dra2-port 3868
```

### Kubernetes Deployment

Create `deployment.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: diameter-gateway
spec:
  replicas: 2
  selector:
    matchLabels:
      app: diameter-gateway
  template:
    metadata:
      labels:
        app: diameter-gateway
    spec:
      containers:
      - name: diameter-gateway
        image: diameter-gateway:latest
        args:
        - "-listen=0.0.0.0:3868"
        - "-origin-host=diameter-gw.example.com"
        - "-dra1-host=dra-primary.example.com"
        - "-dra1-port=3868"
        - "-dra2-host=dra-secondary.example.com"
        - "-dra2-port=3868"
        - "-log=info"
        ports:
        - containerPort: 3868
          protocol: TCP
---
apiVersion: v1
kind: Service
metadata:
  name: diameter-gateway
spec:
  type: LoadBalancer
  ports:
  - port: 3868
    targetPort: 3868
    protocol: TCP
  selector:
    app: diameter-gateway
```

Deploy:

```bash
kubectl apply -f deployment.yaml
```

## Monitoring

### Log Files

Monitor gateway logs:

```bash
# If running with systemd
journalctl -u diameter-gateway -f

# If running in Docker
docker logs -f diameter-gateway

# If running directly
tail -f /var/log/diameter-gateway.log
```

### Key Metrics to Monitor

- `total_requests` / `total_responses` - Should be roughly equal
- `active_sessions` - Should be low under normal load
- `avg_latency_ms` - Should be < 100ms typically
- `timeout_errors` - Should be near zero
- `routing_errors` - Should be near zero
- `failover_count` - Track DRA failures
- `active_dras` - Should match expected count

### Alerts to Set Up

1. **High error rate**: `total_errors / total_requests > 0.01`
2. **High latency**: `avg_latency_ms > 200`
3. **No active DRAs**: `active_dras == 0`
4. **Frequent failovers**: `failover_count > 10 per hour`
5. **High session count**: `active_sessions > 1000`

## Next Steps

- Read the full [README.md](README.md) for detailed documentation
- Review [config.example.yaml](config.example.yaml) for advanced configuration
- Check [gateway_test.go](gateway_test.go) for usage examples
- Set up monitoring and alerting
- Configure log aggregation
- Plan for scaling

## Support

For issues and questions:
- Check logs for detailed error messages
- Review configuration against examples
- Test connectivity to DRA servers
- Verify Diameter protocol compatibility

## Performance Tips

1. **Tune buffer sizes**: Increase for high throughput
   ```bash
   -send-buffer-size 1000 -recv-buffer-size 1000
   ```

2. **Adjust connection limits**: Match expected load
   ```bash
   -max-connections 10000
   ```

3. **Optimize session timeout**: Balance memory vs reliability
   ```bash
   -session-timeout 15s  # Lower = less memory, higher = more reliable
   ```

4. **Enable connection pooling**: For very high load
   ```bash
   -conns-per-dra 3
   ```

5. **Disable verbose logging**: In production
   ```bash
   -log warn  # Only log warnings and errors
   ```
