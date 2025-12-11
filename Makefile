.PHONY: all generate test clean build install

# Default target
all: generate test

# Generate code from proto files
generate:
	@echo "Generating Diameter command code..."
	go run cmd/diameter-codegen/main.go \
		-proto proto/diameter.proto \
		-output commands/base/diameter.pb.go \
		-package base
	@echo "Code generation complete!"

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

# Install the code generator
install:
	@echo "Installing diameter-codegen..."
	go install cmd/diameter-codegen/main.go
	@echo "Installed to GOPATH/bin"

# Clean generated files
clean:
	@echo "Cleaning generated files..."
	rm -f commands/base/diameter.pb.go
	rm -f bin/diameter-codegen
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
	@echo "  all            - Generate code and run tests (default)"
	@echo "  generate       - Generate Go code from proto files"
	@echo "  test           - Run all tests"
	@echo "  test-coverage  - Run tests with coverage report"
	@echo "  build          - Build the code generator binary"
	@echo "  install        - Install code generator to GOPATH/bin"
	@echo "  clean          - Remove generated files"
	@echo "  fmt            - Format Go code"
	@echo "  lint           - Run linter"
	@echo "  help           - Show this help message"
