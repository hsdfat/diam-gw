package main

import (
	"fmt"
	"net"

	"github.com/hsdfat8/diam-gw/commands/base"
	"github.com/hsdfat8/diam-gw/models_base"
)

func main() {
	fmt.Println("=== Diameter Base Protocol Example ===")
	fmt.Println()

	// Example 1: Create and marshal a Capabilities-Exchange-Request (CER)
	fmt.Println("1. Creating Capabilities-Exchange-Request (CER)...")
	cer := createCER()
	cerBytes, err := cer.Marshal()
	if err != nil {
		panic(err)
	}
	fmt.Printf("   Marshaled CER: %d bytes\n", len(cerBytes))
	fmt.Printf("   CER: %s\n\n", cer.String())

	// Example 2: Unmarshal the CER
	fmt.Println("2. Unmarshaling CER...")
	cer2 := &base.CapabilitiesExchangeRequest{}
	if err := cer2.Unmarshal(cerBytes); err != nil {
		panic(err)
	}
	fmt.Printf("   Origin-Host: %s\n", cer2.OriginHost)
	fmt.Printf("   Origin-Realm: %s\n", cer2.OriginRealm)
	fmt.Printf("   Vendor-Id: %d\n", cer2.VendorId)
	fmt.Printf("   Product-Name: %s\n\n", cer2.ProductName)

	// Example 3: Create a Capabilities-Exchange-Answer (CEA)
	fmt.Println("3. Creating Capabilities-Exchange-Answer (CEA)...")
	cea := createCEA(cer.Header.HopByHopID, cer.Header.EndToEndID)
	ceaBytes, err := cea.Marshal()
	if err != nil {
		panic(err)
	}
	fmt.Printf("   Marshaled CEA: %d bytes\n", len(ceaBytes))
	fmt.Printf("   Result-Code: %d (DIAMETER_SUCCESS)\n\n", cea.ResultCode)

	// Example 4: Device Watchdog Request/Answer
	fmt.Println("4. Creating Device-Watchdog-Request (DWR)...")
	dwr := createDWR()
	dwrBytes, err := dwr.Marshal()
	if err != nil {
		panic(err)
	}
	fmt.Printf("   Marshaled DWR: %d bytes\n", len(dwrBytes))
	fmt.Printf("   DWR: %s\n\n", dwr.String())

	fmt.Println("5. Creating Device-Watchdog-Answer (DWA)...")
	dwa := createDWA(dwr.Header.HopByHopID, dwr.Header.EndToEndID)
	dwaBytes, err := dwa.Marshal()
	if err != nil {
		panic(err)
	}
	fmt.Printf("   Marshaled DWA: %d bytes\n", len(dwaBytes))
	fmt.Printf("   Result-Code: %d\n\n", dwa.ResultCode)

	// Example 5: Session Termination
	fmt.Println("6. Creating Session-Termination-Request (STR)...")
	str := createSTR()
	strBytes, err := str.Marshal()
	if err != nil {
		panic(err)
	}
	fmt.Printf("   Marshaled STR: %d bytes\n", len(strBytes))
	fmt.Printf("   Session-Id: %s\n", str.SessionId)
	fmt.Printf("   Termination-Cause: %d (DIAMETER_LOGOUT)\n\n", str.TerminationCause)

	// Example 6: Accounting Request
	fmt.Println("7. Creating Accounting-Request (ACR)...")
	acr := createACR()
	acrBytes, err := acr.Marshal()
	if err != nil {
		panic(err)
	}
	fmt.Printf("   Marshaled ACR: %d bytes\n", len(acrBytes))
	fmt.Printf("   Accounting-Record-Type: %d (START_RECORD)\n", acr.AccountingRecordType)
	fmt.Printf("   Accounting-Record-Number: %d\n\n", acr.AccountingRecordNumber)

	fmt.Println("=== Example Complete ===")
}

func createCER() *base.CapabilitiesExchangeRequest {
	cer := base.NewCapabilitiesExchangeRequest()

	// Set required fields
	cer.OriginHost = models_base.DiameterIdentity("client.example.com")
	cer.OriginRealm = models_base.DiameterIdentity("example.com")
	cer.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("10.1.1.1")),
		models_base.Address(net.ParseIP("10.1.1.2")),
	}
	cer.VendorId = models_base.Unsigned32(10415) // 3GPP
	cer.ProductName = models_base.UTF8String("DiameterGateway/1.0")

	// Set optional fields
	cer.AuthApplicationId = []models_base.Unsigned32{
		models_base.Unsigned32(16777238), // Gx (Policy and Charging Control)
		models_base.Unsigned32(16777251), // S6a (3GPP)
	}
	cer.InbandSecurityId = []models_base.Unsigned32{
		models_base.Unsigned32(0), // NO_INBAND_SECURITY
	}

	// Set header identifiers
	cer.Header.HopByHopID = 0x00000001
	cer.Header.EndToEndID = 0x00000001

	return cer
}

func createCEA(hopByHopID, endToEndID uint32) *base.CapabilitiesExchangeAnswer {
	cea := base.NewCapabilitiesExchangeAnswer()

	// Set required fields
	cea.ResultCode = models_base.Unsigned32(2001) // DIAMETER_SUCCESS
	cea.OriginHost = models_base.DiameterIdentity("server.example.com")
	cea.OriginRealm = models_base.DiameterIdentity("example.com")
	cea.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("192.168.1.1")),
	}
	cea.VendorId = models_base.Unsigned32(10415)
	cea.ProductName = models_base.UTF8String("DiameterServer/1.0")

	// Set optional fields
	cea.AuthApplicationId = []models_base.Unsigned32{
		models_base.Unsigned32(16777238), // Gx
		models_base.Unsigned32(16777251), // S6a
	}

	// Match hop-by-hop and end-to-end IDs from request
	cea.Header.HopByHopID = hopByHopID
	cea.Header.EndToEndID = endToEndID

	return cea
}

func createDWR() *base.DeviceWatchdogRequest {
	dwr := base.NewDeviceWatchdogRequest()

	dwr.OriginHost = models_base.DiameterIdentity("client.example.com")
	dwr.OriginRealm = models_base.DiameterIdentity("example.com")

	dwr.Header.HopByHopID = 0x00000002
	dwr.Header.EndToEndID = 0x00000002

	return dwr
}

func createDWA(hopByHopID, endToEndID uint32) *base.DeviceWatchdogAnswer {
	dwa := base.NewDeviceWatchdogAnswer()

	dwa.ResultCode = models_base.Unsigned32(2001) // DIAMETER_SUCCESS
	dwa.OriginHost = models_base.DiameterIdentity("server.example.com")
	dwa.OriginRealm = models_base.DiameterIdentity("example.com")

	dwa.Header.HopByHopID = hopByHopID
	dwa.Header.EndToEndID = endToEndID

	return dwa
}

func createSTR() *base.SessionTerminationRequest {
	str := base.NewSessionTerminationRequest()

	str.SessionId = models_base.UTF8String("client.example.com;1234567890;1")
	str.OriginHost = models_base.DiameterIdentity("client.example.com")
	str.OriginRealm = models_base.DiameterIdentity("example.com")
	str.DestinationRealm = models_base.DiameterIdentity("server.example.com")
	str.AuthApplicationId = models_base.Unsigned32(16777238) // Gx
	str.TerminationCause = models_base.Enumerated(1)         // DIAMETER_LOGOUT

	str.Header.HopByHopID = 0x00000003
	str.Header.EndToEndID = 0x00000003

	return str
}

func createACR() *base.AccountingRequest {
	acr := base.NewAccountingRequest()

	acr.SessionId = models_base.UTF8String("client.example.com;1234567890;2")
	acr.OriginHost = models_base.DiameterIdentity("client.example.com")
	acr.OriginRealm = models_base.DiameterIdentity("example.com")
	acr.DestinationRealm = models_base.DiameterIdentity("server.example.com")
	acr.AccountingRecordType = models_base.Enumerated(2)  // START_RECORD
	acr.AccountingRecordNumber = models_base.Unsigned32(1)

	acr.Header.HopByHopID = 0x00000004
	acr.Header.EndToEndID = 0x00000004

	return acr
}
