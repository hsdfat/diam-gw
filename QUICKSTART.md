# Quick Start Guide

Get started with the Diameter Code Generator in 5 minutes!

## Installation

```bash
# Clone or navigate to the project
cd diam-gw

# Verify Go is installed
go version  # Should be 1.25.3 or later
```

## Generate Code

```bash
# Generate Diameter base protocol commands
make generate

# Or manually:
go run cmd/diameter-codegen/main.go \
    -proto proto/diameter.proto \
    -output commands/base/diameter.pb.go \
    -package base
```

Output:
```
Parsed 24 AVPs and 14 commands from proto/diameter.proto
Generated code written to commands/base/diameter.pb.go
```

## Run Tests

```bash
make test
```

Output:
```
Running tests...
go test ./...
ok  	github.com/hsdfat8/diam-gw/commands/base	1.219s
ok  	github.com/hsdfat8/diam-gw/models_base	0.707s
```

## Run Example

```bash
go run examples/simple_cer_cea.go
```

## Your First Diameter Message

Create a file `my_diameter_app.go`:

```go
package main

import (
    "fmt"
    "net"

    "github.com/hsdfat8/diam-gw/commands/base"
    "github.com/hsdfat8/diam-gw/models_base"
)

func main() {
    // 1. Create a Capabilities-Exchange-Request
    cer := base.NewCapabilitiesExchangeRequest()

    // 2. Set required fields
    cer.OriginHost = models_base.DiameterIdentity("myapp.example.com")
    cer.OriginRealm = models_base.DiameterIdentity("example.com")
    cer.HostIpAddress = []models_base.Address{
        models_base.Address(net.ParseIP("192.168.1.100")),
    }
    cer.VendorId = models_base.Unsigned32(10415)  // 3GPP
    cer.ProductName = models_base.UTF8String("MyDiameterApp")

    // 3. Set Hop-by-Hop and End-to-End identifiers
    cer.Header.HopByHopID = 1
    cer.Header.EndToEndID = 1

    // 4. Marshal to wire format
    data, err := cer.Marshal()
    if err != nil {
        panic(err)
    }

    fmt.Printf("Marshaled CER message: %d bytes\n", len(data))
    fmt.Printf("Version: %d\n", cer.Header.Version)
    fmt.Printf("Command Code: %d\n", cer.Header.CommandCode)
    fmt.Printf("Application ID: %d\n", cer.Header.ApplicationID)

    // 5. Unmarshal from wire format
    cer2 := &base.CapabilitiesExchangeRequest{}
    if err := cer2.Unmarshal(data); err != nil {
        panic(err)
    }

    fmt.Printf("Received from: %s\n", cer2.OriginHost)
    fmt.Printf("Realm: %s\n", cer2.OriginRealm)
}
```

Run it:

```bash
go run my_diameter_app.go
```

## Next Steps

### 1. Explore Base Protocol Commands

The generator created these commands for you:

- **CER/CEA** - Capabilities Exchange (peer connection setup)
- **DWR/DWA** - Device Watchdog (keepalive)
- **DPR/DPA** - Disconnect Peer (graceful shutdown)
- **RAR/RAA** - Re-Authentication Request
- **STR/STA** - Session Termination Request
- **ASR/ASA** - Abort Session Request
- **ACR/ACA** - Accounting Request

See [examples/simple_cer_cea.go](examples/simple_cer_cea.go) for usage of all commands.

### 2. Create Custom Protocol Definition

Create `proto/myapp.proto`:

```proto
syntax = "diameter1";
package diameter.myapp;

// Define your custom AVPs
avp My-Custom-AVP {
  code = 10000;
  type = UTF8String;
  must = true;
  may_encrypt = false;
}

// Define your custom commands
command My-Custom-Request {
  code = 10001;
  application_id = 999999;
  request = true;
  proxiable = true;

  fixed required Session-Id session_id = 1;
  fixed required Origin-Host origin_host = 2;
  fixed required Origin-Realm origin_realm = 3;
  optional My-Custom-AVP my_custom_avp = 4;
}
```

Generate:

```bash
mkdir -p commands/myapp
go run cmd/diameter-codegen/main.go \
    -proto proto/myapp.proto \
    -output commands/myapp/myapp.pb.go \
    -package myapp
```

### 3. Read the Documentation

- [README.md](README.md) - Full project documentation
- [CODEGEN.md](CODEGEN.md) - Code generator architecture
- [diameter_base_commands.txt](diameter_base_commands.txt) - Protocol details
- [rfc3588_diameter_summary.txt](rfc3588_diameter_summary.txt) - RFC summary

## Common Tasks

### Build the Code Generator Binary

```bash
make build
# Binary will be in bin/diameter-codegen
```

### Run Tests with Coverage

```bash
make test-coverage
# Opens coverage.html in browser
```

### Format Code

```bash
make fmt
```

### Clean Generated Files

```bash
make clean
```

## Troubleshooting

### Build fails with "module not found"

```bash
go mod tidy
```

### Generated code has compilation errors

Make sure your proto file syntax is correct:
- All AVPs referenced in commands must be defined
- Field names use snake_case
- Type names match exactly (case-sensitive)

### Tests fail

```bash
# Clean and regenerate
make clean
make generate
make test
```

## Tips

1. **Use the Makefile** - It handles all common tasks
2. **Check examples/** - Real working code for reference
3. **Start simple** - Begin with basic CER/CEA before complex commands
4. **Test often** - Run `make test` after changes
5. **Read generated code** - Understanding output helps debug issues

## Example Commands

```bash
# Full clean build and test
make clean && make all

# Generate and run example
make generate && go run examples/simple_cer_cea.go

# Build standalone tool
make build
./bin/diameter-codegen -proto proto/diameter.proto -output out.go -package mypackage

# Install to GOPATH
make install
diameter-codegen -help
```

## What's Generated?

For each command, you get:

```go
// Constructor
cer := base.NewCapabilitiesExchangeRequest()

// Marshal to bytes
data, err := cer.Marshal()

// Unmarshal from bytes
err := cer.Unmarshal(data)

// Get message length
length := cer.Len()

// String representation
str := cer.String()
```

## Success!

You now have:
- ✅ A working Diameter code generator
- ✅ Generated Go code for all base protocol commands
- ✅ Tests verifying marshal/unmarshal works correctly
- ✅ Example code showing how to use it

Start building your Diameter application!
