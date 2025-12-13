#!/bin/bash

# Verification script for multi-DRA setup

set -e

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

print_check() {
    if [ $1 -eq 0 ]; then
        echo -e "${GREEN}✓${NC} $2"
    else
        echo -e "${RED}✗${NC} $2"
        exit 1
    fi
}

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║          Multi-DRA Setup Verification                        ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# Check binaries
echo "Checking binaries..."
[ -f "bin/dra-simulator" ]
print_check $? "DRA simulator binary exists"

[ -f "bin/multi-dra-test" ]
print_check $? "Multi-DRA test client binary exists"

# Check scripts
echo ""
echo "Checking scripts..."
[ -f "run-4-dras.sh" ] && [ -x "run-4-dras.sh" ]
print_check $? "run-4-dras.sh exists and is executable"

# Check source files
echo ""
echo "Checking source files..."
[ -f "client/dra_pool.go" ]
print_check $? "client/dra_pool.go exists"

[ -f "examples/multi_dra_test/main.go" ]
print_check $? "examples/multi_dra_test/main.go exists"

# Check documentation
echo ""
echo "Checking documentation..."
[ -f "MULTI_DRA_QUICKSTART.md" ]
print_check $? "MULTI_DRA_QUICKSTART.md exists"

[ -f "MULTI_DRA_TESTING.md" ]
print_check $? "MULTI_DRA_TESTING.md exists"

[ -f "SUMMARY.md" ]
print_check $? "SUMMARY.md exists"

# Check line counts
echo ""
echo "Verifying code size..."
lines=$(wc -l < client/dra_pool.go | tr -d ' ')
if [ "$lines" -gt 500 ]; then
    print_check 0 "client/dra_pool.go has $lines lines (expected >500)"
else
    print_check 1 "client/dra_pool.go has insufficient lines: $lines"
fi

# Summary
echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║                  ✅ Verification Complete!                    ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""
echo "Ready to test:"
echo "  1. ./run-4-dras.sh start"
echo "  2. ./bin/multi-dra-test"
echo "  3. ./run-4-dras.sh kill-p1   (test failover)"
echo "  4. ./run-4-dras.sh start-p1  (test fail-back)"
echo ""
