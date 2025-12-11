package codegen

// CommandDefinition represents a Diameter command definition
type CommandDefinition struct {
	Name          string      // e.g., "Capabilities-Exchange-Request"
	Abbreviation  string      // e.g., "CER"
	Code          uint32      // Command code
	ApplicationID uint32      // Application ID
	Request       bool        // Request or Answer
	Proxiable     bool        // Can be proxied
	Package       string      // Package name for organizing output (e.g., "base", "s6a")
	Fields        []*AVPField // List of AVP fields
}

// CommandFlags represents Diameter command header flags
type CommandFlags struct {
	Request       bool // R-bit
	Proxiable     bool // P-bit
	Error         bool // E-bit
	Retransmitted bool // T-bit
}

// DiameterHeader represents the Diameter message header (20 bytes)
type DiameterHeader struct {
	Version       uint8        // 1 byte - Must be 1
	Length        uint32       // 3 bytes - Total message length
	Flags         CommandFlags // 1 byte
	CommandCode   uint32       // 3 bytes
	ApplicationID uint32       // 4 bytes
	HopByHopID    uint32       // 4 bytes
	EndToEndID    uint32       // 4 bytes
}

// DiameterMessage represents a complete Diameter message
type DiameterMessage struct {
	Header DiameterHeader
	AVPs   []*AVP
}

// Len returns the total message length
func (m *DiameterMessage) Len() int {
	length := 20 // Header is always 20 bytes
	for _, avp := range m.AVPs {
		length += avp.Len()
	}
	return length
}
