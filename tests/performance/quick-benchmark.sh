#!/bin/bash
# Quick Benchmark - Test at specific load levels to find maximum throughput

set -e

COMPOSE_FILE="docker-compose.performance-test.yml"

# Test levels to try (in req/s)
TEST_LEVELS=(1000 2000 3000 5000 7500 10000 15000 20000)

# Configuration
DURATION=30  # Shorter duration for quick testing
RAMP_UP=5

echo "╔════════════════════════════════════════════════════════════════╗"
echo "║         Quick Gateway Throughput Benchmark                     ║"
echo "╚════════════════════════════════════════════════════════════════╝"
echo ""
echo "Testing at levels: ${TEST_LEVELS[@]} req/s"
echo "Duration per test: ${DURATION}s (${RAMP_UP}s ramp-up)"
echo ""

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --duration)
            DURATION="$2"
            shift 2
            ;;
        --levels)
            IFS=',' read -ra TEST_LEVELS <<< "$2"
            shift 2
            ;;
        --help)
            echo "Usage: $0 [options]"
            echo ""
            echo "Options:"
            echo "  --duration SECONDS    Test duration (default: 30)"
            echo "  --levels 1000,2000... Comma-separated list of rates to test"
            echo "  --help                Show this help"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

RESULTS_FILE="quick-benchmark-$(date +%Y%m%d-%H%M%S).txt"

echo "Results will be saved to: $RESULTS_FILE"
echo ""

# Function to run test at specific rate
test_rate() {
    local rate=$1

    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "Testing: ${rate} req/s"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

    # Calculate interface rates
    local s13_rate=$((rate * 40 / 100))
    local s6a_rate=$((rate * 40 / 100))
    local gx_rate=$((rate * 20 / 100))

    # Export environment
    export DURATION=$DURATION
    export RAMP_UP=$RAMP_UP
    export S13_RATE=$s13_rate
    export S6A_RATE=$s6a_rate
    export GX_RATE=$gx_rate

    # Clean up
    podman-compose -f $COMPOSE_FILE down -v >/dev/null 2>&1 || true
    sleep 2

    # Start services
    echo -n "Starting services... "
    podman-compose -f $COMPOSE_FILE up -d >/dev/null 2>&1
    echo "done"

    # Wait for services
    echo -n "Waiting for services... "
    sleep 8
    echo "done"

    # Run test
    echo -n "Running test (${DURATION}s)... "
    sleep $DURATION
    echo "done"

    # Collect metrics
    local app_to_dra=$(podman logs gateway-perf 2>&1 | grep "Messages (Application Only)" | tail -1 | grep -oE 'app_to_dra":[0-9]+' | grep -oE '[0-9]+' 2>/dev/null || echo "0")
    local dra_to_app=$(podman logs gateway-perf 2>&1 | grep "Messages (Application Only)" | tail -1 | grep -oE 'dra_to_app":[0-9]+' | grep -oE '[0-9]+' 2>/dev/null || echo "0")

    if [ "$app_to_dra" = "0" ]; then
        app_to_dra=$(podman logs gateway-perf 2>&1 | grep "app_to_dra_app" | tail -1 | grep -oE '[0-9]+' 2>/dev/null || echo "0")
    fi
    if [ "$dra_to_app" = "0" ]; then
        dra_to_app=$(podman logs gateway-perf 2>&1 | grep "dra_to_app_app" | tail -1 | grep -oE '[0-9]+' 2>/dev/null || echo "0")
    fi

    local total=$((app_to_dra + dra_to_app))
    local actual_rate=$((total / DURATION))
    local success_pct=$((actual_rate * 100 / rate))

    # Check for errors
    local errors=$(podman logs gateway-perf 2>&1 | grep -iE "error|fail" | grep -v "0 errors" | wc -l)

    # Results
    echo ""
    echo "  Target:  ${rate} req/s"
    echo "  Actual:  ${actual_rate} req/s (${success_pct}%)"
    echo "  Messages: ${total} (↑${app_to_dra} ↓${dra_to_app})"
    echo "  Errors:  ${errors}"

    # Status
    if [ $success_pct -ge 95 ] && [ $errors -eq 0 ]; then
        echo "  ✓ PASS"
        echo "${rate},${actual_rate},${total},${errors},PASS" >> $RESULTS_FILE
        return 0
    elif [ $success_pct -ge 80 ]; then
        echo "  ⚠ MARGINAL"
        echo "${rate},${actual_rate},${total},${errors},MARGINAL" >> $RESULTS_FILE
        return 0
    else
        echo "  ✗ FAIL"
        echo "${rate},${actual_rate},${total},${errors},FAIL" >> $RESULTS_FILE
        return 1
    fi
}

# Header for results file
echo "Target_Rate,Actual_Rate,Total_Messages,Errors,Status" > $RESULTS_FILE

# Run tests
max_pass=0
for rate in "${TEST_LEVELS[@]}"; do
    echo ""
    if test_rate $rate; then
        max_pass=$rate
    else
        echo ""
        echo "Failed at ${rate} req/s - stopping benchmark"
        break
    fi
done

# Cleanup
echo ""
echo "Cleaning up..."
podman-compose -f $COMPOSE_FILE down -v >/dev/null 2>&1 || true

# Summary
echo ""
echo "╔════════════════════════════════════════════════════════════════╗"
echo "║                    Quick Benchmark Results                     ║"
echo "╚════════════════════════════════════════════════════════════════╝"
echo ""

if [ $max_pass -gt 0 ]; then
    echo "✓ Maximum sustainable rate: ${max_pass} req/s"
else
    echo "✗ Failed to sustain even minimum rate"
fi

echo ""
echo "Detailed results:"
tail -n +2 $RESULTS_FILE | column -t -s,
echo ""

echo "Results saved to: $RESULTS_FILE"
echo ""
