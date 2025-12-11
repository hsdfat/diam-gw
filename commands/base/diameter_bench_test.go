package base

import (
	"net"
	"testing"

	"github.com/hsdfat8/diam-gw/models_base"
)

// BenchmarkCERMarshal benchmarks the Marshal performance for CER messages
func BenchmarkCERMarshal(b *testing.B) {
	cer := NewCapabilitiesExchangeRequest()
	cer.OriginHost = models_base.DiameterIdentity("client.example.com")
	cer.OriginRealm = models_base.DiameterIdentity("example.com")
	cer.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("10.1.1.1")),
		models_base.Address(net.ParseIP("10.1.1.2")),
	}
	cer.VendorId = models_base.Unsigned32(10415)
	cer.ProductName = models_base.UTF8String("BenchmarkTest")
	cer.AuthApplicationId = []models_base.Unsigned32{
		models_base.Unsigned32(16777238),
		models_base.Unsigned32(16777251),
	}
	cer.Header.HopByHopID = 1
	cer.Header.EndToEndID = 1

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := cer.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCERUnmarshal benchmarks the Unmarshal performance for CER messages
func BenchmarkCERUnmarshal(b *testing.B) {
	cer := NewCapabilitiesExchangeRequest()
	cer.OriginHost = models_base.DiameterIdentity("client.example.com")
	cer.OriginRealm = models_base.DiameterIdentity("example.com")
	cer.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("10.1.1.1")),
	}
	cer.VendorId = models_base.Unsigned32(10415)
	cer.ProductName = models_base.UTF8String("BenchmarkTest")
	cer.Header.HopByHopID = 1
	cer.Header.EndToEndID = 1

	data, _ := cer.Marshal()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		msg := &CapabilitiesExchangeRequest{}
		err := msg.Unmarshal(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCERMarshalUnmarshal benchmarks the full round-trip
func BenchmarkCERMarshalUnmarshal(b *testing.B) {
	cer := NewCapabilitiesExchangeRequest()
	cer.OriginHost = models_base.DiameterIdentity("client.example.com")
	cer.OriginRealm = models_base.DiameterIdentity("example.com")
	cer.HostIpAddress = []models_base.Address{
		models_base.Address(net.ParseIP("10.1.1.1")),
	}
	cer.VendorId = models_base.Unsigned32(10415)
	cer.ProductName = models_base.UTF8String("BenchmarkTest")
	cer.Header.HopByHopID = 1
	cer.Header.EndToEndID = 1

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		data, err := cer.Marshal()
		if err != nil {
			b.Fatal(err)
		}

		msg := &CapabilitiesExchangeRequest{}
		err = msg.Unmarshal(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDWRMarshal benchmarks DWR (smaller message)
func BenchmarkDWRMarshal(b *testing.B) {
	dwr := NewDeviceWatchdogRequest()
	dwr.OriginHost = models_base.DiameterIdentity("client.example.com")
	dwr.OriginRealm = models_base.DiameterIdentity("example.com")
	dwr.Header.HopByHopID = 1
	dwr.Header.EndToEndID = 1

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := dwr.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkACRMarshal benchmarks ACR (typical application message)
func BenchmarkACRMarshal(b *testing.B) {
	acr := NewAccountingRequest()
	acr.SessionId = models_base.UTF8String("client.example.com;1234567890;1")
	acr.OriginHost = models_base.DiameterIdentity("client.example.com")
	acr.OriginRealm = models_base.DiameterIdentity("example.com")
	acr.DestinationRealm = models_base.DiameterIdentity("server.example.com")
	acr.AccountingRecordType = models_base.Enumerated(2)
	acr.AccountingRecordNumber = models_base.Unsigned32(1)
	acr.Header.HopByHopID = 1
	acr.Header.EndToEndID = 1

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := acr.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParallelCERMarshal benchmarks parallel marshaling
func BenchmarkParallelCERMarshal(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		cer := NewCapabilitiesExchangeRequest()
		cer.OriginHost = models_base.DiameterIdentity("client.example.com")
		cer.OriginRealm = models_base.DiameterIdentity("example.com")
		cer.HostIpAddress = []models_base.Address{
			models_base.Address(net.ParseIP("10.1.1.1")),
		}
		cer.VendorId = models_base.Unsigned32(10415)
		cer.ProductName = models_base.UTF8String("BenchmarkTest")
		cer.Header.HopByHopID = 1
		cer.Header.EndToEndID = 1

		for pb.Next() {
			_, err := cer.Marshal()
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
