#!/bin/bash
# Setup script for Diameter Gateway with Consul configuration

set -e

echo "=== Diameter Gateway Consul Configuration Setup ==="
echo

# Check if consul is installed
if ! command -v consul &> /dev/null; then
    echo "Consul not found. Installing..."
    echo "On macOS: brew install consul"
    echo "On Linux: see https://www.consul.io/downloads"
    exit 1
fi

# Start Consul in dev mode (for testing)
echo "Starting Consul in development mode..."
consul agent -dev -ui &
CONSUL_PID=$!
echo "Consul started with PID: $CONSUL_PID"
sleep 2

# Store Diameter Gateway configuration in Consul
echo
echo "Storing Diameter Gateway production configuration in Consul..."

consul kv put config/diam-gw/production - <<'EOF'
{
  "gateway": {
    "origin_host": "gateway.mobile.example.com",
    "origin_realm": "mobile.example.com",
    "vendor_id": 10415,
    "product_name": "DiameterGateway",
    "firmware_version": 1
  },
  "server": {
    "listen_addr": "0.0.0.0:3868",
    "max_connections": 2000,
    "handshake_timeout": "10s",
    "read_timeout": "30s",
    "write_timeout": "30s",
    "idle_timeout": "300s",
    "watchdog_interval": "30s",
    "watchdog_timeout": "10s",
    "watchdog_retries": 3,
    "read_buffer_size": 8192,
    "write_buffer_size": 8192
  },
  "client": {
    "connect_timeout": "5s",
    "request_timeout": "10s",
    "retry_attempts": 3,
    "retry_delay": "100ms",
    "max_idle_conns": 10,
    "max_conns_per_host": 100,
    "idle_conn_timeout": "90s",
    "watchdog_interval": "30s",
    "watchdog_timeout": "10s"
  },
  "dras": [
    {
      "name": "dra-primary",
      "host": "dra1.example.com",
      "port": 3868,
      "priority": 100,
      "weight": 100,
      "enabled": true,
      "destination_host": "dra1.example.com",
      "destination_realm": "example.com",
      "health_check_interval": "10s",
      "health_check_timeout": "5s",
      "max_failures": 3,
      "failure_window": "1m",
      "recovery_interval": "30s"
    },
    {
      "name": "dra-secondary",
      "host": "dra2.example.com",
      "port": 3868,
      "priority": 200,
      "weight": 100,
      "enabled": true,
      "destination_host": "dra2.example.com",
      "destination_realm": "example.com",
      "health_check_interval": "10s",
      "health_check_timeout": "5s",
      "max_failures": 3,
      "failure_window": "1m",
      "recovery_interval": "30s"
    }
  ],
  "logging": {
    "level": "info",
    "format": "json",
    "log_messages": false,
    "log_message_max_len": 1024
  },
  "metrics": {
    "enabled": true,
    "port": 9091,
    "path": "/metrics"
  }
}
EOF

echo "✓ Configuration stored in Consul"

# Verify the configuration
echo
echo "Verifying configuration..."
consul kv get config/diam-gw/production | jq .

echo
echo "=== Setup Complete ==="
echo
echo "Configuration is now stored in Consul at: config/diam-gw/production"
echo "Consul UI available at: http://localhost:8500/ui"
echo
echo "Then run your Diameter Gateway with:"
echo "  go run examples/config/main.go"
echo
echo "To update DRA configuration (will trigger hot reload):"
echo
echo "  # Add a new DRA"
echo "  consul kv put config/diam-gw/production @updated-config.json"
echo
echo "  # Change DRA priority"
echo "  # Edit the JSON and update the priority field"
echo
echo "  # Disable a DRA"
echo "  # Set 'enabled: false' for the DRA"
echo
echo "Example: Update to add third DRA:"
cat <<'EXAMPLE'
{
  "dras": [
    {...existing DRAs...},
    {
      "name": "dra-tertiary",
      "host": "dra3.example.com",
      "port": 3868,
      "priority": 300,
      "weight": 50,
      "enabled": true
    }
  ]
}
EXAMPLE

echo
echo "To stop Consul:"
echo "  kill $CONSUL_PID"
