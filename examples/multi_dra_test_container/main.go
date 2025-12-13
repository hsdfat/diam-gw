package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/hsdfat8/diam-gw/client"
	"github.com/hsdfat8/diam-gw/commands/s13"
	"github.com/hsdfat8/diam-gw/models_base"
	"github.com/hsdfat8/diam-gw/pkg/logger"
)

// getEnv gets environment variable with default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt gets environment variable as int with default value
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

// getEnvDuration gets environment variable as duration with default value
func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

func main() {
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║  Multi-DRA Priority-Based Client (Containerized)            ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Initialize logger
	logger.SetLevel("info")

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Read configuration from environment variables
	dra1Host := getEnv("DRA1_HOST", "172.20.0.11")
	dra1Port := getEnvInt("DRA1_PORT", 3868)
	dra2Host := getEnv("DRA2_HOST", "172.20.0.12")
	dra2Port := getEnvInt("DRA2_PORT", 3868)
	dra3Host := getEnv("DRA3_HOST", "172.20.0.13")
	dra3Port := getEnvInt("DRA3_PORT", 3868)
	dra4Host := getEnv("DRA4_HOST", "172.20.0.14")
	dra4Port := getEnvInt("DRA4_PORT", 3868)

	// Configure DRA pool
	config := client.DefaultDRAPoolConfig()
	config.OriginHost = "client.example.com"
	config.OriginRealm = "example.com"
	config.ConnectionsPerDRA = 1
	config.HealthCheckInterval = 5 * time.Second

	// Read DWR configuration from environment variables
	config.DWRInterval = getEnvDuration("DWR_INTERVAL", 10*time.Second)
	config.DWRTimeout = getEnvDuration("DWR_TIMEOUT", 5*time.Second)
	config.MaxDWRFailures = getEnvInt("MAX_DWR_FAILURES", 3)

	config.DRAs = []*client.DRAServerConfig{
		{
			Name:     "DRA-1",
			Host:     dra1Host,
			Port:     dra1Port,
			Priority: 1,
			Weight:   10,
		},
		{
			Name:     "DRA-2",
			Host:     dra2Host,
			Port:     dra2Port,
			Priority: 1,
			Weight:   10,
		},
		{
			Name:     "DRA-3",
			Host:     dra3Host,
			Port:     dra3Port,
			Priority: 2,
			Weight:   10,
		},
		{
			Name:     "DRA-4",
			Host:     dra4Host,
			Port:     dra4Port,
			Priority: 2,
			Weight:   10,
		},
	}

	// Display configuration
	displayConfig(config)

	// Wait for DRAs to be ready (give them time to start)
	fmt.Println("Waiting for DRAs to be ready...")
	time.Sleep(5 * time.Second)

	// Create DRA pool
	fmt.Println("Creating DRA pool...")
	pool, err := client.NewDRAPool(ctx, config)
	if err != nil {
		logger.Log.Errorw("Failed to create DRA pool", "error", err)
		os.Exit(1)
	}

	// Start the pool
	fmt.Println("Starting DRA pool (connecting to all 4 DRAs)...")
	if err := pool.Start(); err != nil {
		logger.Log.Errorw("Failed to start DRA pool", "error", err)
		os.Exit(1)
	}

	logger.Log.Infow("DRA pool started successfully")

	// Wait for connections to establish
	fmt.Println("Waiting for connections to establish...")
	time.Sleep(3 * time.Second)

	// Display initial status
	displayPoolStatus(pool)

	// Start sending test messages
	fmt.Println("\n" + "═══════════════════════════════════════════════════════════")
	fmt.Println("Starting message sending test...")
	fmt.Println("Messages will be sent ONLY to Priority 1 DRAs")
	fmt.Println("═══════════════════════════════════════════════════════════" + "\n")

	// Send test messages
	go sendTestMessages(ctx, pool)

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Display stats periodically
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sigChan:
			logger.Log.Infow("Shutdown signal received, stopping client...")
			cancel()

			// Display final stats
			fmt.Println("\n" + "═══════════════════════════════════════════════════════════")
			fmt.Println("Final Statistics:")
			fmt.Println("═══════════════════════════════════════════════════════════")
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

func sendTestMessages(ctx context.Context, pool *client.DRAPool) {
	ticker := time.NewTicker(2 * time.Second)
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
			meicr.AuthSessionState = models_base.Enumerated(1)

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

			activePriority := pool.GetActivePriority()
			logger.Log.Infow("Sent MEICR",
				"message_num", messageCount,
				"active_priority", activePriority,
				"session_id", meicr.SessionId)
		}
	}
}

func displayConfig(config *client.DRAPoolConfig) {
	fmt.Println("Configuration:")
	fmt.Printf("  Origin-Host:         %s\n", config.OriginHost)
	fmt.Printf("  Origin-Realm:        %s\n", config.OriginRealm)
	fmt.Printf("  Connections/DRA:     %d\n", config.ConnectionsPerDRA)
	fmt.Printf("  Total DRAs:          %d\n", len(config.DRAs))
	fmt.Println()

	fmt.Println("DRA Configuration:")
	for _, dra := range config.DRAs {
		fmt.Printf("  [%s] %s:%d - Priority %d (Weight: %d)\n",
			dra.Name, dra.Host, dra.Port, dra.Priority, dra.Weight)
	}
	fmt.Println()
}

func displayPoolStatus(pool *client.DRAPool) {
	stats := pool.GetStats()

	fmt.Println("\n" + "════════════════════════════════════════════════════════════")
	fmt.Println("DRA Pool Status")
	fmt.Println("════════════════════════════════════════════════════════════")
	fmt.Printf("  Active Priority:      %d %s\n", stats.CurrentPriority, getPriorityLabel(stats.CurrentPriority))
	fmt.Printf("  Total DRAs:           %d\n", stats.TotalDRAs)
	fmt.Printf("  Active DRAs:          %d\n", stats.ActiveDRAs)
	fmt.Printf("  Total Connections:    %d\n", stats.TotalConnections)
	fmt.Printf("  Active Connections:   %d\n", stats.ActiveConnections)
	fmt.Printf("  Messages Sent:        %d\n", stats.TotalMessagesSent)
	fmt.Printf("  Messages Received:    %d\n", stats.TotalMessagesRecv)
	fmt.Printf("  Failover Count:       %d\n", stats.FailoverCount.Load())
	fmt.Println()

	fmt.Println("Individual DRA Status:")
	fmt.Println("────────────────────────────────────────────────────────────")

	draNames := []string{"DRA-1", "DRA-2", "DRA-3", "DRA-4"}
	for _, name := range draNames {
		draPool := pool.GetDRAPool(name)
		if draPool == nil {
			continue
		}

		draStats := draPool.GetStats()
		isHealthy := draPool.IsHealthy()

		status := "🟢 HEALTHY"
		if !isHealthy {
			status = "🔴 DOWN"
		}

		var draConfig *client.DRAServerConfig
		for _, dra := range pool.GetDRAsByPriority(1) {
			if dra.Name == name {
				draConfig = dra
				break
			}
		}
		if draConfig == nil {
			for _, dra := range pool.GetDRAsByPriority(2) {
				if dra.Name == name {
					draConfig = dra
					break
				}
			}
		}

		activeMarker := ""
		if draConfig != nil && draConfig.Priority == stats.CurrentPriority && isHealthy {
			activeMarker = " ⭐ ACTIVE"
		}

		fmt.Printf("  [%s]%s\n", name, activeMarker)
		fmt.Printf("    Status:            %s\n", status)
		if draConfig != nil {
			fmt.Printf("    Priority:          %d\n", draConfig.Priority)
			fmt.Printf("    Address:           %s:%d\n", draConfig.Host, draConfig.Port)
		}
		fmt.Printf("    Active Conns:      %d/%d\n", draStats.ActiveConnections, draStats.TotalConnections)
		fmt.Printf("    Messages Sent:     %d\n", draStats.TotalMessagesSent)
		fmt.Printf("    Messages Recv:     %d\n", draStats.TotalMessagesRecv)
		fmt.Printf("    Reconnects:        %d\n", draStats.TotalReconnects)
		fmt.Println()
	}

	fmt.Println("════════════════════════════════════════════════════════════")
}

func getPriorityLabel(priority int) string {
	switch priority {
	case 1:
		return "(PRIMARY)"
	case 2:
		return "(BACKUP)"
	default:
		return ""
	}
}
