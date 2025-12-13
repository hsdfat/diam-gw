#!/bin/bash

# Self-Test Script for DWR Testing Framework
# Validates that all components are working correctly

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# Configuration
COMPOSE_CMD="${COMPOSE_CMD:-podman-compose}"
COMPOSE_FILE="docker-compose-dwr-test.yml"

# Test results
TESTS_PASSED=0
TESTS_FAILED=0
TESTS_TOTAL=0

print_header() {
    echo -e "${BLUE}╔══════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║         DWR Testing Framework - Self Check                  ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════════════════╝${NC}"
    echo ""
}

print_test() {
    echo -e "${CYAN}▶ Test $1: $2${NC}"
}

pass() {
    echo -e "  ${GREEN}✓ PASS${NC} $1"
    ((TESTS_PASSED++))
    ((TESTS_TOTAL++))
}

fail() {
    echo -e "  ${RED}✗ FAIL${NC} $1"
    ((TESTS_FAILED++))
    ((TESTS_TOTAL++))
}

info() {
    echo -e "  ${YELLOW}ℹ${NC} $1"
}

# Test 1: Check prerequisites
test_prerequisites() {
    print_test 1 "Prerequisites Check"

    # Check compose command
    if command -v $COMPOSE_CMD &> /dev/null; then
        local version=$($COMPOSE_CMD version 2>&1 | head -1)
        pass "$COMPOSE_CMD found: $version"
    else
        fail "$COMPOSE_CMD not found"
        info "Install: pip install podman-compose"
    fi

    # Check podman
    if command -v podman &> /dev/null; then
        local version=$(podman --version)
        pass "Podman found: $version"
    else
        fail "Podman not found"
    fi

    # Check Go
    if command -v go &> /dev/null; then
        local version=$(go version)
        pass "Go found: $version"
    else
        fail "Go not found"
    fi

    echo ""
}

# Test 2: Check files exist
test_files_exist() {
    print_test 2 "File Existence Check"

    local files=(
        "docker-compose-dwr-test.yml"
        "test-dwr-failover.sh"
        "test-scenario-template.sh"
        "test-scenario-gradual-degradation.sh"
        "test-scenario-rapid-recovery.sh"
        "DWR_TESTING_GUIDE.md"
        "QUICKSTART_DWR_TESTS.md"
        "Makefile"
    )

    for file in "${files[@]}"; do
        if [ -f "$file" ]; then
            pass "$file exists"
        else
            fail "$file missing"
        fi
    done

    echo ""
}

# Test 3: Check scripts are executable
test_executables() {
    print_test 3 "Script Executability Check"

    local scripts=(
        "test-dwr-failover.sh"
        "test-scenario-template.sh"
        "test-scenario-gradual-degradation.sh"
        "test-scenario-rapid-recovery.sh"
    )

    for script in "${scripts[@]}"; do
        if [ -x "$script" ]; then
            pass "$script is executable"
        else
            fail "$script is not executable"
            info "Run: chmod +x $script"
        fi
    done

    echo ""
}

# Test 4: Check code compiles
test_code_compilation() {
    print_test 4 "Code Compilation Check"

    # Test client code
    if go build ./client/... 2>/dev/null; then
        pass "Client code compiles"
    else
        fail "Client code compilation failed"
    fi

    # Test simulator code
    if [ -d "simulator/dra" ]; then
        if go build ./simulator/dra/... 2>/dev/null; then
            pass "DRA simulator code compiles"
        else
            fail "DRA simulator compilation failed"
        fi
    else
        info "DRA simulator directory not found"
    fi

    echo ""
}

# Test 5: Check Go tests pass
test_go_tests() {
    print_test 5 "Go Unit Tests Check"

    if go test ./client/... -v > /tmp/go-test-output.log 2>&1; then
        pass "Client unit tests pass"
        local test_count=$(grep -c "PASS:" /tmp/go-test-output.log || echo "0")
        info "Tests passed: $test_count"
    else
        fail "Client unit tests failed"
        info "Check /tmp/go-test-output.log for details"
    fi

    echo ""
}

# Test 6: Validate compose file
test_compose_file() {
    print_test 6 "Compose File Validation"

    if $COMPOSE_CMD -f "$COMPOSE_FILE" config > /dev/null 2>&1; then
        pass "Compose file is valid"
    else
        fail "Compose file validation failed"
        info "Run: $COMPOSE_CMD -f $COMPOSE_FILE config"
    fi

    # Check services defined
    local services=$($COMPOSE_CMD -f "$COMPOSE_FILE" config --services 2>/dev/null | wc -l)
    if [ "$services" -ge 5 ]; then
        pass "All services defined (count: $services)"
    else
        fail "Missing services (found: $services, expected: 5+)"
    fi

    echo ""
}

# Test 7: Test build (without running)
test_build() {
    print_test 7 "Container Build Test"

    info "This will build container images (may take a few minutes)..."

    if $COMPOSE_CMD -f "$COMPOSE_FILE" build > /tmp/compose-build.log 2>&1; then
        pass "Container images built successfully"
    else
        fail "Container build failed"
        info "Check /tmp/compose-build.log for details"
    fi

    echo ""
}

# Test 8: Quick smoke test
test_smoke_test() {
    print_test 8 "Quick Smoke Test"

    info "Starting services briefly to verify they work..."

    # Start services
    if $COMPOSE_CMD -f "$COMPOSE_FILE" up -d > /tmp/compose-up.log 2>&1; then
        pass "Services started"
    else
        fail "Failed to start services"
        info "Check /tmp/compose-up.log"
        return
    fi

    # Wait a bit
    sleep 10

    # Check if services are running
    local running=$($COMPOSE_CMD -f "$COMPOSE_FILE" ps | grep -c "Up" || echo "0")
    if [ "$running" -ge 4 ]; then
        pass "DRA services are running (count: $running)"
    else
        fail "Not all services running (found: $running)"
    fi

    # Check logs for errors
    if $COMPOSE_CMD -f "$COMPOSE_FILE" logs 2>&1 | grep -qi "panic\|fatal error"; then
        fail "Found panic/fatal errors in logs"
    else
        pass "No critical errors in logs"
    fi

    # Cleanup
    info "Cleaning up test services..."
    $COMPOSE_CMD -f "$COMPOSE_FILE" down > /dev/null 2>&1

    echo ""
}

# Test 9: Check Makefile targets
test_makefile_targets() {
    print_test 9 "Makefile Targets Check"

    local targets=(
        "test-compose"
        "test-dwr-failover"
        "test-dwr-all"
        "compose-up"
        "compose-down"
        "compose-logs"
    )

    for target in "${targets[@]}"; do
        if grep -q "^${target}:" Makefile; then
            pass "Target '$target' exists"
        else
            fail "Target '$target' missing"
        fi
    done

    echo ""
}

# Test 10: Check documentation
test_documentation() {
    print_test 10 "Documentation Check"

    # Check DWR_TESTING_GUIDE.md
    if [ -f "DWR_TESTING_GUIDE.md" ]; then
        local lines=$(wc -l < DWR_TESTING_GUIDE.md)
        if [ "$lines" -gt 100 ]; then
            pass "DWR_TESTING_GUIDE.md complete ($lines lines)"
        else
            fail "DWR_TESTING_GUIDE.md too short ($lines lines)"
        fi
    fi

    # Check QUICKSTART
    if [ -f "QUICKSTART_DWR_TESTS.md" ]; then
        if grep -q "Quick Start" QUICKSTART_DWR_TESTS.md; then
            pass "QUICKSTART_DWR_TESTS.md has content"
        else
            fail "QUICKSTART_DWR_TESTS.md incomplete"
        fi
    fi

    echo ""
}

# Summary
print_summary() {
    echo -e "${BLUE}╔══════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║                    Test Summary                              ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo "Total Tests:  $TESTS_TOTAL"
    echo -e "Passed:       ${GREEN}$TESTS_PASSED${NC}"
    echo -e "Failed:       ${RED}$TESTS_FAILED${NC}"
    echo ""

    if [ $TESTS_FAILED -eq 0 ]; then
        echo -e "${GREEN}╔══════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${GREEN}║              ✓ ALL TESTS PASSED!                             ║${NC}"
        echo -e "${GREEN}╚══════════════════════════════════════════════════════════════╝${NC}"
        echo ""
        echo "The DWR testing framework is ready to use!"
        echo ""
        echo "Next steps:"
        echo "  1. Run quick test:    COMPOSE_CMD=podman-compose ./test-dwr-failover.sh test1"
        echo "  2. Run all tests:     COMPOSE_CMD=podman-compose make test-compose"
        echo "  3. Read the guide:    cat DWR_TESTING_GUIDE.md"
        echo ""
        return 0
    else
        echo -e "${RED}╔══════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${RED}║              ✗ SOME TESTS FAILED                             ║${NC}"
        echo -e "${RED}╚══════════════════════════════════════════════════════════════╝${NC}"
        echo ""
        echo "Please fix the failed tests before proceeding."
        echo "Check the output above for details."
        echo ""
        return 1
    fi
}

# Main
main() {
    print_header

    echo "Running self-check with: $COMPOSE_CMD"
    echo ""

    test_prerequisites
    test_files_exist
    test_executables
    test_code_compilation
    test_go_tests
    test_compose_file
    test_build
    test_smoke_test
    test_makefile_targets
    test_documentation

    print_summary
}

# Run
main "$@"
