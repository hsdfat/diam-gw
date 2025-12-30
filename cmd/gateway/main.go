package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	govclient "github.com/chronnie/governance/client"
	"github.com/chronnie/governance/models"
	"github.com/hsdfat/diam-gw/client"
	"github.com/hsdfat/diam-gw/gateway"
	"github.com/hsdfat/diam-gw/internal/config"
	"github.com/hsdfat/diam-gw/pkg/logger"
	"github.com/hsdfat/diam-gw/server"
)

func main() {
	// Load configuration
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	log := logger.New("diam-gw", cfg.Logging.Level)
	log.Infow("Starting Diameter Gateway",
		"version", "1.0.0",
		"listen", cfg.Server.ListenAddr,
		"origin_host", cfg.Gateway.OriginHost)

	// Create gateway configuration
	gatewayConfig := createGatewayConfig(cfg)

	// Create gateway
	gw, err := gateway.NewGateway(gatewayConfig, log)
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
	go statsReporter(ctx, gw, log, cfg.Gateway.StatsInterval)

	log.Infow("Gateway is running",
		"listen_address", cfg.Server.ListenAddr,
		"dra_count", len(cfg.DRAPool.DRAs))

	// Register with governance manager if enabled
	var govClient *govclient.Client
	if cfg.Governance.Enabled {
		govClient = registerWithGovernance(cfg, log)
	}

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Infow("Shutting down gracefully...")
	cancel()

	// Unregister from governance
	if govClient != nil {
		govClient.StopHeartbeat()
		if err := govClient.Unregister(); err != nil {
			log.Error("Failed to unregister from governance", "error", err)
		} else {
			log.Infow("✓ Unregistered from governance manager")
		}
	}

	// Stop gateway
	if err := gw.Stop(); err != nil {
		log.Errorw("Error stopping gateway", "error", err)
	}

	log.Infow("Gateway stopped successfully")
}

func createGatewayConfig(cfg *config.Config) *gateway.GatewayConfig {
	// Server configuration (inbound from Logic Apps)
	inServerConfig := &server.ServerConfig{
		ListenAddress:  cfg.Server.ListenAddr,
		MaxConnections: cfg.Server.MaxConnections,
		ConnectionConfig: &server.ConnectionConfig{
			OriginHost:       cfg.Gateway.OriginHost,
			OriginRealm:      cfg.Gateway.OriginRealm,
			ProductName:      cfg.Gateway.ProductName,
			VendorID:         cfg.Gateway.VendorID,
			ReadTimeout:      cfg.Server.ReadTimeout,
			WriteTimeout:     cfg.Server.WriteTimeout,
			WatchdogInterval: cfg.Server.WatchdogInterval,
			WatchdogTimeout:  cfg.Server.WatchdogTimeout,
			MaxMessageSize:   cfg.Server.MaxMessageSize,
			SendChannelSize:  cfg.Server.SendChannelSize,
			RecvChannelSize:  cfg.Server.RecvChannelSize,
			HandleWatchdog:   cfg.Server.HandleWatchdog,
		},
		RecvChannelSize: cfg.Server.RecvChannelSize,
	}

	// DRA pool configuration (outbound to DRA servers)
	draConfigs := make([]*client.DRAServerConfig, len(cfg.DRAPool.DRAs))
	for i, dra := range cfg.DRAPool.DRAs {
		draConfigs[i] = &client.DRAServerConfig{
			Name:     dra.Name,
			Host:     dra.Host,
			Port:     dra.Port,
			Priority: dra.Priority,
			Weight:   dra.Weight,
		}
	}

	draPoolConfig := &client.DRAPoolConfig{
		DRAs:                draConfigs,
		OriginHost:          cfg.Gateway.OriginHost,
		OriginRealm:         cfg.Gateway.OriginRealm,
		ProductName:         cfg.Gateway.ProductName,
		VendorID:            cfg.Gateway.VendorID,
		ConnectionsPerDRA:   cfg.DRAPool.ConnectionsPerDRA,
		ConnectTimeout:      cfg.DRAPool.ConnectTimeout,
		CERTimeout:          cfg.DRAPool.CERTimeout,
		DWRInterval:         cfg.DRAPool.DWRInterval,
		DWRTimeout:          cfg.DRAPool.DWRTimeout,
		MaxDWRFailures:      cfg.DRAPool.MaxDWRFailures,
		HealthCheckInterval: cfg.DRAPool.HealthCheckInterval,
		ReconnectInterval:   cfg.DRAPool.ReconnectInterval,
		MaxReconnectDelay:   cfg.DRAPool.MaxReconnectDelay,
		ReconnectBackoff:    cfg.DRAPool.ReconnectBackoff,
		SendBufferSize:      cfg.DRAPool.SendBufferSize,
		RecvBufferSize:      cfg.DRAPool.RecvBufferSize,
	}

	// Internal client pool configuration (for forwarding to Logic Apps)
	inClientConfig := &client.PoolConfig{
		OriginHost:          cfg.Gateway.OriginHost,
		OriginRealm:         cfg.Gateway.OriginRealm,
		ProductName:         cfg.Gateway.ProductName,
		VendorID:            cfg.Gateway.VendorID,
		DialTimeout:         cfg.Client.DialTimeout,
		SendTimeout:         cfg.Client.SendTimeout,
		CERTimeout:          cfg.Client.CERTimeout,
		DWRInterval:         cfg.Client.DWRInterval,
		DWRTimeout:          cfg.Client.DWRTimeout,
		MaxDWRFailures:      cfg.Client.MaxDWRFailures,
		AuthAppIDs:          cfg.Client.AuthAppIDs,
		SendBufferSize:      cfg.Client.SendBufferSize,
		RecvBufferSize:      cfg.Client.RecvBufferSize,
		ReconnectEnabled:    cfg.Client.ReconnectEnabled,
		ReconnectInterval:   cfg.Client.ReconnectInterval,
		MaxReconnectDelay:   cfg.Client.MaxReconnectDelay,
		ReconnectBackoff:    cfg.Client.ReconnectBackoff,
		HealthCheckInterval: cfg.Client.HealthCheckInterval,
	}

	return &gateway.GatewayConfig{
		InServerConfig:        inServerConfig,
		DRAPoolConfig:         draPoolConfig,
		InClientConfig:        inClientConfig,
		DRASupported:          cfg.DRAPool.Supported,
		OriginHost:            cfg.Gateway.OriginHost,
		OriginRealm:           cfg.Gateway.OriginRealm,
		ProductName:           cfg.Gateway.ProductName,
		VendorID:              cfg.Gateway.VendorID,
		SessionTimeout:        cfg.Gateway.SessionTimeout,
		EnableRequestLogging:  cfg.Gateway.EnableReqLog,
		EnableResponseLogging: cfg.Gateway.EnableRespLog,
	}
}

func registerWithGovernance(cfg *config.Config, log logger.Logger) *govclient.Client {
	// Governance URL is now loaded from environment variables via pkg/config/env
	governanceURL := cfg.Governance.URL

	podName := cfg.Governance.PodName
	if podName == "" {
		podName = os.Getenv("POD_NAME")
	}
	if podName == "" {
		podName, _ = os.Hostname()
	}

	// Create governance client
	govClient := govclient.NewClient(&govclient.ClientConfig{
		ManagerURL:  governanceURL,
		ServiceName: cfg.Governance.ServiceName,
		PodName:     podName,
	})

	go govClient.StartHTTPServerWithClient(govclient.HTTPServerConfig{
		Port: 9092,
	})

	// Wait a bit for server to start
	time.Sleep(200 * time.Millisecond)

	// Extract IP from listen address
	listenIP := strings.Split(cfg.Server.ListenAddr, ":")[0]
	if listenIP == "" || listenIP == "0.0.0.0" {
		// Try to get pod IP from environment or use hostname
		if podIP := os.Getenv("POD_IP"); podIP != "" {
			listenIP = podIP
		} else {
			listenIP = "localhost"
		}
	}
	listenPort := 3868 // Default diameter port

	// Build subscriptions
	subscriptions := make([]models.Subscription, 0)
	for _, subName := range cfg.Governance.Subscriptions {
		subscriptions = append(subscriptions, models.Subscription{
			ServiceName: subName,
			ProviderIDs: []string{}, // Subscribe to all providers
		})
	}

	// Register diam-gw service and subscribe to configured services
	registration := &models.ServiceRegistration{
		ServiceName: cfg.Governance.ServiceName,
		PodName:     podName,
		Providers: []models.ProviderInfo{
			{
				ProviderID: "diameter",
				Protocol:   models.ProtocolTCP,
				IP:         listenIP,
				Port:       listenPort,
			},
		},
		HealthCheckURL:  fmt.Sprintf("http://%s:9092/health", listenIP),
		NotificationURL: fmt.Sprintf("http://%s:9092/notify", listenIP),
		Subscriptions:   []models.Subscription{{
			ServiceName: "eir-diameter",
			ProviderIDs: []string{"eir-diameter"},
		}},
	}

	resp, err := govClient.Register(registration)
	if err != nil {
		log.Warnw("Failed to register with governance manager", "error", err)
		if cfg.Governance.FailOnError {
			panic(err)
		}
	} else {
		log.Infow("✓ Registered with governance manager",
			"url", governanceURL,
			"service", cfg.Governance.ServiceName,
			"pod", podName,
			"own_pods", len(resp.Pods),
			"subscribed_services", len(resp.SubscribedServices))

		// Log subscription details
		for svcName, pods := range resp.SubscribedServices {
			log.Infow("  Subscription", "service", svcName, "pods", len(pods))
		}
	}

	// Start heartbeat
	govClient.StartHeartbeat()
	log.Infow("✓ Started governance heartbeat")

	return govClient
}

func statsReporter(ctx context.Context, gw *gateway.Gateway, log logger.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
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
