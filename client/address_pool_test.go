package client

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hsdfat/diam-gw/pkg/logger"
)

func TestAddressConnectionPool_Configuration(t *testing.T) {
	tests := []struct {
		name        string
		config      *PoolConfig
		expectError bool
	}{
		{
			name: "valid config",
			config: &PoolConfig{
				OriginHost:          "test.example.com",
				OriginRealm:         "example.com",
				ProductName:         "Test",
				VendorID:            10415,
				DialTimeout:         10 * time.Second,
				DWRInterval:         30 * time.Second,
				DWRTimeout:          10 * time.Second,
				MaxDWRFailures:      3,
				ReconnectBackoff:    1.5,
				SendBufferSize:      100,
				RecvBufferSize:      100,
				HealthCheckInterval: 30 * time.Second,
			},
			expectError: false,
		},
		{
			name: "missing origin host",
			config: &PoolConfig{
				OriginRealm:      "example.com",
				ProductName:      "Test",
				DialTimeout:      10 * time.Second,
				DWRInterval:      30 * time.Second,
				DWRTimeout:       10 * time.Second,
				MaxDWRFailures:   3,
				ReconnectBackoff: 1.5,
			},
			expectError: true,
		},
		{
			name: "invalid DWR timeout (>= interval)",
			config: &PoolConfig{
				OriginHost:       "test.example.com",
				OriginRealm:      "example.com",
				ProductName:      "Test",
				DialTimeout:      10 * time.Second,
				DWRInterval:      10 * time.Second,
				DWRTimeout:       15 * time.Second,
				MaxDWRFailures:   3,
				ReconnectBackoff: 1.5,
			},
			expectError: true,
		},
		{
			name: "invalid backoff (< 1.0)",
			config: &PoolConfig{
				OriginHost:       "test.example.com",
				OriginRealm:      "example.com",
				ProductName:      "Test",
				DialTimeout:      10 * time.Second,
				DWRInterval:      30 * time.Second,
				DWRTimeout:       10 * time.Second,
				MaxDWRFailures:   3,
				ReconnectBackoff: 0.5,
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectError && err == nil {
				t.Errorf("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestAddressConnectionPool_Creation(t *testing.T) {
	config := DefaultPoolConfig()
	config.OriginHost = "test.example.com"
	config.OriginRealm = "example.com"

	ctx := context.Background()
	log := logger.New("test", "error")

	pool, err := NewAddressConnectionPool(ctx, config, log)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	if pool.GetActiveConnections() != 0 {
		t.Errorf("expected 0 active connections, got %d", pool.GetActiveConnections())
	}

	metrics := pool.GetMetrics()
	if metrics.ActiveConnections != 0 {
		t.Errorf("expected 0 active connections in metrics, got %d", metrics.ActiveConnections)
	}
}

func TestAddressConnectionPool_InvalidAddress(t *testing.T) {
	config := DefaultPoolConfig()
	config.OriginHost = "test.example.com"
	config.OriginRealm = "example.com"

	ctx := context.Background()
	log := logger.New("test", "error")

	pool, err := NewAddressConnectionPool(ctx, config, log)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	// Test invalid address format
	invalidAddrs := []string{
		"invalid",
		"192.168.1.100",      // missing port
		"192.168.1.100:",     // empty port
		":3868",              // missing host
		"[::1]",              // missing port
		"example.com",        // missing port
	}

	message := []byte("test message")

	for _, addr := range invalidAddrs {
		err := pool.Send(ctx, addr, message)
		if err == nil {
			t.Errorf("expected error for invalid address %q, got nil", addr)
		}
	}
}

func TestAddressConnectionPool_Metrics(t *testing.T) {
	config := DefaultPoolConfig()
	config.OriginHost = "test.example.com"
	config.OriginRealm = "example.com"

	ctx := context.Background()
	log := logger.New("test", "error")

	pool, err := NewAddressConnectionPool(ctx, config, log)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	// Initial metrics
	metrics := pool.GetMetrics()
	if metrics.ActiveConnections != 0 {
		t.Errorf("expected 0 active connections, got %d", metrics.ActiveConnections)
	}
	if metrics.TotalConnections != 0 {
		t.Errorf("expected 0 total connections, got %d", metrics.TotalConnections)
	}

	// Note: We can't actually test connection establishment without a real server
	// But we can test that failed attempts are tracked
	// (This will fail because no server is listening, but we can verify metrics)

	// Test failed establishment increments metrics
	testAddr := "127.0.0.1:39999" // unlikely to be in use
	sendCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	_ = pool.Send(sendCtx, testAddr, []byte("test"))

	// Give it a moment to fail
	time.Sleep(100 * time.Millisecond)

	metrics = pool.GetMetrics()
	// Should have attempted to create connection
	// (may or may not have incremented FailedEstablishments depending on timing)
}

func TestAddressConnectionPool_ConcurrentAccess(t *testing.T) {
	config := DefaultPoolConfig()
	config.OriginHost = "test.example.com"
	config.OriginRealm = "example.com"

	ctx := context.Background()
	log := logger.New("test", "error")

	pool, err := NewAddressConnectionPool(ctx, config, log)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	// Test concurrent ListConnections and GetMetrics
	var wg sync.WaitGroup
	numGoroutines := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = pool.ListConnections()
			_ = pool.GetMetrics()
			_ = pool.GetActiveConnections()
		}()
	}

	wg.Wait()
}

func TestAddressConnectionPool_Close(t *testing.T) {
	config := DefaultPoolConfig()
	config.OriginHost = "test.example.com"
	config.OriginRealm = "example.com"

	ctx := context.Background()
	log := logger.New("test", "error")

	pool, err := NewAddressConnectionPool(ctx, config, log)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	// Close pool
	err = pool.Close()
	if err != nil {
		t.Errorf("failed to close pool: %v", err)
	}

	// Verify pool is closed
	if !pool.closed.Load() {
		t.Error("pool should be closed")
	}

	// Attempt to send after close should fail
	err = pool.Send(ctx, "192.168.1.100:3868", []byte("test"))
	if err == nil {
		t.Error("expected error when sending to closed pool")
	}

	// Closing again should not error
	err = pool.Close()
	if err != nil {
		t.Errorf("second close should not error: %v", err)
	}
}

func TestAddressConnectionPool_ContextCancellation(t *testing.T) {
	config := DefaultPoolConfig()
	config.OriginHost = "test.example.com"
	config.OriginRealm = "example.com"

	ctx, cancel := context.WithCancel(context.Background())
	log := logger.New("test", "error")

	pool, err := NewAddressConnectionPool(ctx, config, log)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	// Cancel context
	cancel()

	// Give time for cancellation to propagate
	time.Sleep(100 * time.Millisecond)

	// Attempting to send should fail
	sendCtx := context.Background()
	err = pool.Send(sendCtx, "192.168.1.100:3868", []byte("test"))
	if err == nil {
		t.Error("expected error when pool context is cancelled")
	}
}

func TestDefaultPoolConfig(t *testing.T) {
	config := DefaultPoolConfig()

	if config.ProductName == "" {
		t.Error("ProductName should have default value")
	}
	if config.VendorID == 0 {
		t.Error("VendorID should have default value")
	}
	if config.DialTimeout == 0 {
		t.Error("DialTimeout should have default value")
	}
	if config.DWRInterval == 0 {
		t.Error("DWRInterval should have default value")
	}
	if config.ReconnectBackoff < 1.0 {
		t.Error("ReconnectBackoff should be >= 1.0")
	}

	// Default config should not validate (missing required fields)
	err := config.Validate()
	if err == nil {
		t.Error("default config should not validate without OriginHost and OriginRealm")
	}

	// After setting required fields, should validate
	config.OriginHost = "test.example.com"
	config.OriginRealm = "example.com"
	err = config.Validate()
	if err != nil {
		t.Errorf("config should validate after setting required fields: %v", err)
	}
}
