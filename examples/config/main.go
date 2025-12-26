package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	gwconfig "github.com/hsdfat/diam-gw/pkg/config"
)

func main() {
	// Example 1: File-based configuration
	fileBasedExample()

	// Example 2: Remote configuration with Consul
	// consulExample()

	// Example 3: Hot reload with DRA updates
	// hotReloadDRAExample()

	// Example 4: Layered configuration
	// layeredConfigExample()
}

// fileBasedExample demonstrates basic file-based configuration
func fileBasedExample() {
	fmt.Println("=== Diameter Gateway File-Based Configuration Example ===")

	loader, err := gwconfig.NewLoader(gwconfig.LoaderConfig{
		ConfigFile:            "gateway.yaml",
		ConfigFileSearchPaths: []string{".", "./config", "/etc/diam-gw"},
		EnvPrefix:             "DIAMGW_",
		EnableHotReload:       false,
	})
	if err != nil {
		log.Fatalf("Failed to create loader: %v", err)
	}
	defer loader.Close()

	cfg, err := loader.Load(context.Background())
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("Gateway: %s@%s\n",
		cfg.Gateway.OriginHost,
		cfg.Gateway.OriginRealm)
	fmt.Printf("Server: %s (max connections: %d)\n",
		cfg.Server.ListenAddr,
		cfg.Server.MaxConnections)
	fmt.Printf("Client: timeout=%v, retries=%d\n",
		cfg.Client.RequestTimeout,
		cfg.Client.RetryAttempts)

	fmt.Println("\nConfigured DRAs:")
	for i, dra := range cfg.DRAs {
		fmt.Printf("  %d. %s (%s:%d) - priority=%d, weight=%d, enabled=%v\n",
			i+1,
			dra.Name,
			dra.Host,
			dra.Port,
			dra.Priority,
			dra.Weight,
			dra.Enabled)
	}

	fmt.Printf("\nLogging: %s/%s\n", cfg.Logging.Level, cfg.Logging.Format)
	fmt.Printf("Metrics: enabled=%v, port=%d\n\n", cfg.Metrics.Enabled, cfg.Metrics.Port)
}

// consulExample demonstrates Consul-based remote configuration
func consulExample() {
	fmt.Println("=== Consul Remote Configuration Example ===")

	loader, err := gwconfig.NewLoader(gwconfig.LoaderConfig{
		EnvPrefix: "DIAMGW_",
		RemoteConfig: &gwconfig.RemoteConfig{
			Provider:  "consul",
			Endpoints: []string{"localhost:8500"},
			Key:       "config/diam-gw/production",
		},
		EnableHotReload: true,
		ReloadCallback: func(cfg *gwconfig.Config) error {
			log.Printf("Configuration reloaded from Consul!")
			log.Printf("DRAs configured: %d", len(cfg.DRAs))
			return nil
		},
	})
	if err != nil {
		log.Fatalf("Failed to create loader: %v", err)
	}
	defer loader.Close()

	cfg, err := loader.Load(context.Background())
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("Loaded from Consul: %s@%s\n",
		cfg.Gateway.OriginHost,
		cfg.Gateway.OriginRealm)
	fmt.Printf("DRAs: %d configured\n\n", len(cfg.DRAs))
}

// hotReloadDRAExample demonstrates hot reload for DRA configuration changes
func hotReloadDRAExample() {
	fmt.Println("=== Hot Reload DRA Configuration Example ===")

	loader, err := gwconfig.NewLoader(gwconfig.LoaderConfig{
		ConfigFile:      "gateway.yaml",
		EnvPrefix:       "DIAMGW_",
		EnableHotReload: true,
		ReloadCallback: func(cfg *gwconfig.Config) error {
			log.Println("🔄 DRA Configuration reloaded!")

			for i, dra := range cfg.DRAs {
				log.Printf("  DRA %d: %s (%s:%d) priority=%d enabled=%v",
					i+1, dra.Name, dra.Host, dra.Port, dra.Priority, dra.Enabled)
			}

			// Here you would update your DRA pool
			// draPool.UpdateDRAs(cfg.DRAs)

			return nil
		},
	})
	if err != nil {
		log.Fatalf("Failed to create loader: %v", err)
	}
	defer loader.Close()

	ctx := context.Background()

	// Load initial config
	cfg, err := loader.Load(ctx)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("Initial DRA configuration:\n")
	for i, dra := range cfg.DRAs {
		fmt.Printf("  %d. %s - priority=%d, enabled=%v\n",
			i+1, dra.Name, dra.Priority, dra.Enabled)
	}

	fmt.Println("\nWatching for DRA configuration changes...")
	fmt.Println("Try editing gateway.yaml to:")
	fmt.Println("  - Add/remove DRAs")
	fmt.Println("  - Change DRA priorities")
	fmt.Println("  - Enable/disable DRAs")
	fmt.Println("Press Ctrl+C to exit")

	// Start watching in background
	go func() {
		err := loader.Watch(ctx, func(newCfg *gwconfig.Config) error {
			// This callback is invoked when config changes
			log.Printf("Applying new DRA configuration...")

			// Compare old vs new
			if len(cfg.DRAs) != len(newCfg.DRAs) {
				log.Printf("DRA count changed: %d -> %d",
					len(cfg.DRAs), len(newCfg.DRAs))
			}

			// Update reference
			cfg = newCfg
			return nil
		})
		if err != nil {
			log.Printf("Watch error: %v", err)
		}
	}()

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down...")
}

// layeredConfigExample demonstrates layered configuration with priorities
func layeredConfigExample() {
	fmt.Println("=== Layered Configuration Example ===")
	fmt.Println("Priority: Env Vars > Consul > Config File")

	// Set environment overrides
	os.Setenv("DIAMGW_GATEWAY_ORIGIN_HOST", "gateway-prod.example.com")
	os.Setenv("DIAMGW_SERVER_MAX_CONNECTIONS", "2000")
	os.Setenv("DIAMGW_LOGGING_LEVEL", "debug")
	defer func() {
		os.Unsetenv("DIAMGW_GATEWAY_ORIGIN_HOST")
		os.Unsetenv("DIAMGW_SERVER_MAX_CONNECTIONS")
		os.Unsetenv("DIAMGW_LOGGING_LEVEL")
	}()

	loader, err := gwconfig.NewLoader(gwconfig.LoaderConfig{
		// Layer 1: Base config file
		ConfigFile: "gateway.yaml",

		// Layer 2: Remote config (commented - would override file)
		// RemoteConfig: &gwconfig.RemoteConfig{
		//     Provider:  "consul",
		//     Endpoints: []string{"localhost:8500"},
		//     Key:       "config/diam-gw/production",
		// },

		// Layer 3: Environment variables (highest priority)
		EnvPrefix: "DIAMGW_",
	})
	if err != nil {
		log.Fatalf("Failed to create loader: %v", err)
	}
	defer loader.Close()

	cfg, err := loader.Load(context.Background())
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("Gateway Origin Host: %s (from env var)\n", cfg.Gateway.OriginHost)
	fmt.Printf("Server Max Connections: %d (from env var)\n", cfg.Server.MaxConnections)
	fmt.Printf("Log Level: %s (from env var)\n", cfg.Logging.Level)
	fmt.Printf("Gateway Origin Realm: %s (from config file)\n", cfg.Gateway.OriginRealm)
	fmt.Println()
}

// confdExample demonstrates confd-managed configuration
func confdExample() {
	fmt.Println("=== Confd-Managed Configuration Example ===")

	loader, err := gwconfig.NewLoader(gwconfig.LoaderConfig{
		EnvPrefix: "DIAMGW_",
		RemoteConfig: &gwconfig.RemoteConfig{
			Provider:  "confd",
			Endpoints: []string{"localhost:8500"}, // Confd backend
			Key:       "config/diam-gw/production",
		},
		EnableHotReload: true,
		ReloadCallback: func(cfg *gwconfig.Config) error {
			log.Println("Configuration reloaded via confd!")
			return nil
		},
	})
	if err != nil {
		log.Fatalf("Failed to create loader: %v", err)
	}
	defer loader.Close()

	cfg, err := loader.Load(context.Background())
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("Loaded via confd: %s@%s\n",
		cfg.Gateway.OriginHost,
		cfg.Gateway.OriginRealm)
	fmt.Println()
}

// validationExample demonstrates configuration validation
func validationExample() {
	fmt.Println("=== Configuration Validation Example ===")

	// This would fail validation due to invalid port
	invalidConfig := map[string]interface{}{
		"gateway": map[string]interface{}{
			"origin_host":  "", // Required field missing
			"origin_realm": "example.com",
		},
		"server": map[string]interface{}{
			"listen_addr": "0.0.0.0:3868",
			"port":        70000, // Invalid port (>65535)
		},
	}

	fmt.Printf("Testing with invalid config: %+v\n", invalidConfig)
	fmt.Println("Expected validation errors:")
	fmt.Println("  - gateway.origin_host: field is required")
	fmt.Println("  - server.port: must be at most 65535")
	fmt.Println()
}
