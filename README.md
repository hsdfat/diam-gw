# Diameter Gateway - Code Generator

A Protocol Buffers-like code generator for Diameter protocol commands and AVPs in Go.

## Overview

This project provides a code generation system for Diameter base protocol commands, similar to how Protocol Buffers generates code from `.proto` files. It allows you to define Diameter commands and AVPs in a declarative format and automatically generates Go structs with Marshal/Unmarshal methods.

## Features

- **Proto-like syntax** for defining Diameter commands and AVPs
- **Automatic code generation** with Marshal/Unmarshal methods
- **Type-safe** Go structs for all Diameter commands
- **Complete base protocol support** (CER/CEA, DWR/DWA, DPR/DPA, RAR/RAA, STR/STA, ASR/ASA, ACR/ACA)
- **Built-in length calculation** and padding
- **Round-trip serialization** guaranteed

## Project Structure

```
diam-gw/
├── proto/                      # Protocol definition files
│   └── diameter.proto         # Base protocol definitions
├── codegen/                   # Code generator package
│   ├── avp.go                # AVP structures
│   ├── command.go            # Command structures
│   ├── parser.go             # Proto file parser
│   └── generator.go          # Code generator
├── cmd/
│   └── diameter-codegen/     # Code generator CLI
│       └── main.go
├── commands/                  # Generated command code
│   └── base/
│       ├── diameter.pb.go    # Generated from proto
│       └── diameter_test.go  # Tests for generated code
└── models_base/               # Base data types
    ├── datatype.go
    ├── unsigned32.go
    ├── utf8string.go
    └── ...
```

## Getting Started

### 1. Define Your Protocol

Create a `.proto` file with Diameter AVP and command definitions:

```proto
syntax = "diameter1";

package diameter.base;

option go_package = "github.com/hsdfat8/diam-gw/commands/base";

// Define AVPs
avp Origin-Host {
  code = 264;
  type = DiameterIdentity;
  must = true;
  may_encrypt = false;
}

// Define Commands
command Capabilities-Exchange-Request {
  code = 257;
  application_id = 0;
  request = true;
  proxiable = false;

  fixed required Origin-Host origin_host = 1;
  fixed required Origin-Realm origin_realm = 2;
  repeated required Host-IP-Address host_ip_address = 3;
  // ... more fields
}
```

### 2. Generate Code

Run the code generator:

```bash
go run cmd/diameter-codegen/main.go \
  -proto proto/diameter.proto \
  -output commands/base/diameter.pb.go \
  -package base
```

### 3. Use Generated Code

```go
package main

import (
    "net"
    "github.com/hsdfat8/diam-gw/commands/base"
    "github.com/hsdfat8/diam-gw/models_base"
)

func main() {
    // Create a Capabilities-Exchange-Request
    cer := base.NewCapabilitiesExchangeRequest()

    // Set required fields
    cer.OriginHost = models_base.DiameterIdentity("client.example.com")
    cer.OriginRealm = models_base.DiameterIdentity("example.com")
    cer.HostIpAddress = []models_base.Address{
        models_base.Address(net.ParseIP("127.0.0.1")),
    }
    cer.VendorId = models_base.Unsigned32(10415) // 3GPP
    cer.ProductName = models_base.UTF8String("MyApp")

    // Set optional fields
    cer.AuthApplicationId = []models_base.Unsigned32{
        models_base.Unsigned32(16777238), // Gx
    }

    // Set identifiers
    cer.Header.HopByHopID = 1
    cer.Header.EndToEndID = 1

    // Marshal to bytes
    data, err := cer.Marshal()
    if err != nil {
        panic(err)
    }

    // Unmarshal from bytes
    cer2 := &base.CapabilitiesExchangeRequest{}
    if err := cer2.Unmarshal(data); err != nil {
        panic(err)
    }

    // Get message length
    length := cer.Len()

    // Get string representation
    str := cer.String()
}
```

## Proto File Syntax

### AVP Definition

```proto
avp AVP-Name {
  code = <uint32>;              // AVP code
  type = <TypeName>;            // Data type
  must = <bool>;                // M-bit (mandatory)
  may_encrypt = <bool>;         // P-bit (protected)
  vendor_id = <uint32>;         // Optional vendor ID
}
```

**Supported Types:**
- `Unsigned32`, `Unsigned64`
- `Integer32`, `Integer64`
- `Float32`, `Float64`
- `OctetString`
- `UTF8String`
- `DiameterIdentity`
- `DiameterURI`
- `Enumerated`
- `Address`
- `Time`
- `IPFilterRule`
- `QoSFilterRule`
- `Grouped`

### Command Definition

```proto
command Command-Name {
  code = <uint32>;              // Command code
  application_id = <uint32>;    // Application ID
  request = <bool>;             // Request or Answer
  proxiable = <bool>;           // P-bit in command flags

  // Field definitions
  fixed required AVP-Name field_name = 1;
  repeated optional AVP-Name field_name = 2;
  optional AVP-Name field_name = 3;
}
```

**Field Modifiers:**
- `fixed` - Fixed position in ABNF
- `repeated` - Can appear multiple times (generates slice)
- `required` - Must be present (generates non-pointer)
- `optional` - May be absent (generates pointer)

## Base Protocol Commands

The following Diameter base protocol commands are included:

| Command | Code | Abbreviation | Description |
|---------|------|--------------|-------------|
| Capabilities-Exchange-Request | 257 | CER | Peer connection establishment |
| Capabilities-Exchange-Answer | 257 | CEA | CER response |
| Device-Watchdog-Request | 280 | DWR | Keepalive mechanism |
| Device-Watchdog-Answer | 280 | DWA | DWR response |
| Disconnect-Peer-Request | 282 | DPR | Graceful disconnect |
| Disconnect-Peer-Answer | 282 | DPA | DPR response |
| Re-Auth-Request | 258 | RAR | Server-initiated re-auth |
| Re-Auth-Answer | 258 | RAA | RAR response |
| Session-Termination-Request | 275 | STR | Client session termination |
| Session-Termination-Answer | 275 | STA | STR response |
| Abort-Session-Request | 274 | ASR | Server session abort |
| Abort-Session-Answer | 274 | ASA | ASR response |
| Accounting-Request | 271 | ACR | Accounting record |
| Accounting-Answer | 271 | ACA | ACR response |

## Generated Code Features

Each generated command struct includes:

- **Constructor**: `New<CommandName>()` - Creates new message with default header
- **Marshal()**: `([]byte, error)` - Serializes to Diameter wire format
- **Unmarshal()**: `([]byte) error` - Deserializes from bytes
- **Len()**: `int` - Returns total message length
- **String()**: `string` - Human-readable representation

### Example Generated Struct

```go
type CapabilitiesExchangeRequest struct {
    Header DiameterHeader

    OriginHost                  models_base.DiameterIdentity // Required
    OriginRealm                 models_base.DiameterIdentity // Required
    HostIpAddress               []models_base.Address        // Required
    VendorId                    models_base.Unsigned32       // Required
    ProductName                 models_base.UTF8String       // Required
    OriginStateId               *models_base.Unsigned32      // Optional
    SupportedVendorId           []models_base.Unsigned32     // Optional
    AuthApplicationId           []models_base.Unsigned32     // Optional
    // ... more fields
}
```

## Diameter Header Structure

The generated code includes proper Diameter header handling:

```go
type DiameterHeader struct {
    Version       uint8        // Must be 1
    Length        uint32       // Total message length (3 bytes)
    Flags         CommandFlags // Command flags
    CommandCode   uint32       // Command code (3 bytes)
    ApplicationID uint32       // Application ID (4 bytes)
    HopByHopID    uint32       // Request/answer matching
    EndToEndID    uint32       // Duplicate detection
}

type CommandFlags struct {
    Request       bool // R-bit
    Proxiable     bool // P-bit
    Error         bool // E-bit
    Retransmitted bool // T-bit
}
```

## Testing

Run tests for generated code:

```bash
cd commands/base
go test -v
```

All generated code includes comprehensive tests for:
- Marshal/Unmarshal round-trip
- Field value preservation
- Header flag handling
- Message length calculation
- String representation

## Advanced Usage

### Creating Custom Applications

You can extend the base protocol by creating your own `.proto` files:

```proto
syntax = "diameter1";

package diameter.gx;

option go_package = "github.com/hsdfat8/diam-gw/commands/gx";

// Define application-specific AVPs
avp Rating-Group {
  code = 432;
  type = Unsigned32;
  must = true;
  may_encrypt = false;
}

// Define application-specific commands
command Credit-Control-Request {
  code = 272;
  application_id = 16777238; // Gx
  request = true;
  proxiable = true;

  // Include base AVPs and application AVPs
  fixed required Session-Id session_id = 1;
  fixed required Origin-Host origin_host = 2;
  // ... more fields
}
```

### Extending Data Types

The code generator uses the `models_base` package for AVP data types. You can add custom types by:

1. Implementing the `models_base.Type` interface
2. Adding the type to `models_base.Available` map
3. Using it in your proto definitions

## References

- **RFC 3588**: Diameter Base Protocol (obsoleted by RFC 6733)
- **RFC 6733**: Diameter Base Protocol
- [diameter_base_commands.txt](diameter_base_commands.txt) - Detailed command descriptions
- [rfc3588_diameter_summary.txt](rfc3588_diameter_summary.txt) - Protocol summary

## License

This project is part of the Diameter Gateway implementation.
