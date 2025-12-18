package codegen

import "github.com/hsdfat/diam-gw/models_base"

// AVPDefinition represents an AVP definition from the proto file
type AVPDefinition struct {
	Name          string // e.g., "Origin-Host"
	Code          uint32
	Type          models_base.TypeID
	TypeName      string      // e.g., "DiameterIdentity"
	Must          bool        // M-bit (Mandatory)
	MayEncrypt    bool        // P-bit (Protected)
	VendorID      uint32      // 0 for IETF AVPs
	GroupedFields []*AVPField // For Grouped AVPs
}

// AVPField represents a field in a command message
type AVPField struct {
	AVP       *AVPDefinition
	FieldName string // Go field name, e.g., "OriginHost"
	Fixed     bool   // Fixed position
	Required  bool   // Must be present
	Repeated  bool   // Can appear multiple times (slice)
	Position  int    // Position in ABNF definition
}

// AVPFlags represents AVP header flags
type AVPFlags struct {
	Vendor    bool // V-bit
	Mandatory bool // M-bit
	Protected bool // P-bit
}

// AVPHeader represents the AVP header structure
type AVPHeader struct {
	Code     uint32
	Flags    AVPFlags
	Length   uint32 // Total length including header
	VendorID uint32 // Only present if V-bit is set
}

// AVP represents a complete AVP with header and data
type AVP struct {
	Header AVPHeader
	Data   models_base.Type
}

// Len returns total AVP length including header and padding
func (a *AVP) Len() int {
	headerLen := 8 // Basic header: 4 (code) + 4 (flags + length)
	if a.Header.Flags.Vendor {
		headerLen = 12 // With vendor ID
	}
	dataLen := a.Data.Len()
	totalLen := headerLen + dataLen
	// Padding to 32-bit boundary
	padding := (4 - (totalLen % 4)) % 4
	return totalLen + padding
}

// Padding returns padding bytes needed
func (a *AVP) Padding() int {
	headerLen := 8
	if a.Header.Flags.Vendor {
		headerLen = 12
	}
	totalLen := headerLen + a.Data.Len()
	return (4 - (totalLen % 4)) % 4
}
