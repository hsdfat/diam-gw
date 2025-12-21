package gateway_test

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/gateway"
	"github.com/hsdfat/diam-gw/server"
)

func TestGatewayConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *gateway.GatewayConfig
		wantErr bool
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
		},
		{
			name: "missing OriginHost",
			config: &gateway.GatewayConfig{
				OriginRealm:   "example.com",
				DRAPoolConfig: &client.DRAPoolConfig{},
			},
			wantErr: true,
		},
		{
			name: "missing OriginRealm",
			config: &gateway.GatewayConfig{
				OriginHost:    "gw.example.com",
				DRAPoolConfig: &client.DRAPoolConfig{},
			},
			wantErr: true,
		},
		{
			name: "missing DRAPoolConfig",
			config: &gateway.GatewayConfig{
				OriginHost:  "gw.example.com",
				OriginRealm: "example.com",
			},
			wantErr: true,
		},
		{
			name: "valid config",
			config: &gateway.GatewayConfig{
				OriginHost:  "gw.example.com",
				OriginRealm: "example.com",
				ProductName: "Test-Gateway",
				VendorID:    10415,
				DRAPoolConfig: &client.DRAPoolConfig{
					DRAs: []*client.DRAServerConfig{
						{
							Name:     "DRA-1",
							Host:     "127.0.0.1",
							Port:     3868,
							Priority: 1,
						},
					},
					ConnectionsPerDRA: 1,
				},
				InServerConfig:  server.DefaultServerConfig(),
				OutServerConfig: server.DefaultServerConfig(),
				SessionTimeout:  30 * time.Second,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := gateway.ValidateGatewayConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("validategateway.GatewayConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultGatewayConfig(t *testing.T) {
	config := gateway.DefaultGatewayConfig()

	if config.OriginHost == "" {
		t.Error("Expected OriginHost to be set")
	}
	if config.OriginRealm == "" {
		t.Error("Expected OriginRealm to be set")
	}
	if config.ProductName == "" {
		t.Error("Expected ProductName to be set")
	}
	if config.VendorID == 0 {
		t.Error("Expected VendorID to be set")
	}
	if config.SessionTimeout == 0 {
		t.Error("Expected SessionTimeout to be set")
	}
	if config.InServerConfig == nil || config.OutServerConfig == nil {
		t.Error("Expected ServerConfig to be set")
	}
}

func TestMessageHeaderParsing(t *testing.T) {
	// Create a test Diameter message header
	header := make([]byte, 20)

	// Version
	header[0] = 1

	// Message length (20 bytes)
	binary.BigEndian.PutUint32(header[0:4], 20)
	header[0] = 1 // Restore version

	// Command flags (Request bit set)
	header[4] = 0x80 // R=1

	// Command code (316 = ULR)
	binary.BigEndian.PutUint32(header[4:8], 316)
	header[4] = 0x80 // Restore flags

	// Application ID (16777251 = S6a)
	binary.BigEndian.PutUint32(header[8:12], 16777251)

	// Hop-by-Hop ID
	binary.BigEndian.PutUint32(header[12:16], 0x12345678)

	// End-to-End ID
	binary.BigEndian.PutUint32(header[16:20], 0xABCDEF01)

	// Parse message
	msgInfo, err := client.ParseMessageHeader(header)
	if err != nil {
		t.Fatalf("Failed to parse header: %v", err)
	}

	// Verify parsed values
	if msgInfo.CommandCode != 316 {
		t.Errorf("Expected command code 316, got %d", msgInfo.CommandCode)
	}
	if msgInfo.ApplicationID != 16777251 {
		t.Errorf("Expected app ID 16777251, got %d", msgInfo.ApplicationID)
	}
	if msgInfo.HopByHopID != 0x12345678 {
		t.Errorf("Expected H2H 0x12345678, got 0x%x", msgInfo.HopByHopID)
	}
	if msgInfo.EndToEndID != 0xABCDEF01 {
		t.Errorf("Expected E2E 0xABCDEF01, got 0x%x", msgInfo.EndToEndID)
	}
	if !msgInfo.Flags.Request {
		t.Error("Expected Request flag to be set")
	}
}
