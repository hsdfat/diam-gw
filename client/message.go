package client

import (
	"fmt"

	"github.com/hsdfat/diam-gw/pkg/connection"
)

// DiameterMessageType represents the type of Diameter message
type DiameterMessageType = connection.DiameterMessageType

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
type MessageFlags = connection.MessageFlags

// MessageInfo holds parsed information from a Diameter message header
type MessageInfo = connection.MessageInfo

// ParseMessageHeader parses a Diameter message header
var ParseMessageHeader = connection.ParseMessageHeader

// ResultCode represents Diameter result codes
type ResultCode uint32

const (
	// Success codes (2xxx)
	ResultCodeSuccess ResultCode = 2001

	// Protocol errors (3xxx)
	ResultCodeCommandUnsupported     ResultCode = 3001
	ResultCodeUnableToDeliver        ResultCode = 3002
	ResultCodeRealmNotServed         ResultCode = 3003
	ResultCodeTooBusy                ResultCode = 3004
	ResultCodeLoopDetected           ResultCode = 3005
	ResultCodeRedirectIndication     ResultCode = 3006
	ResultCodeApplicationUnsupported ResultCode = 3007
	ResultCodeInvalidHDRBits         ResultCode = 3008
	ResultCodeInvalidAVPBits         ResultCode = 3009

	// Transient failures (4xxx)
	ResultCodeAuthenticationRejected ResultCode = 4001
	ResultCodeOutOfSpace             ResultCode = 4002
	ResultCodeElectionLost           ResultCode = 4003

	// Permanent failures (5xxx)
	ResultCodeAVPUnsupported        ResultCode = 5001
	ResultCodeUnknownSessionID      ResultCode = 5002
	ResultCodeAuthorizationRejected ResultCode = 5003
	ResultCodeInvalidAVPValue       ResultCode = 5004
	ResultCodeMissingAVP            ResultCode = 5005
	ResultCodeResourcesExceeded     ResultCode = 5006
	ResultCodeContradictingAVPs     ResultCode = 5007
	ResultCodeAVPNotAllowed         ResultCode = 5008
	ResultCodeAVPOccursTooManyTimes ResultCode = 5009
	ResultCodeNoCommonApplication   ResultCode = 5010
	ResultCodeUnsupportedVersion    ResultCode = 5011
	ResultCodeUnableToComply        ResultCode = 5012
	ResultCodeInvalidBitInHeader    ResultCode = 5013
	ResultCodeInvalidAVPLength      ResultCode = 5014
	ResultCodeInvalidMessageLength  ResultCode = 5015
	ResultCodeInvalidAVPBitCombo    ResultCode = 5016
	ResultCodeNoCommonSecurity      ResultCode = 5017
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
