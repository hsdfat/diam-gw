package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// Config holds the application configuration
type Config struct {
	Gateway    GatewayConfig
	Server     ServerConfig
	DRAPool    DRAPoolConfig
	Client     ClientConfig
	Logging    LoggingConfig
	Metrics    MetricsConfig
	Governance GovernanceConfig
}

// GatewayConfig holds gateway identity configuration
type GatewayConfig struct {
	OriginHost      string
	OriginRealm     string
	ProductName     string
	VendorID        uint32
	SessionTimeout  time.Duration
	EnableReqLog    bool
	EnableRespLog   bool
	StatsInterval   time.Duration
}

// ServerConfig holds inbound Diameter server configuration
type ServerConfig struct {
	ListenAddr       string
	MaxConnections   int
	ReadTimeout      time.Duration
	WriteTimeout     time.Duration
	WatchdogInterval time.Duration
	WatchdogTimeout  time.Duration
	MaxMessageSize   int
	SendChannelSize  int
	RecvChannelSize  int
	HandleWatchdog   bool
}

// DRAPoolConfig holds DRA pool configuration
type DRAPoolConfig struct {
	DRAs                []DRAConfig
	Supported           bool
	ConnectionsPerDRA   int
	ConnectTimeout      time.Duration
	CERTimeout          time.Duration
	DWRInterval         time.Duration
	DWRTimeout          time.Duration
	MaxDWRFailures      int
	HealthCheckInterval time.Duration
	ReconnectInterval   time.Duration
	MaxReconnectDelay   time.Duration
	ReconnectBackoff    float64
	SendBufferSize      int
	RecvBufferSize      int
}

// DRAConfig holds individual DRA configuration
type DRAConfig struct {
	Name     string
	Host     string
	Port     int
	Priority int
	Weight   int
}

// ClientConfig holds outbound client pool configuration
type ClientConfig struct {
	DialTimeout         time.Duration
	SendTimeout         time.Duration
	CERTimeout          time.Duration
	DWRInterval         time.Duration
	DWRTimeout          time.Duration
	MaxDWRFailures      int
	AuthAppIDs          []uint32
	SendBufferSize      int
	RecvBufferSize      int
	ReconnectEnabled    bool
	ReconnectInterval   time.Duration
	MaxReconnectDelay   time.Duration
	ReconnectBackoff    float64
	HealthCheckInterval time.Duration
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level  string // "debug", "info", "warn", "error"
	Format string // "json", "text"
}

// MetricsConfig holds metrics configuration
type MetricsConfig struct {
	Enabled bool
	Port    int
	Path    string
}

// GovernanceConfig holds governance/service discovery configuration
type GovernanceConfig struct {
	Enabled      bool
	URL          string
	ServiceName  string
	PodName      string
	Subscriptions []string
	FailOnError  bool
}

// Load loads configuration from file and environment variables
// Priority order (highest to lowest):
// 1. Environment variables (prefixed with DIAMGW_)
// 2. Config file specified by configPath
// 3. config.yaml in standard paths
// 4. config.default.yaml as fallback
// 5. Hardcoded defaults
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// Set default values (lowest priority)
	setDefaults(v)

	// Set config file paths
	if configPath != "" {
		// Use specified config file
		v.SetConfigFile(configPath)
	} else {
		// Search for config.yaml first, then fall back to config.default.yaml
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("./config")
		v.AddConfigPath("/etc/diam-gw")
	}

	// Read environment variables (highest priority)
	v.AutomaticEnv()
	v.SetEnvPrefix("DIAMGW")

	// Try to read config file
	configFileRead := false
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Config file not found, try default config
			v.SetConfigName("config.default")
			if err := v.ReadInConfig(); err != nil {
				if _, ok := err.(viper.ConfigFileNotFoundError); ok {
					// No config files found; using defaults and environment variables
					fmt.Println("Warning: No config file found, using defaults and environment variables")
				} else {
					return nil, fmt.Errorf("failed to read default config file: %w", err)
				}
			} else {
				configFileRead = true
			}
		} else {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	} else {
		configFileRead = true
	}

	if configFileRead {
		fmt.Printf("Using config file: %s\n", v.ConfigFileUsed())
	}

	// Unmarshal config
	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate config
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// setDefaults sets default configuration values
func setDefaults(v *viper.Viper) {
	// Gateway defaults
	v.SetDefault("gateway.originHost", "diameter-gw.example.com")
	v.SetDefault("gateway.originRealm", "example.com")
	v.SetDefault("gateway.productName", "Diameter-Gateway")
	v.SetDefault("gateway.vendorID", 10415)
	v.SetDefault("gateway.sessionTimeout", "30s")
	v.SetDefault("gateway.enableReqLog", false)
	v.SetDefault("gateway.enableRespLog", false)
	v.SetDefault("gateway.statsInterval", "60s")

	// Server defaults
	v.SetDefault("server.listenAddr", "0.0.0.0:3868")
	v.SetDefault("server.maxConnections", 1000)
	v.SetDefault("server.readTimeout", "30s")
	v.SetDefault("server.writeTimeout", "10s")
	v.SetDefault("server.watchdogInterval", "30s")
	v.SetDefault("server.watchdogTimeout", "10s")
	v.SetDefault("server.maxMessageSize", 65535)
	v.SetDefault("server.sendChannelSize", 100)
	v.SetDefault("server.recvChannelSize", 1000)
	v.SetDefault("server.handleWatchdog", true)

	// DRA Pool defaults
	v.SetDefault("draPool.supported", true)
	v.SetDefault("draPool.connectionsPerDRA", 1)
	v.SetDefault("draPool.connectTimeout", "10s")
	v.SetDefault("draPool.cerTimeout", "5s")
	v.SetDefault("draPool.dwrInterval", "30s")
	v.SetDefault("draPool.dwrTimeout", "10s")
	v.SetDefault("draPool.maxDWRFailures", 3)
	v.SetDefault("draPool.healthCheckInterval", "10s")
	v.SetDefault("draPool.reconnectInterval", "5s")
	v.SetDefault("draPool.maxReconnectDelay", "5m")
	v.SetDefault("draPool.reconnectBackoff", 1.5)
	v.SetDefault("draPool.sendBufferSize", 100)
	v.SetDefault("draPool.recvBufferSize", 100)

	// Default DRAs
	v.SetDefault("draPool.dras", []map[string]interface{}{
		{
			"name":     "DRA-1",
			"host":     "127.0.0.1",
			"port":     3869,
			"priority": 1,
			"weight":   100,
		},
		{
			"name":     "DRA-2",
			"host":     "127.0.0.1",
			"port":     3870,
			"priority": 2,
			"weight":   100,
		},
	})

	// Client defaults
	v.SetDefault("client.dialTimeout", "5s")
	v.SetDefault("client.sendTimeout", "10s")
	v.SetDefault("client.cerTimeout", "5s")
	v.SetDefault("client.dwrInterval", "30s")
	v.SetDefault("client.dwrTimeout", "10s")
	v.SetDefault("client.maxDWRFailures", 3)
	v.SetDefault("client.authAppIDs", []uint32{16777251, 16777252})
	v.SetDefault("client.sendBufferSize", 1000)
	v.SetDefault("client.recvBufferSize", 1000)
	v.SetDefault("client.reconnectEnabled", false)
	v.SetDefault("client.reconnectInterval", "2s")
	v.SetDefault("client.maxReconnectDelay", "30s")
	v.SetDefault("client.reconnectBackoff", 1.5)
	v.SetDefault("client.healthCheckInterval", "10s")

	// Logging defaults
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")

	// Metrics defaults
	v.SetDefault("metrics.enabled", true)
	v.SetDefault("metrics.port", 9091)
	v.SetDefault("metrics.path", "/metrics")

	// Governance defaults
	v.SetDefault("governance.enabled", true)
	v.SetDefault("governance.url", "http://telco-governance:8080")
	v.SetDefault("governance.serviceName", "diam-gw")
	v.SetDefault("governance.subscriptions", []string{"eir-diameter"})
	v.SetDefault("governance.failOnError", false)
}

// Validate validates the configuration
func (c *Config) Validate() error {
	// Validate Gateway configuration
	if err := c.Gateway.Validate(); err != nil {
		return fmt.Errorf("gateway config: %w", err)
	}

	// Validate Server configuration
	if err := c.Server.Validate(); err != nil {
		return fmt.Errorf("server config: %w", err)
	}

	// Validate DRAPool configuration
	if err := c.DRAPool.Validate(); err != nil {
		return fmt.Errorf("draPool config: %w", err)
	}

	// Validate Client configuration
	if err := c.Client.Validate(); err != nil {
		return fmt.Errorf("client config: %w", err)
	}

	// Validate Logging configuration
	if err := c.Logging.Validate(); err != nil {
		return fmt.Errorf("logging config: %w", err)
	}

	// Validate Metrics configuration
	if err := c.Metrics.Validate(); err != nil {
		return fmt.Errorf("metrics config: %w", err)
	}

	// Validate Governance configuration
	if err := c.Governance.Validate(); err != nil {
		return fmt.Errorf("governance config: %w", err)
	}

	return nil
}

// Validate validates the GatewayConfig
func (c *GatewayConfig) Validate() error {
	if c.OriginHost == "" {
		return fmt.Errorf("originHost is required")
	}
	if c.OriginRealm == "" {
		return fmt.Errorf("originRealm is required")
	}
	if c.ProductName == "" {
		return fmt.Errorf("productName is required")
	}
	if c.SessionTimeout < 0 {
		return fmt.Errorf("sessionTimeout must be non-negative")
	}
	if c.StatsInterval < 0 {
		return fmt.Errorf("statsInterval must be non-negative")
	}
	return nil
}

// Validate validates the ServerConfig
func (c *ServerConfig) Validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("listenAddr is required")
	}
	if c.MaxConnections < 1 {
		return fmt.Errorf("maxConnections must be at least 1")
	}
	if c.ReadTimeout < 0 {
		return fmt.Errorf("readTimeout must be non-negative")
	}
	if c.WriteTimeout < 0 {
		return fmt.Errorf("writeTimeout must be non-negative")
	}
	if c.WatchdogInterval < 0 {
		return fmt.Errorf("watchdogInterval must be non-negative")
	}
	if c.WatchdogTimeout < 0 {
		return fmt.Errorf("watchdogTimeout must be non-negative")
	}
	if c.MaxMessageSize < 1 {
		return fmt.Errorf("maxMessageSize must be at least 1")
	}
	if c.SendChannelSize < 1 {
		return fmt.Errorf("sendChannelSize must be at least 1")
	}
	if c.RecvChannelSize < 1 {
		return fmt.Errorf("recvChannelSize must be at least 1")
	}
	return nil
}

// Validate validates the DRAPoolConfig
func (c *DRAPoolConfig) Validate() error {
	if c.Supported && len(c.DRAs) == 0 {
		return fmt.Errorf("at least one DRA is required when DRA support is enabled")
	}
	for i, dra := range c.DRAs {
		if err := dra.Validate(); err != nil {
			return fmt.Errorf("dra[%d]: %w", i, err)
		}
	}
	if c.ConnectionsPerDRA < 1 {
		return fmt.Errorf("connectionsPerDRA must be at least 1")
	}
	if c.ConnectTimeout < 0 {
		return fmt.Errorf("connectTimeout must be non-negative")
	}
	if c.CERTimeout < 0 {
		return fmt.Errorf("cerTimeout must be non-negative")
	}
	if c.DWRInterval < 0 {
		return fmt.Errorf("dwrInterval must be non-negative")
	}
	if c.DWRTimeout < 0 {
		return fmt.Errorf("dwrTimeout must be non-negative")
	}
	if c.MaxDWRFailures < 1 {
		return fmt.Errorf("maxDWRFailures must be at least 1")
	}
	if c.HealthCheckInterval < 0 {
		return fmt.Errorf("healthCheckInterval must be non-negative")
	}
	if c.ReconnectInterval < 0 {
		return fmt.Errorf("reconnectInterval must be non-negative")
	}
	if c.MaxReconnectDelay < 0 {
		return fmt.Errorf("maxReconnectDelay must be non-negative")
	}
	if c.ReconnectBackoff <= 0 {
		return fmt.Errorf("reconnectBackoff must be positive")
	}
	if c.SendBufferSize < 1 {
		return fmt.Errorf("sendBufferSize must be at least 1")
	}
	if c.RecvBufferSize < 1 {
		return fmt.Errorf("recvBufferSize must be at least 1")
	}
	return nil
}

// Validate validates the DRAConfig
func (c *DRAConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if c.Host == "" {
		return fmt.Errorf("host is required")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", c.Port)
	}
	if c.Priority < 1 {
		return fmt.Errorf("priority must be at least 1")
	}
	if c.Weight < 1 {
		return fmt.Errorf("weight must be at least 1")
	}
	return nil
}

// Validate validates the ClientConfig
func (c *ClientConfig) Validate() error {
	if c.DialTimeout < 0 {
		return fmt.Errorf("dialTimeout must be non-negative")
	}
	if c.SendTimeout < 0 {
		return fmt.Errorf("sendTimeout must be non-negative")
	}
	if c.CERTimeout < 0 {
		return fmt.Errorf("cerTimeout must be non-negative")
	}
	if c.DWRInterval < 0 {
		return fmt.Errorf("dwrInterval must be non-negative")
	}
	if c.DWRTimeout < 0 {
		return fmt.Errorf("dwrTimeout must be non-negative")
	}
	if c.MaxDWRFailures < 1 {
		return fmt.Errorf("maxDWRFailures must be at least 1")
	}
	if c.SendBufferSize < 1 {
		return fmt.Errorf("sendBufferSize must be at least 1")
	}
	if c.RecvBufferSize < 1 {
		return fmt.Errorf("recvBufferSize must be at least 1")
	}
	if c.ReconnectEnabled {
		if c.ReconnectInterval < 0 {
			return fmt.Errorf("reconnectInterval must be non-negative")
		}
		if c.MaxReconnectDelay < 0 {
			return fmt.Errorf("maxReconnectDelay must be non-negative")
		}
		if c.ReconnectBackoff <= 0 {
			return fmt.Errorf("reconnectBackoff must be positive")
		}
	}
	if c.HealthCheckInterval < 0 {
		return fmt.Errorf("healthCheckInterval must be non-negative")
	}
	return nil
}

// Validate validates the LoggingConfig
func (c *LoggingConfig) Validate() error {
	validLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLevels[c.Level] {
		return fmt.Errorf("level must be one of: debug, info, warn, error")
	}
	validFormats := map[string]bool{
		"json": true,
		"text": true,
	}
	if !validFormats[c.Format] {
		return fmt.Errorf("format must be one of: json, text")
	}
	return nil
}

// Validate validates the MetricsConfig
func (c *MetricsConfig) Validate() error {
	if !c.Enabled {
		return nil // No validation needed if metrics is disabled
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", c.Port)
	}
	if c.Path == "" {
		return fmt.Errorf("path is required when metrics is enabled")
	}
	if c.Path[0] != '/' {
		return fmt.Errorf("path must start with /")
	}
	return nil
}

// Validate validates the GovernanceConfig
func (c *GovernanceConfig) Validate() error {
	if !c.Enabled {
		return nil // No validation needed if governance is disabled
	}
	if c.URL == "" {
		return fmt.Errorf("url is required when governance is enabled")
	}
	if c.ServiceName == "" {
		return fmt.Errorf("serviceName is required when governance is enabled")
	}
	return nil
}
