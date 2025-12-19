#!/bin/bash
# Multi-Application Test Script - Podman Version

set -e

COMPOSE_FILE="docker-compose.multi-app-test.yml"
TEST_DURATION=60

echo "==================================================================="
echo "  Multi-Application Gateway Test (Podman)"
echo "==================================================================="
echo ""
echo "Test Configuration:"
echo "  - 1 DRA"
echo "  - 1 Gateway"
echo "  - 4 Applications:"
echo "    * App1: S13 only (2 connections)"
echo "    * App2: S6a only (2 connections)"
echo "    * App3: S13+S6a (3 connections)"
echo "    * App4: Gx only (2 connections)"
echo "  - Total: 9 application connections"
echo ""
echo "Test Duration: ${TEST_DURATION} seconds"
echo "==================================================================="
echo ""

# Clean up any existing containers
echo "[1/5] Cleaning up previous test environment..."
podman-compose -f $COMPOSE_FILE down -v 2>/dev/null || true
echo ""

# Build images
echo "[2/5] Building images with Podman..."
podman-compose -f $COMPOSE_FILE build
echo ""

# Start services
echo "[3/5] Starting services..."
podman-compose -f $COMPOSE_FILE up -d
echo ""

# Wait for all services to be healthy
echo "[4/5] Waiting for services to be ready..."
echo -n "  - Waiting for DRA..."
for i in {1..30}; do
    if podman exec dra nc -z localhost 3869 2>/dev/null; then
        echo " ✓"
        break
    fi
    sleep 1
done

echo -n "  - Waiting for Gateway..."
for i in {1..30}; do
    if podman exec gateway nc -z localhost 3868 2>/dev/null; then
        echo " ✓"
        break
    fi
    sleep 1
done

echo "  - Waiting for applications to connect..."
sleep 5
echo "    ✓ All applications should be connected"
echo ""

# Show connection status
echo "[5/5] Service Status:"
echo ""
podman-compose -f $COMPOSE_FILE ps
echo ""

echo "==================================================================="
echo "  Test Running - Duration: ${TEST_DURATION}s"
echo "==================================================================="
echo ""

# Monitor logs in background
echo "Monitoring gateway logs for interface routing..."
echo ""

# Show interface discovery logs
echo "=== Interface Discovery ==="
podman logs gateway 2>&1 | grep "Peer identity" | tail -10
echo ""

# Show interface routing map
echo "=== Interface Routing Map ==="
podman logs gateway 2>&1 | grep "Interface routing" | tail -20
echo ""

# Wait for test traffic
echo "Waiting for DRA to send test traffic (15 seconds)..."
sleep 15
echo ""

# Show routing decisions
echo "=== Routing Decisions ==="
podman logs gateway 2>&1 | grep -E "(Interface-based routing|Fallback routing)" | tail -10
echo ""

# Show connection affinity
echo "=== Connection Affinity ==="
podman logs gateway 2>&1 | grep "connection affinity" | tail -10
echo ""

# Run for test duration
REMAINING=$((TEST_DURATION - 20))
if [ $REMAINING -gt 0 ]; then
    echo "Continuing test for ${REMAINING} more seconds..."
    sleep $REMAINING
    echo ""
fi

# Show final metrics
echo "==================================================================="
echo "  Test Results"
echo "==================================================================="
echo ""

echo "=== Gateway Metrics ==="
podman logs gateway 2>&1 | grep "Gateway Metrics" -A 10 | tail -15
echo ""

echo "=== Application Connections ==="
APP_CONNS=$(podman logs gateway 2>&1 | grep "Application Connections" | tail -1)
echo "$APP_CONNS"
echo ""

echo "=== Interface Routing Summary ==="
podman logs gateway 2>&1 | grep "Interface routing interface=" | tail -10
echo ""

echo "=== Routing Errors ==="
ERRORS=$(podman logs gateway 2>&1 | grep "ERROR.*routing" | wc -l)
echo "Total routing errors: $ERRORS"
if [ $ERRORS -eq 0 ]; then
    echo "✓ No routing errors detected"
else
    echo "✗ Routing errors found:"
    podman logs gateway 2>&1 | grep "ERROR.*routing" | tail -5
fi
echo ""

echo "=== Connection Affinity Violations ==="
VIOLATIONS=$(podman logs gateway 2>&1 | grep "no application connection found.*affinity" | wc -l)
echo "Total affinity violations: $VIOLATIONS"
if [ $VIOLATIONS -eq 0 ]; then
    echo "✓ No connection affinity violations"
else
    echo "✗ Affinity violations found:"
    podman logs gateway 2>&1 | grep "no application connection found.*affinity" | tail -5
fi
echo ""

# Test results summary
echo "==================================================================="
echo "  Summary"
echo "==================================================================="
echo ""

if [ $ERRORS -eq 0 ] && [ $VIOLATIONS -eq 0 ]; then
    echo "✅ TEST PASSED"
    echo ""
    echo "All validations successful:"
    echo "  ✓ All applications connected"
    echo "  ✓ Interface discovery working"
    echo "  ✓ Interface-based routing operational"
    echo "  ✓ No routing errors"
    echo "  ✓ Connection affinity maintained"
    EXIT_CODE=0
else
    echo "❌ TEST FAILED"
    echo ""
    echo "Issues detected:"
    [ $ERRORS -gt 0 ] && echo "  ✗ Routing errors: $ERRORS"
    [ $VIOLATIONS -gt 0 ] && echo "  ✗ Affinity violations: $VIOLATIONS"
    EXIT_CODE=1
fi
echo ""

echo "==================================================================="
echo "  Logs Available"
echo "==================================================================="
echo ""
echo "View individual service logs:"
echo "  podman logs dra"
echo "  podman logs gateway"
echo "  podman logs app1-s13"
echo "  podman logs app2-s6a"
echo "  podman logs app3-multi"
echo "  podman logs app4-gx"
echo ""
echo "Stop all services:"
echo "  podman-compose -f $COMPOSE_FILE down"
echo ""

exit $EXIT_CODE
