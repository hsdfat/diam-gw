.PHONY: all generate test clean build install build-dra build-examples

# Default target
all: generate test

# Generate code from proto files
generate:
	@echo "Generating Diameter command code from all proto files..."
	@echo "This will create package folders based on proto file headers"
	go run cmd/diameter-codegen/main.go \
		-proto-dir proto \
		-output-dir commands
	@echo "Code generation complete!"

# Generate code with tests from all proto files
generate-with-tests:
	@echo "Generating Diameter command code and tests from all proto files..."
	@echo "This will create package folders based on proto file headers"
	go run cmd/diameter-codegen/main.go \
		-proto-dir proto \
		-output-dir commands \
		-tests
	@echo "Code and test generation complete!"

# Generate code, tests, and PCAP files
generate-with-pcaps:
	@echo "Generating Diameter command code, tests, and PCAP files..."
	@echo "This will create package folders based on proto file headers"
	go run cmd/diameter-codegen/main.go \
		-proto-dir proto \
		-output-dir commands \
		-tests
	@echo "Generating PCAP reference files..."
	@go test ./commands/base -run "PCAP" -v > /dev/null 2>&1 || true
	@go test ./commands/s13 -run "PCAP" -v > /dev/null 2>&1 || true
	@go test ./commands/s6a -run "PCAP" -v > /dev/null 2>&1 || true
	@echo "PCAP files generated in commands/*/testdata/"
	@echo "Generation complete!"

# Generate code from a specific proto file (for development/testing)
generate-single:
	@echo "Generating code from single proto file..."
	@if [ -z "$(PROTO)" ]; then \
		echo "Error: PROTO variable not set. Usage: make generate-single PROTO=proto/diameter.proto"; \
		exit 1; \
	fi
	go run cmd/diameter-codegen/main.go \
		-proto $(PROTO) \
		-output-dir commands
	@echo "Code generation complete!"

# Generate code with tests from a specific proto file
generate-single-with-tests:
	@echo "Generating code and tests from single proto file..."
	@if [ -z "$(PROTO)" ]; then \
		echo "Error: PROTO variable not set. Usage: make generate-single-with-tests PROTO=proto/diameter.proto"; \
		exit 1; \
	fi
	go run cmd/diameter-codegen/main.go \
		-proto $(PROTO) \
		-output-dir commands \
		-tests
	@echo "Code and test generation complete!"

# Run tests
test:
	@echo "Running tests..."
	go test ./...

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Build the code generator
build:
	@echo "Building diameter-codegen..."
	go build -o bin/diameter-codegen cmd/diameter-codegen/main.go
	@echo "Built: bin/diameter-codegen"

# Build DRA simulator
build-dra:
	@echo "Building DRA simulator..."
	@mkdir -p bin
	go build -o bin/dra-simulator simulator/dra/*.go
	@echo "Built: bin/dra-simulator"
	@echo ""
	@echo "Usage: bin/dra-simulator [options]"
	@echo "  -host string         DRA listening host (default \"0.0.0.0\")"
	@echo "  -port int            DRA listening port (default 3868)"
	@echo "  -origin-host string  DRA Origin-Host (default \"dra.example.com\")"
	@echo "  -origin-realm string DRA Origin-Realm (default \"example.com\")"
	@echo "  -verbose             Enable verbose logging"

# Build all examples
build-examples:
	@echo "Building examples..."
	@mkdir -p bin
	@echo "Building basic example..."
	go build -o bin/basic-example examples/basic/*.go
	@echo "Building S13 client example..."
	go build -o bin/s13-client examples/s13_client/*.go
	@echo "Building simple pool example..."
	go build -o bin/simple-pool examples/simple_pool/*.go
	@echo "All examples built in bin/"

# Install the code generator
install:
	@echo "Installing diameter-codegen..."
	go install cmd/diameter-codegen/main.go
	@echo "Installed to GOPATH/bin"

# Clean generated files
clean:
	@echo "Cleaning generated files..."
	rm -rf commands/base commands/s6a commands/s13
	rm -f bin/diameter-codegen bin/dra-simulator
	rm -f bin/basic-example bin/s13-client bin/simple-pool
	rm -f coverage.out coverage.html
	rm -rf testdata
	rm -rf **/testdata
	@echo "Clean complete!"

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...

# Run linter
lint:
	@echo "Running linter..."
	golangci-lint run || true

# Show help
help:
	@echo "Diameter Gateway - Code Generator"
	@echo ""
	@echo "Available targets:"
	@echo "  all                        - Generate code and run tests (default)"
	@echo "  generate                   - Generate Go code from all proto files in proto/"
	@echo "  generate-with-tests        - Generate Go code AND unit tests from all proto files"
	@echo "  generate-with-pcaps        - Generate code, tests, AND reference PCAP files"
	@echo "  generate-single            - Generate code from specific proto file (use PROTO=path)"
	@echo "                               Example: make generate-single PROTO=proto/diameter.proto"
	@echo "  generate-single-with-tests - Generate code and tests from specific proto file"
	@echo "  test                       - Run all tests"
	@echo "  test-coverage              - Run tests with coverage report"
	@echo "  build                      - Build the code generator binary"
	@echo "  build-dra                  - Build DRA simulator binary"
	@echo "  build-examples             - Build all example binaries"
	@echo "  install                    - Install code generator to GOPATH/bin"
	@echo "  clean                      - Remove generated files and binaries"
	@echo "  fmt                        - Format Go code"
	@echo "  lint                       - Run linter"
	@echo "  help                       - Show this help message"
	@echo ""
	@echo "Test Generation:"
	@echo "  Generated tests include:"
	@echo "    - Unit tests for message creation and initialization"
	@echo "    - Validation tests for required fields"
	@echo "    - Marshal/Unmarshal roundtrip tests"
	@echo "    - PCAP file generation tests (for Wireshark analysis)"
	@echo ""
	@echo "PCAP File Generation:"
	@echo "  Use 'make generate-with-pcaps' to create reference PCAP files"
	@echo "  PCAP files are saved to commands/*/testdata/ directories"
	@echo "  These files can be opened in Wireshark for protocol analysis"
	@echo "  Files are pre-generated and can be committed to version control"
	@echo ""
	@echo "DRA Simulator:"
	@echo "  Use 'make build-dra' to build the DRA simulator"
	@echo "  Run with: bin/dra-simulator -help for options"
