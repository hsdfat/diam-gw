package client

import (
	"net"
	"time"
)

// DRAConfig holds the configuration for connecting to a DRA
type DRAConfig struct {
	// Connection settings
	Host string // DRA hostname or IP address
	Port int    // DRA port (typically 3868)

	// Diameter identity
	OriginHost  string // Local origin host (e.g., "gateway.example.com")
	OriginRealm string // Local origin realm (e.g., "example.com")
	ProductName string // Product name to advertise
	VendorID    uint32 // Vendor ID (3GPP = 10415)

	// Connection pool
	ConnectionCount int // Number of connections to maintain per DRA

	// Timeouts
	ConnectTimeout time.Duration // TCP connection timeout
	CERTimeout     time.Duration // CER/CEA exchange timeout
	DWRInterval    time.Duration // Interval between watchdog requests
	DWRTimeout     time.Duration // Watchdog timeout
	MaxDWRFailures int           // Maximum consecutive DWR failures before reconnecting

	// Reconnection strategy
	ReconnectInterval time.Duration // Initial reconnect delay
	MaxReconnectDelay time.Duration // Maximum reconnect delay
	ReconnectBackoff  float64       // Backoff multiplier for exponential backoff

	// Buffer sizes
	SendBufferSize int // Send channel buffer size
	RecvBufferSize int // Receive channel buffer size

	// Application IDs to advertise in CER
	AuthAppIDs []uint32 // Auth-Application-IDs
	AcctAppIDs []uint32 // Acct-Application-IDs

	// AcquireLocalAddr, when non-nil, is called before each TCP dial to
	// obtain the local address (source IP + port) the dialer must bind.
	// The owner string is the connection id, useful for diagnostics. The
	// returned error becomes the dial failure: per the telco profile, the
	// connection MUST NOT silently fall back to an OS-chosen ephemeral
	// port — exhaustion is a hard failure that the reconnect loop will
	// surface and retry.
	//
	// Production wires this to portpool.Pool.Acquire via a thin closure
	// in pkg/connmanager. Leaving it nil preserves the legacy OS-ephemeral
	// behaviour for code paths that have not migrated yet.
	AcquireLocalAddr func(owner string) (*net.TCPAddr, error)

	// ReleaseLocalAddr, when non-nil, is called to return a previously
	// acquired local address back to its source. It runs on dial failure
	// and on connection close, including the deferred-cleanup paths used
	// after handshake failures, so the pool never leaks.
	ReleaseLocalAddr func(*net.TCPAddr)
}

// DefaultConfig returns a DRAConfig with sensible defaults
func DefaultConfig() *DRAConfig {
	return &DRAConfig{
		Port:              3868,
		ProductName:       "Diameter-GW-S13",
		VendorID:          10415, // 3GPP
		ConnectionCount:   5,
		ConnectTimeout:    10 * time.Second,
		CERTimeout:        5 * time.Second,
		DWRInterval:       30 * time.Second,
		DWRTimeout:        10 * time.Second,
		MaxDWRFailures:    3,
		ReconnectInterval: 5 * time.Second,
		MaxReconnectDelay: 5 * time.Minute,
		ReconnectBackoff:  1.5,
		SendBufferSize:    1000,
		RecvBufferSize:    1000,
	}
}

// Validate checks if the configuration is valid
func (c *DRAConfig) Validate() error {
	if c.Host == "" {
		return ErrInvalidConfig{Field: "Host", Reason: "must not be empty"}
	}
	if c.Port <= 0 || c.Port > 65535 {
		return ErrInvalidConfig{Field: "Port", Reason: "must be between 1 and 65535"}
	}
	if c.OriginHost == "" {
		return ErrInvalidConfig{Field: "OriginHost", Reason: "must not be empty"}
	}
	if c.OriginRealm == "" {
		return ErrInvalidConfig{Field: "OriginRealm", Reason: "must not be empty"}
	}
	if c.ProductName == "" {
		return ErrInvalidConfig{Field: "ProductName", Reason: "must not be empty"}
	}
	if c.ConnectionCount <= 0 {
		return ErrInvalidConfig{Field: "ConnectionCount", Reason: "must be greater than 0"}
	}
	if c.DWRInterval <= 0 {
		return ErrInvalidConfig{Field: "DWRInterval", Reason: "must be greater than 0"}
	}
	if c.DWRTimeout <= 0 {
		return ErrInvalidConfig{Field: "DWRTimeout", Reason: "must be greater than 0"}
	}
	if c.DWRTimeout >= c.DWRInterval {
		return ErrInvalidConfig{Field: "DWRTimeout", Reason: "must be less than DWRInterval"}
	}
	if c.MaxDWRFailures <= 0 {
		return ErrInvalidConfig{Field: "MaxDWRFailures", Reason: "must be greater than 0"}
	}
	return nil
}
