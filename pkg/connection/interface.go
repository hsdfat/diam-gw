package connection

import (
	"context"
	"crypto/tls"
	"net"
)

// Conn interface is used by a handler to send diameter messages.
// This interface is shared between server and client implementations.
type Conn interface {
	Write(b []byte) (int, error)                    // Writes a msg to the connection
	WriteStream(b []byte, stream uint) (int, error) // Writes a msg to the connection's stream
	Close() error                                   // Close the connection
	LocalAddr() net.Addr                            // Returns the local IP
	RemoteAddr() net.Addr                           // Returns the remote IP
	TLS() *tls.ConnectionState                      // TLS or nil when not using TLS
	Context() context.Context                       // Returns the internal context
	SetContext(ctx context.Context)                 // Stores a new context
	Connection() net.Conn                           // Returns network connection
}

// The CloseNotifier interface is implemented by Conns which
// allow detecting when the underlying connection has gone away.
//
// This mechanism can be used to detect if a peer has disconnected.
type CloseNotifier interface {
	// CloseNotify returns a channel that is closed
	// when the client connection has gone away.
	CloseNotify() <-chan struct{}
}
