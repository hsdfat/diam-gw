package main

import (
	"context"
	"flag"
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
	vendorID    = flag.Int("vendor-id", 10415, "Vendor ID (default: 3GPP)")

	// DRA configuration
	dra1Host     = flag.String("dra1-host", "127.0.0.1", "Primary DRA host")
	dra1Port     = flag.Int("dra1-port", 3869, "Primary DRA port")
	dra2Host     = flag.String("dra2-host", "127.0.0.1", "Secondary DRA host")
	dra2Port     = flag.Int("dra2-port", 3870, "Secondary DRA port")
	draSupported = flag.Bool("dra-supported", true, "Enable DRA support")

	// Advanced settings
	maxConnections  = flag.Int("max-connections", 1000, "Maximum inbound connections")
	connsPerDRA     = flag.Int("conns-per-dra", 1, "Connections per DRA")
	sessionTimeout  = flag.Duration("session-timeout", 30*time.Second, "Session timeout")
	enableReqLog    = flag.Bool("log-requests", false, "Enable request logging")
	enableRespLog   = flag.Bool("log-responses", false, "Enable response logging")
	statsInterval   = flag.Duration("stats-interval", 60*time.Second, "Statistics logging interval")

	// Timeout settings
	readTimeout      = flag.Duration("read-timeout", 30*time.Second, "Connection read timeout")
	writeTimeout     = flag.Duration("write-timeout", 10*time.Second, "Connection write timeout")
	watchdogInterval = flag.Duration("watchdog-interval", 30*time.Second, "Watchdog interval")
	watchdogTimeout  = flag.Duration("watchdog-timeout", 10*time.Second, "Watchdog timeout")
	connectTimeout   = flag.Duration("connect-timeout", 10*time.Second, "DRA connect timeout")
	cerTimeout       = flag.Duration("cer-timeout", 5*time.Second, "CER timeout")
	dwrInterval      = flag.Duration("dwr-interval", 30*time.Second, "DWR interval")
	dwrTimeout       = flag.Duration("dwr-timeout", 10*time.Second, "DWR timeout")
	maxDWRFailures   = flag.Int("max-dwr-failures", 3, "Maximum DWR failures before reconnect")
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

	// Register with governance manager
	governanceURL := os.Getenv("GOVERNANCE_URL")
	if governanceURL == "" {
		governanceURL = "http://telco-governance:8080"
	}

	podName := os.Getenv("POD_NAME")
	if podName == "" {
		podName, _ = os.Hostname()
	}

	govClient := govclient.NewClient(&govclient.ClientConfig{
		ManagerURL:  governanceURL,
		ServiceName: "diam-gw",
		PodName:     podName,
	})

	// Extract IP from listen address
	listenIP := strings.Split(*listenAddr, ":")[0]
	listenPort := 3868 // Default diameter port

	// Register diam-gw service and subscribe to eir-diameter group for IP/port discovery
	registration := &models.ServiceRegistration{
		ServiceName: "diam-gw",
		PodName:     podName,
		Providers: []models.ProviderInfo{
			{
				Protocol: models.ProtocolTCP,
				IP:       listenIP,
				Port:     listenPort,
			},
		},
		HealthCheckURL:  fmt.Sprintf("http://%s:8081/health", listenIP),               // Assume health endpoint
		NotificationURL: fmt.Sprintf("http://%s:8081/governance/notify", listenIP),   // Assume notification endpoint
		Subscriptions:   []string{"eir-diameter"}, // Subscribe to eir-diameter group to get Diameter IP/port
	}

	if err := govClient.Register(registration); err != nil {
		log.Warnw("Failed to register with governance manager", "error", err)
	} else {
		log.Infow("✓ Registered with governance manager",
			"url", governanceURL,
			"subscriptions", registration.Subscriptions)
	}

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Infow("Shutting down gracefully...")
	cancel()

	// Unregister from governance
	if err := govClient.Unregister(); err != nil {
		log.Warnw("Failed to unregister from governance", "error", err)
	} else {
		log.Infow("✓ Unregistered from governance manager")
	}

	// Stop gateway
	if err := gw.Stop(); err != nil {
		log.Errorw("Error stopping gateway", "error", err)
	}

	log.Infow("Gateway stopped successfully")
}

func createGatewayConfig() *gateway.GatewayConfig {
	// Server configuration (inbound from Logic Apps)
	inServerConfig := &server.ServerConfig{
		ListenAddress:  *listenAddr,
		MaxConnections: *maxConnections,
		ConnectionConfig: &server.ConnectionConfig{
			OriginHost:       *originHost,
			OriginRealm:      *originRealm,
			ProductName:      *productName,
			VendorID:         uint32(*vendorID),
			ReadTimeout:      *readTimeout,
			WriteTimeout:     *writeTimeout,
			WatchdogInterval: *watchdogInterval,
			WatchdogTimeout:  *watchdogTimeout,
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
		OriginHost:          *originHost,
		OriginRealm:         *originRealm,
		ProductName:         *productName,
		VendorID:            uint32(*vendorID),
		ConnectionsPerDRA:   *connsPerDRA,
		ConnectTimeout:      *connectTimeout,
		CERTimeout:          *cerTimeout,
		DWRInterval:         *dwrInterval,
		DWRTimeout:          *dwrTimeout,
		MaxDWRFailures:      *maxDWRFailures,
		HealthCheckInterval: 10 * time.Second,
		ReconnectInterval:   5 * time.Second,
		MaxReconnectDelay:   5 * time.Minute,
		ReconnectBackoff:    1.5,
		SendBufferSize:      100,
		RecvBufferSize:      100,
	}

	// Internal client pool configuration (for forwarding to Logic Apps)
	inClientConfig := &client.PoolConfig{
		OriginHost:          *originHost,
		OriginRealm:         *originRealm,
		ProductName:         *productName,
		VendorID:            uint32(*vendorID),
		DialTimeout:         5 * time.Second,
		SendTimeout:         10 * time.Second,
		CERTimeout:          *cerTimeout,
		DWRInterval:         *dwrInterval,
		DWRTimeout:          *dwrTimeout,
		MaxDWRFailures:      *maxDWRFailures,
		AuthAppIDs:          []uint32{16777251, 16777252}, // S6a and S13
		SendBufferSize:      1000,
		RecvBufferSize:      1000,
		ReconnectEnabled:    false,
		ReconnectInterval:   2 * time.Second,
		MaxReconnectDelay:   30 * time.Second,
		ReconnectBackoff:    1.5,
		HealthCheckInterval: 10 * time.Second,
	}

	return &gateway.GatewayConfig{
		InServerConfig:        inServerConfig,
		DRAPoolConfig:         draPoolConfig,
		InClientConfig:        inClientConfig,
		DRASupported:          *draSupported,
		OriginHost:            *originHost,
		OriginRealm:           *originRealm,
		ProductName:           *productName,
		VendorID:              uint32(*vendorID),
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
