#!/bin/bash
# Example script to run the DRA simulator with different configurations

echo "=== DRA Simulator Examples ==="
echo ""

# Example 1: Basic usage with default settings
echo "Example 1: Run with default settings (Ctrl+C to stop)"
echo "Command: ./dra-simulator"
echo ""

# Example 2: Custom port and debug logging
echo "Example 2: Run on custom port with debug logging"
echo "Command: ./dra-simulator -listen '0.0.0.0:13868' -log-level debug"
echo ""

# Example 3: Custom identity
echo "Example 3: Run with custom Diameter identity"
echo "Command: ./dra-simulator \\"
echo "  -origin-host 'my-dra.telco.com' \\"
echo "  -origin-realm 'telco.com' \\"
echo "  -product-name 'My-DRA' \\"
echo "  -vendor-id 12345"
echo ""

# Example 4: Fast watchdog with frequent statistics
echo "Example 4: Run with fast watchdog and frequent stats"
echo "Command: ./dra-simulator \\"
echo "  -dwr-interval 10s \\"
echo "  -stats-interval 20s \\"
echo "  -log-level info"
echo ""

# Example 5: Production-like settings
echo "Example 5: Production-like settings"
echo "Command: ./dra-simulator \\"
echo "  -listen '0.0.0.0:3868' \\"
echo "  -origin-host 'dra01.prod.example.com' \\"
echo "  -origin-realm 'prod.example.com' \\"
echo "  -product-name 'Production-DRA' \\"
echo "  -dwr-interval 30s \\"
echo "  -stats-interval 300s \\"
echo "  -log-level warn"
echo ""

# Prompt for which example to run
echo "Which example would you like to run? (1-5, or press Enter to exit)"
read -r choice

case $choice in
    1)
        ./dra-simulator
        ;;
    2)
        ./dra-simulator -listen "0.0.0.0:13868" -log-level debug
        ;;
    3)
        ./dra-simulator \
            -origin-host "my-dra.telco.com" \
            -origin-realm "telco.com" \
            -product-name "My-DRA" \
            -vendor-id 12345
        ;;
    4)
        ./dra-simulator \
            -dwr-interval 10s \
            -stats-interval 20s \
            -log-level info
        ;;
    5)
        ./dra-simulator \
            -listen "0.0.0.0:3868" \
            -origin-host "dra01.prod.example.com" \
            -origin-realm "prod.example.com" \
            -product-name "Production-DRA" \
            -dwr-interval 30s \
            -stats-interval 300s \
            -log-level warn
        ;;
    *)
        echo "Exiting..."
        exit 0
        ;;
esac
