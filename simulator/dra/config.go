package main

import (
	"fmt"
	"time"
)

// Config holds the DRA simulator configuration
type Config struct {
	// Network settings
	Host string
	Port int

	// Diameter identity
	OriginHost  string
	OriginRealm string
	ProductName string
	VendorID    uint32

	// Connection limits
	MaxConnections int

	// Timeouts
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	DWRInterval  time.Duration

	// Metrics
	EnableMetrics   bool
	MetricsInterval time.Duration

	// Routing
	EnableRouting  bool
	BackendServers string // Comma-separated list
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("host cannot be empty")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if c.OriginHost == "" {
		return fmt.Errorf("origin-host cannot be empty")
	}
	if c.OriginRealm == "" {
		return fmt.Errorf("origin-realm cannot be empty")
	}
	if c.ProductName == "" {
		return fmt.Errorf("product-name cannot be empty")
	}
	if c.MaxConnections <= 0 {
		return fmt.Errorf("max-connections must be greater than 0")
	}
	if c.ReadTimeout <= 0 {
		return fmt.Errorf("read-timeout must be greater than 0")
	}
	if c.WriteTimeout <= 0 {
		return fmt.Errorf("write-timeout must be greater than 0")
	}
	if c.DWRInterval <= 0 {
		return fmt.Errorf("dwr-interval must be greater than 0")
	}
	if c.EnableMetrics && c.MetricsInterval <= 0 {
		return fmt.Errorf("metrics-interval must be greater than 0 when metrics enabled")
	}
	if c.EnableRouting && c.BackendServers == "" {
		return fmt.Errorf("backend-servers required when routing enabled")
	}
	return nil
}
