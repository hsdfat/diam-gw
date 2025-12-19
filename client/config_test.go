package client

import (
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.Port != 3868 {
		t.Errorf("Expected port 3868, got %d", config.Port)
	}

	if config.VendorID != 10415 {
		t.Errorf("Expected VendorID 10415, got %d", config.VendorID)
	}

	if config.ConnectionCount != 5 {
		t.Errorf("Expected ConnectionCount 5, got %d", config.ConnectionCount)
	}

	if config.DWRInterval != 30*time.Second {
		t.Errorf("Expected DWRInterval 30s, got %v", config.DWRInterval)
	}
}

func TestDRAConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *DRAConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: &DRAConfig{
				Host:            "dra.example.com",
				Port:            3868,
				OriginHost:      "gateway.example.com",
				OriginRealm:     "example.com",
				ProductName:     "Test",
				ConnectionCount: 1,
				DWRInterval:     30 * time.Second,
				DWRTimeout:      10 * time.Second,
				MaxDWRFailures:  3,
			},
			wantErr: false,
		},
		{
			name: "empty host",
			config: &DRAConfig{
				Host:            "",
				Port:            3868,
				OriginHost:      "gateway.example.com",
				OriginRealm:     "example.com",
				ProductName:     "Test",
				ConnectionCount: 1,
				DWRInterval:     30 * time.Second,
				DWRTimeout:      10 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "invalid port",
			config: &DRAConfig{
				Host:            "dra.example.com",
				Port:            0,
				OriginHost:      "gateway.example.com",
				OriginRealm:     "example.com",
				ProductName:     "Test",
				ConnectionCount: 1,
				DWRInterval:     30 * time.Second,
				DWRTimeout:      10 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "empty origin host",
			config: &DRAConfig{
				Host:            "dra.example.com",
				Port:            3868,
				OriginHost:      "",
				OriginRealm:     "example.com",
				ProductName:     "Test",
				ConnectionCount: 1,
				DWRInterval:     30 * time.Second,
				DWRTimeout:      10 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "empty origin realm",
			config: &DRAConfig{
				Host:            "dra.example.com",
				Port:            3868,
				OriginHost:      "gateway.example.com",
				OriginRealm:     "",
				ProductName:     "Test",
				ConnectionCount: 1,
				DWRInterval:     30 * time.Second,
				DWRTimeout:      10 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "zero connection count",
			config: &DRAConfig{
				Host:            "dra.example.com",
				Port:            3868,
				OriginHost:      "gateway.example.com",
				OriginRealm:     "example.com",
				ProductName:     "Test",
				ConnectionCount: 0,
				DWRInterval:     30 * time.Second,
				DWRTimeout:      10 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "DWR timeout >= interval",
			config: &DRAConfig{
				Host:            "dra.example.com",
				Port:            3868,
				OriginHost:      "gateway.example.com",
				OriginRealm:     "example.com",
				ProductName:     "Test",
				ConnectionCount: 1,
				DWRInterval:     30 * time.Second,
				DWRTimeout:      30 * time.Second,
				MaxDWRFailures:  3,
			},
			wantErr: true,
		},
		{
			name: "zero max DWR failures",
			config: &DRAConfig{
				Host:            "dra.example.com",
				Port:            3868,
				OriginHost:      "gateway.example.com",
				OriginRealm:     "example.com",
				ProductName:     "Test",
				ConnectionCount: 1,
				DWRInterval:     30 * time.Second,
				DWRTimeout:      10 * time.Second,
				MaxDWRFailures:  0,
			},
			wantErr: true,
		},
		{
			name: "negative max DWR failures",
			config: &DRAConfig{
				Host:            "dra.example.com",
				Port:            3868,
				OriginHost:      "gateway.example.com",
				OriginRealm:     "example.com",
				ProductName:     "Test",
				ConnectionCount: 1,
				DWRInterval:     30 * time.Second,
				DWRTimeout:      10 * time.Second,
				MaxDWRFailures:  -1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
