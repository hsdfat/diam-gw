package client

import (
	"strings"
	"testing"
)

func TestErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		contains string
	}{
		{
			name: "ErrInvalidConfig",
			err: ErrInvalidConfig{
				Field:  "Host",
				Reason: "must not be empty",
			},
			contains: "Host",
		},
		{
			name: "ErrConnectionClosed",
			err: ErrConnectionClosed{
				ConnectionID: "conn-1",
			},
			contains: "conn-1",
		},
		{
			name: "ErrConnectionTimeout",
			err: ErrConnectionTimeout{
				Operation: "CER/CEA",
				Timeout:   "5s",
			},
			contains: "CER/CEA",
		},
		{
			name: "ErrHandshakeFailed",
			err: ErrHandshakeFailed{
				Reason:     "invalid response",
				ResultCode: 5001,
			},
			contains: "handshake failed",
		},
		{
			name: "ErrInvalidMessage",
			err: ErrInvalidMessage{
				Reason: "message too short",
			},
			contains: "invalid message",
		},
		{
			name:     "ErrPoolClosed",
			err:      ErrPoolClosed{},
			contains: "pool is closed",
		},
		{
			name:     "ErrNoActiveConnections",
			err:      ErrNoActiveConnections{},
			contains: "no active connections",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errStr := tt.err.Error()
			if !strings.Contains(strings.ToLower(errStr), strings.ToLower(tt.contains)) {
				t.Errorf("Error() = %v, want to contain %v", errStr, tt.contains)
			}
		})
	}
}
