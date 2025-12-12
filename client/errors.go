package client

import "fmt"

// ErrInvalidConfig represents a configuration validation error
type ErrInvalidConfig struct {
	Field  string
	Reason string
}

func (e ErrInvalidConfig) Error() string {
	return fmt.Sprintf("invalid config field %s: %s", e.Field, e.Reason)
}

// ErrConnectionClosed indicates the connection is closed
type ErrConnectionClosed struct {
	ConnectionID string
}

func (e ErrConnectionClosed) Error() string {
	return fmt.Sprintf("connection %s is closed", e.ConnectionID)
}

// ErrConnectionTimeout indicates a connection operation timed out
type ErrConnectionTimeout struct {
	Operation string
	Timeout   string
}

func (e ErrConnectionTimeout) Error() string {
	return fmt.Sprintf("%s timeout after %s", e.Operation, e.Timeout)
}

// ErrHandshakeFailed indicates CER/CEA handshake failed
type ErrHandshakeFailed struct {
	Reason     string
	ResultCode uint32
}

func (e ErrHandshakeFailed) Error() string {
	if e.ResultCode != 0 {
		return fmt.Sprintf("handshake failed: %s (result code: %d)", e.Reason, e.ResultCode)
	}
	return fmt.Sprintf("handshake failed: %s", e.Reason)
}

// ErrInvalidMessage indicates an invalid Diameter message
type ErrInvalidMessage struct {
	Reason string
}

func (e ErrInvalidMessage) Error() string {
	return fmt.Sprintf("invalid message: %s", e.Reason)
}

// ErrPoolClosed indicates the connection pool is closed
type ErrPoolClosed struct{}

func (e ErrPoolClosed) Error() string {
	return "connection pool is closed"
}

// ErrNoActiveConnections indicates no active connections are available
type ErrNoActiveConnections struct{}

func (e ErrNoActiveConnections) Error() string {
	return "no active connections available"
}
