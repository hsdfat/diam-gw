# Diameter Gateway - Deployment Checklist

Use this checklist to ensure a successful production deployment of the Diameter Gateway.

## Pre-Deployment

### Environment Preparation

- [ ] Go 1.21+ installed
- [ ] Network connectivity verified between gateway and DRA servers
- [ ] Firewall rules configured (port 3868 or custom)
- [ ] DNS resolution working (if using hostnames for DRAs)
- [ ] System resources allocated (CPU, memory, disk)
- [ ] Monitoring infrastructure ready (log aggregation, metrics)

### Configuration

- [ ] Gateway identity configured
  - [ ] Origin-Host set (FQDN recommended)
  - [ ] Origin-Realm set
  - [ ] Product-Name set
  - [ ] Vendor-ID set (default: 10415)

- [ ] Server settings configured
  - [ ] Listen address set
  - [ ] Max connections set (based on capacity planning)
  - [ ] Read/write timeouts configured
  - [ ] Watchdog intervals configured

- [ ] DRA pool configured
  - [ ] Primary DRA(s) configured (Priority 1)
  - [ ] Secondary DRA(s) configured (Priority 2+)
  - [ ] DRA hostnames/IPs verified
  - [ ] DRA ports verified
  - [ ] Connection counts per DRA set
  - [ ] Health check intervals configured
  - [ ] Reconnection strategy configured

- [ ] Session management configured
  - [ ] Session timeout set (recommend: 30-60s)
  - [ ] Buffer sizes configured

- [ ] Logging configured
  - [ ] Log level set (info for production)
  - [ ] Request/response logging decision made
  - [ ] Log rotation configured (if file-based)

### Testing in Staging

- [ ] Build successful
  ```bash
  cd cmd/gateway && go build -o diameter-gateway
  ```

- [ ] Unit tests passing
  ```bash
  cd gateway && go test -v
  ```

- [ ] Configuration file validated
  ```bash
  ./diameter-gateway -config config.yaml -validate
  ```

- [ ] DRA connectivity tested
  ```bash
  telnet <dra-host> <dra-port>
  ```

- [ ] Gateway starts successfully
  ```bash
  ./diameter-gateway -config config.yaml
  ```

- [ ] CER/CEA exchange successful (check logs)
- [ ] DWR/DWA exchange successful (check logs)
- [ ] Test request/response flow working
- [ ] Failover tested (stop primary DRA, verify failover)
- [ ] Failback tested (restart primary DRA, verify failback)
- [ ] Load testing completed
- [ ] Performance benchmarks acceptable
- [ ] Memory usage within limits
- [ ] CPU usage within limits
- [ ] No memory leaks detected

## Deployment

### Initial Deployment

- [ ] Binary deployed to production server
  ```bash
  scp diameter-gateway user@prod-server:/opt/diameter-gateway/
  ```

- [ ] Configuration deployed
  ```bash
  scp config.yaml user@prod-server:/opt/diameter-gateway/
  ```

- [ ] Service user created (recommend: non-root)
  ```bash
  sudo useradd -r -s /bin/false diameter
  ```

- [ ] Directories created with proper permissions
  ```bash
  sudo mkdir -p /opt/diameter-gateway
  sudo mkdir -p /var/log/diameter-gateway
  sudo chown -R diameter:diameter /opt/diameter-gateway
  sudo chown -R diameter:diameter /var/log/diameter-gateway
  ```

- [ ] Systemd service file deployed
  ```bash
  sudo cp diameter-gateway.service /etc/systemd/system/
  sudo systemctl daemon-reload
  ```

- [ ] Service enabled
  ```bash
  sudo systemctl enable diameter-gateway
  ```

### Startup Verification

- [ ] Service started
  ```bash
  sudo systemctl start diameter-gateway
  ```

- [ ] Service status checked
  ```bash
  sudo systemctl status diameter-gateway
  ```

- [ ] Logs reviewed for errors
  ```bash
  journalctl -u diameter-gateway -f
  ```

- [ ] DRA connections established (check logs)
  ```
  INFO  DRA pool started  active_priority=1
  INFO  Gateway started successfully
  ```

- [ ] Priority level correct (Priority 1 active)
  ```
  INFO  active_priority=1
  ```

- [ ] All configured DRAs healthy
  ```
  INFO  DRA Status  name=DRA-1  status=HEALTHY
  INFO  DRA Status  name=DRA-2  status=HEALTHY
  ```

## Post-Deployment Verification

### Functional Testing

- [ ] Logic App can connect to gateway
- [ ] CER/CEA exchange successful from Logic App
- [ ] Test request forwarded to DRA
- [ ] Response received back at Logic App
- [ ] H2H ID preserved correctly
- [ ] E2E ID preserved correctly
- [ ] Multiple concurrent requests handled
- [ ] Session tracking working (check statistics)

### Performance Verification

- [ ] Response latency acceptable
  ```
  avg_latency_ms < 100ms (typical)
  ```

- [ ] Throughput meets requirements
  ```
  requests/second matches expected load
  ```

- [ ] CPU usage normal
  ```
  < 30% under normal load
  ```

- [ ] Memory usage stable
  ```
  No continuous growth = no memory leak
  ```

- [ ] Active sessions count reasonable
  ```
  active_sessions < 1000 typically
  ```

### High Availability Verification

- [ ] Primary DRA failure triggers failover
  ```bash
  # Stop primary DRA
  # Verify: "Failing over to lower priority"
  ```

- [ ] Secondary DRA handles traffic
  ```
  active_priority=2
  ```

- [ ] Primary DRA recovery triggers failback
  ```bash
  # Restart primary DRA
  # Verify: "Failing back to higher priority"
  ```

- [ ] No message loss during failover
- [ ] Latency acceptable during failover

### Error Handling Verification

- [ ] Session timeout handled correctly
- [ ] Invalid messages logged and dropped
- [ ] Connection loss triggers reconnection
- [ ] Reconnection backoff working
- [ ] Max reconnect delay respected

## Monitoring Setup

### Metrics Collection

- [ ] Statistics logging enabled
  ```
  -stats-interval 60s
  ```

- [ ] Key metrics monitored:
  - [ ] total_requests
  - [ ] total_responses
  - [ ] total_errors
  - [ ] active_sessions
  - [ ] avg_latency_ms
  - [ ] active_priority
  - [ ] active_dras
  - [ ] failover_count

### Alerting

- [ ] High error rate alert configured
  ```
  total_errors / total_requests > 0.01
  ```

- [ ] High latency alert configured
  ```
  avg_latency_ms > 200
  ```

- [ ] No DRAs available alert configured
  ```
  active_dras == 0
  ```

- [ ] Frequent failover alert configured
  ```
  failover_count > 10 per hour
  ```

- [ ] High session count alert configured
  ```
  active_sessions > 1000
  ```

- [ ] Service down alert configured
  ```
  systemctl status != active
  ```

### Log Management

- [ ] Log aggregation configured (e.g., ELK, Splunk)
- [ ] Log retention policy set
- [ ] Log rotation configured
- [ ] Log search/filter capabilities verified
- [ ] Error logs monitored

## Operations

### Daily Checks

- [ ] Service running
  ```bash
  systemctl status diameter-gateway
  ```

- [ ] DRA health status
  ```bash
  journalctl -u diameter-gateway | grep "DRA Status"
  ```

- [ ] Error count
  ```bash
  journalctl -u diameter-gateway | grep ERROR | wc -l
  ```

- [ ] Statistics review
  ```bash
  journalctl -u diameter-gateway | grep "Gateway Statistics"
  ```

### Weekly Checks

- [ ] Review failover events
- [ ] Analyze latency trends
- [ ] Check for memory/CPU trends
- [ ] Review log volumes
- [ ] Verify backup/DR procedures

### Monthly Checks

- [ ] Capacity planning review
- [ ] Performance benchmarking
- [ ] Security updates applied
- [ ] Configuration review
- [ ] Documentation updates

## Maintenance Procedures

### Graceful Restart

```bash
# Send SIGTERM for graceful shutdown
sudo systemctl stop diameter-gateway

# Verify clean shutdown
journalctl -u diameter-gateway | tail -20

# Start service
sudo systemctl start diameter-gateway

# Verify startup
systemctl status diameter-gateway
```

### Configuration Update

```bash
# Update configuration file
sudo vi /opt/diameter-gateway/config.yaml

# Validate configuration
./diameter-gateway -config config.yaml -validate

# Graceful restart
sudo systemctl restart diameter-gateway

# Verify
systemctl status diameter-gateway
```

### Add/Remove DRA

```yaml
# Edit configuration
dras:
  - name: "DRA-1"
    host: "10.0.1.100"
    port: 3868
    priority: 1
  - name: "DRA-3"  # New DRA
    host: "10.0.3.100"
    port: 3868
    priority: 1
```

```bash
# Apply changes
sudo systemctl restart diameter-gateway

# Verify new DRA connected
journalctl -u diameter-gateway | grep "DRA-3"
```

### Emergency Shutdown

```bash
# Immediate stop (not graceful)
sudo systemctl kill -s SIGKILL diameter-gateway

# Or
sudo pkill -9 diameter-gateway
```

## Disaster Recovery

### Backup Procedures

- [ ] Configuration files backed up
- [ ] Binary version documented
- [ ] Deployment scripts backed up
- [ ] Service files backed up

### Recovery Procedures

- [ ] Restore configuration from backup
- [ ] Deploy binary
- [ ] Start service
- [ ] Verify connectivity
- [ ] Test failover/failback

### Rollback Procedures

- [ ] Previous version binary available
- [ ] Previous configuration available
- [ ] Rollback tested in staging
- [ ] Rollback playbook documented

## Security Checklist

- [ ] Gateway runs as non-root user
- [ ] File permissions restricted
- [ ] Firewall rules applied
- [ ] TLS configured (if required)
- [ ] Access logs enabled
- [ ] Security updates applied
- [ ] Sensitive data not logged
- [ ] Configuration file secured (no world-readable)

## Compliance & Documentation

- [ ] Deployment documented
- [ ] Network diagram updated
- [ ] Configuration management system updated
- [ ] Change control process followed
- [ ] Stakeholders notified
- [ ] Runbook updated
- [ ] On-call procedures updated

## Sign-off

- [ ] Development team approval
- [ ] Operations team approval
- [ ] Security team approval
- [ ] Management approval

---

**Deployment Date**: _______________

**Deployed By**: _______________

**Approved By**: _______________

**Notes**:
