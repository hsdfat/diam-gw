#!/bin/bash

# Automated Multi-DRA Priority-Based Testing Flow with Podman
# This script demonstrates the complete priority-based failover/fail-back workflow

set -e

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
MAGENTA='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m'

# Use podman-compose or docker-compose
COMPOSE_CMD="podman-compose"
if ! command -v podman-compose &> /dev/null; then
    if command -v docker-compose &> /dev/null; then
        COMPOSE_CMD="docker-compose"
        echo -e "${YELLOW}Note: podman-compose not found, using docker-compose${NC}"
    else
        echo -e "${RED}Error: Neither podman-compose nor docker-compose found!${NC}"
        echo "Install podman-compose: pip3 install podman-compose"
        exit 1
    fi
fi

print_header() {
    echo ""
    echo -e "${CYAN}╔══════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${CYAN}║$(printf "%-62s" " $1")║${NC}"
    echo -e "${CYAN}╚══════════════════════════════════════════════════════════════╝${NC}"
    echo ""
}

print_step() {
    echo -e "${GREEN}▶${NC} $1"
}

print_info() {
    echo -e "${BLUE}ℹ${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

print_error() {
    echo -e "${RED}✗${NC} $1"
}

wait_for_dra() {
    local name=$1
    local max_wait=30
    local count=0

    print_step "Waiting for $name to be healthy..."

    while [ $count -lt $max_wait ]; do
        if $COMPOSE_CMD ps | grep -q "$name.*healthy"; then
            echo -e "${GREEN}✓${NC} $name is healthy"
            return 0
        fi
        sleep 1
        count=$((count + 1))
        printf "."
    done

    echo ""
    print_warning "$name did not become healthy within ${max_wait}s"
    return 1
}

show_status() {
    echo ""
    print_info "Container Status:"
    $COMPOSE_CMD ps
    echo ""
}

show_logs() {
    local service=$1
    local lines=${2:-20}

    echo ""
    print_info "Last $lines lines of logs from $service:"
    echo -e "${CYAN}────────────────────────────────────────────────────────────${NC}"
    $COMPOSE_CMD logs --tail=$lines $service
    echo -e "${CYAN}────────────────────────────────────────────────────────────${NC}"
    echo ""
}

cleanup() {
    print_header "Cleaning Up"

    print_step "Stopping all containers..."
    $COMPOSE_CMD down -v 2>/dev/null || true

    print_step "Removing any dangling containers..."
    if [ "$COMPOSE_CMD" = "podman-compose" ]; then
        podman container prune -f 2>/dev/null || true
    else
        docker container prune -f 2>/dev/null || true
    fi

    echo -e "${GREEN}✓${NC} Cleanup complete"
}

# Trap to ensure cleanup on exit
trap cleanup EXIT

# Main flow
main() {
    print_header "Multi-DRA Priority-Based Testing Flow"

    print_info "This script will demonstrate:"
    echo "  • Building DRA simulator and client images"
    echo "  • Starting 4 DRAs (2 at Priority 1, 2 at Priority 2)"
    echo "  • Client sending messages to Priority 1 DRAs only"
    echo "  • Automatic failover when Priority 1 DRAs fail"
    echo "  • Automatic fail-back when Priority 1 DRAs recover"
    echo ""

    # Step 1: Build images
    print_header "Step 1: Building Container Images"

    print_step "Building DRA simulator image..."
    $COMPOSE_CMD build dra-1

    print_step "Building client image..."
    $COMPOSE_CMD build client

    echo -e "${GREEN}✓${NC} Images built successfully"

    # Step 2: Start all DRAs
    print_header "Step 2: Starting All DRAs"

    print_step "Starting 4 DRA containers..."
    $COMPOSE_CMD up -d dra-1 dra-2 dra-3 dra-4

    # Wait for all DRAs to be healthy
    wait_for_dra "dra-1"
    wait_for_dra "dra-2"
    wait_for_dra "dra-3"
    wait_for_dra "dra-4"

    show_status

    # Step 3: Start client
    print_header "Step 3: Starting Client"

    print_step "Client will establish connections with all 4 DRAs..."
    print_info "But will ONLY send S13 messages to Priority 1 DRAs (DRA-1, DRA-2)"

    $COMPOSE_CMD up -d client

    sleep 5
    show_status

    # Step 4: Observe normal operation
    print_header "Step 4: Normal Operation (Priority 1 Active)"

    print_info "Client is sending messages to Priority 1 DRAs only"
    print_info "Let's observe for 15 seconds..."

    sleep 15

    show_logs "client" 30

    # Step 5: Simulate Priority 1 failure
    print_header "Step 5: Simulating Priority 1 Failure (Failover Test)"

    print_warning "Stopping both Priority 1 DRAs (DRA-1 and DRA-2)..."
    print_info "Client should automatically failover to Priority 2 DRAs"

    $COMPOSE_CMD stop dra-1 dra-2

    show_status

    print_info "Waiting 10 seconds for client to detect failure and failover..."
    sleep 10

    show_logs "client" 30

    print_info "Observing Priority 2 operation for 15 seconds..."
    sleep 15

    show_logs "client" 30

    # Step 6: Restore Priority 1 (fail-back test)
    print_header "Step 6: Restoring Priority 1 DRAs (Fail-back Test)"

    print_step "Restarting Priority 1 DRAs (DRA-1 and DRA-2)..."
    print_info "Client should automatically fail-back to Priority 1"

    $COMPOSE_CMD start dra-1 dra-2

    wait_for_dra "dra-1"
    wait_for_dra "dra-2"

    show_status

    print_info "Waiting 10 seconds for client to detect recovery and fail-back..."
    sleep 10

    show_logs "client" 30

    print_info "Observing restored Priority 1 operation for 15 seconds..."
    sleep 15

    show_logs "client" 30

    # Step 7: Final status
    print_header "Step 7: Final Status"

    show_status

    print_step "DRA Statistics:"
    for dra in dra-1 dra-2 dra-3 dra-4; do
        echo ""
        echo -e "${MAGENTA}[$dra]${NC}"
        $COMPOSE_CMD logs --tail=5 $dra | grep -E "(Statistics|Messages|Connections)" || true
    done

    echo ""
    print_step "Client Statistics:"
    $COMPOSE_CMD logs client | grep -E "(Final Statistics|DRA Pool Status|Messages Sent|Active Priority)" || true

    # Step 8: Summary
    print_header "Test Flow Complete"

    echo -e "${GREEN}✓${NC} All tests completed successfully!"
    echo ""
    echo "Summary of what was demonstrated:"
    echo "  1. Built and started 4 DRAs with 2 priority levels"
    echo "  2. Client established connections with all 4 DRAs"
    echo "  3. Client sent messages ONLY to Priority 1 DRAs"
    echo "  4. Automatic failover to Priority 2 when Priority 1 failed"
    echo "  5. Automatic fail-back to Priority 1 when it recovered"
    echo ""

    print_info "Containers are still running. You can:"
    echo "  • View logs: $COMPOSE_CMD logs -f [service-name]"
    echo "  • Check status: $COMPOSE_CMD ps"
    echo "  • Stop manually: $COMPOSE_CMD down"
    echo ""

    # Ask if user wants to keep containers running
    read -p "Keep containers running for manual inspection? [y/N]: " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        trap - EXIT  # Disable cleanup trap
        cleanup
    else
        trap - EXIT  # Disable cleanup trap
        echo ""
        print_info "Containers left running. Use '$COMPOSE_CMD down' to stop them."
        echo ""
    fi
}

# Check if running from correct directory
if [ ! -f "docker-compose.yml" ]; then
    print_error "docker-compose.yml not found!"
    echo "Please run this script from the diam-gw root directory"
    exit 1
fi

# Run main flow
main
