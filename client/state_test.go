package client

import "testing"

func TestConnectionState_String(t *testing.T) {
	tests := []struct {
		state    ConnectionState
		expected string
	}{
		{StateDisconnected, "DISCONNECTED"},
		{StateConnecting, "CONNECTING"},
		{StateCERSent, "CER_SENT"},
		{StateOpen, "OPEN"},
		{StateDWRSent, "DWR_SENT"},
		{StateFailed, "FAILED"},
		{StateReconnecting, "RECONNECTING"},
		{StateClosed, "CLOSED"},
		{ConnectionState(99), "UNKNOWN(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.state.String(); got != tt.expected {
				t.Errorf("String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestConnectionState_IsActive(t *testing.T) {
	tests := []struct {
		state    ConnectionState
		expected bool
	}{
		{StateDisconnected, false},
		{StateConnecting, false},
		{StateCERSent, false},
		{StateOpen, true},
		{StateDWRSent, true},
		{StateFailed, false},
		{StateReconnecting, false},
		{StateClosed, false},
	}

	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			if got := tt.state.IsActive(); got != tt.expected {
				t.Errorf("IsActive() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestConnectionState_CanReconnect(t *testing.T) {
	tests := []struct {
		state    ConnectionState
		expected bool
	}{
		{StateDisconnected, true},
		{StateConnecting, false},
		{StateCERSent, false},
		{StateOpen, false},
		{StateDWRSent, false},
		{StateFailed, true},
		{StateReconnecting, false},
		{StateClosed, false},
	}

	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			if got := tt.state.CanReconnect(); got != tt.expected {
				t.Errorf("CanReconnect() = %v, want %v", got, tt.expected)
			}
		})
	}
}
