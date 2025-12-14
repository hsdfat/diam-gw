package client

import (
	"encoding/binary"
	"fmt"
)

// DiameterMessageType represents the type of Diameter message
type DiameterMessageType int

const (
	// Message types based on command code
	MessageTypeCER DiameterMessageType = 257 // Capabilities-Exchange-Request
	MessageTypeCEA DiameterMessageType = 257 // Capabilities-Exchange-Answer
	MessageTypeDWR DiameterMessageType = 280 // Device-Watchdog-Request
	MessageTypeDWA DiameterMessageType = 280 // Device-Watchdog-Answer
	MessageTypeDPR DiameterMessageType = 282 // Disconnect-Peer-Request
	MessageTypeDPA DiameterMessageType = 282 // Disconnect-Peer-Answer
)

// MessageFlags represents Diameter message flags
type MessageFlags struct {
	Request   bool
	Proxiable bool
	Error     bool
	Retrans   bool
}

// MessageInfo holds parsed information from a Diameter message header
type MessageInfo struct {
	Version       uint8
	Length        uint32
	CommandCode   uint32
	ApplicationID uint32
	HopByHopID    uint32
	EndToEndID    uint32
	Flags         MessageFlags
	IsRequest     bool
	IsProxiable   bool
	IsError       bool
	IsRetrans     bool
}

// ParseMessageHeader parses a Diameter message header
func ParseMessageHeader(data []byte) (*MessageInfo, error) {
	if len(data) < 20 {
		return nil, ErrInvalidMessage{Reason: "message too short for header"}
	}

	info := &MessageInfo{
		Version:       data[0],
		Length:        binary.BigEndian.Uint32([]byte{0, data[1], data[2], data[3]}),
		CommandCode:   binary.BigEndian.Uint32([]byte{0, data[5], data[6], data[7]}),
		ApplicationID: binary.BigEndian.Uint32(data[8:12]),
		HopByHopID:    binary.BigEndian.Uint32(data[12:16]),
		EndToEndID:    binary.BigEndian.Uint32(data[16:20]),
	}

	// Parse flags
	flags := data[4]
	info.IsRequest = (flags & 0x80) != 0
	info.IsProxiable = (flags & 0x40) != 0
	info.IsError = (flags & 0x20) != 0
	info.IsRetrans = (flags & 0x10) != 0

	// Also set Flags struct
	info.Flags.Request = info.IsRequest
	info.Flags.Proxiable = info.IsProxiable
	info.Flags.Error = info.IsError
	info.Flags.Retrans = info.IsRetrans

	// Validate version
	if info.Version != 1 {
		return nil, ErrInvalidMessage{Reason: fmt.Sprintf("unsupported version: %d", info.Version)}
	}

	// Validate length
	if info.Length < 20 {
		return nil, ErrInvalidMessage{Reason: fmt.Sprintf("invalid length: %d", info.Length)}
	}

	return info, nil
}

// IsBaseProtocol returns true if the message is a base protocol message
func (m *MessageInfo) IsBaseProtocol() bool {
	return m.ApplicationID == 0
}

// GetMessageType returns the message type
func (m *MessageInfo) GetMessageType() DiameterMessageType {
	return DiameterMessageType(m.CommandCode)
}

// String returns a string representation of the message info
func (m *MessageInfo) String() string {
	msgType := "Answer"
	if m.IsRequest {
		msgType = "Request"
	}

	cmdName := "Unknown"
	switch m.CommandCode {
	case 257:
		cmdName = "CER/CEA"
	case 280:
		cmdName = "DWR/DWA"
	case 282:
		cmdName = "DPR/DPA"
	case 324:
		cmdName = "ECR/ECA"
	}

	return fmt.Sprintf("%s %s (Code=%d, AppID=%d, H2H=%d, E2E=%d)",
		cmdName, msgType, m.CommandCode, m.ApplicationID, m.HopByHopID, m.EndToEndID)
}

// ResultCode represents Diameter result codes
type ResultCode uint32

const (
	// Success codes (2xxx)
	ResultCodeSuccess ResultCode = 2001

	// Protocol errors (3xxx)
	ResultCodeCommandUnsupported ResultCode = 3001
	ResultCodeUnableToDeliver    ResultCode = 3002
	ResultCodeRealmNotServed     ResultCode = 3003
	ResultCodeTooBusy            ResultCode = 3004
	ResultCodeLoopDetected       ResultCode = 3005
	ResultCodeRedirectIndication ResultCode = 3006
	ResultCodeApplicationUnsupported ResultCode = 3007
	ResultCodeInvalidHDRBits     ResultCode = 3008
	ResultCodeInvalidAVPBits     ResultCode = 3009

	// Transient failures (4xxx)
	ResultCodeAuthenticationRejected ResultCode = 4001
	ResultCodeOutOfSpace            ResultCode = 4002
	ResultCodeElectionLost          ResultCode = 4003

	// Permanent failures (5xxx)
	ResultCodeAVPUnsupported     ResultCode = 5001
	ResultCodeUnknownSessionID   ResultCode = 5002
	ResultCodeAuthorizationRejected ResultCode = 5003
	ResultCodeInvalidAVPValue    ResultCode = 5004
	ResultCodeMissingAVP         ResultCode = 5005
	ResultCodeResourcesExceeded  ResultCode = 5006
	ResultCodeContradictingAVPs  ResultCode = 5007
	ResultCodeAVPNotAllowed      ResultCode = 5008
	ResultCodeAVPOccursTooManyTimes ResultCode = 5009
	ResultCodeNoCommonApplication ResultCode = 5010
	ResultCodeUnsupportedVersion ResultCode = 5011
	ResultCodeUnableToComply     ResultCode = 5012
	ResultCodeInvalidBitInHeader ResultCode = 5013
	ResultCodeInvalidAVPLength   ResultCode = 5014
	ResultCodeInvalidMessageLength ResultCode = 5015
	ResultCodeInvalidAVPBitCombo ResultCode = 5016
	ResultCodeNoCommonSecurity   ResultCode = 5017
)

// IsSuccess returns true if the result code indicates success
func (r ResultCode) IsSuccess() bool {
	return r >= 2000 && r < 3000
}

// String returns the string representation of the result code
func (r ResultCode) String() string {
	switch r {
	case ResultCodeSuccess:
		return "DIAMETER_SUCCESS"
	case ResultCodeCommandUnsupported:
		return "DIAMETER_COMMAND_UNSUPPORTED"
	case ResultCodeUnableToDeliver:
		return "DIAMETER_UNABLE_TO_DELIVER"
	case ResultCodeRealmNotServed:
		return "DIAMETER_REALM_NOT_SERVED"
	case ResultCodeTooBusy:
		return "DIAMETER_TOO_BUSY"
	case ResultCodeNoCommonApplication:
		return "DIAMETER_NO_COMMON_APPLICATION"
	case ResultCodeUnsupportedVersion:
		return "DIAMETER_UNSUPPORTED_VERSION"
	default:
		return fmt.Sprintf("RESULT_CODE_%d", r)
	}
}
