package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hsdfat8/diam-gw/client"
	"github.com/hsdfat8/diam-gw/commands/s13"
	"github.com/hsdfat8/diam-gw/models_base"
	"github.com/hsdfat8/diam-gw/pkg/logger"
)

func main() {
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║     Diameter Client Testing with DRA Simulator            ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Initialize logger
	logger.SetLevel("info")

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Configure DRA connection
	config := client.DefaultConfig()
	config.Host = "127.0.0.1"
	config.Port = 3868
	config.OriginHost = "client.example.com"
	config.OriginRealm = "example.com"
	config.ConnectionCount = 3

	// Validate configuration
	if err := config.Validate(); err != nil {
		logger.Log.Errorw("Invalid configuration", "error", err)
		os.Exit(1)
	}

	fmt.Println("Configuration:")
	fmt.Printf("  DRA Address:         %s:%d\n", config.Host, config.Port)
	fmt.Printf("  Origin-Host:         %s\n", config.OriginHost)
	fmt.Printf("  Origin-Realm:        %s\n", config.OriginRealm)
	fmt.Printf("  Connection Count:    %d\n", config.ConnectionCount)
	fmt.Println()

	// Create connection pool
	fmt.Println("Creating connection pool...")
	pool, err := client.NewConnectionPool(ctx, config)
	if err != nil {
		logger.Log.Errorw("Failed to create connection pool", "error", err)
		os.Exit(1)
	}

	// Start the pool
	fmt.Println("Starting connection pool...")
	if err := pool.Start(); err != nil {
		logger.Log.Errorw("Failed to start connection pool", "error", err)
		os.Exit(1)
	}

	logger.Log.Infow("Connection pool started successfully")

	// Wait for connections to establish
	fmt.Println("Waiting for connections to establish...")
	time.Sleep(2 * time.Second)

	// Display pool status
	displayPoolStatus(pool)

	// Start sending test messages
	fmt.Println("\n" + "═══════════════════════════════════════════════════════")
	fmt.Println("Starting message sending test...")
	fmt.Println("═══════════════════════════════════════════════════════" + "\n")

	// Send test messages
	go sendTestMessages(ctx, pool)

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Display stats periodically
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sigChan:
			logger.Log.Infow("Shutdown signal received, stopping client...")
			cancel()

			// Display final stats
			fmt.Println("\n" + "═══════════════════════════════════════════════════════")
			fmt.Println("Final Statistics:")
			fmt.Println("═══════════════════════════════════════════════════════")
			displayPoolStatus(pool)

			// Close pool
			if err := pool.Close(); err != nil {
				logger.Log.Errorw("Error closing pool", "error", err)
			}

			logger.Log.Infow("Client stopped gracefully")
			return

		case <-ticker.C:
			displayPoolStatus(pool)
		}
	}
}

func sendTestMessages(ctx context.Context, pool *client.ConnectionPool) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	messageCount := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			messageCount++

			// Create ME-Identity-Check-Request
			meicr := s13.NewMEIdentityCheckRequest()
			meicr.SessionId = models_base.UTF8String(fmt.Sprintf("client.example.com;%d;%d",
				time.Now().Unix(), messageCount))
			meicr.OriginHost = models_base.DiameterIdentity("client.example.com")
			meicr.OriginRealm = models_base.DiameterIdentity("example.com")
			destHost := models_base.DiameterIdentity("dra.example.com")
			meicr.DestinationHost = &destHost
			meicr.DestinationRealm = models_base.DiameterIdentity("example.com")
			meicr.AuthSessionState = models_base.Enumerated(1) // NO_STATE_MAINTAINED

			// Set IMEI
			imei := models_base.UTF8String("123456789012345")
			meicr.TerminalInformation = &s13.TerminalInformation{
				Imei: &imei,
			}

			// Marshal message
			data, err := meicr.Marshal()
			if err != nil {
				logger.Log.Errorw("Failed to marshal MEICR", "error", err)
				continue
			}

			// Send via pool
			if err := pool.Send(data); err != nil {
				logger.Log.Errorw("Failed to send message", "error", err, "count", messageCount)
				continue
			}

			logger.Log.Infow("Sent MEICR", "message_num", messageCount, "session_id", meicr.SessionId)
		}
	}
}

func displayPoolStatus(pool *client.ConnectionPool) {
	stats := pool.GetStats()
	conns := pool.GetAllConnections()

	fmt.Println("\n" + "───────────────────────────────────────────────────────")
	fmt.Println("Connection Pool Status:")
	fmt.Println("───────────────────────────────────────────────────────")
	fmt.Printf("  Total Connections:    %d\n", len(conns))
	fmt.Printf("  Active Connections:   %d\n", stats.ActiveConnections)
	fmt.Printf("  Messages Sent:        %d\n", stats.TotalMessagesSent)
	fmt.Printf("  Messages Received:    %d\n", stats.TotalMessagesRecv)
	fmt.Printf("  Bytes Sent:           %d\n", stats.TotalBytesSent)
	fmt.Printf("  Bytes Received:       %d\n", stats.TotalBytesRecv)
	fmt.Printf("  Total Reconnects:     %d\n", stats.TotalReconnects)

	fmt.Println("\nConnection Details:")
	for _, conn := range conns {
		state := conn.GetState()
		connStats := conn.GetStats()

		fmt.Printf("  [%s] State: %-12s | Sent: %d | Received: %d | Reconnects: %d\n",
			conn.ID(),
			state.String(),
			connStats.MessagesSent.Load(),
			connStats.MessagesReceived.Load(),
			connStats.Reconnects.Load())
	}
	fmt.Println("───────────────────────────────────────────────────────")
}
