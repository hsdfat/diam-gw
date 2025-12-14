package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hsdfat8/diam-gw/pkg/logger"
)

var (
	host            = flag.String("host", "0.0.0.0", "DRA listening host")
	port            = flag.Int("port", 3868, "DRA listening port")
	originHost      = flag.String("origin-host", "dra.example.com", "DRA Origin-Host")
	originRealm     = flag.String("origin-realm", "example.com", "DRA Origin-Realm")
	productName     = flag.String("product-name", "DRA-Simulator/1.0", "Product name")
	vendorID        = flag.Uint("vendor-id", 10415, "Vendor ID (3GPP=10415)")
	enableMetrics   = flag.Bool("metrics", true, "Enable metrics collection")
	metricsInterval = flag.Duration("metrics-interval", 10*time.Second, "Metrics reporting interval")
	enableRouting   = flag.Bool("routing", false, "Enable message routing to backend servers")
	backendServers  = flag.String("backends", "", "Comma-separated list of backend servers (host:port)")
	maxConnections  = flag.Int("max-connections", 1000, "Maximum concurrent connections")
	readTimeout     = flag.Duration("read-timeout", 30*time.Second, "Connection read timeout")
	writeTimeout    = flag.Duration("write-timeout", 30*time.Second, "Connection write timeout")
	dwrInterval     = flag.Duration("dwr-interval", 30*time.Second, "Device Watchdog Request interval")
	verbose         = flag.Bool("verbose", false, "Enable verbose logging")
	sendMICR        = flag.Bool("send-micr", false, "Send MICR test message after client connects")
	micrIMEI        = flag.String("micr-imei", "123456789012345", "IMEI to use in test MICR")
	micrDelay       = flag.Duration("micr-delay", 5*time.Second, "Delay before sending MICR after client connects")
)

func main() {
	flag.Parse()

	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║         Diameter Routing Agent (DRA) Simulator            ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Initialize logger

	// Create DRA configuration
	config := &Config{
		Host:            *host,
		Port:            *port,
		OriginHost:      *originHost,
		OriginRealm:     *originRealm,
		ProductName:     *productName,
		VendorID:        uint32(*vendorID),
		MaxConnections:  *maxConnections,
		ReadTimeout:     *readTimeout,
		WriteTimeout:    *writeTimeout,
		DWRInterval:     *dwrInterval,
		EnableMetrics:   *enableMetrics,
		MetricsInterval: *metricsInterval,
		EnableRouting:   *enableRouting,
		BackendServers:  *backendServers,
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		logger.Log.Errorw("Invalid configuration", "error", err)
		os.Exit(1)
	}

	// Display configuration
	displayConfig(config)

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create DRA server
	dra, err := NewDRA(ctx, config)
	if err != nil {
		logger.Log.Errorw("Failed to create DRA", "error", err)
		os.Exit(1)
	}

	// Start DRA server
	if err := dra.Start(); err != nil {
		logger.Log.Errorw("Failed to start DRA", "error", err)
		os.Exit(1)
	}

	logger.Log.Infow("DRA simulator started successfully",
		"host", config.Host,
		"port", config.Port,
		"origin_host", config.OriginHost,
		"origin_realm", config.OriginRealm)

	// Send MICR test if enabled
	if *sendMICR {
		go func() {
			time.Sleep(*micrDelay)
			logger.Log.Infow("Attempting to send test MICR", "imei", *micrIMEI)

			conn := dra.GetFirstConnection()
			if conn == nil {
				logger.Log.Warnw("No client connections available for MICR test")
				return
			}

			if err := conn.SendMICR(*micrIMEI); err != nil {
				logger.Log.Errorw("Failed to send test MICR", "error", err)
			} else {
				logger.Log.Infow("Test MICR sent successfully", "imei", *micrIMEI)
			}
		}()
	}

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	logger.Log.Infow("Shutdown signal received, stopping DRA...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := dra.Shutdown(shutdownCtx); err != nil {
		logger.Log.Errorw("Error during shutdown", "error", err)
		os.Exit(1)
	}

	logger.Log.Infow("DRA simulator stopped gracefully")
}

func displayConfig(config *Config) {
	fmt.Println("Configuration:")
	fmt.Printf("  Listen Address:      %s:%d\n", config.Host, config.Port)
	fmt.Printf("  Origin-Host:         %s\n", config.OriginHost)
	fmt.Printf("  Origin-Realm:        %s\n", config.OriginRealm)
	fmt.Printf("  Product-Name:        %s\n", config.ProductName)
	fmt.Printf("  Vendor-ID:           %d\n", config.VendorID)
	fmt.Printf("  Max Connections:     %d\n", config.MaxConnections)
	fmt.Printf("  Read Timeout:        %s\n", config.ReadTimeout)
	fmt.Printf("  Write Timeout:       %s\n", config.WriteTimeout)
	fmt.Printf("  DWR Interval:        %s\n", config.DWRInterval)
	fmt.Printf("  Metrics Enabled:     %v\n", config.EnableMetrics)
	if config.EnableMetrics {
		fmt.Printf("  Metrics Interval:    %s\n", config.MetricsInterval)
	}
	fmt.Printf("  Routing Enabled:     %v\n", config.EnableRouting)
	if config.EnableRouting && config.BackendServers != "" {
		fmt.Printf("  Backend Servers:     %s\n", config.BackendServers)
	}
	fmt.Println()
}
