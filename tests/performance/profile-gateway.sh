#!/bin/bash
# Gateway Profiling Script - Run performance test with CPU profiling enabled

set -e

COMPOSE_FILE="docker-compose.performance-test.yml"
PROFILE_DURATION=${DURATION:-60}
TARGET_RATE=${RATE:-5000}

echo "╔════════════════════════════════════════════════════════════════╗"
echo "║            Gateway Performance Profiling                       ║"
echo "╚════════════════════════════════════════════════════════════════╝"
echo ""
echo "This will run the gateway with profiling enabled"
echo "Duration: ${PROFILE_DURATION}s"
echo "Rate: ${TARGET_RATE} req/s"
echo ""

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --duration)
            PROFILE_DURATION="$2"
            shift 2
            ;;
        --rate)
            TARGET_RATE="$2"
            shift 2
            ;;
        --help)
            echo "Usage: $0 [options]"
            echo ""
            echo "Options:"
            echo "  --duration SECONDS    Profile duration (default: 60)"
            echo "  --rate REQ_PER_SEC    Target request rate (default: 5000)"
            echo "  --help                Show this help"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Create profile directory
PROFILE_DIR="profiles-$(date +%Y%m%d-%H%M%S)"
mkdir -p $PROFILE_DIR

echo "Profiles will be saved to: $PROFILE_DIR"
echo ""

# Calculate interface rates
S13_RATE=$((TARGET_RATE * 40 / 100))
S6A_RATE=$((TARGET_RATE * 40 / 100))
GX_RATE=$((TARGET_RATE * 20 / 100))

# Export environment
export DURATION=$PROFILE_DURATION
export RAMP_UP=10
export S13_RATE
export S6A_RATE
export GX_RATE

# Clean up
echo "[1/6] Cleaning up previous containers..."
podman-compose -f $COMPOSE_FILE down -v >/dev/null 2>&1 || true
sleep 2

# Build with profiling enabled
echo "[2/6] Building images..."
podman-compose -f $COMPOSE_FILE build >/dev/null 2>&1

# Start services
echo "[3/6] Starting services..."
podman-compose -f $COMPOSE_FILE up -d

# Wait for services
echo "[4/6] Waiting for services to be ready..."
sleep 10

# Enable pprof endpoints (if exposed)
echo "[5/6] Running performance test with profiling..."
echo "  Duration: ${PROFILE_DURATION}s"
echo "  Target rate: ${TARGET_RATE} req/s"
echo ""

# Function to collect profile
collect_profile() {
    local name=$1
    local url=$2
    local output=$3

    echo -n "  Collecting ${name} profile... "
    if curl -s "http://localhost:6060/${url}" -o "${PROFILE_DIR}/${output}" 2>/dev/null; then
        echo "✓"
    else
        echo "✗ (pprof not available on :6060)"
    fi
}

# Wait for test to start
sleep 15

echo "Collecting profiles..."
echo ""

# CPU profile
echo -n "  CPU profile (30s)... "
if curl -s "http://localhost:6060/debug/pprof/profile?seconds=30" -o "${PROFILE_DIR}/cpu.prof" 2>/dev/null; then
    echo "✓"
else
    echo "✗ (pprof not available - ensure gateway runs with pprof enabled)"
    echo ""
    echo "To enable pprof, add to gateway main.go:"
    echo "  import _ \"net/http/pprof\""
    echo "  go func() { http.ListenAndServe(\":6060\", nil) }()"
fi

# Heap profile
collect_profile "heap" "debug/pprof/heap" "heap.prof"

# Goroutine profile
collect_profile "goroutine" "debug/pprof/goroutine" "goroutine.prof"

# Block profile
collect_profile "block" "debug/pprof/block" "block.prof"

# Mutex profile
collect_profile "mutex" "debug/pprof/mutex" "mutex.prof"

# Wait for test to complete
echo ""
echo "[6/6] Waiting for test to complete..."
remaining=$((PROFILE_DURATION - 45))
if [ $remaining -gt 0 ]; then
    sleep $remaining
fi

# Collect final metrics
echo ""
echo "Collecting metrics..."

podman logs gateway-perf 2>&1 | tail -100 > "${PROFILE_DIR}/gateway.log"
podman logs dra-perf 2>&1 | tail -100 > "${PROFILE_DIR}/dra.log"

# Extract performance metrics
APP_TO_DRA=$(podman logs gateway-perf 2>&1 | grep "Messages (Application Only)" | tail -1 | grep -oE 'app_to_dra":[0-9]+' | grep -oE '[0-9]+' || echo "0")
DRA_TO_APP=$(podman logs gateway-perf 2>&1 | grep "Messages (Application Only)" | tail -1 | grep -oE 'dra_to_app":[0-9]+' | grep -oE '[0-9]+' || echo "0")

if [ "$APP_TO_DRA" = "0" ]; then
    APP_TO_DRA=$(podman logs gateway-perf 2>&1 | grep "app_to_dra_app" | tail -1 | grep -oE '[0-9]+' || echo "0")
fi
if [ "$DRA_TO_APP" = "0" ]; then
    DRA_TO_APP=$(podman logs gateway-perf 2>&1 | grep "dra_to_app_app" | tail -1 | grep -oE '[0-9]+' || echo "0")
fi

TOTAL=$((APP_TO_DRA + DRA_TO_APP))
ACTUAL_RATE=$((TOTAL / PROFILE_DURATION))

# Save metrics
cat > "${PROFILE_DIR}/metrics.txt" <<EOF
Performance Test Results
========================

Target Rate:     ${TARGET_RATE} req/s
Actual Rate:     ${ACTUAL_RATE} req/s
Success:         $((ACTUAL_RATE * 100 / TARGET_RATE))%

Messages:
  App → DRA:     ${APP_TO_DRA}
  DRA → App:     ${DRA_TO_APP}
  Total:         ${TOTAL}

Test Duration:   ${PROFILE_DURATION}s
EOF

# Cleanup
echo ""
echo "Stopping containers..."
podman-compose -f $COMPOSE_FILE down -v >/dev/null 2>&1 || true

# Summary
echo ""
echo "╔════════════════════════════════════════════════════════════════╗"
echo "║                    Profiling Complete                          ║"
echo "╚════════════════════════════════════════════════════════════════╝"
echo ""

cat "${PROFILE_DIR}/metrics.txt"

echo ""
echo "Profiles saved to: $PROFILE_DIR/"
echo ""
echo "Analyze CPU profile:"
echo "  go tool pprof ${PROFILE_DIR}/cpu.prof"
echo "  > top10"
echo "  > list <function_name>"
echo "  > web"
echo ""
echo "Analyze heap profile:"
echo "  go tool pprof ${PROFILE_DIR}/heap.prof"
echo ""
echo "Generate flame graph (if you have go-torch):"
echo "  go-torch --file ${PROFILE_DIR}/flame.svg ${PROFILE_DIR}/cpu.prof"
echo ""
