#!/bin/bash

# Modular Test Scenario Template
# Clone this file to create custom test scenarios
# Example: cp test-scenario-template.sh test-my-scenario.sh

set -e

# ============================================================================
# CONFIGURATION - Modify these for your scenario
# ============================================================================

SCENARIO_NAME="Template Scenario"
SCENARIO_DESCRIPTION="Description of what this scenario tests"

# Test parameters
DWR_INTERVAL="${DWR_INTERVAL:-10s}"
DWR_TIMEOUT="${DWR_TIMEOUT:-5s}"
MAX_DWR_FAILURES="${MAX_DWR_FAILURES:-3}"

# Compose settings
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose-dwr-test.yml}"
COMPOSE_CMD="${COMPOSE_CMD:-docker-compose}"

# Test duration
TEST_DURATION="${TEST_DURATION:-60}"

# ============================================================================
# UTILITY FUNCTIONS - Generally don't need to modify
# ============================================================================

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
NC='\033[0m'

print_header() {
    echo -e "${BLUE}╔══════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║  $SCENARIO_NAME${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════════════════╝${NC}"
    echo -e "${YELLOW}$SCENARIO_DESCRIPTION${NC}"
    echo ""
}

print_section() {
    echo -e "\n${CYAN}▶ $1${NC}"
    echo -e "${CYAN}$(printf '─%.0s' {1..60})${NC}"
}

print_success() {
    echo -e "${GREEN}✓${NC} $1"
}

print_error() {
    echo -e "${RED}✗${NC} $1"
}

print_info() {
    echo -e "${YELLOW}ℹ${NC} $1"
}

print_warning() {
    echo -e "${MAGENTA}⚠${NC} $1"
}

# Log file
LOG_DIR="../../logs/scenarios"
mkdir -p "$LOG_DIR"
LOG_FILE="$LOG_DIR/$(basename $0 .sh)_$(date +%Y%m%d_%H%M%S).log"

log() {
    echo "[$(date +'%H:%M:%S')] $1" | tee -a "$LOG_FILE"
}

# Check if compose is available
check_compose() {
    if ! command -v $COMPOSE_CMD &> /dev/null; then
        print_error "$COMPOSE_CMD not found"
        exit 1
    fi
    print_success "$COMPOSE_CMD available"
}

# Start services
start_services() {
    print_section "Starting Services"
    log "Starting services with compose"

    export DWR_INTERVAL DWR_TIMEOUT MAX_DWR_FAILURES

    $COMPOSE_CMD -f "$COMPOSE_FILE" up -d --build >> "$LOG_FILE" 2>&1

    print_info "Waiting for services to be healthy..."
    sleep 10

    print_success "Services started"
}

# Stop services
stop_services() {
    print_section "Stopping Services"
    log "Stopping services"
    $COMPOSE_CMD -f "$COMPOSE_FILE" down >> "$LOG_FILE" 2>&1
    print_success "Services stopped"
}

# Get service status
get_service_status() {
    local service=$1
    if $COMPOSE_CMD -f "$COMPOSE_FILE" ps | grep -q "$service.*Up"; then
        echo "running"
    else
        echo "stopped"
    fi
}

# Pause service (simulates network issues)
pause_service() {
    local service=$1
    local duration=${2:-10}

    print_info "Pausing $service for ${duration}s..."
    log "Pausing $service for ${duration}s"

    $COMPOSE_CMD -f "$COMPOSE_FILE" pause "$service" >> "$LOG_FILE" 2>&1
    sleep "$duration"
    $COMPOSE_CMD -f "$COMPOSE_FILE" unpause "$service" >> "$LOG_FILE" 2>&1

    print_success "$service resumed"
}

# Stop service (simulates DRA crash)
stop_service() {
    local service=$1

    print_info "Stopping $service..."
    log "Stopping $service"

    $COMPOSE_CMD -f "$COMPOSE_FILE" stop "$service" >> "$LOG_FILE" 2>&1
    print_success "$service stopped"
}

# Start service
start_service() {
    local service=$1

    print_info "Starting $service..."
    log "Starting $service"

    $COMPOSE_CMD -f "$COMPOSE_FILE" start "$service" >> "$LOG_FILE" 2>&1
    sleep 5
    print_success "$service started"
}

# Run client
run_client() {
    local mode=${1:-normal}
    local duration=${2:-$TEST_DURATION}

    print_section "Running Client ($mode mode)"
    log "Starting client in $mode mode for ${duration}s"

    $COMPOSE_CMD -f "$COMPOSE_FILE" run --rm \
        -e TEST_MODE="$mode" \
        -e TEST_DURATION="${duration}s" \
        client-dwr-test | tee -a "$LOG_FILE"
}

# Run client in background
run_client_background() {
    local name=${1:-test-client}
    local duration=${2:-$TEST_DURATION}

    print_section "Starting Client in Background"
    log "Starting client in background for ${duration}s"

    $COMPOSE_CMD -f "$COMPOSE_FILE" run -d --name "$name" \
        -e TEST_DURATION="${duration}s" \
        client-dwr-test

    print_success "Client started (container: $name)"
}

# Wait for background client
wait_client() {
    local name=${1:-test-client}

    print_info "Waiting for client to complete..."
    $COMPOSE_CMD -f "$COMPOSE_FILE" wait "$name" || true

    print_section "Client Logs"
    $COMPOSE_CMD -f "$COMPOSE_FILE" logs "$name" | tee -a "$LOG_FILE"

    docker rm -f "$name" 2>/dev/null || true
}

# Show service logs
show_logs() {
    print_section "Service Logs"

    for service in dra-1 dra-2 dra-3 dra-4; do
        echo -e "\n${CYAN}=== $service ===${NC}"
        $COMPOSE_CMD -f "$COMPOSE_FILE" logs --tail=10 "$service"
    done
}

# Cleanup
cleanup() {
    log "Cleanup"
    docker rm -f test-client 2>/dev/null || true
    stop_services
    print_info "Logs saved to: $LOG_FILE"
}

# ============================================================================
# TEST SCENARIO - Modify this function for your specific test
# ============================================================================

run_test_scenario() {
    print_section "Test Configuration"
    echo "DWR Interval:     $DWR_INTERVAL"
    echo "DWR Timeout:      $DWR_TIMEOUT"
    echo "Max Failures:     $MAX_DWR_FAILURES"
    echo "Test Duration:    ${TEST_DURATION}s"
    echo ""

    # ========================================================================
    # CUSTOMIZE YOUR TEST STEPS HERE
    # ========================================================================

    # Example Step 1: Start with all services healthy
    print_section "Step 1: Initialize Test"
    start_services
    sleep 5

    # Example Step 2: Run client in background
    print_section "Step 2: Start Client"
    run_client_background "test-client" 90
    sleep 10

    # Example Step 3: Simulate a failure
    print_section "Step 3: Simulate Failure"
    pause_service "dra-1" 20

    # Example Step 4: Wait for results
    print_section "Step 4: Collect Results"
    wait_client "test-client"

    # Example Step 5: Show logs
    show_logs

    # ========================================================================
    # END OF CUSTOM TEST STEPS
    # ========================================================================

    print_section "Test Complete"
    print_success "Scenario completed successfully"
}

# ============================================================================
# MAIN EXECUTION
# ============================================================================

main() {
    # Setup
    trap cleanup EXIT INT TERM

    print_header
    check_compose

    # Run the test
    run_test_scenario

    # Summary
    print_section "Summary"
    print_info "Test: $SCENARIO_NAME"
    print_info "Status: COMPLETED"
    print_info "Log: $LOG_FILE"
}

# Run main
main "$@"
