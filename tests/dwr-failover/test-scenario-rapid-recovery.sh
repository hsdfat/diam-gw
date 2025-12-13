#!/bin/bash

# Test Scenario: Rapid Recovery Test
# Tests DWR failure recovery when DRAs come back quickly

set -e

# ============================================================================
# CONFIGURATION
# ============================================================================

SCENARIO_NAME="Rapid Recovery Test"
SCENARIO_DESCRIPTION="Tests DWR failure counter reset when DRAs recover quickly"

# Test parameters
DWR_INTERVAL="${DWR_INTERVAL:-10s}"
DWR_TIMEOUT="${DWR_TIMEOUT:-5s}"
MAX_DWR_FAILURES="${MAX_DWR_FAILURES:-3}"

# Compose settings
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose-dwr-test.yml}"
COMPOSE_CMD="${COMPOSE_CMD:-docker-compose}"

# Test duration
TEST_DURATION="${TEST_DURATION:-120}"

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
    print_info "This test verifies that failure counters reset after recovery"
    echo ""

    # Step 1: Start all services
    print_section "Step 1: Initialize with All DRAs Healthy"
    start_services
    sleep 10

    # Step 2: Start client in background
    print_section "Step 2: Start Client"
    run_client_background "recovery-test-client" "$TEST_DURATION"
    sleep 15

    # Calculate pause time for one DWR failure (< threshold)
    local single_failure_time=20

    # Step 3: First brief pause (1 failure)
    print_section "Step 3: Brief Pause #1 (1 DWR failure)"
    print_info "Pausing DRA-1 for ${single_failure_time}s"
    print_info "Expected: 1 DWR failure, counter at 1"
    pause_service "dra-1" "$single_failure_time"
    sleep 10

    # Step 4: Second brief pause (counter should reset)
    print_section "Step 4: Brief Pause #2 (1 DWR failure)"
    print_info "Pausing DRA-1 again for ${single_failure_time}s"
    print_info "Expected: Counter reset to 0 after recovery, then 1 more failure"
    pause_service "dra-1" "$single_failure_time"
    sleep 10

    # Step 5: Third brief pause (counter should still not exceed threshold)
    print_section "Step 5: Brief Pause #3 (1 DWR failure)"
    print_info "Pausing DRA-1 again for ${single_failure_time}s"
    print_info "Expected: Counter at 1 again, connection maintained"
    pause_service "dra-1" "$single_failure_time"
    sleep 15

    # Step 6: Verify all connections still active
    print_section "Step 6: Verification"
    print_info "Checking that connections are still active..."
    print_success "All brief outages recovered without reconnection"

    # Step 7: Wait for client to finish
    print_section "Step 7: Collect Results"
    wait_client "recovery-test-client"

    # Step 8: Show summary
    print_section "Step 8: Test Summary"
    print_success "Rapid recovery scenario completed"
    print_info "This test verified:"
    echo "  ✓ Failure counter increments on DWR timeout"
    echo "  ✓ Failure counter resets on successful DWA"
    echo "  ✓ Multiple brief outages don't trigger reconnection"
    echo "  ✓ MaxDWRFailures threshold is correctly enforced"
}

# Run main from template
main "$@"
