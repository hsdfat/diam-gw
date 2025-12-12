package main

import (
	"context"
	"time"

	"github.com/hsdfat8/diam-gw/client"
	"github.com/hsdfat8/diam-gw/pkg/logger"
)

// Simple example demonstrating basic connection pool usage
func main() {
	logger.Log.Infow("Simple Connection Pool Example")
	logger.Log.Infow("================================")

	// Create configuration
	config := client.DefaultConfig()
	config.Host = "127.0.0.1"
	config.Port = 3868
	config.OriginHost = "simple-client.example.com"
	config.OriginRealm = "example.com"
	config.ProductName = "Simple-Example"
	config.ConnectionCount = 3 // Just 3 connections for this simple example

	// Create context
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create connection pool
	logger.Log.Infow("Creating connection pool...")
	pool, err := client.NewConnectionPool(ctx, config)
	if err != nil {
		logger.Log.Fatalf("Failed to create pool: %v", err)
	}

	// Start the pool
	logger.Log.Infow("Starting connection pool...")
	if err := pool.Start(); err != nil {
		logger.Log.Warnf("Warning: Failed to start pool: %v", err)
		logger.Log.Infow("This is expected if no DRA server is running")
	}
	defer pool.Close()

	// Wait for connections
	logger.Log.Infow("Waiting for connections to establish...")
	if err := pool.WaitForConnection(10 * time.Second); err != nil {
		logger.Log.Warnf("No connections available: %v", err)
		logger.Log.Infow("Example will continue to show pool state...")
	} else {
		logger.Log.Infow("✓ At least one connection is active!")
	}

	// Display connection states
	time.Sleep(2 * time.Second)
	displayPoolState(pool)

	// Display statistics every 5 seconds
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	count := 0
	for range ticker.C {
		count++
		displayPoolState(pool)

		if count >= 3 {
			break
		}
	}

	logger.Log.Infow("\nShutting down...")
	logger.Log.Infow("Pool closed successfully")
}

func displayPoolState(pool *client.ConnectionPool) {
	logger.Log.Infow("\n--- Pool State ---")

	// Get statistics
	stats := pool.GetStats()
	logger.Log.Infof("Total Connections: %d", stats.TotalConnections)
	logger.Log.Infof("Active Connections: %d", stats.ActiveConnections)
	logger.Log.Infof("Messages Sent: %d", stats.TotalMessagesSent)
	logger.Log.Infof("Messages Received: %d", stats.TotalMessagesRecv)
	logger.Log.Infof("Bytes Sent: %d", stats.TotalBytesSent)
	logger.Log.Infof("Bytes Received: %d", stats.TotalBytesRecv)
	logger.Log.Infof("Reconnections: %d", stats.TotalReconnects)

	// Get connection states
	logger.Log.Infow("\nConnection States:")
	states := pool.GetConnectionStates()
	for connID, state := range states {
		logger.Log.Infof("  %s: %s", connID, state.String())
	}

	// Health check
	if pool.IsHealthy() {
		logger.Log.Infow("\n✓ Pool is healthy")
	} else {
		logger.Log.Infow("\n✗ Pool is unhealthy (no active connections)")
	}
	logger.Log.Infow("------------------")
}
