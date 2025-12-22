package client

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestManagedConnection_FailureCallback tests that failure callbacks trigger immediate cleanup
func TestManagedConnection_FailureCallback(t *testing.T) {
	ctx := context.Background()

	config := DefaultPoolConfig()
	config.OriginHost = "test-client.example.com"
	config.OriginRealm = "example.com"
	config.DWRInterval = 1 * time.Second
	config.DWRTimeout = 500 * time.Millisecond
	config.MaxDWRFailures = 2
	config.ReconnectEnabled = false // Disable reconnect for testing
	config.HealthCheckInterval = 10 * time.Second // Set high to ensure callback fires first

	pool, err := NewAddressConnectionPool(ctx, config, nil)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.Close()

	// Track callback invocations
	var callbackFired sync.WaitGroup
	callbackFired.Add(1)
	var callbackErr error

	// Create a managed connection with callback
	mc := &ManagedConnection{
		conn:      nil, // Will be set after conn creation
		address:   "127.0.0.1:9999",
		createdAt: time.Now(),
		pool:      pool,
	}

	mc.ctx, mc.cancel = context.WithCancel(ctx)

	// Create a connection
	conn := NewConnection(mc.ctx, "test-conn", &DRAConfig{
		Host:              "127.0.0.1",
		Port:              9999,
		OriginHost:        config.OriginHost,
		OriginRealm:       config.OriginRealm,
		ProductName:       config.ProductName,
		VendorID:          config.VendorID,
		ConnectionCount:   1,
		ConnectTimeout:    1 * time.Second,
		CERTimeout:        1 * time.Second,
		DWRInterval:       config.DWRInterval,
		DWRTimeout:        config.DWRTimeout,
		MaxDWRFailures:    config.MaxDWRFailures,
		ReconnectInterval: 5 * time.Second,
		SendBufferSize:    100,
		RecvBufferSize:    100,
	}, pool.logger)

	mc.conn = conn

	// Set failure callback
	conn.SetOnFailure(func(err error) {
		callbackErr = err
		callbackFired.Done()
		mc.onConnectionFailure(err)
	})

	// Simulate a failure
	go func() {
		time.Sleep(100 * time.Millisecond)
		conn.handleFailure(ErrConnectionClosed{ConnectionID: "test-conn"})
	}()

	// Wait for callback
	done := make(chan struct{})
	go func() {
		callbackFired.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - callback was fired
		if callbackErr == nil {
			t.Error("Expected callback error to be set")
		}
		t.Logf("Callback fired with error: %v", callbackErr)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for failure callback")
	}

	// Give a moment for cleanup
	time.Sleep(200 * time.Millisecond)

	// Verify that the managed connection context was cancelled
	select {
	case <-mc.ctx.Done():
		t.Log("Managed connection context was cancelled as expected")
	default:
		t.Error("Expected managed connection context to be cancelled")
	}
}

// TestConnection_OnFailureCallback tests the callback mechanism in Connection
func TestConnection_OnFailureCallback(t *testing.T) {
	ctx := context.Background()

	config := &DRAConfig{
		Host:              "127.0.0.1",
		Port:              9999,
		OriginHost:        "test.example.com",
		OriginRealm:       "example.com",
		ProductName:       "TestClient",
		VendorID:          10415,
		ConnectTimeout:    1 * time.Second,
		CERTimeout:        1 * time.Second,
		DWRInterval:       1 * time.Second,
		DWRTimeout:        500 * time.Millisecond,
		MaxDWRFailures:    3,
		ReconnectInterval: 5 * time.Second,
		SendBufferSize:    100,
		RecvBufferSize:    100,
	}

	conn := NewConnection(ctx, "test-conn", config, nil)

	// Set up callback
	var callbackMu sync.Mutex
	callbackCalled := false
	var capturedErr error

	conn.SetOnFailure(func(err error) {
		callbackMu.Lock()
		defer callbackMu.Unlock()
		callbackCalled = true
		capturedErr = err
	})

	// Trigger callback directly (not through handleFailure which requires full setup)
	testErr := ErrConnectionClosed{ConnectionID: "test-conn"}
	conn.callOnFailure(testErr)

	// Wait a bit for the goroutine to execute
	time.Sleep(100 * time.Millisecond)

	// Verify callback was called
	callbackMu.Lock()
	defer callbackMu.Unlock()

	if !callbackCalled {
		t.Error("Expected failure callback to be called")
	}

	if capturedErr == nil {
		t.Error("Expected error to be captured in callback")
	}

	t.Logf("Callback successfully captured error: %v", capturedErr)
}

// TestConnection_MultipleCallbackCalls tests that callback can be called multiple times
func TestConnection_MultipleCallbackCalls(t *testing.T) {
	ctx := context.Background()

	config := &DRAConfig{
		Host:              "127.0.0.1",
		Port:              9999,
		OriginHost:        "test.example.com",
		OriginRealm:       "example.com",
		ProductName:       "TestClient",
		VendorID:          10415,
		ConnectTimeout:    1 * time.Second,
		CERTimeout:        1 * time.Second,
		DWRInterval:       1 * time.Second,
		DWRTimeout:        500 * time.Millisecond,
		MaxDWRFailures:    3,
		ReconnectInterval: 5 * time.Second,
		SendBufferSize:    100,
		RecvBufferSize:    100,
	}

	conn := NewConnection(ctx, "test-conn", config, nil)
	conn.DisableReconnect()

	// Set up callback that counts calls
	var callbackMu sync.Mutex
	callCount := 0

	conn.SetOnFailure(func(err error) {
		callbackMu.Lock()
		defer callbackMu.Unlock()
		callCount++
	})

	// Call callback directly (simulating internal behavior)
	conn.callOnFailure(ErrConnectionClosed{ConnectionID: "test-conn"})
	conn.callOnFailure(ErrConnectionClosed{ConnectionID: "test-conn"})

	// Wait for goroutines
	time.Sleep(100 * time.Millisecond)

	// Verify both calls were received
	callbackMu.Lock()
	defer callbackMu.Unlock()

	if callCount != 2 {
		t.Errorf("Expected 2 callback calls, got %d", callCount)
	}
}

// TestConnection_NilCallback tests that nil callback doesn't cause panic
func TestConnection_NilCallback(t *testing.T) {
	ctx := context.Background()

	config := &DRAConfig{
		Host:              "127.0.0.1",
		Port:              9999,
		OriginHost:        "test.example.com",
		OriginRealm:       "example.com",
		ProductName:       "TestClient",
		VendorID:          10415,
		ConnectTimeout:    1 * time.Second,
		CERTimeout:        1 * time.Second,
		DWRInterval:       1 * time.Second,
		DWRTimeout:        500 * time.Millisecond,
		MaxDWRFailures:    3,
		ReconnectInterval: 5 * time.Second,
		SendBufferSize:    100,
		RecvBufferSize:    100,
	}

	conn := NewConnection(ctx, "test-conn", config, nil)

	// Don't set callback - should handle gracefully

	// This should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("callOnFailure panicked with nil callback: %v", r)
		}
	}()

	conn.callOnFailure(ErrConnectionClosed{ConnectionID: "test-conn"})

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	t.Log("Successfully handled nil callback without panic")
}
