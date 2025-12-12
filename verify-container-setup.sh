#!/bin/bash

# Quick verification script for container setup

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

check_file() {
    if [ -f "$1" ]; then
        echo -e "${GREEN}✓${NC} $1"
        return 0
    else
        echo -e "${RED}✗${NC} $1 (missing)"
        return 1
    fi
}

check_executable() {
    if [ -x "$1" ]; then
        echo -e "${GREEN}✓${NC} $1 (executable)"
        return 0
    else
        echo -e "${RED}✗${NC} $1 (not executable)"
        return 1
    fi
}

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║        Container Setup Verification                          ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

echo "Core Container Files:"
check_file "Dockerfile.dra"
check_file "Dockerfile.client"
check_file "docker-compose.yml"
echo ""

echo "Application Code:"
check_file "examples/multi_dra_test_container/main.go"
echo ""

echo "Scripts:"
check_executable "podman-flow.sh"
check_executable "run-4-dras.sh"
check_executable "verify-multi-dra.sh"
echo ""

echo "Documentation:"
check_file "CONTAINER_SETUP.md"
check_file "QUICKSTART_CONTAINERS.md"
check_file "CONTAINERIZATION_SUMMARY.md"
check_file "CONTAINER_CHECKLIST.md"
echo ""

echo "Related Documentation:"
check_file "MULTI_DRA_QUICKSTART.md"
check_file "MULTI_DRA_TESTING.md"
check_file "SUMMARY.md"
echo ""

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║                 Ready to Test!                                ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""
echo "Quick Start Commands:"
echo "  Automated test:  ./podman-flow.sh"
echo "  Manual test:     podman-compose up -d"
echo "  View logs:       podman-compose logs -f client"
echo "  Stop all:        podman-compose down"
echo ""
