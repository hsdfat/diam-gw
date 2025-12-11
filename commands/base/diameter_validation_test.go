package base

import (
	"net"
	"strings"
	"testing"

	"github.com/hsdfat8/diam-gw/models_base"
)

func TestCERValidation(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() *CapabilitiesExchangeRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid CER",
			setup: func() *CapabilitiesExchangeRequest {
				cer := NewCapabilitiesExchangeRequest()
				cer.OriginHost = models_base.DiameterIdentity("host.example.com")
				cer.OriginRealm = models_base.DiameterIdentity("example.com")
				cer.HostIpAddress = []models_base.Address{
					models_base.Address(net.ParseIP("127.0.0.1")),
				}
				cer.VendorId = models_base.Unsigned32(10415)
				cer.ProductName = models_base.UTF8String("Test")
				return cer
			},
			wantErr: false,
		},
		{
			name: "missing Origin-Host",
			setup: func() *CapabilitiesExchangeRequest {
				cer := NewCapabilitiesExchangeRequest()
				cer.OriginRealm = models_base.DiameterIdentity("example.com")
				cer.HostIpAddress = []models_base.Address{
					models_base.Address(net.ParseIP("127.0.0.1")),
				}
				cer.VendorId = models_base.Unsigned32(10415)
				cer.ProductName = models_base.UTF8String("Test")
				return cer
			},
			wantErr: true,
			errMsg:  "Origin-Host",
		},
		{
			name: "missing Origin-Realm",
			setup: func() *CapabilitiesExchangeRequest {
				cer := NewCapabilitiesExchangeRequest()
				cer.OriginHost = models_base.DiameterIdentity("host.example.com")
				cer.HostIpAddress = []models_base.Address{
					models_base.Address(net.ParseIP("127.0.0.1")),
				}
				cer.VendorId = models_base.Unsigned32(10415)
				cer.ProductName = models_base.UTF8String("Test")
				return cer
			},
			wantErr: true,
			errMsg:  "Origin-Realm",
		},
		{
			name: "missing Host-IP-Address",
			setup: func() *CapabilitiesExchangeRequest {
				cer := NewCapabilitiesExchangeRequest()
				cer.OriginHost = models_base.DiameterIdentity("host.example.com")
				cer.OriginRealm = models_base.DiameterIdentity("example.com")
				cer.VendorId = models_base.Unsigned32(10415)
				cer.ProductName = models_base.UTF8String("Test")
				return cer
			},
			wantErr: true,
			errMsg:  "Host-IP-Address",
		},
		{
			name: "missing Product-Name",
			setup: func() *CapabilitiesExchangeRequest {
				cer := NewCapabilitiesExchangeRequest()
				cer.OriginHost = models_base.DiameterIdentity("host.example.com")
				cer.OriginRealm = models_base.DiameterIdentity("example.com")
				cer.HostIpAddress = []models_base.Address{
					models_base.Address(net.ParseIP("127.0.0.1")),
				}
				cer.VendorId = models_base.Unsigned32(10415)
				return cer
			},
			wantErr: true,
			errMsg:  "Product-Name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cer := tt.setup()
			err := cer.Validate()

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestCEAValidation(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() *CapabilitiesExchangeAnswer
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid CEA",
			setup: func() *CapabilitiesExchangeAnswer {
				cea := NewCapabilitiesExchangeAnswer()
				cea.ResultCode = models_base.Unsigned32(2001)
				cea.OriginHost = models_base.DiameterIdentity("host.example.com")
				cea.OriginRealm = models_base.DiameterIdentity("example.com")
				cea.HostIpAddress = []models_base.Address{
					models_base.Address(net.ParseIP("127.0.0.1")),
				}
				cea.VendorId = models_base.Unsigned32(10415)
				cea.ProductName = models_base.UTF8String("Test")
				return cea
			},
			wantErr: false,
		},
		{
			name: "missing Host-IP-Address",
			setup: func() *CapabilitiesExchangeAnswer {
				cea := NewCapabilitiesExchangeAnswer()
				cea.ResultCode = models_base.Unsigned32(2001)
				cea.OriginHost = models_base.DiameterIdentity("host.example.com")
				cea.OriginRealm = models_base.DiameterIdentity("example.com")
				cea.VendorId = models_base.Unsigned32(10415)
				cea.ProductName = models_base.UTF8String("Test")
				return cea
			},
			wantErr: true,
			errMsg:  "Host-IP-Address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cea := tt.setup()
			err := cea.Validate()

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestMarshalValidation(t *testing.T) {
	// Test that Marshal calls Validate and returns error
	cer := NewCapabilitiesExchangeRequest()
	// Don't set required fields

	_, err := cer.Marshal()
	if err == nil {
		t.Error("expected Marshal to fail validation, got nil error")
	}

	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("expected 'validation failed' in error, got: %v", err)
	}
}

func TestDWRValidation(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() *DeviceWatchdogRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid DWR",
			setup: func() *DeviceWatchdogRequest {
				dwr := NewDeviceWatchdogRequest()
				dwr.OriginHost = models_base.DiameterIdentity("host.example.com")
				dwr.OriginRealm = models_base.DiameterIdentity("example.com")
				return dwr
			},
			wantErr: false,
		},
		{
			name: "missing Origin-Host",
			setup: func() *DeviceWatchdogRequest {
				dwr := NewDeviceWatchdogRequest()
				dwr.OriginRealm = models_base.DiameterIdentity("example.com")
				return dwr
			},
			wantErr: true,
			errMsg:  "Origin-Host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dwr := tt.setup()
			err := dwr.Validate()

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestSTRValidation(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() *SessionTerminationRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid STR",
			setup: func() *SessionTerminationRequest {
				str := NewSessionTerminationRequest()
				str.SessionId = models_base.UTF8String("session123")
				str.OriginHost = models_base.DiameterIdentity("host.example.com")
				str.OriginRealm = models_base.DiameterIdentity("example.com")
				str.DestinationRealm = models_base.DiameterIdentity("dest.example.com")
				str.AuthApplicationId = models_base.Unsigned32(16777238)
				str.TerminationCause = models_base.Enumerated(1)
				return str
			},
			wantErr: false,
		},
		{
			name: "missing Session-Id",
			setup: func() *SessionTerminationRequest {
				str := NewSessionTerminationRequest()
				str.OriginHost = models_base.DiameterIdentity("host.example.com")
				str.OriginRealm = models_base.DiameterIdentity("example.com")
				str.DestinationRealm = models_base.DiameterIdentity("dest.example.com")
				str.AuthApplicationId = models_base.Unsigned32(16777238)
				str.TerminationCause = models_base.Enumerated(1)
				return str
			},
			wantErr: true,
			errMsg:  "Session-Id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			str := tt.setup()
			err := str.Validate()

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestValidateBeforeMarshal(t *testing.T) {
	// Ensure Marshal automatically validates
	tests := []struct {
		name    string
		msg     interface{ Marshal() ([]byte, error) }
		wantErr bool
	}{
		{
			name: "valid CER marshals successfully",
			msg: func() *CapabilitiesExchangeRequest {
				cer := NewCapabilitiesExchangeRequest()
				cer.OriginHost = models_base.DiameterIdentity("host.example.com")
				cer.OriginRealm = models_base.DiameterIdentity("example.com")
				cer.HostIpAddress = []models_base.Address{
					models_base.Address(net.ParseIP("127.0.0.1")),
				}
				cer.VendorId = models_base.Unsigned32(10415)
				cer.ProductName = models_base.UTF8String("Test")
				return cer
			}(),
			wantErr: false,
		},
		{
			name:    "invalid CER fails to marshal",
			msg:     NewCapabilitiesExchangeRequest(), // Empty, all required fields missing
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.msg.Marshal()
			if tt.wantErr && err == nil {
				t.Error("expected Marshal to fail, got nil error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
