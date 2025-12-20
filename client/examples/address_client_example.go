package main

import (
	"context"
	"fmt"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/pkg/logger"
)

func main() {
	// Example 1: Basic usage with default configuration
	basicExample()

	// Example 2: Custom configuration with timeouts
	customConfigExample()

	// Example 3: Concurrent sends to multiple addresses
	concurrentExample()

	// Example 4: Monitoring metrics
	metricsExample()
}

// basicExample demonstrates basic usage of AddressClient
func basicExample() {
	fmt.Println("=== Basic Example ===")

	// Create logger
	log := logger.New("example", "info")

	// Create client with default configuration
	config := client.DefaultPoolConfig()
	config.OriginHost = "client.example.com"
	config.OriginRealm = "example.com"

	ctx := context.Background()
	addressClient, err := client.NewAddressClient(ctx, config, log)
	if err != nil {
		log.Fatalw("Failed to create client", "error", err)
	}
	defer addressClient.Close()

	// Prepare a Diameter message (example: CCR)
	// In real usage, you would construct proper Diameter messages
	message := []byte("DIAMETER_MESSAGE_HERE")

	// Send to remote address - connection will be created automatically
	remoteAddr := "192.168.1.100:3868"
	err = addressClient.Send(remoteAddr, message)
	if err != nil {
		log.Errorw("Failed to send message", "error", err)
		return
	}

	fmt.Printf("Message sent successfully to %s\n", remoteAddr)

	// Send another message to the same address - reuses existing connection
	err = addressClient.Send(remoteAddr, message)
	if err != nil {
		log.Errorw("Failed to send second message", "error", err)
		return
	}

	fmt.Println("Second message sent (reused connection)")
}

// customConfigExample demonstrates custom configuration
func customConfigExample() {
	fmt.Println("\n=== Custom Configuration Example ===")

	log := logger.New("example", "info")

	// Create custom configuration
	config := &client.PoolConfig{
		OriginHost:  "gateway.telecom.com",
		OriginRealm: "telecom.com",
		ProductName: "My-Diameter-Gateway",
		VendorID:    10415, // 3GPP

		// Timeouts
		DialTimeout: 5 * time.Second,
		SendTimeout: 3 * time.Second,
		CERTimeout:  5 * time.Second,

		// DWR/DWA keepalive settings
		DWRInterval:    30 * time.Second,
		DWRTimeout:     10 * time.Second,
		MaxDWRFailures: 3,

		// Reconnection strategy
		ReconnectEnabled:  true,
		ReconnectInterval: 5 * time.Second,
		MaxReconnectDelay: 5 * time.Minute,
		ReconnectBackoff:  2.0, // Double delay on each retry

		// Connection lifecycle
		IdleTimeout:     10 * time.Minute, // Close idle connections after 10 min
		MaxConnLifetime: 1 * time.Hour,    // Refresh connections hourly

		// Application IDs
		AuthAppIDs: []uint32{16777252}, // S13 interface
		AcctAppIDs: []uint32{},

		// Buffer sizes
		SendBufferSize: 200,
		RecvBufferSize: 200,

		// Health check
		HealthCheckInterval: 30 * time.Second,
	}

	ctx := context.Background()
	addressClient, err := client.NewAddressClient(ctx, config, log)
	if err != nil {
		log.Fatalw("Failed to create client", "error", err)
	}
	defer addressClient.Close()

	// Send with custom timeout
	message := []byte("DIAMETER_MESSAGE")
	remoteAddr := "10.0.0.50:3868"

	err = addressClient.SendWithTimeout(remoteAddr, message, 5*time.Second)
	if err != nil {
		log.Errorw("Failed to send with timeout", "error", err)
		return
	}

	fmt.Printf("Message sent with custom timeout to %s\n", remoteAddr)
}

// concurrentExample demonstrates concurrent sends to multiple addresses
func concurrentExample() {
	fmt.Println("\n=== Concurrent Example ===")

	log := logger.New("example", "info")

	config := client.DefaultPoolConfig()
	config.OriginHost = "client.example.com"
	config.OriginRealm = "example.com"

	ctx := context.Background()
	addressClient, err := client.NewAddressClient(ctx, config, log)
	if err != nil {
		log.Fatalw("Failed to create client", "error", err)
	}
	defer addressClient.Close()

	// List of remote addresses
	remoteAddrs := []string{
		"192.168.1.100:3868",
		"192.168.1.101:3868",
		"192.168.1.102:3868",
		"10.0.0.50:3868",
		"10.0.0.51:3868",
	}

	// Send messages concurrently to different addresses
	// Each address will get its own connection, managed automatically
	done := make(chan bool)

	for i, addr := range remoteAddrs {
		go func(index int, remoteAddr string) {
			message := []byte(fmt.Sprintf("MESSAGE_%d", index))

			// Send multiple messages from this goroutine
			for j := 0; j < 5; j++ {
				err := addressClient.SendWithTimeout(remoteAddr, message, 10*time.Second)
				if err != nil {
					log.Errorw("Failed to send",
						"goroutine", index,
						"message_num", j,
						"remote_addr", remoteAddr,
						"error", err)
				} else {
					fmt.Printf("Goroutine %d sent message %d to %s\n", index, j, remoteAddr)
				}

				time.Sleep(100 * time.Millisecond)
			}

			done <- true
		}(i, addr)
	}

	// Wait for all goroutines
	for range remoteAddrs {
		<-done
	}

	// Check how many connections were created
	fmt.Printf("Total active connections: %d\n", addressClient.GetActiveConnections())
	fmt.Printf("Connected to: %v\n", addressClient.ListConnections())
}

// metricsExample demonstrates metrics tracking
func metricsExample() {
	fmt.Println("\n=== Metrics Example ===")

	log := logger.New("example", "info")

	config := client.DefaultPoolConfig()
	config.OriginHost = "client.example.com"
	config.OriginRealm = "example.com"

	ctx := context.Background()
	addressClient, err := client.NewAddressClient(ctx, config, log)
	if err != nil {
		log.Fatalw("Failed to create client", "error", err)
	}
	defer addressClient.Close()

	// Send some messages
	message := []byte("TEST_MESSAGE")
	remoteAddrs := []string{
		"192.168.1.100:3868",
		"192.168.1.101:3868",
	}

	for _, addr := range remoteAddrs {
		for i := 0; i < 10; i++ {
			err := addressClient.Send(addr, message)
			if err != nil {
				log.Errorw("Send failed", "error", err)
			}
		}
	}

	// Get statistics
	stats := addressClient.GetStats()

	fmt.Println("\n--- Client Statistics ---")
	fmt.Printf("Total Requests:  %d\n", stats.TotalRequests)
	fmt.Printf("Total Responses: %d\n", stats.TotalResponses)
	fmt.Printf("Total Errors:    %d\n", stats.TotalErrors)
	fmt.Printf("Total Timeouts:  %d\n", stats.TotalTimeouts)

	fmt.Println("\n--- Pool Metrics ---")
	poolMetrics := stats.PoolMetrics
	fmt.Printf("Active Connections:         %d\n", poolMetrics.ActiveConnections)
	fmt.Printf("Total Connections Created:  %d\n", poolMetrics.TotalConnections)
	fmt.Printf("Failed Establishments:      %d\n", poolMetrics.FailedEstablishments)
	fmt.Printf("Reconnect Attempts:         %d\n", poolMetrics.ReconnectAttempts)
	fmt.Printf("Messages Sent:              %d\n", poolMetrics.MessagesSent)
	fmt.Printf("Messages Received:          %d\n", poolMetrics.MessagesReceived)
	fmt.Printf("Bytes Sent:                 %d\n", poolMetrics.BytesSent)
	fmt.Printf("Bytes Received:             %d\n", poolMetrics.BytesReceived)
	fmt.Printf("Connections Closed:         %d\n", poolMetrics.ConnectionsClosed)
	fmt.Printf("Idle Timeouts:              %d\n", poolMetrics.IdleTimeouts)
	fmt.Printf("Max Lifetime Expirations:   %d\n", poolMetrics.MaxLifetimeExpirations)

	// List all active connections
	fmt.Println("\n--- Active Connections ---")
	for _, addr := range addressClient.ListConnections() {
		if conn, exists := addressClient.GetConnection(addr); exists {
			connStats := conn.GetStats()
			fmt.Printf("  %s:\n", addr)
			fmt.Printf("    Messages Sent:     %d\n", connStats.MessagesSent.Load())
			fmt.Printf("    Messages Received: %d\n", connStats.MessagesReceived.Load())
			fmt.Printf("    Bytes Sent:        %d\n", connStats.BytesSent.Load())
			fmt.Printf("    Bytes Received:    %d\n", connStats.BytesReceived.Load())
			fmt.Printf("    State:             %s\n", conn.GetState())
		}
	}
}

// contextExample demonstrates context usage for cancellation
func contextExample() {
	fmt.Println("\n=== Context Example ===")

	log := logger.New("example", "info")

	config := client.DefaultPoolConfig()
	config.OriginHost = "client.example.com"
	config.OriginRealm = "example.com"

	ctx := context.Background()
	addressClient, err := client.NewAddressClient(ctx, config, log)
	if err != nil {
		log.Fatalw("Failed to create client", "error", err)
	}
	defer addressClient.Close()

	// Create context with timeout
	sendCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	message := []byte("TEST_MESSAGE")
	remoteAddr := "192.168.1.100:3868"

	err = addressClient.SendWithContext(sendCtx, remoteAddr, message)
	if err != nil {
		if sendCtx.Err() == context.DeadlineExceeded {
			fmt.Println("Send operation timed out")
		} else {
			log.Errorw("Send failed", "error", err)
		}
		return
	}

	fmt.Println("Message sent successfully with context")
}
