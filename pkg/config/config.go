package config

import (
	"context"
	"fmt"
	"time"

	sharedconfig "github.com/hsdfat/telco/utils/config"
)

// Config represents the complete Diameter Gateway configuration
type Config struct {
	Gateway GatewayConfig `json:"gateway" yaml:"gateway"`
	Server  ServerConfig  `json:"server" yaml:"server"`
	Client  ClientConfig  `json:"client" yaml:"client"`
	DRAs    []DRAConfig   `json:"dras" yaml:"dras"`
	Logging LoggingConfig `json:"logging" yaml:"logging"`
	Metrics MetricsConfig `json:"metrics" yaml:"metrics"`
}

// GatewayConfig configures the gateway identity
type GatewayConfig struct {
	OriginHost      string `json:"origin_host" yaml:"origin_host" env:"ORIGIN_HOST" validate:"required"`
	OriginRealm     string `json:"origin_realm" yaml:"origin_realm" env:"ORIGIN_REALM" validate:"required"`
	VendorID        uint32 `json:"vendor_id" yaml:"vendor_id" env:"VENDOR_ID" envDefault:"10415"`
	ProductName     string `json:"product_name" yaml:"product_name" env:"PRODUCT_NAME" envDefault:"DiameterGateway"`
	FirmwareVersion uint32 `json:"firmware_version" yaml:"firmware_version" env:"FIRMWARE_VERSION" envDefault:"1"`
}

// ServerConfig configures the inbound Diameter server
type ServerConfig struct {
	ListenAddr       string        `json:"listen_addr" yaml:"listen_addr" env:"LISTEN_ADDR" envDefault:"0.0.0.0:3868" validate:"required"`
	MaxConnections   int           `json:"max_connections" yaml:"max_connections" env:"MAX_CONNECTIONS" envDefault:"1000" validate:"min=1"`
	HandshakeTimeout time.Duration `json:"handshake_timeout" yaml:"handshake_timeout" env:"HANDSHAKE_TIMEOUT" envDefault:"10s"`
	ReadTimeout      time.Duration `json:"read_timeout" yaml:"read_timeout" env:"READ_TIMEOUT" envDefault:"30s"`
	WriteTimeout     time.Duration `json:"write_timeout" yaml:"write_timeout" env:"WRITE_TIMEOUT" envDefault:"30s"`
	IdleTimeout      time.Duration `json:"idle_timeout" yaml:"idle_timeout" env:"IDLE_TIMEOUT" envDefault:"300s"`

	// Watchdog settings
	WatchdogInterval time.Duration `json:"watchdog_interval" yaml:"watchdog_interval" env:"WATCHDOG_INTERVAL" envDefault:"30s"`
	WatchdogTimeout  time.Duration `json:"watchdog_timeout" yaml:"watchdog_timeout" env:"WATCHDOG_TIMEOUT" envDefault:"10s"`
	WatchdogRetries  int           `json:"watchdog_retries" yaml:"watchdog_retries" env:"WATCHDOG_RETRIES" envDefault:"3"`

	// Buffer sizes
	ReadBufferSize  int `json:"read_buffer_size" yaml:"read_buffer_size" env:"READ_BUFFER_SIZE" envDefault:"8192"`
	WriteBufferSize int `json:"write_buffer_size" yaml:"write_buffer_size" env:"WRITE_BUFFER_SIZE" envDefault:"8192"`
}

// ClientConfig configures outbound client behavior
type ClientConfig struct {
	ConnectTimeout time.Duration `json:"connect_timeout" yaml:"connect_timeout" env:"CONNECT_TIMEOUT" envDefault:"5s"`
	RequestTimeout time.Duration `json:"request_timeout" yaml:"request_timeout" env:"REQUEST_TIMEOUT" envDefault:"10s"`
	RetryAttempts  int           `json:"retry_attempts" yaml:"retry_attempts" env:"RETRY_ATTEMPTS" envDefault:"3"`
	RetryDelay     time.Duration `json:"retry_delay" yaml:"retry_delay" env:"RETRY_DELAY" envDefault:"100ms"`

	// Connection pool settings
	MaxIdleConns    int           `json:"max_idle_conns" yaml:"max_idle_conns" env:"MAX_IDLE_CONNS" envDefault:"10"`
	MaxConnsPerHost int           `json:"max_conns_per_host" yaml:"max_conns_per_host" env:"MAX_CONNS_PER_HOST" envDefault:"100"`
	IdleConnTimeout time.Duration `json:"idle_conn_timeout" yaml:"idle_conn_timeout" env:"IDLE_CONN_TIMEOUT" envDefault:"90s"`

	// Watchdog settings
	WatchdogInterval time.Duration `json:"watchdog_interval" yaml:"watchdog_interval" env:"WATCHDOG_INTERVAL" envDefault:"30s"`
	WatchdogTimeout  time.Duration `json:"watchdog_timeout" yaml:"watchdog_timeout" env:"WATCHDOG_TIMEOUT" envDefault:"10s"`
}

// DRAConfig configures a Diameter Routing Agent
type DRAConfig struct {
	Name     string `json:"name" yaml:"name" validate:"required"`
	Host     string `json:"host" yaml:"host" validate:"required"`
	Port     int    `json:"port" yaml:"port" validate:"min=1,max=65535"`
	Priority int    `json:"priority" yaml:"priority" envDefault:"100"` // Lower = higher priority
	Weight   int    `json:"weight" yaml:"weight" envDefault:"100"`     // For load balancing
	Enabled  bool   `json:"enabled" yaml:"enabled" envDefault:"true"`

	// Optional realm/host for this DRA
	DestinationHost  string `json:"destination_host" yaml:"destination_host"`
	DestinationRealm string `json:"destination_realm" yaml:"destination_realm"`

	// Health check settings
	HealthCheckInterval time.Duration `json:"health_check_interval" yaml:"health_check_interval" envDefault:"10s"`
	HealthCheckTimeout  time.Duration `json:"health_check_timeout" yaml:"health_check_timeout" envDefault:"5s"`

	// Failover settings
	MaxFailures      int           `json:"max_failures" yaml:"max_failures" envDefault:"3"`
	FailureWindow    time.Duration `json:"failure_window" yaml:"failure_window" envDefault:"1m"`
	RecoveryInterval time.Duration `json:"recovery_interval" yaml:"recovery_interval" envDefault:"30s"`
}

// LoggingConfig configures logging
type LoggingConfig struct {
	Level  string `json:"level" yaml:"level" env:"LEVEL" envDefault:"info" validate:"oneof=debug info warn error"`
	Format string `json:"format" yaml:"format" env:"FORMAT" envDefault:"json" validate:"oneof=json text"`

	// Message logging for debugging
	LogMessages      bool `json:"log_messages" yaml:"log_messages" env:"LOG_MESSAGES" envDefault:"false"`
	LogMessageMaxLen int  `json:"log_message_max_len" yaml:"log_message_max_len" env:"LOG_MESSAGE_MAX_LEN" envDefault:"1024"`
}

// MetricsConfig configures metrics exposition
type MetricsConfig struct {
	Enabled bool   `json:"enabled" yaml:"enabled" env:"ENABLED" envDefault:"true"`
	Port    int    `json:"port" yaml:"port" env:"PORT" envDefault:"9091" validate:"min=1,max=65535"`
	Path    string `json:"path" yaml:"path" env:"PATH" envDefault:"/metrics"`
}

// LoaderConfig configures how configuration is loaded
type LoaderConfig struct {
	// ConfigFile is the path to the YAML configuration file
	ConfigFile string

	// ConfigFileSearchPaths are directories to search for config file
	ConfigFileSearchPaths []string

	// EnvPrefix is the prefix for environment variables (e.g., "DIAMGW_")
	EnvPrefix string

	// RemoteConfig configures remote config server
	RemoteConfig *RemoteConfig

	// EnableHotReload enables automatic config reloading
	EnableHotReload bool

	// ReloadCallback is called when config is reloaded
	ReloadCallback func(*Config) error
}

// RemoteConfig configures remote configuration source
type RemoteConfig struct {
	Provider  string     `json:"provider" yaml:"provider" validate:"oneof=consul etcd"`
	Endpoints []string   `json:"endpoints" yaml:"endpoints" validate:"required"`
	Key       string     `json:"key" yaml:"key" validate:"required"`
	TLS       *TLSConfig `json:"tls" yaml:"tls"`
}

// TLSConfig holds TLS configuration
type TLSConfig struct {
	CertFile string `json:"cert_file" yaml:"cert_file"`
	KeyFile  string `json:"key_file" yaml:"key_file"`
	CAFile   string `json:"ca_file" yaml:"ca_file"`
}

// Loader loads and manages Diameter Gateway configuration
type Loader struct {
	manager *sharedconfig.Manager
	config  *Config
}

// NewLoader creates a configuration loader
func NewLoader(cfg LoaderConfig) (*Loader, error) {
	var providers []sharedconfig.Provider
	var watcher sharedconfig.Watcher

	// Add file provider if configured
	if cfg.ConfigFile != "" {
		fileProvider, err := sharedconfig.NewFileProvider(sharedconfig.FileProviderConfig{
			Path:        cfg.ConfigFile,
			SearchPaths: cfg.ConfigFileSearchPaths,
			Required:    false,
		})
		if err == nil {
			providers = append(providers, fileProvider)

			// Setup file watcher for hot reload
			if cfg.EnableHotReload {
				fw, err := sharedconfig.NewFileWatcher(
					[]string{cfg.ConfigFile},
					100*time.Millisecond,
				)
				if err == nil {
					watcher = fw
				}
			}
		}
	}

	// Add remote provider if configured
	if cfg.RemoteConfig != nil {
		var remoteProvider sharedconfig.Provider
		var err error

		remoteCfg := sharedconfig.RemoteProviderConfig{
			Type:        sharedconfig.RemoteProviderType(cfg.RemoteConfig.Provider),
			Endpoints:   cfg.RemoteConfig.Endpoints,
			Key:         cfg.RemoteConfig.Key,
			Timeout:     10 * time.Second,
			RetryConfig: sharedconfig.DefaultRetryConfig(),
		}

		if cfg.RemoteConfig.TLS != nil {
			remoteCfg.TLSConfig = &sharedconfig.TLSConfig{
				CertFile: cfg.RemoteConfig.TLS.CertFile,
				KeyFile:  cfg.RemoteConfig.TLS.KeyFile,
				CAFile:   cfg.RemoteConfig.TLS.CAFile,
			}
		}

		switch cfg.RemoteConfig.Provider {
		case "consul":
			remoteProvider, err = sharedconfig.NewConsulProvider(remoteCfg)
		case "etcd":
			remoteProvider, err = sharedconfig.NewEtcdProvider(remoteCfg)
		default:
			return nil, fmt.Errorf("unsupported remote provider: %s", cfg.RemoteConfig.Provider)
		}

		if err != nil {
			return nil, fmt.Errorf("failed to create remote provider: %w", err)
		}

		providers = append(providers, remoteProvider)
	}

	// Add environment variable provider (highest priority)
	if cfg.EnvPrefix != "" {
		envProvider := sharedconfig.NewEnvProvider(sharedconfig.EnvProviderConfig{
			Prefix:    cfg.EnvPrefix,
			Separator: "_",
		})
		providers = append(providers, envProvider)
	}

	// Create validator
	configInstance := &Config{}
	validator := sharedconfig.NewStructValidator(configInstance)

	// Create manager
	manager := sharedconfig.NewManager(sharedconfig.ManagerConfig{
		Providers:       providers,
		Validator:       validator,
		Watcher:         watcher,
		EnableHotReload: cfg.EnableHotReload,
		ReloadCallback: func(data map[string]interface{}) error {
			if cfg.ReloadCallback != nil {
				return cfg.ReloadCallback(configInstance)
			}
			return nil
		},
	})

	return &Loader{
		manager: manager,
		config:  configInstance,
	}, nil
}

// Load loads the configuration
func (l *Loader) Load(ctx context.Context) (*Config, error) {
	data, err := l.manager.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Unmarshal into Config struct
	if err := sharedconfig.UnmarshalEnv(data, l.config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return l.config, nil
}

// Watch starts watching for configuration changes
func (l *Loader) Watch(ctx context.Context, callback func(*Config) error) error {
	return l.manager.Watch(ctx, func(data map[string]interface{}) error {
		// Unmarshal into new config instance
		newConfig := &Config{}
		if err := sharedconfig.UnmarshalEnv(data, newConfig); err != nil {
			return err
		}

		l.config = newConfig
		if callback != nil {
			return callback(newConfig)
		}
		return nil
	})
}

// Close closes the configuration loader
func (l *Loader) Close() error {
	return l.manager.Close()
}

// DefaultConfig returns configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Gateway: GatewayConfig{
			VendorID:        10415,
			ProductName:     "DiameterGateway",
			FirmwareVersion: 1,
		},
		Server: ServerConfig{
			ListenAddr:       "0.0.0.0:3868",
			MaxConnections:   1000,
			HandshakeTimeout: 10 * time.Second,
			ReadTimeout:      30 * time.Second,
			WriteTimeout:     30 * time.Second,
			IdleTimeout:      300 * time.Second,
			WatchdogInterval: 30 * time.Second,
			WatchdogTimeout:  10 * time.Second,
			WatchdogRetries:  3,
			ReadBufferSize:   8192,
			WriteBufferSize:  8192,
		},
		Client: ClientConfig{
			ConnectTimeout:   5 * time.Second,
			RequestTimeout:   10 * time.Second,
			RetryAttempts:    3,
			RetryDelay:       100 * time.Millisecond,
			MaxIdleConns:     10,
			MaxConnsPerHost:  100,
			IdleConnTimeout:  90 * time.Second,
			WatchdogInterval: 30 * time.Second,
			WatchdogTimeout:  10 * time.Second,
		},
		DRAs: []DRAConfig{},
		Logging: LoggingConfig{
			Level:            "info",
			Format:           "json",
			LogMessages:      false,
			LogMessageMaxLen: 1024,
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Port:    9091,
			Path:    "/metrics",
		},
	}
}
