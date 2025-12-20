package gateway_test

import (
	"encoding/binary"
	"fmt"

	"github.com/hsdfat/diam-gw/commands/base"
	"github.com/hsdfat/diam-gw/models_base"
)

// createTestMessage creates a Diameter message for testing
func createTestMessage(cmdCode uint32, appID uint32, hopByHopID uint32, endToEndID uint32, body []byte) []byte {
	totalLen := 20 + len(body)
	msg := make([]byte, totalLen)

	// Version
	msg[0] = 1

	// Message length
	binary.BigEndian.PutUint32(msg[0:4], uint32(totalLen))
	msg[0] = 1 // Restore version

	// Command flags (Request bit set)
	msg[4] = 0x80

	// Command code
	binary.BigEndian.PutUint32(msg[4:8], cmdCode)
	msg[4] = 0x80 // Restore flags

	// Application ID
	binary.BigEndian.PutUint32(msg[8:12], appID)

	// Hop-by-Hop ID
	binary.BigEndian.PutUint32(msg[12:16], hopByHopID)

	// End-to-End ID
	binary.BigEndian.PutUint32(msg[16:20], endToEndID)

	// Body
	if len(body) > 0 {
		copy(msg[20:], body)
	}

	return msg
}

// extractHopByHopID extracts Hop-by-Hop ID from a Diameter message
func extractHopByHopID(msg []byte) uint32 {
	if len(msg) < 20 {
		return 0
	}
	return binary.BigEndian.Uint32(msg[12:16])
}

// extractEndToEndID extracts End-to-End ID from a Diameter message
func extractEndToEndID(msg []byte) uint32 {
	if len(msg) < 20 {
		return 0
	}
	return binary.BigEndian.Uint32(msg[16:20])
}

// createCEA creates a Capabilities-Exchange-Answer from a CER
func createCEA(cer []byte, originHost, originRealm string) ([]byte, error) {
	// Parse CER
	cerMsg := &base.CapabilitiesExchangeRequest{}
	if err := cerMsg.Unmarshal(cer); err != nil {
		return nil, err
	}

	// Create CEA
	cea := base.NewCapabilitiesExchangeAnswer()
	cea.Header.HopByHopID = cerMsg.Header.HopByHopID
	cea.Header.EndToEndID = cerMsg.Header.EndToEndID
	cea.ResultCode = 2001 // DIAMETER_SUCCESS
	cea.OriginHost = models_base.DiameterIdentity(originHost)
	cea.OriginRealm = models_base.DiameterIdentity(originRealm)
	cea.ProductName = models_base.UTF8String("DRA-Simulator")
	cea.VendorId = models_base.Unsigned32(10415)

	return cea.Marshal()
}

// createDWA creates a Device-Watchdog-Answer from a DWR
func createDWA(dwr []byte, originHost, originRealm string) ([]byte, error) {
	// Parse DWR header for IDs
	if len(dwr) < 20 {
		return nil, fmt.Errorf("message too short")
	}

	h2h := extractHopByHopID(dwr)
	e2e := extractEndToEndID(dwr)

	// Create DWA
	dwa := base.NewDeviceWatchdogAnswer()
	dwa.Header.HopByHopID = h2h
	dwa.Header.EndToEndID = e2e
	dwa.ResultCode = 2001
	dwa.OriginHost = models_base.DiameterIdentity(originHost)
	dwa.OriginRealm = models_base.DiameterIdentity(originRealm)

	return dwa.Marshal()
}
