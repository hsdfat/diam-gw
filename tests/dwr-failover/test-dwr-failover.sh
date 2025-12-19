#!/bin/bash

# DWR Failover Testing Script
# Tests the new MaxDWRFailures threshold feature with automated scenarios

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
NC='\033[0m' # No Color

# Configuration
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose-dwr-test.yml}"
COMPOSE_CMD="${COMPOSE_CMD:-podman-compose}"
LOG_DIR="../../logs/dwr-test"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)

# Test parameters
DWR_INTERVAL="${DWR_INTERVAL:-10s}"
DWR_TIMEOUT="${DWR_TIMEOUT:-5s}"
MAX_DWR_FAILURES="${MAX_DWR_FAILURES:-3}"

# Functions
print_header() {
    echo -e "${BLUE}╔══════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║         DWR Failure Threshold Testing Suite                 ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════════════════╝${NC}"
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

# Initialize logging
setup_logging() {
    mkdir -p "$LOG_DIR"
    MAIN_LOG="$LOG_DIR/test_${TIMESTAMP}.log"
    print_info "Logging to: $MAIN_LOG"
    echo "DWR Failover Test - Started at $(date)" > "$MAIN_LOG"
}

# Log message
log() {
    echo "[$(date +'%H:%M:%S')] $1" >> "$MAIN_LOG"
}

# Check prerequisites
check_prerequisites() {
    print_section "Checking Prerequisites"

    # Check for docker/podman
    if command -v $COMPOSE_CMD &> /dev/null; then
        print_success "$COMPOSE_CMD found"
    else
        print_error "$COMPOSE_CMD not found. Install docker-compose or podman-compose"
        exit 1
    fi

    # Check compose file
    if [ -f "$COMPOSE_FILE" ]; then
        print_success "Compose file found: $COMPOSE_FILE"
    else
        print_error "Compose file not found: $COMPOSE_FILE"
        exit 1
    fi

    # Check if client binary can be built
    if [ -d "examples/multi_dra_test" ]; then
        print_success "Test client source found"
    else
        print_warning "Test client source not found"
    fi
}

# Display test configuration
show_config() {
    print_section "Test Configuration"
    echo "DWR Interval:       $DWR_INTERVAL"
    echo "DWR Timeout:        $DWR_TIMEOUT"
    echo "Max DWR Failures:   $MAX_DWR_FAILURES"
    echo "Compose File:       $COMPOSE_FILE"
    echo "Log Directory:      $LOG_DIR"
}

# Start services
start_services() {
    print_section "Starting Services"
    log "Starting services"

    export DWR_INTERVAL DWR_TIMEOUT MAX_DWR_FAILURES

    $COMPOSE_CMD -f "$COMPOSE_FILE" up -d --build

    print_info "Waiting for services to be healthy..."
    sleep 10

    # Check service health
    local healthy=0
    for i in {1..30}; do
        if $COMPOSE_CMD -f "$COMPOSE_FILE" ps | grep -q "healthy"; then
            healthy=1
            break
        fi
        sleep 1
    done

    if [ $healthy -eq 1 ]; then
        print_success "All DRAs are healthy"
    else
        print_warning "Some DRAs may not be healthy yet"
    fi
}

# Stop services
stop_services() {
    print_section "Stopping Services"
    log "Stopping services"
    $COMPOSE_CMD -f "$COMPOSE_FILE" down
    print_success "Services stopped"
}

# Run client test
run_client_test() {
    local test_name=$1
    local duration=${2:-60}

    print_section "Running Test: $test_name"
    log "Test: $test_name - Duration: ${duration}s"

    $COMPOSE_CMD -f "$COMPOSE_FILE" run --rm \
        -e TEST_MODE="$test_name" \
        -e TEST_DURATION="${duration}s" \
        client-dwr-test

    print_success "Test completed: $test_name"
}

# Simulate DWR failure by pausing DRA
simulate_dwr_failure() {
    local dra_name=$1
    local duration=${2:-30}

    print_section "Simulating DWR Failure: $dra_name"
    log "Pausing $dra_name for ${duration}s to trigger DWR timeouts"

    print_info "Pausing $dra_name for ${duration}s..."
    $COMPOSE_CMD -f "$COMPOSE_FILE" pause "$dra_name"

    print_info "Waiting ${duration}s..."
    sleep "$duration"

    print_info "Resuming $dra_name..."
    $COMPOSE_CMD -f "$COMPOSE_FILE" unpause "$dra_name"

    print_success "DWR failure simulation completed"
}

# Test Scenario 1: Normal operation
test_normal_operation() {
    print_section "Test 1: Normal Operation"
    log "TEST 1: Normal Operation"

    start_services
    sleep 5

    print_info "Running client with all DRAs healthy..."
    run_client_test "normal" 30

    print_success "Test 1 completed"
}

# Test Scenario 2: Single DWR failure (below threshold)
test_single_dwr_failure() {
    print_section "Test 2: Single DWR Failure (Below Threshold)"
    log "TEST 2: Single DWR Failure"

    print_info "Client should tolerate failures < MaxDWRFailures ($MAX_DWR_FAILURES)"

    # Start client in background
    $COMPOSE_CMD -f "$COMPOSE_FILE" run -d --name dwr-test-client \
        -e TEST_DURATION="90s" \
        client-dwr-test

    sleep 10

    # Pause DRA-1 briefly (less than threshold)
    local pause_time=$((DWR_INTERVAL + DWR_TIMEOUT + 2))
    print_info "Pausing DRA-1 for ${pause_time}s (1 DWR failure)"
    $COMPOSE_CMD -f "$COMPOSE_FILE" pause dra-1
    sleep "$pause_time"
    $COMPOSE_CMD -f "$COMPOSE_FILE" unpause dra-1

    print_info "Waiting for test to complete..."
    $COMPOSE_CMD -f "$COMPOSE_FILE" wait dwr-test-client || true
    $COMPOSE_CMD -f "$COMPOSE_FILE" logs dwr-test-client

    $COMPOSE_CMD rm -f dwr-test-client 2>/dev/null || podman rm -f dwr-test-client 2>/dev/null || true

    print_success "Test 2 completed"
}

# Test Scenario 3: Multiple DWR failures (exceeds threshold)
test_multiple_dwr_failures() {
    print_section "Test 3: Multiple DWR Failures (Exceeds Threshold)"
    log "TEST 3: Multiple DWR Failures"

    print_info "Triggering $MAX_DWR_FAILURES consecutive DWR failures"
    print_info "Client should reconnect after threshold is exceeded"

    # Start client in background
    $COMPOSE_CMD -f "$COMPOSE_FILE" run -d --name dwr-test-client \
        -e TEST_DURATION="120s" \
        client-dwr-test

    sleep 10

    # Calculate pause time to trigger MAX_DWR_FAILURES failures
    local pause_time=$(( (DWR_INTERVAL + DWR_TIMEOUT + 2) * MAX_DWR_FAILURES ))
    print_info "Pausing DRA-1 for ${pause_time}s to trigger $MAX_DWR_FAILURES failures"

    $COMPOSE_CMD -f "$COMPOSE_FILE" pause dra-1
    sleep "$pause_time"
    $COMPOSE_CMD -f "$COMPOSE_FILE" unpause dra-1

    print_info "Waiting for test to complete..."
    $COMPOSE_CMD -f "$COMPOSE_FILE" wait dwr-test-client || true
    $COMPOSE_CMD -f "$COMPOSE_FILE" logs dwr-test-client

    $COMPOSE_CMD rm -f dwr-test-client 2>/dev/null || podman rm -f dwr-test-client 2>/dev/null || true

    print_success "Test 3 completed"
}

# Test Scenario 4: Priority failover with DWR failures
test_priority_failover() {
    print_section "Test 4: Priority Failover with DWR Failures"
    log "TEST 4: Priority Failover"

    print_info "Testing failover from Priority 1 to Priority 2"

    # Start client in background
    $COMPOSE_CMD -f "$COMPOSE_FILE" run -d --name dwr-test-client \
        -e TEST_DURATION="120s" \
        client-dwr-test

    sleep 15

    # Pause both Priority 1 DRAs
    print_info "Pausing all Priority 1 DRAs (dra-1, dra-2)..."
    $COMPOSE_CMD -f "$COMPOSE_FILE" pause dra-1 dra-2

    print_info "Waiting for failover to Priority 2..."
    sleep 40

    # Resume Priority 1 DRAs
    print_info "Resuming Priority 1 DRAs for fail-back..."
    $COMPOSE_CMD -f "$COMPOSE_FILE" unpause dra-1 dra-2

    print_info "Waiting for fail-back to Priority 1..."
    sleep 30

    print_info "Collecting logs..."
    $COMPOSE_CMD -f "$COMPOSE_FILE" logs dwr-test-client

    $COMPOSE_CMD rm -f dwr-test-client 2>/dev/null || podman rm -f dwr-test-client 2>/dev/null || true

    print_success "Test 4 completed"
}

# Show test logs
show_logs() {
    print_section "Service Logs"

    for service in dra-1 dra-2 dra-3 dra-4 client-dwr-test; do
        echo -e "\n${CYAN}=== $service ===${NC}"
        $COMPOSE_CMD -f "$COMPOSE_FILE" logs --tail=20 "$service" 2>/dev/null || echo "No logs for $service"
    done
}

# Cleanup
cleanup() {
    print_info "Cleaning up..."
    $COMPOSE_CMD rm -f dwr-test-client 2>/dev/null || podman rm -f dwr-test-client 2>/dev/null || true
    stop_services
}

# Show test list with descriptions
show_list() {
    echo -e "${BLUE}╔══════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║       DWR Failure Threshold - Available Tests               ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "${CYAN}Test Scenarios:${NC}"
    echo ""
    echo -e "  ${GREEN}test1${NC} - Normal Operation ✅"
    echo -e "         Verify baseline with all DRAs healthy"
    echo -e "         Expected: No failures, counter stays at 0"
    echo -e "         Duration: ~30s"
    echo ""
    echo -e "  ${YELLOW}test2${NC} - Single DWR Failure ⚠️"
    echo -e "         Brief DRA pause (1 failure, below threshold)"
    echo -e "         Expected: Counter 1/3, no reconnect, auto-reset"
    echo -e "         Duration: ~90s"
    echo ""
    echo -e "  ${RED}test3${NC} - Multiple DWR Failures 🔴"
    echo -e "         Long DRA pause (3+ failures, exceeds threshold)"
    echo -e "         Expected: Counter hits 3/3, triggers reconnect"
    echo -e "         Duration: ~120s"
    echo ""
    echo -e "  ${MAGENTA}test4${NC} - Priority Failover 🔄"
    echo -e "         P1 DRAs fail → P2, then recover → P1"
    echo -e "         Expected: Failover and fail-back working"
    echo -e "         Duration: ~120s"
    echo ""
    echo -e "${CYAN}Other Commands:${NC}"
    echo ""
    echo -e "  ${GREEN}all${NC}      - Run all test scenarios"
    echo -e "  ${GREEN}start${NC}    - Start services only"
    echo -e "  ${GREEN}stop${NC}     - Stop services"
    echo -e "  ${GREEN}logs${NC}     - Show service logs"
    echo -e "  ${GREEN}cleanup${NC}  - Clean up running containers"
    echo -e "  ${GREEN}list${NC}     - Show this test list"
    echo -e "  ${GREEN}help${NC}     - Show detailed help"
    echo ""
    echo -e "${CYAN}Configuration:${NC}"
    echo "  DWR Interval:      $DWR_INTERVAL"
    echo "  DWR Timeout:       $DWR_TIMEOUT"
    echo "  Max DWR Failures:  $MAX_DWR_FAILURES"
    echo ""
    echo -e "${CYAN}Quick Examples:${NC}"
    echo "  $0 test1                    # Run normal operation test"
    echo "  $0 all                      # Run all tests"
    echo "  MAX_DWR_FAILURES=2 $0 test3 # Custom threshold"
    echo ""
}

# Show help
show_help() {
    cat << EOF
DWR Failover Testing Script

Usage: $0 [command] [options]

Commands:
  all               Run all test scenarios (default)
  list              List all available tests with descriptions
  test1             Test 1: Normal operation
  test2             Test 2: Single DWR failure (below threshold)
  test3             Test 3: Multiple DWR failures (exceeds threshold)
  test4             Test 4: Priority failover with DWR failures
  start             Start services only
  stop              Stop services
  logs              Show service logs
  cleanup           Clean up running containers
  help              Show this help

Environment Variables:
  DWR_INTERVAL      DWR interval (default: 10s)
  DWR_TIMEOUT       DWR timeout (default: 5s)
  MAX_DWR_FAILURES  Maximum DWR failures before reconnect (default: 3)
  COMPOSE_CMD       Compose command: docker-compose or podman-compose

Examples:
  # List all available tests
  $0 list

  # Run all tests with default settings
  $0 all

  # Run with custom DWR settings
  DWR_INTERVAL=5s DWR_TIMEOUT=2s MAX_DWR_FAILURES=2 $0 all

  # Run specific test
  $0 test3

  # Use podman-compose
  COMPOSE_CMD=podman-compose $0 all

EOF
}

# Main script
main() {
    local command=${1:-all}

    # Setup trap for cleanup (except for list/help commands)
    if [[ "$command" != "list" && "$command" != "--list" && "$command" != "-l" && "$command" != "help" && "$command" != "--help" && "$command" != "-h" ]]; then
        trap cleanup EXIT INT TERM
    fi

    case $command in
        all)
            print_header
            setup_logging
            check_prerequisites
            show_config

            test_normal_operation
            sleep 5
            test_single_dwr_failure
            sleep 5
            test_multiple_dwr_failures
            sleep 5
            test_priority_failover

            print_section "Test Summary"
            print_success "All tests completed!"
            print_info "Logs saved to: $MAIN_LOG"
            ;;

        test1)
            print_header
            setup_logging
            check_prerequisites
            show_config
            test_normal_operation
            ;;

        test2)
            print_header
            setup_logging
            check_prerequisites
            show_config
            start_services
            test_single_dwr_failure
            ;;

        test3)
            print_header
            setup_logging
            check_prerequisites
            show_config
            start_services
            test_multiple_dwr_failures
            ;;

        test4)
            print_header
            setup_logging
            check_prerequisites
            show_config
            start_services
            test_priority_failover
            ;;

        start)
            print_header
            check_prerequisites
            show_config
            start_services
            print_info "Services started. Use '$0 stop' to stop them"
            ;;

        stop)
            stop_services
            ;;

        logs)
            show_logs
            ;;

        cleanup)
            cleanup
            ;;

        list|--list|-l)
            show_list
            ;;

        help|--help|-h)
            show_help
            ;;

        *)
            print_error "Unknown command: $command"
            echo ""
            show_list
            exit 1
            ;;
    esac
}

# Run main
main "$@"
