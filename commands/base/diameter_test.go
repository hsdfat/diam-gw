package base

import (
	"bytes"
	"net"
	"testing"

	"github.com/hsdfat8/diam-gw/models_base"
)

func TestCapabilitiesExchangeRequest(t *testing.T) {
	// Create a new CER message
	cer := NewCapabilitiesExchangeRequest()

	// Set required fields
	cer.OriginHost = models_base.DiameterIdentity("client.example.com")
	cer.OriginRealm = models_base.DiameterIdentity("example.com")
	cer.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("127.0.0.1")),
	}
	cer.VendorId = models_base.Unsigned32(10415) // 3GPP
	cer.ProductName = models_base.UTF8String("TestClient")

	// Set optional fields
	cer.AuthApplicationId = []models_base.Unsigned32{
		models_base.Unsigned32(16777238), // Gx application
	}

	// Set hop-by-hop and end-to-end IDs
	cer.Header.HopByHopID = 1
	cer.Header.EndToEndID = 1

	// Marshal the message
	data, err := cer.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal CER: %v", err)
	}

	// Verify the message is at least the header size
	if len(data) < 20 {
		t.Fatalf("Marshaled data too short: %d bytes", len(data))
	}

	// Verify header fields
	if data[0] != 1 {
		t.Errorf("Expected version 1, got %d", data[0])
	}

	// Unmarshal the message
	cer2 := &CapabilitiesExchangeRequest{}
	if err := cer2.Unmarshal(data); err != nil {
		t.Fatalf("Failed to unmarshal CER: %v", err)
	}

	// Verify fields
	if cer2.OriginHost != cer.OriginHost {
		t.Errorf("OriginHost mismatch: got %s, want %s", cer2.OriginHost, cer.OriginHost)
	}
	if cer2.OriginRealm != cer.OriginRealm {
		t.Errorf("OriginRealm mismatch: got %s, want %s", cer2.OriginRealm, cer.OriginRealm)
	}
	if cer2.VendorId != cer.VendorId {
		t.Errorf("VendorId mismatch: got %d, want %d", cer2.VendorId, cer.VendorId)
	}
	if cer2.ProductName != cer.ProductName {
		t.Errorf("ProductName mismatch: got %s, want %s", cer2.ProductName, cer.ProductName)
	}
}

func TestCapabilitiesExchangeAnswer(t *testing.T) {
	// Create a new CEA message
	cea := NewCapabilitiesExchangeAnswer()

	// Set required fields
	cea.ResultCode = models_base.Unsigned32(2001) // DIAMETER_SUCCESS
	cea.OriginHost = models_base.DiameterIdentity("server.example.com")
	cea.OriginRealm = models_base.DiameterIdentity("example.com")
	cea.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("192.168.1.1")),
	}
	cea.VendorId = models_base.Unsigned32(10415)
	cea.ProductName = models_base.UTF8String("TestServer")

	// Set hop-by-hop and end-to-end IDs
	cea.Header.HopByHopID = 1
	cea.Header.EndToEndID = 1

	// Marshal
	data, err := cea.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal CEA: %v", err)
	}

	// Unmarshal
	cea2 := &CapabilitiesExchangeAnswer{}
	if err := cea2.Unmarshal(data); err != nil {
		t.Fatalf("Failed to unmarshal CEA: %v", err)
	}

	// Verify
	if cea2.ResultCode != cea.ResultCode {
		t.Errorf("ResultCode mismatch: got %d, want %d", cea2.ResultCode, cea.ResultCode)
	}
}

func TestDeviceWatchdogRequest(t *testing.T) {
	// Create a new DWR message
	dwr := NewDeviceWatchdogRequest()

	dwr.OriginHost = models_base.DiameterIdentity("client.example.com")
	dwr.OriginRealm = models_base.DiameterIdentity("example.com")
	dwr.Header.HopByHopID = 2
	dwr.Header.EndToEndID = 2

	// Marshal
	data, err := dwr.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal DWR: %v", err)
	}

	// Unmarshal
	dwr2 := &DeviceWatchdogRequest{}
	if err := dwr2.Unmarshal(data); err != nil {
		t.Fatalf("Failed to unmarshal DWR: %v", err)
	}

	// Verify
	if dwr2.OriginHost != dwr.OriginHost {
		t.Errorf("OriginHost mismatch: got %s, want %s", dwr2.OriginHost, dwr.OriginHost)
	}
}

func TestDeviceWatchdogAnswer(t *testing.T) {
	dwa := NewDeviceWatchdogAnswer()

	dwa.ResultCode = models_base.Unsigned32(2001)
	dwa.OriginHost = models_base.DiameterIdentity("server.example.com")
	dwa.OriginRealm = models_base.DiameterIdentity("example.com")
	dwa.Header.HopByHopID = 2
	dwa.Header.EndToEndID = 2

	// Test marshal/unmarshal
	data, err := dwa.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	dwa2 := &DeviceWatchdogAnswer{}
	if err := dwa2.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if dwa2.ResultCode != dwa.ResultCode {
		t.Errorf("ResultCode mismatch")
	}
}

func TestDisconnectPeerRequest(t *testing.T) {
	dpr := NewDisconnectPeerRequest()

	dpr.OriginHost = models_base.DiameterIdentity("client.example.com")
	dpr.OriginRealm = models_base.DiameterIdentity("example.com")
	dpr.DisconnectCause = models_base.Enumerated(0) // REBOOTING
	dpr.Header.HopByHopID = 3
	dpr.Header.EndToEndID = 3

	data, err := dpr.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	dpr2 := &DisconnectPeerRequest{}
	if err := dpr2.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if dpr2.DisconnectCause != dpr.DisconnectCause {
		t.Errorf("DisconnectCause mismatch")
	}
}

func TestDisconnectPeerAnswer(t *testing.T) {
	dpa := NewDisconnectPeerAnswer()

	dpa.ResultCode = models_base.Unsigned32(2001)
	dpa.OriginHost = models_base.DiameterIdentity("server.example.com")
	dpa.OriginRealm = models_base.DiameterIdentity("example.com")
	dpa.Header.HopByHopID = 3
	dpa.Header.EndToEndID = 3

	data, err := dpa.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	dpa2 := &DisconnectPeerAnswer{}
	if err := dpa2.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if dpa2.ResultCode != dpa.ResultCode {
		t.Errorf("ResultCode mismatch")
	}
}

func TestSessionTerminationRequest(t *testing.T) {
	str := NewSessionTerminationRequest()

	str.SessionId = models_base.UTF8String("session123")
	str.OriginHost = models_base.DiameterIdentity("client.example.com")
	str.OriginRealm = models_base.DiameterIdentity("example.com")
	str.DestinationRealm = models_base.DiameterIdentity("server.example.com")
	str.AuthApplicationId = models_base.Unsigned32(16777238)
	str.TerminationCause = models_base.Enumerated(1) // DIAMETER_LOGOUT

	data, err := str.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	str2 := &SessionTerminationRequest{}
	if err := str2.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if str2.SessionId != str.SessionId {
		t.Errorf("SessionId mismatch")
	}
}

func TestAbortSessionRequest(t *testing.T) {
	asr := NewAbortSessionRequest()

	asr.SessionId = models_base.UTF8String("session456")
	asr.OriginHost = models_base.DiameterIdentity("server.example.com")
	asr.OriginRealm = models_base.DiameterIdentity("example.com")
	asr.DestinationRealm = models_base.DiameterIdentity("client.example.com")
	asr.DestinationHost = models_base.DiameterIdentity("client.example.com")
	asr.AuthApplicationId = models_base.Unsigned32(16777238)

	data, err := asr.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	asr2 := &AbortSessionRequest{}
	if err := asr2.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if asr2.SessionId != asr.SessionId {
		t.Errorf("SessionId mismatch")
	}
}

func TestAccountingRequest(t *testing.T) {
	acr := NewAccountingRequest()

	acr.SessionId = models_base.UTF8String("acct-session789")
	acr.OriginHost = models_base.DiameterIdentity("client.example.com")
	acr.OriginRealm = models_base.DiameterIdentity("example.com")
	acr.DestinationRealm = models_base.DiameterIdentity("server.example.com")
	acr.AccountingRecordType = models_base.Enumerated(2) // START_RECORD
	acr.AccountingRecordNumber = models_base.Unsigned32(1)

	data, err := acr.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	acr2 := &AccountingRequest{}
	if err := acr2.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if acr2.AccountingRecordType != acr.AccountingRecordType {
		t.Errorf("AccountingRecordType mismatch")
	}
	if acr2.AccountingRecordNumber != acr.AccountingRecordNumber {
		t.Errorf("AccountingRecordNumber mismatch")
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	// Test that marshal -> unmarshal produces identical results
	original := NewCapabilitiesExchangeRequest()
	original.OriginHost = models_base.DiameterIdentity("test.example.com")
	original.OriginRealm = models_base.DiameterIdentity("example.com")
	original.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("10.0.0.1")),
		models_base.Address(net.ParseIP("10.0.0.2")),
	}
	original.VendorId = models_base.Unsigned32(12345)
	original.ProductName = models_base.UTF8String("RoundTripTest")
	original.AuthApplicationId = []models_base.Unsigned32{
		models_base.Unsigned32(1),
		models_base.Unsigned32(2),
		models_base.Unsigned32(3),
	}
	original.Header.HopByHopID = 0x12345678
	original.Header.EndToEndID = 0x87654321

	// Marshal
	data1, err := original.Marshal()
	if err != nil {
		t.Fatalf("First marshal failed: %v", err)
	}

	// Unmarshal
	decoded := &CapabilitiesExchangeRequest{}
	if err := decoded.Unmarshal(data1); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Marshal again
	data2, err := decoded.Marshal()
	if err != nil {
		t.Fatalf("Second marshal failed: %v", err)
	}

	// Compare bytes
	if !bytes.Equal(data1, data2) {
		t.Errorf("Round trip marshal/unmarshal produced different bytes")
		t.Logf("Original length: %d, Round-trip length: %d", len(data1), len(data2))
	}

	// Verify header fields
	if decoded.Header.HopByHopID != original.Header.HopByHopID {
		t.Errorf("HopByHopID mismatch: got 0x%X, want 0x%X",
			decoded.Header.HopByHopID, original.Header.HopByHopID)
	}
	if decoded.Header.EndToEndID != original.Header.EndToEndID {
		t.Errorf("EndToEndID mismatch: got 0x%X, want 0x%X",
			decoded.Header.EndToEndID, original.Header.EndToEndID)
	}

	// Verify repeated fields
	if len(decoded.HostIpAddress) != len(original.HostIpAddress) {
		t.Errorf("HostIpAddress count mismatch: got %d, want %d",
			len(decoded.HostIpAddress), len(original.HostIpAddress))
	}
	if len(decoded.AuthApplicationId) != len(original.AuthApplicationId) {
		t.Errorf("AuthApplicationId count mismatch: got %d, want %d",
			len(decoded.AuthApplicationId), len(original.AuthApplicationId))
	}
}

func TestHeaderFlags(t *testing.T) {
	// Test request flag
	cer := NewCapabilitiesExchangeRequest()
	if !cer.Header.Flags.Request {
		t.Error("CER should have Request flag set")
	}

	cea := NewCapabilitiesExchangeAnswer()
	if cea.Header.Flags.Request {
		t.Error("CEA should not have Request flag set")
	}

	// Test proxiable flag
	rar := NewReAuthRequest()
	if !rar.Header.Flags.Proxiable {
		t.Error("RAR should have Proxiable flag set")
	}

	dwr := NewDeviceWatchdogRequest()
	if dwr.Header.Flags.Proxiable {
		t.Error("DWR should not have Proxiable flag set")
	}
}

func TestLen(t *testing.T) {
	cer := NewCapabilitiesExchangeRequest()
	cer.OriginHost = models_base.DiameterIdentity("host.example.com")
	cer.OriginRealm = models_base.DiameterIdentity("example.com")
	cer.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("127.0.0.1")),
	}
	cer.VendorId = models_base.Unsigned32(10415)
	cer.ProductName = models_base.UTF8String("Test")

	length := cer.Len()
	if length < 20 {
		t.Errorf("Message length %d is less than minimum header size 20", length)
	}

	data, _ := cer.Marshal()
	if len(data) != length {
		t.Errorf("Len() returned %d but Marshal() produced %d bytes", length, len(data))
	}
}

func TestString(t *testing.T) {
	dwr := NewDeviceWatchdogRequest()
	dwr.OriginHost = models_base.DiameterIdentity("test.example.com")
	dwr.OriginRealm = models_base.DiameterIdentity("example.com")

	str := dwr.String()
	if str == "" {
		t.Error("String() returned empty string")
	}
	t.Logf("DWR String representation: %s", str)
}
