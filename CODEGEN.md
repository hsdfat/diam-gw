# Diameter Code Generator Documentation

## Overview

This document describes the Diameter protocol code generator, a tool similar to Protocol Buffers that generates Go code from declarative protocol definitions.

## Architecture

```
┌─────────────────┐
│ diameter.proto  │  Protocol definition file
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│     Parser      │  Parses proto syntax
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   Generator     │  Generates Go code
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ diameter.pb.go  │  Generated Go structs
└─────────────────┘
```

## Components

### 1. Proto File Format (`proto/diameter.proto`)

Defines AVPs and commands using a declarative syntax:

```proto
syntax = "diameter1";
package diameter.base;

avp Origin-Host {
  code = 264;
  type = DiameterIdentity;
  must = true;
  may_encrypt = false;
}

command Capabilities-Exchange-Request {
  code = 257;
  application_id = 0;
  request = true;
  proxiable = false;

  fixed required Origin-Host origin_host = 1;
  repeated optional Auth-Application-Id auth_application_id = 2;
}
```

### 2. Parser (`codegen/parser.go`)

- Reads `.proto` files
- Parses AVP and command definitions
- Builds internal representation

**Key Types:**
- `AVPDefinition`: Represents an AVP with code, type, flags
- `CommandDefinition`: Represents a command with fields
- `AVPField`: Represents a field in a command

### 3. Generator (`codegen/generator.go`)

Generates Go code with:

- **Type definitions**: `DiameterHeader`, `CommandFlags`
- **Constants**: AVP codes, Command codes
- **Structs**: One per command
- **Methods**: Marshal, Unmarshal, Len, String
- **Helper functions**: Header marshaling, AVP marshaling

### 4. Generated Code Structure

Each command generates:

```go
type CapabilitiesExchangeRequest struct {
    Header DiameterHeader

    // Required fields (non-pointer)
    OriginHost models_base.DiameterIdentity

    // Optional fields (pointer)
    OriginStateId *models_base.Unsigned32

    // Repeated fields (slice)
    AuthApplicationId []models_base.Unsigned32
}

// Constructor
func NewCapabilitiesExchangeRequest() *CapabilitiesExchangeRequest

// Serialization
func (m *CapabilitiesExchangeRequest) Marshal() ([]byte, error)
func (m *CapabilitiesExchangeRequest) Unmarshal(data []byte) error

// Utilities
func (m *CapabilitiesExchangeRequest) Len() int
func (m *CapabilitiesExchangeRequest) String() string
```

## Wire Format

### Diameter Header (20 bytes)

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|    Version    |                 Message Length                |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
| Command Flags |                  Command-Code                 |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                         Application-ID                        |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                      Hop-by-Hop Identifier                    |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                      End-to-End Identifier                    |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

**Flags:**
- R (Request): 0x80
- P (Proxiable): 0x40
- E (Error): 0x20
- T (Retransmitted): 0x10

### AVP Format

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                           AVP Code                            |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|V M P r r r r r|                  AVP Length                   |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                        Vendor-ID (opt)                        |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|    Data ...
+-+-+-+-+-+-+-+-+
```

**Flags:**
- V (Vendor): 0x80
- M (Mandatory): 0x40
- P (Protected): 0x20

**Padding:** All AVPs padded to 32-bit (4-byte) boundary

## Marshal/Unmarshal Flow

### Marshal Process

1. Create AVP list
2. For each field:
   - Serialize data using `models_base` types
   - Create AVP header (code, flags, length)
   - Add padding
3. Concatenate all AVPs
4. Create Diameter header
5. Combine header + AVPs

### Unmarshal Process

1. Parse Diameter header (20 bytes)
2. Parse AVP data:
   - Read AVP header (8 or 12 bytes)
   - Extract AVP code and length
   - Read AVP data
   - Skip padding
3. Match AVP codes to struct fields
4. Decode using appropriate decoder

## Field Modifiers

| Modifier | Effect | Go Type |
|----------|--------|---------|
| `required` | Must be present | Value (non-pointer) |
| `optional` | May be absent | Pointer |
| `repeated` | Can appear multiple times | Slice |
| `fixed` | Fixed position in ABNF | (same as above) |

## Data Type Mapping

| Proto Type | Go Type | Wire Format |
|------------|---------|-------------|
| `Unsigned32` | `models_base.Unsigned32` | 4 bytes, big-endian |
| `Unsigned64` | `models_base.Unsigned64` | 8 bytes, big-endian |
| `Integer32` | `models_base.Integer32` | 4 bytes, big-endian signed |
| `Integer64` | `models_base.Integer64` | 8 bytes, big-endian signed |
| `Float32` | `models_base.Float32` | 4 bytes, IEEE 754 |
| `Float64` | `models_base.Float64` | 8 bytes, IEEE 754 |
| `UTF8String` | `models_base.UTF8String` | Variable, UTF-8 encoded |
| `OctetString` | `models_base.OctetString` | Variable, raw bytes |
| `DiameterIdentity` | `models_base.DiameterIdentity` | Variable, FQDN string |
| `DiameterURI` | `models_base.DiameterURI` | Variable, aaa:// or aaas:// |
| `Enumerated` | `models_base.Enumerated` | 4 bytes, integer value |
| `Address` | `models_base.Address` | 2 bytes (type) + address |
| `Time` | `models_base.Time` | 4 bytes, NTP timestamp |
| `Grouped` | `models_base.Grouped` | Nested AVPs |

## Usage Examples

### Generate Code

```bash
# Using the CLI tool
go run cmd/diameter-codegen/main.go \
    -proto proto/diameter.proto \
    -output commands/base/diameter.pb.go \
    -package base

# Using Makefile
make generate
```

### Use Generated Code

```go
// Create message
cer := base.NewCapabilitiesExchangeRequest()
cer.OriginHost = models_base.DiameterIdentity("client.example.com")
cer.OriginRealm = models_base.DiameterIdentity("example.com")
cer.VendorId = models_base.Unsigned32(10415)

// Marshal to bytes
data, err := cer.Marshal()

// Unmarshal from bytes
cer2 := &base.CapabilitiesExchangeRequest{}
err = cer2.Unmarshal(data)

// Get length
length := cer.Len()
```

## Base Protocol Commands

Generated commands (RFC 6733):

| Command | Code | Request | Proxiable | Purpose |
|---------|------|---------|-----------|---------|
| CER/CEA | 257 | ✓/✗ | ✗ | Capabilities exchange |
| DWR/DWA | 280 | ✓/✗ | ✗ | Device watchdog |
| DPR/DPA | 282 | ✓/✗ | ✗ | Disconnect peer |
| RAR/RAA | 258 | ✓/✗ | ✓ | Re-authentication |
| STR/STA | 275 | ✓/✗ | ✓ | Session termination |
| ASR/ASA | 274 | ✓/✗ | ✓ | Abort session |
| ACR/ACA | 271 | ✓/✗ | ✓ | Accounting |

## Extending the Generator

### Add Custom Application

1. Create new proto file:

```proto
syntax = "diameter1";
package diameter.gx;

avp Charging-Rule-Name {
  code = 1005;
  type = OctetString;
  must = true;
  may_encrypt = false;
  vendor_id = 10415;  // 3GPP
}

command Credit-Control-Request {
  code = 272;
  application_id = 16777238;  // Gx
  request = true;
  proxiable = true;

  fixed required Session-Id session_id = 1;
  // ... more fields
}
```

2. Generate code:

```bash
go run cmd/diameter-codegen/main.go \
    -proto proto/gx.proto \
    -output commands/gx/gx.pb.go \
    -package gx
```

### Add Custom Data Type

1. Implement `models_base.Type` interface:

```go
type MyCustomType struct {
    value string
}

func (t MyCustomType) Serialize() []byte { /* ... */ }
func (t MyCustomType) Len() int { /* ... */ }
func (t MyCustomType) Padding() int { /* ... */ }
func (t MyCustomType) Type() TypeID { /* ... */ }
func (t MyCustomType) String() string { /* ... */ }
```

2. Register in `models_base.Available`

3. Add to generator type mapping

## Testing

```bash
# Run all tests
make test

# Run with coverage
make test-coverage

# Run specific test
go test -v ./commands/base -run TestCapabilitiesExchangeRequest
```

## Performance Considerations

- **Allocation**: Marshal creates new byte slices
- **Zero-copy**: Unmarshal could be optimized for zero-copy
- **Pooling**: Consider sync.Pool for frequent marshal/unmarshal
- **Validation**: Unmarshal performs basic validation only

## Future Enhancements

1. **Validation**: Add field validation in generated code
2. **Builder pattern**: Fluent API for message construction
3. **JSON mapping**: Marshal/unmarshal to/from JSON
4. **Code comments**: Include RFC references in generated code
5. **Performance**: Zero-copy unmarshal, buffer pooling
6. **Tools**: Message inspector, hex dump formatter

## References

- [RFC 6733](https://tools.ietf.org/html/rfc6733) - Diameter Base Protocol
- [RFC 3588](https://tools.ietf.org/html/rfc3588) - Diameter Base Protocol (obsolete)
- [Protocol Buffers](https://developers.google.com/protocol-buffers) - Inspiration for design
