package client

import "fmt"

// ConnectionState represents the state of a Diameter connection
type ConnectionState int

const (
	// StateDisconnected is the initial state with no connection
	StateDisconnected ConnectionState = iota
	// StateConnecting indicates TCP connection in progress
	StateConnecting
	// StateCERSent indicates CER has been sent, waiting for CEA
	StateCERSent
	// StateOpen indicates connection is established and ready
	StateOpen
	// StateDWRSent indicates watchdog request sent, waiting for DWA
	StateDWRSent
	// StateFailed indicates connection has failed
	StateFailed
	// StateReconnecting indicates attempting to reconnect
	StateReconnecting
	// StateClosed indicates connection is intentionally closed
	StateClosed
)

// String returns the string representation of ConnectionState
func (s ConnectionState) String() string {
	switch s {
	case StateDisconnected:
		return "DISCONNECTED"
	case StateConnecting:
		return "CONNECTING"
	case StateCERSent:
		return "CER_SENT"
	case StateOpen:
		return "OPEN"
	case StateDWRSent:
		return "DWR_SENT"
	case StateFailed:
		return "FAILED"
	case StateReconnecting:
		return "RECONNECTING"
	case StateClosed:
		return "CLOSED"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", s)
	}
}

// IsActive returns true if the connection state is active (can send/receive messages)
func (s ConnectionState) IsActive() bool {
	return s == StateOpen || s == StateDWRSent
}

// CanReconnect returns true if the connection can attempt reconnection
func (s ConnectionState) CanReconnect() bool {
	return s == StateFailed || s == StateDisconnected
}
