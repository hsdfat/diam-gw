#!/bin/bash
# Maximum Throughput Benchmark Script
# Finds the maximum sustainable throughput of the Diameter Gateway

set -e

COMPOSE_FILE="docker-compose.performance-test.yml"

# Configuration
TEST_DURATION=60
RAMP_UP=5
MIN_RATE=1000
MAX_RATE=10000
STEP_SIZE=1000
SUCCESS_THRESHOLD=95  # 95% of target rate

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo "╔════════════════════════════════════════════════════════════════╗"
echo "║     Diameter Gateway Maximum Throughput Benchmark             ║"
echo "╚════════════════════════════════════════════════════════════════╝"
echo ""
echo "Configuration:"
echo "  Test Duration:       ${TEST_DURATION}s"
echo "  Ramp-up Time:        ${RAMP_UP}s"
echo "  Starting Rate:       ${MIN_RATE} req/s"
echo "  Maximum Rate:        ${MAX_RATE} req/s"
echo "  Step Size:           ${STEP_SIZE} req/s"
echo "  Success Threshold:   ${SUCCESS_THRESHOLD}%"
echo ""

# Results tracking
RESULTS_FILE="benchmark-results-$(date +%Y%m%d-%H%M%S).csv"
echo "Target_Rate,Actual_Rate,App_To_DRA,DRA_To_App,Total_Messages,Errors,Success_Percent,Status" > $RESULTS_FILE

# Function to run a single test
run_test() {
    local target_rate=$1

    echo ""
    echo "================================================================="
    echo "  Testing at ${target_rate} req/s"
    echo "================================================================="

    # Calculate per-interface rates (40% S13, 40% S6a, 20% Gx)
    local s13_rate=$((target_rate * 40 / 100))
    local s6a_rate=$((target_rate * 40 / 100))
    local gx_rate=$((target_rate * 20 / 100))

    export DURATION=$TEST_DURATION
    export RAMP_UP=$RAMP_UP
    export S13_RATE=$s13_rate
    export S6A_RATE=$s6a_rate
    export GX_RATE=$gx_rate
    export TARGET_RATE=$target_rate

    # Clean up
    echo "[1/4] Cleaning up previous test..."
    podman-compose -f $COMPOSE_FILE down -v 2>/dev/null || true
    sleep 2

    # Start services
    echo "[2/4] Starting services..."
    podman-compose -f $COMPOSE_FILE up -d

    # Wait for services
    echo "[3/4] Waiting for services to be ready..."
    sleep 10

    # Wait for test to complete
    echo "[4/4] Running test for ${TEST_DURATION}s..."

    # Show progress
    local sustain_time=$((TEST_DURATION - RAMP_UP))
    sleep $RAMP_UP
    echo "  Ramp-up complete, now at sustained load..."

    for i in $(seq 1 $sustain_time); do
        if [ $((i % 10)) -eq 0 ]; then
            pct=$((i * 100 / sustain_time))
            printf "\r  Progress: [%-40s] %3d%%" $(printf '#%.0s' $(seq 1 $((pct * 40 / 100)))) $pct
        fi
        sleep 1
    done
    printf "\r  Progress: [%-40s] 100%%\n" $(printf '#%.0s' $(seq 1 40))

    # Collect results
    echo ""
    echo "Collecting results..."

    # Extract metrics
    local app_to_dra=$(podman logs gateway-perf 2>&1 | grep "Messages (Application Only)" | tail -1 | grep -oE 'app_to_dra":[0-9]+' | grep -oE '[0-9]+' || echo "0")
    local dra_to_app=$(podman logs gateway-perf 2>&1 | grep "Messages (Application Only)" | tail -1 | grep -oE 'dra_to_app":[0-9]+' | grep -oE '[0-9]+' || echo "0")

    # Fallback to alternate format
    if [ "$app_to_dra" = "0" ]; then
        app_to_dra=$(podman logs gateway-perf 2>&1 | grep "app_to_dra_app" | tail -1 | grep -oE '[0-9]+' || echo "0")
    fi
    if [ "$dra_to_app" = "0" ]; then
        dra_to_app=$(podman logs gateway-perf 2>&1 | grep "dra_to_app_app" | tail -1 | grep -oE '[0-9]+' || echo "0")
    fi

    local total_messages=$((app_to_dra + dra_to_app))
    local actual_rate=$((total_messages / TEST_DURATION))

    # Check for errors
    local routing_errors=$(podman logs gateway-perf 2>&1 | grep "ERROR.*routing" | wc -l)
    local connection_errors=$(podman logs gateway-perf 2>&1 | grep "ERROR.*connection" | wc -l)
    local total_errors=$((routing_errors + connection_errors))

    # Calculate success percentage
    local success_percent=0
    if [ $target_rate -gt 0 ]; then
        success_percent=$((actual_rate * 100 / target_rate))
    fi

    # Determine status
    local status="FAIL"
    if [ $success_percent -ge $SUCCESS_THRESHOLD ] && [ $total_errors -eq 0 ]; then
        status="PASS"
    elif [ $success_percent -ge $SUCCESS_THRESHOLD ]; then
        status="PASS_WITH_ERRORS"
    elif [ $total_errors -gt 0 ]; then
        status="FAIL_ERRORS"
    else
        status="FAIL_THROUGHPUT"
    fi

    # Display results
    echo ""
    echo "Results:"
    echo "  Target Rate:         ${target_rate} req/s"
    echo "  Actual Rate:         ${actual_rate} req/s (${success_percent}%)"
    echo "  App → DRA:           ${app_to_dra}"
    echo "  DRA → App:           ${dra_to_app}"
    echo "  Total Messages:      ${total_messages}"
    echo "  Errors:              ${total_errors}"

    if [ "$status" = "PASS" ]; then
        echo -e "  Status:              ${GREEN}✓ PASS${NC}"
    elif [ "$status" = "PASS_WITH_ERRORS" ]; then
        echo -e "  Status:              ${YELLOW}⚠ PASS (with errors)${NC}"
    else
        echo -e "  Status:              ${RED}✗ FAIL${NC}"
    fi

    # Save to CSV
    echo "${target_rate},${actual_rate},${app_to_dra},${dra_to_app},${total_messages},${total_errors},${success_percent},${status}" >> $RESULTS_FILE

    # Return status (0 = continue, 1 = stop)
    if [ "$status" = "FAIL_THROUGHPUT" ] || [ "$status" = "FAIL_ERRORS" ]; then
        return 1
    fi
    return 0
}

# Main benchmark loop
echo ""
echo "Starting benchmark..."
echo ""

current_rate=$MIN_RATE
max_successful_rate=0
failed=false

while [ $current_rate -le $MAX_RATE ] && [ "$failed" = false ]; do
    if run_test $current_rate; then
        max_successful_rate=$current_rate
        current_rate=$((current_rate + STEP_SIZE))
    else
        failed=true
        echo ""
        echo "================================================================="
        echo -e "${YELLOW}Maximum sustainable rate found: ${max_successful_rate} req/s${NC}"
        echo "================================================================="
    fi
done

# Cleanup
echo ""
echo "Cleaning up..."
podman-compose -f $COMPOSE_FILE down -v 2>/dev/null || true

# Generate summary report
echo ""
echo "╔════════════════════════════════════════════════════════════════╗"
echo "║                    Benchmark Summary                           ║"
echo "╚════════════════════════════════════════════════════════════════╝"
echo ""
echo "Results saved to: $RESULTS_FILE"
echo ""

# Display summary table
echo "Rate (req/s)  | Actual (req/s) | Success % | Status"
echo "------------- | -------------- | --------- | ------"
tail -n +2 $RESULTS_FILE | while IFS=, read -r target actual app_to_dra dra_to_app total errors success status; do
    printf "%-13s | %-14s | %-9s | %s\n" "$target" "$actual" "${success}%" "$status"
done

echo ""
if [ $max_successful_rate -gt 0 ]; then
    echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${GREEN}Maximum Sustainable Throughput: ${max_successful_rate} req/s${NC}"
    echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
else
    echo -e "${RED}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${RED}Failed to sustain even minimum rate (${MIN_RATE} req/s)${NC}"
    echo -e "${RED}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
fi
echo ""

# Performance recommendations
echo "Performance Analysis:"
echo ""

if [ $max_successful_rate -lt 1000 ]; then
    echo -e "${RED}⚠ CRITICAL: Performance is below baseline (1000 req/s)${NC}"
    echo "Recommendations:"
    echo "  1. Check for errors in logs"
    echo "  2. Verify all services are running"
    echo "  3. Check system resource usage (CPU, memory)"
    echo "  4. Review gateway configuration"
elif [ $max_successful_rate -lt 5000 ]; then
    echo -e "${YELLOW}⚠ WARNING: Performance is below expected (5000 req/s)${NC}"
    echo "Recommendations:"
    echo "  1. Increase DRA connections (--dra-conns)"
    echo "  2. Increase application connections per interface"
    echo "  3. Tune buffer sizes"
    echo "  4. Enable CPU profiling to find bottlenecks"
elif [ $max_successful_rate -ge 10000 ]; then
    echo -e "${GREEN}✓ EXCELLENT: Gateway can sustain 10K+ req/s${NC}"
    echo "System is performing at maximum tested capacity."
else
    echo -e "${GREEN}✓ GOOD: Gateway is performing well${NC}"
    echo "Consider testing at higher rates to find true maximum."
fi

echo ""
echo "View detailed results:"
echo "  cat $RESULTS_FILE"
echo ""
echo "View logs:"
echo "  podman logs gateway-perf"
echo "  podman logs dra-perf"
echo ""
