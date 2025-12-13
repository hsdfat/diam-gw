#!/bin/bash

# DRA Simulator Test Script
# This script helps you quickly test the DRA simulator with the client

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Functions
print_header() {
    echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║         DRA Simulator Test Helper                         ║${NC}"
    echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
    echo ""
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

# Check if binaries exist
check_binaries() {
    if [ ! -f "bin/dra-simulator" ]; then
        print_error "DRA simulator not found. Building..."
        make build-dra || exit 1
        print_success "DRA simulator built"
    else
        print_success "DRA simulator found"
    fi

    if [ ! -f "bin/test-with-dra" ]; then
        print_error "Test client not found. Building..."
        make build-examples || exit 1
        print_success "Test client built"
    else
        print_success "Test client found"
    fi
}

# Start DRA in background
start_dra() {
    print_info "Starting DRA simulator on port 3868..."
    ./bin/dra-simulator -verbose &
    DRA_PID=$!
    echo $DRA_PID > /tmp/dra-simulator.pid

    # Wait for DRA to start
    sleep 2

    if ps -p $DRA_PID > /dev/null; then
        print_success "DRA simulator started (PID: $DRA_PID)"
    else
        print_error "Failed to start DRA simulator"
        exit 1
    fi
}

# Stop DRA
stop_dra() {
    if [ -f /tmp/dra-simulator.pid ]; then
        DRA_PID=$(cat /tmp/dra-simulator.pid)
        if ps -p $DRA_PID > /dev/null 2>&1; then
            print_info "Stopping DRA simulator (PID: $DRA_PID)..."
            kill $DRA_PID
            sleep 1
            print_success "DRA simulator stopped"
        fi
        rm -f /tmp/dra-simulator.pid
    fi
}

# Run test client
run_test_client() {
    print_info "Starting test client..."
    echo ""
    ./bin/test-with-dra
}

# Cleanup on exit
cleanup() {
    echo ""
    print_info "Cleaning up..."
    stop_dra
    print_success "Done"
}

# Main script
main() {
    print_header

    # Setup cleanup trap
    trap cleanup EXIT INT TERM

    # Check/build binaries
    check_binaries
    echo ""

    # Start DRA
    start_dra
    echo ""

    # Run test client
    run_test_client
}

# Run main
main
