#!/bin/bash

# Script to run 4 DRA simulators with different priorities
# Priority 1 (Primary): DRA-1 (port 3868), DRA-2 (port 3869)
# Priority 2 (Backup):  DRA-3 (port 3870), DRA-4 (port 3871)

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Functions
print_header() {
    echo -e "${BLUE}╔══════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║     Multi-DRA Setup - Priority-Based with Failover          ║${NC}"
    echo -e "${BLUE}╚══════════════════════════════════════════════════════════════╝${NC}"
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

print_section() {
    echo -e "${CYAN}$1${NC}"
}

# Check if binary exists
check_binary() {
    if [ ! -f "bin/dra-simulator" ]; then
        print_error "DRA simulator not found. Building..."
        make build-dra || exit 1
        print_success "DRA simulator built"
    else
        print_success "DRA simulator found"
    fi
}

# Kill any existing DRA processes
cleanup_existing() {
    print_info "Cleaning up existing DRA processes..."

    pkill -f "dra-simulator" 2>/dev/null || true

    # Wait for ports to be released
    sleep 1

    # Check if ports are free
    for port in 3868 3869 3870 3871; do
        if lsof -Pi :$port -sTCP:LISTEN -t >/dev/null 2>&1; then
            print_error "Port $port still in use. Force killing..."
            lsof -ti :$port | xargs kill -9 2>/dev/null || true
        fi
    done

    sleep 1
    print_success "Cleanup complete"
}

# Start a single DRA
start_dra() {
    local name=$1
    local port=$2
    local priority=$3
    local log_file="logs/dra-${name}.log"

    mkdir -p logs

    print_info "Starting ${name} on port ${port} (Priority ${priority})..."

    ./bin/dra-simulator \
        -host 127.0.0.1 \
        -port $port \
        -origin-host "dra-${name}.example.com" \
        -origin-realm "example.com" \
        -product-name "DRA-${name}/1.0" \
        -metrics-interval 15s \
        -verbose \
        > "$log_file" 2>&1 &

    local pid=$!
    echo $pid > "logs/dra-${name}.pid"

    # Wait a bit for startup
    sleep 0.5

    # Check if process started
    if ps -p $pid > /dev/null; then
        print_success "${name} started (PID: $pid, Port: $port, Priority: $priority)"
    else
        print_error "${name} failed to start"
        return 1
    fi
}

# Stop all DRAs
stop_all_dras() {
    print_section "\nStopping all DRAs..."

    for name in 1 2 3 4; do
        if [ -f "logs/dra-${name}.pid" ]; then
            pid=$(cat "logs/dra-${name}.pid")
            if ps -p $pid > /dev/null 2>&1; then
                print_info "Stopping DRA-${name} (PID: $pid)..."
                kill $pid 2>/dev/null || true
            fi
            rm -f "logs/dra-${name}.pid"
        fi
    done

    sleep 1
    print_success "All DRAs stopped"
}

# Show status
show_status() {
    print_section "\nDRA Status:"
    echo "────────────────────────────────────────────────────────────"

    for name in 1 2 3 4; do
        local port=$((3867 + name))
        local priority=1
        if [ $name -gt 2 ]; then
            priority=2
        fi

        if [ -f "logs/dra-${name}.pid" ]; then
            pid=$(cat "logs/dra-${name}.pid")
            if ps -p $pid > /dev/null 2>&1; then
                echo -e "  [DRA-${name}] ${GREEN}RUNNING${NC} - PID: $pid, Port: $port, Priority: $priority"
            else
                echo -e "  [DRA-${name}] ${RED}STOPPED${NC} - Port: $port, Priority: $priority"
            fi
        else
            echo -e "  [DRA-${name}] ${RED}NOT STARTED${NC} - Port: $port, Priority: $priority"
        fi
    done

    echo "────────────────────────────────────────────────────────────"
}

# Show help
show_help() {
    echo "Usage: $0 [command]"
    echo ""
    echo "Commands:"
    echo "  start         Start all 4 DRAs"
    echo "  stop          Stop all DRAs"
    echo "  restart       Restart all DRAs"
    echo "  status        Show DRA status"
    echo "  logs [NAME]   Show logs for DRA-NAME (1-4)"
    echo "  kill-p1       Kill Priority 1 DRAs (for failover testing)"
    echo "  start-p1      Start Priority 1 DRAs"
    echo "  help          Show this help"
    echo ""
    echo "Priority Groups:"
    echo "  Priority 1 (Primary): DRA-1 (3868), DRA-2 (3869)"
    echo "  Priority 2 (Backup):  DRA-3 (3870), DRA-4 (3871)"
}

# Main script
main() {
    local command=${1:-start}

    case $command in
        start)
            print_header
            check_binary
            echo ""
            cleanup_existing
            echo ""

            print_section "Starting DRAs with priority configuration:"
            echo "  Priority 1 (Primary): DRA-1, DRA-2"
            echo "  Priority 2 (Backup):  DRA-3, DRA-4"
            echo ""

            # Start all DRAs
            start_dra "1" 3868 1
            start_dra "2" 3869 1
            start_dra "3" 3870 2
            start_dra "4" 3871 2

            echo ""
            show_status
            echo ""
            print_success "All DRAs started successfully!"
            echo ""
            print_info "Logs are in logs/ directory"
            print_info "To view logs: tail -f logs/dra-1.log"
            print_info "To stop all DRAs: ./run-4-dras.sh stop"
            print_info "To test failover: ./run-4-dras.sh kill-p1"
            ;;

        stop)
            stop_all_dras
            ;;

        restart)
            stop_all_dras
            sleep 2
            $0 start
            ;;

        status)
            show_status
            ;;

        logs)
            local name=${2:-1}
            if [ ! -f "logs/dra-${name}.log" ]; then
                print_error "Log file for DRA-${name} not found"
                exit 1
            fi
            tail -f "logs/dra-${name}.log"
            ;;

        kill-p1)
            print_section "Killing Priority 1 DRAs (for failover testing)..."
            echo ""

            for name in 1 2; do
                if [ -f "logs/dra-${name}.pid" ]; then
                    pid=$(cat "logs/dra-${name}.pid")
                    if ps -p $pid > /dev/null 2>&1; then
                        print_info "Killing DRA-${name} (PID: $pid)..."
                        kill $pid 2>/dev/null || true
                        print_success "DRA-${name} killed"
                    fi
                fi
            done

            echo ""
            print_info "Priority 1 DRAs killed. Client should failover to Priority 2 (DRA-3, DRA-4)"
            ;;

        start-p1)
            print_section "Starting Priority 1 DRAs..."
            echo ""

            start_dra "1" 3868 1
            start_dra "2" 3869 1

            echo ""
            print_info "Priority 1 DRAs started. Client should fail back to Priority 1"
            ;;

        help|--help|-h)
            show_help
            ;;

        *)
            print_error "Unknown command: $command"
            echo ""
            show_help
            exit 1
            ;;
    esac
}

# Trap Ctrl+C
trap 'echo ""; stop_all_dras; exit 0' INT TERM

# Run main
main "$@"
