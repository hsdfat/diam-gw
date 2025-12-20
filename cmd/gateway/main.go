package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/gateway"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

var (
	listenAddr = flag.String("listen", "0.0.0.0:3868", "Gateway listen address")
	logLevel   = flag.String("log", "info", "Log level (debug, info, warn, error)")

	// Gateway identity
	originHost  = flag.String("origin-host", "diameter-gw.example.com", "Gateway Origin-Host")
	originRealm = flag.String("origin-realm", "example.com", "Gateway Origin-Realm")
	productName = flag.String("product", "Diameter-Gateway", "Product name")

	// DRA configuration
	dra1Host     = flag.String("dra1-host", "127.0.0.1", "Primary DRA host")
	dra1Port     = flag.Int("dra1-port", 3869, "Primary DRA port")
	dra2Host     = flag.String("dra2-host", "127.0.0.1", "Secondary DRA host")
	dra2Port     = flag.Int("dra2-port", 3870, "Secondary DRA port")

	// Advanced settings
	maxConnections  = flag.Int("max-connections", 1000, "Maximum inbound connections")
	connsPerDRA     = flag.Int("conns-per-dra", 1, "Connections per DRA")
	sessionTimeout  = flag.Duration("session-timeout", 30*time.Second, "Session timeout")
	enableReqLog    = flag.Bool("log-requests", false, "Enable request logging")
	enableRespLog   = flag.Bool("log-responses", false, "Enable response logging")
	statsInterval   = flag.Duration("stats-interval", 60*time.Second, "Statistics logging interval")
)

func main() {
	flag.Parse()

	// Initialize logger
	log := logger.New("gateway-main", *logLevel)
	log.Infow("Starting Diameter Gateway",
		"version", "1.0.0",
		"listen", *listenAddr,
		"origin_host", *originHost)

	// Create gateway configuration
	config := createGatewayConfig()

	// Create gateway
	gw, err := gateway.NewGateway(config, log)
	if err != nil {
		log.Fatalw("Failed to create gateway", "error", err)
	}

	// Start gateway
	if err := gw.Start(); err != nil {
		log.Fatalw("Failed to start gateway", "error", err)
	}

	// Start statistics reporter
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go statsReporter(ctx, gw, log)

	log.Infow("Gateway is running",
		"listen_address", *listenAddr,
		"primary_dra", fmt.Sprintf("%s:%d", *dra1Host, *dra1Port),
		"secondary_dra", fmt.Sprintf("%s:%d", *dra2Host, *dra2Port))

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Infow("Shutting down gracefully...")
	cancel()

	// Stop gateway
	if err := gw.Stop(); err != nil {
		log.Errorw("Error stopping gateway", "error", err)
	}

	log.Infow("Gateway stopped successfully")
}

func createGatewayConfig() *gateway.GatewayConfig {
	// Server configuration (inbound from Logic Apps)
	serverConfig := &server.ServerConfig{
		ListenAddress:  *listenAddr,
		MaxConnections: *maxConnections,
		ConnectionConfig: &server.ConnectionConfig{
			ReadTimeout:      30 * time.Second,
			WriteTimeout:     10 * time.Second,
			WatchdogInterval: 30 * time.Second,
			WatchdogTimeout:  10 * time.Second,
			MaxMessageSize:   65535,
			SendChannelSize:  100,
			RecvChannelSize:  100,
			HandleWatchdog:   true,
		},
		RecvChannelSize: 1000,
	}

	// DRA pool configuration (outbound to DRA servers)
	draPoolConfig := &client.DRAPoolConfig{
		DRAs: []*client.DRAServerConfig{
			{
				Name:     "DRA-1",
				Host:     *dra1Host,
				Port:     *dra1Port,
				Priority: 1, // Higher priority (primary)
				Weight:   100,
			},
			{
				Name:     "DRA-2",
				Host:     *dra2Host,
				Port:     *dra2Port,
				Priority: 2, // Lower priority (secondary)
				Weight:   100,
			},
		},
		ConnectionsPerDRA:   *connsPerDRA,
		ConnectTimeout:      10 * time.Second,
		CERTimeout:          5 * time.Second,
		DWRInterval:         30 * time.Second,
		DWRTimeout:          10 * time.Second,
		MaxDWRFailures:      3,
		HealthCheckInterval: 10 * time.Second,
		ReconnectInterval:   5 * time.Second,
		MaxReconnectDelay:   5 * time.Minute,
		ReconnectBackoff:    1.5,
		SendBufferSize:      100,
		RecvBufferSize:      100,
	}

	return &gateway.GatewayConfig{
		ServerConfig:          serverConfig,
		DRAPoolConfig:         draPoolConfig,
		OriginHost:            *originHost,
		OriginRealm:           *originRealm,
		ProductName:           *productName,
		VendorID:              10415, // 3GPP
		SessionTimeout:        *sessionTimeout,
		EnableRequestLogging:  *enableReqLog,
		EnableResponseLogging: *enableRespLog,
	}
}

func statsReporter(ctx context.Context, gw *gateway.Gateway, log logger.Logger) {
	ticker := time.NewTicker(*statsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats := gw.GetStats()
			draStats := gw.GetDRAPool().GetStats()

			log.Infow("=== Gateway Statistics ===")
			log.Infow("Gateway",
				"total_requests", stats.TotalRequests,
				"total_responses", stats.TotalResponses,
				"active_sessions", stats.ActiveSessions,
				"total_errors", stats.TotalErrors,
				"avg_latency_ms", fmt.Sprintf("%.2f", stats.AverageLatencyMs))

			log.Infow("Forwarding",
				"to_dra", stats.TotalForwarded,
				"from_dra", stats.TotalFromDRA,
				"timeout_errors", stats.TimeoutErrors,
				"routing_errors", stats.RoutingErrors)

			log.Infow("Sessions",
				"created", stats.SessionsCreated,
				"completed", stats.SessionsCompleted,
				"expired", stats.SessionsExpired)

			log.Infow("DRA Pool",
				"active_priority", draStats.CurrentPriority,
				"total_dras", draStats.TotalDRAs,
				"active_dras", draStats.ActiveDRAs,
				"total_connections", draStats.TotalConnections,
				"active_connections", draStats.ActiveConnections,
				"failover_count", draStats.FailoverCount.Load())

			log.Infow("DRA Messages",
				"sent", draStats.TotalMessagesSent,
				"received", draStats.TotalMessagesRecv)

			log.Infow("==========================")
		}
	}
}
