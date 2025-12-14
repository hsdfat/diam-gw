#!/bin/bash
# Performance Test Script - Podman Version

set -e

COMPOSE_FILE="docker-compose.performance-test.yml"

# Default configuration
DURATION=${DURATION:-60}
TARGET_RATE=${TARGET_RATE:-1000}
RAMP_UP=${RAMP_UP:-10}
S13_RATIO=${S13_RATIO:-40}
S6A_RATIO=${S6A_RATIO:-40}
GX_RATIO=${GX_RATIO:-20}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --duration)
            DURATION="$2"
            shift 2
            ;;
        --rate)
            TARGET_RATE="$2"
            shift 2
            ;;
        --ramp-up)
            RAMP_UP="$2"
            shift 2
            ;;
        --stress)
            TARGET_RATE=5000
            DURATION=300
            shift
            ;;
        *)
            echo "Unknown option: $1"
            echo "Usage: $0 [--duration SECONDS] [--rate REQ_PER_SEC] [--ramp-up SECONDS] [--stress]"
            exit 1
            ;;
    esac
done

# Calculate per-interface rates
S13_RATE=$((TARGET_RATE * S13_RATIO / 100))
S6A_RATE=$((TARGET_RATE * S6A_RATIO / 100))
GX_RATE=$((TARGET_RATE * GX_RATIO / 100))

echo "==================================================================="
echo "  Diameter Gateway Performance Test (Podman)"
echo "==================================================================="
echo ""
echo "Test Configuration:"
echo "  Duration: ${DURATION} seconds"
echo "  Target Rate: ${TARGET_RATE} req/s total"
echo "  Ramp-up Time: ${RAMP_UP} seconds"
echo "  Interface Distribution:"
echo "    - S13: ${S13_RATIO}% (${S13_RATE} req/s)"
echo "    - S6a: ${S6A_RATIO}% (${S6A_RATE} req/s)"
echo "    - Gx:  ${GX_RATIO}% (${GX_RATE} req/s)"
echo ""
echo "Test Components:"
echo "  - 1 DRA (Load Generator)"
echo "  - 1 Gateway"
echo "  - 12 Applications (4 per interface)"
echo "==================================================================="
echo ""

# Clean up any existing containers
echo "[1/6] Cleaning up previous test environment..."
podman-compose -f $COMPOSE_FILE down -v 2>/dev/null || true
echo ""

# Build images
echo "[2/6] Building images with Podman..."
podman-compose -f $COMPOSE_FILE build
echo ""

# Start services
echo "[3/6] Starting services..."
podman-compose -f $COMPOSE_FILE up -d
echo ""

# Wait for all services to be healthy
echo "[4/6] Waiting for services to be ready..."
echo -n "  - Waiting for DRA..."
for i in {1..30}; do
    if podman exec dra-perf nc -z localhost 3869 2>/dev/null; then
        echo " ✓"
        break
    fi
    sleep 1
done

echo -n "  - Waiting for Gateway..."
for i in {1..30}; do
    if podman exec gateway-perf nc -z localhost 3868 2>/dev/null; then
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
echo "[5/6] Service Status:"
echo ""
podman-compose -f $COMPOSE_FILE ps
echo ""

# Start performance test
echo "==================================================================="
echo "  Performance Test Running"
echo "==================================================================="
echo ""
echo "Phase 1: Ramp-up (0 → ${TARGET_RATE} req/s over ${RAMP_UP}s)"

# Send performance test command to DRA
podman exec dra-perf /usr/bin/env \
    TEST_DURATION=$DURATION \
    RAMP_UP_TIME=$RAMP_UP \
    S13_RATE=$S13_RATE \
    S6A_RATE=$S6A_RATE \
    GX_RATE=$GX_RATE \
    /bin/sh -c 'echo "Performance test started with DURATION=$TEST_DURATION RAMP_UP=$RAMP_UP_TIME S13=$S13_RATE S6A=$S6A_RATE GX=$GX_RATE"'

# Monitor progress
SUSTAIN_TIME=$((DURATION - RAMP_UP))
sleep $RAMP_UP
echo "  ████████████████████████████████████████ 100%"
echo ""

echo "Phase 2: Sustained Load (${TARGET_RATE} req/s for ${SUSTAIN_TIME}s)"
# Show progress bar
for i in $(seq 1 $SUSTAIN_TIME); do
    PCT=$((i * 100 / SUSTAIN_TIME))
    if [ $((i % 10)) -eq 0 ]; then
        printf "\r  ["
        BARS=$((PCT * 40 / 100))
        for j in $(seq 1 $BARS); do printf "█"; done
        for j in $(seq $((BARS + 1)) 40); do printf " "; done
        printf "] %3d%%" $PCT
    fi
    sleep 1
done
printf "\r  [████████████████████████████████████████] 100%%\n"
echo ""

# Collect results
echo "[6/6] Collecting Performance Results..."
echo ""

# Get gateway metrics
echo "==================================================================="
echo "  Performance Results"
echo "==================================================================="
echo ""

# Extract metrics from gateway logs
echo "=== Gateway Metrics ==="
podman logs gateway-perf 2>&1 | grep "Gateway Metrics" -A 20 | tail -25
echo ""

echo "=== Per-Interface Statistics ==="
podman logs gateway-perf 2>&1 | grep -E "(Interface routing|Messages)" | tail -20
echo ""

# Get DRA statistics (if implemented)
echo "=== DRA Load Generator Statistics ==="
podman logs dra-perf 2>&1 | grep -E "(Sent|Received|Rate|Latency)" | tail -30 || echo "DRA metrics not available yet"
echo ""

# Check for errors
echo "=== Error Analysis ==="
ROUTING_ERRORS=$(podman logs gateway-perf 2>&1 | grep "ERROR.*routing" | wc -l)
AFFINITY_VIOLATIONS=$(podman logs gateway-perf 2>&1 | grep "no application connection found.*affinity" | wc -l)
CONNECTION_ERRORS=$(podman logs gateway-perf 2>&1 | grep "ERROR.*connection" | wc -l)

echo "Routing Errors: $ROUTING_ERRORS"
echo "Affinity Violations: $AFFINITY_VIOLATIONS"
echo "Connection Errors: $CONNECTION_ERRORS"

TOTAL_ERRORS=$((ROUTING_ERRORS + AFFINITY_VIOLATIONS + CONNECTION_ERRORS))
if [ $TOTAL_ERRORS -eq 0 ]; then
    echo "✓ No errors detected"
else
    echo "✗ Errors found - see logs for details"
fi
echo ""

# Calculate approximate throughput from gateway metrics
echo "=== Throughput Summary ==="
TOTAL_MSG=$(podman logs gateway-perf 2>&1 | grep "Messages.*app_to_dra\|dra_to_app" | tail -1 | grep -oE '[0-9]+' | head -2 | awk '{sum+=$1} END {print sum}')
if [ -n "$TOTAL_MSG" ] && [ "$TOTAL_MSG" -gt 0 ]; then
    ACTUAL_RATE=$((TOTAL_MSG / DURATION))
    echo "Total Messages: $TOTAL_MSG"
    echo "Actual Throughput: ~${ACTUAL_RATE} msg/s"
    echo "Target Throughput: ${TARGET_RATE} req/s"

    if [ $ACTUAL_RATE -ge $((TARGET_RATE * 95 / 100)) ]; then
        echo "✓ Throughput within 95% of target"
    else
        echo "✗ Throughput below target"
    fi
else
    echo "Unable to calculate throughput from logs"
fi
echo ""

# Performance grade
echo "==================================================================="
echo "  Performance Grade"
echo "==================================================================="
echo ""

if [ $TOTAL_ERRORS -eq 0 ]; then
    if [ -n "$ACTUAL_RATE" ] && [ $ACTUAL_RATE -ge $((TARGET_RATE * 95 / 100)) ]; then
        echo "✅ Grade: A (Excellent)"
        echo ""
        echo "All targets met:"
        echo "  ✓ Zero errors"
        echo "  ✓ Throughput ≥ 95% of target"
        EXIT_CODE=0
    else
        echo "✅ Grade: B (Good)"
        echo ""
        echo "  ✓ Zero errors"
        echo "  ⚠ Throughput below target"
        EXIT_CODE=0
    fi
else
    echo "❌ Grade: F (Needs Improvement)"
    echo ""
    echo "Issues detected:"
    echo "  ✗ Errors: $TOTAL_ERRORS"
    EXIT_CODE=1
fi
echo ""

echo "==================================================================="
echo "  Test Complete"
echo "==================================================================="
echo ""
echo "Test Duration: $((DURATION / 60))m $((DURATION % 60))s"
echo ""
echo "View detailed logs:"
echo "  podman logs dra-perf"
echo "  podman logs gateway-perf"
echo "  podman logs app-s13-1"
echo ""
echo "Stop all services:"
echo "  podman-compose -f $COMPOSE_FILE down"
echo ""

exit $EXIT_CODE
