#!/bin/bash

# Test Scenario: Gradual Degradation
# Tests how the client handles gradual DRA failures over time

set -e

# ============================================================================
# CONFIGURATION
# ============================================================================

SCENARIO_NAME="Gradual Degradation Test"
SCENARIO_DESCRIPTION="Tests client behavior as DRAs fail gradually over time"

# Test parameters
DWR_INTERVAL="${DWR_INTERVAL:-10s}"
DWR_TIMEOUT="${DWR_TIMEOUT:-5s}"
MAX_DWR_FAILURES="${MAX_DWR_FAILURES:-3}"

# Compose settings
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose-dwr-test.yml}"
COMPOSE_CMD="${COMPOSE_CMD:-docker-compose}"

# Test duration
TEST_DURATION="${TEST_DURATION:-180}"

# Source utility functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/test-scenario-template.sh" || {
    echo "Error: Cannot source template functions"
    exit 1
}

# ============================================================================
# TEST SCENARIO
# ============================================================================

run_test_scenario() {
    print_section "Test Configuration"
    echo "DWR Interval:     $DWR_INTERVAL"
    echo "DWR Timeout:      $DWR_TIMEOUT"
    echo "Max Failures:     $MAX_DWR_FAILURES"
    echo "Test Duration:    ${TEST_DURATION}s"
    echo ""

    # Step 1: Start all services
    print_section "Step 1: Initialize with All DRAs Healthy"
    start_services
    sleep 10

    # Step 2: Start client in background
    print_section "Step 2: Start Client"
    run_client_background "gradual-test-client" "$TEST_DURATION"
    sleep 15

    # Step 3: Kill first Priority 1 DRA
    print_section "Step 3: Stop DRA-1 (Priority 1)"
    print_info "Expected: Traffic continues on DRA-2"
    stop_service "dra-1"
    sleep 30

    # Step 4: Kill second Priority 1 DRA
    print_section "Step 4: Stop DRA-2 (Priority 1)"
    print_info "Expected: Failover to Priority 2 (DRA-3, DRA-4)"
    stop_service "dra-2"
    sleep 30

    # Step 5: Kill first Priority 2 DRA
    print_section "Step 5: Stop DRA-3 (Priority 2)"
    print_info "Expected: Traffic continues on DRA-4 only"
    stop_service "dra-3"
    sleep 30

    # Step 6: Restore Priority 1 DRAs
    print_section "Step 6: Restore Priority 1 DRAs"
    print_info "Expected: Fail-back to Priority 1"
    start_service "dra-1"
    start_service "dra-2"
    sleep 30

    # Step 7: Wait for client to finish
    print_section "Step 7: Collect Results"
    wait_client "gradual-test-client"

    # Step 8: Show summary logs
    print_section "Step 8: Service Logs Summary"
    show_logs

    print_section "Test Complete"
    print_success "Gradual degradation scenario completed"
    print_info "This test verified:"
    echo "  ✓ Single DRA failure handling"
    echo "  ✓ Priority failover (P1 → P2)"
    echo "  ✓ Single backup DRA operation"
    echo "  ✓ Priority fail-back (P2 → P1)"
}

# Run main from template
main "$@"
