package base

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcapgo"
	"github.com/hsdfat8/diam-gw/models_base"
)

// keepPcapFiles determines if pcap files should be kept after tests
// Set KEEP_PCAP=1 environment variable to keep the files
// var keepPcapFiles = os.Getenv("KEEP_PCAP") == "1"

// TestWriteCommandToPcap tests writing a Diameter command message to a pcap file
func TestWriteCommandToPcap(t *testing.T) {
	// Create testdata directory if it doesn't exist
	if err := os.MkdirAll("testdata", 0755); err != nil {
		t.Fatalf("Failed to create testdata directory: %v", err)
	}

	// Create a pcap file in testdata folder
	pcapFile := filepath.Join("testdata", "test_diameter.pcap")
	// if !keepPcapFiles {
	// 	defer os.Remove(pcapFile) // Clean up after test
	// }

	// Create and configure a CER message
	cer := NewCapabilitiesExchangeRequest()
	cer.OriginHost = models_base.DiameterIdentity("client.example.com")
	cer.OriginRealm = models_base.DiameterIdentity("example.com")
	cer.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("192.168.1.100")),
	}
	cer.VendorId = models_base.Unsigned32(10415) // 3GPP
	cer.ProductName = models_base.UTF8String("TestClient")
	cer.Header.HopByHopID = 0x12345678
	cer.Header.EndToEndID = 0x87654321

	// Marshal the Diameter message
	diameterData, err := cer.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal CER: %v", err)
	}

	// Write to pcap file
	err = writeDiameterToPcap(pcapFile, diameterData, net.ParseIP("192.168.1.100"), net.ParseIP("192.168.1.1"), 3868)
	if err != nil {
		t.Fatalf("Failed to write pcap file: %v", err)
	}

	// Verify file was created
	fileInfo, err := os.Stat(pcapFile)
	if err != nil {
		t.Fatalf("Pcap file was not created: %v", err)
	}
	if fileInfo.Size() == 0 {
		t.Fatal("Pcap file is empty")
	}

	t.Logf("Successfully created pcap file: %s (%d bytes)", pcapFile, fileInfo.Size())
	t.Logf("You can open this file in Wireshark to view the Diameter message")
}

// writeDiameterToPcap writes a Diameter message to a pcap file with proper network layers
func writeDiameterToPcap(filename string, diameterData []byte, srcIP, dstIP net.IP, port int) error {
	// Create the pcap file
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	// Create pcap writer
	w := pcapgo.NewWriter(f)
	err = w.WriteFileHeader(65536, layers.LinkTypeEthernet)
	if err != nil {
		return err
	}

	// Create packet layers
	// Ethernet layer
	ethernet := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e},
		DstMAC:       net.HardwareAddr{0x00, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e},
		EthernetType: layers.EthernetTypeIPv4,
	}

	// IP layer
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}

	// TCP layer
	tcp := &layers.TCP{
		SrcPort: layers.TCPPort(port),
		DstPort: layers.TCPPort(3868), // Diameter default port
		Seq:     1000,
		Ack:     0,
		SYN:     false,
		ACK:     true,
		PSH:     true,
		Window:  65535,
	}

	// Set TCP options for better compatibility
	tcp.Options = []layers.TCPOption{
		{
			OptionType:   layers.TCPOptionKindMSS,
			OptionLength: 4,
			OptionData:   []byte{0x05, 0xb4}, // MSS = 1460
		},
		{
			OptionType: layers.TCPOptionKindNop,
		},
		{
			OptionType:   layers.TCPOptionKindWindowScale,
			OptionLength: 3,
			OptionData:   []byte{0x07}, // Window scale = 7
		},
	}

	// Calculate TCP checksum
	tcp.SetNetworkLayerForChecksum(ip)

	// Serialize the packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	// Create payload (Diameter data)
	payload := gopacket.Payload(diameterData)

	// Serialize all layers
	err = gopacket.SerializeLayers(buf, opts,
		ethernet,
		ip,
		tcp,
		payload,
	)
	if err != nil {
		return err
	}

	// Write packet to pcap file
	ci := gopacket.CaptureInfo{
		Timestamp:     time.Now(),
		CaptureLength: len(buf.Bytes()),
		Length:        len(buf.Bytes()),
	}

	err = w.WritePacket(ci, buf.Bytes())
	if err != nil {
		return err
	}

	return nil
}

// TestWriteMultipleCommandsToPcap tests writing multiple Diameter messages to a pcap file
func TestWriteMultipleCommandsToPcap(t *testing.T) {
	// Create testdata directory if it doesn't exist
	if err := os.MkdirAll("testdata", 0755); err != nil {
		t.Fatalf("Failed to create testdata directory: %v", err)
	}

	pcapFile := filepath.Join("testdata", "test_diameter_multiple.pcap")
	// if !keepPcapFiles {
	// 	defer os.Remove(pcapFile)
	// }

	// Create pcap file
	f, err := os.Create(pcapFile)
	if err != nil {
		t.Fatalf("Failed to create pcap file: %v", err)
	}
	defer f.Close()

	w := pcapgo.NewWriter(f)
	err = w.WriteFileHeader(65536, layers.LinkTypeEthernet)
	if err != nil {
		t.Fatalf("Failed to write pcap header: %v", err)
	}

	srcIP := net.ParseIP("10.0.0.1")
	dstIP := net.ParseIP("10.0.0.2")

	// Create CER
	cer := NewCapabilitiesExchangeRequest()
	cer.OriginHost = models_base.DiameterIdentity("client.example.com")
	cer.OriginRealm = models_base.DiameterIdentity("example.com")
	cer.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("10.0.0.1")),
	}
	cer.VendorId = models_base.Unsigned32(10415)
	cer.ProductName = models_base.UTF8String("TestClient")
	cer.Header.HopByHopID = 1
	cer.Header.EndToEndID = 1

	cerData, err := cer.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal CER: %v", err)
	}

	// Write CER packet
	err = writePacketToPcap(w, cerData, srcIP, dstIP, 50000, 3868, 1000, 0, time.Now())
	if err != nil {
		t.Fatalf("Failed to write CER packet: %v", err)
	}

	// Create CEA
	cea := NewCapabilitiesExchangeAnswer()
	cea.ResultCode = models_base.Unsigned32(2001) // DIAMETER_SUCCESS
	cea.OriginHost = models_base.DiameterIdentity("server.example.com")
	cea.OriginRealm = models_base.DiameterIdentity("example.com")
	cea.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("10.0.0.2")),
	}
	cea.VendorId = models_base.Unsigned32(10415)
	cea.ProductName = models_base.UTF8String("TestServer")
	cea.Header.HopByHopID = 1
	cea.Header.EndToEndID = 1

	ceaData, err := cea.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal CEA: %v", err)
	}

	// Write CEA packet (response)
	err = writePacketToPcap(w, ceaData, dstIP, srcIP, 3868, 50000, 2000, 1000+uint32(len(cerData)), time.Now().Add(10*time.Millisecond))
	if err != nil {
		t.Fatalf("Failed to write CEA packet: %v", err)
	}

	// Verify file
	fileInfo, err := os.Stat(pcapFile)
	if err != nil {
		t.Fatalf("Pcap file was not created: %v", err)
	}
	if fileInfo.Size() == 0 {
		t.Fatal("Pcap file is empty")
	}

	t.Logf("Successfully created pcap file with multiple packets: %s (%d bytes)", pcapFile, fileInfo.Size())
}

// writePacketToPcap writes a single packet to an existing pcap writer
func writePacketToPcap(w *pcapgo.Writer, diameterData []byte, srcIP, dstIP net.IP, srcPort, dstPort int, seq, ack uint32, timestamp time.Time) error {
	// Ethernet layer
	ethernet := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e},
		DstMAC:       net.HardwareAddr{0x00, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e},
		EthernetType: layers.EthernetTypeIPv4,
	}

	// IP layer
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}

	// TCP layer
	tcp := &layers.TCP{
		SrcPort: layers.TCPPort(srcPort),
		DstPort: layers.TCPPort(dstPort),
		Seq:     seq,
		Ack:     ack,
		SYN:     ack == 0 && seq == 1000, // Only for SYN packet
		ACK:     ack > 0,                 // ACK if we have acknowledgment number
		PSH:     true,                    // Push flag for data packets
		Window:  65535,
	}

	tcp.SetNetworkLayerForChecksum(ip)

	// Serialize
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	payload := gopacket.Payload(diameterData)

	err := gopacket.SerializeLayers(buf, opts,
		ethernet,
		ip,
		tcp,
		payload,
	)
	if err != nil {
		return err
	}

	// Write packet
	ci := gopacket.CaptureInfo{
		Timestamp:     timestamp,
		CaptureLength: len(buf.Bytes()),
		Length:        len(buf.Bytes()),
	}

	return w.WritePacket(ci, buf.Bytes())
}

// TestWriteDeviceWatchdogToPcap tests writing a DWR message to pcap
func TestWriteDeviceWatchdogToPcap(t *testing.T) {
	// Create testdata directory if it doesn't exist
	if err := os.MkdirAll("testdata", 0755); err != nil {
		t.Fatalf("Failed to create testdata directory: %v", err)
	}

	pcapFile := filepath.Join("testdata", "test_dwr.pcap")
	// if !keepPcapFiles {
	// 	defer os.Remove(pcapFile)
	// }

	// Create DWR
	dwr := NewDeviceWatchdogRequest()
	dwr.OriginHost = models_base.DiameterIdentity("client.example.com")
	dwr.OriginRealm = models_base.DiameterIdentity("example.com")
	dwr.Header.HopByHopID = 0xABCDEF00
	dwr.Header.EndToEndID = 0x00FEDCBA

	dwrData, err := dwr.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal DWR: %v", err)
	}

	// Write to pcap
	err = writeDiameterToPcap(pcapFile, dwrData, net.ParseIP("10.1.1.1"), net.ParseIP("10.1.1.2"), 3868)
	if err != nil {
		t.Fatalf("Failed to write pcap file: %v", err)
	}

	// Verify
	fileInfo, err := os.Stat(pcapFile)
	if err != nil {
		t.Fatalf("Pcap file was not created: %v", err)
	}

	t.Logf("Successfully created DWR pcap file: %s (%d bytes)", pcapFile, fileInfo.Size())
}

// Helper function to write Diameter message with SCTP instead of TCP
// This is optional since Diameter can use both TCP and SCTP
func writeDiameterToPcapSCTP(filename string, diameterData []byte, srcIP, dstIP net.IP) error {
	// SCTP implementation would go here
	// For now, we'll use TCP which is simpler and also valid for Diameter
	// SCTP requires additional complexity with SCTP chunks
	return writeDiameterToPcap(filename, diameterData, srcIP, dstIP, 3868)
}
