package logger

import (
	"fmt"
	"os"
	"time"

	"github.com/hsdfat/go-zlog/logger"
	"github.com/hsdfat/go-zlog/sink"
	"go.uber.org/zap"
)

// Log is the global logger instance for the diam-gw project
var Log logger.LoggerI = logger.NewLogger()

func init() {
	Log.(*logger.Logger).SugaredLogger = Log.(*logger.Logger).SugaredLogger.WithOptions(zap.AddCallerSkip(1))
}

// CentralizedConfig holds centralized logging configuration
// This is separate from internal/config to avoid import cycles
type CentralizedConfig struct {
	Enabled       bool
	Backend       string
	LokiURL       string
	HTTPURL       string
	TenantID      string
	BearerToken   string
	BufferSize    int
	FlushInterval int
	MaxBatchSize  int
	Labels        map[string]string
}

// InitializeWithCentralizedLogging initializes the logger with centralized logging support
func InitializeWithCentralizedLogging(cfg *CentralizedConfig, logLevel string) error {
	var sinks []sink.Sink

	// Setup centralized logging if enabled
	if cfg != nil && cfg.Enabled {
		var remoteSink sink.Sink
		var err error

		// Create the appropriate sink based on backend type
		switch cfg.Backend {
		case "loki":
			remoteSink, err = createLokiSink(cfg)
		case "http":
			remoteSink, err = createHTTPSink(cfg)
		default:
			return fmt.Errorf("unsupported centralized logging backend: %s", cfg.Backend)
		}

		if err != nil {
			return fmt.Errorf("failed to create centralized logging sink: %w", err)
		}

		// Wrap with buffering
		bufferedSink := sink.NewBufferedSink(remoteSink, createSinkConfig(cfg))
		sinks = append(sinks, bufferedSink)
	}

	// Create new logger with remote sinks
	loggerConfig := &logger.LoggerConfig{
		EnableConsole: true,
		RemoteSinks:   sinks,
	}

	newLogger := logger.NewLoggerWithConfig(loggerConfig)
	newLogger.SugaredLogger = newLogger.SugaredLogger.WithOptions(zap.AddCallerSkip(1))
	Log = newLogger

	// Set log level
	if logLevel != "" {
		SetLevel(logLevel)
	}

	return nil
}

// createLokiSink creates a Loki sink from configuration
func createLokiSink(cfg *CentralizedConfig) (sink.Sink, error) {
	hostname, _ := os.Hostname()

	labels := map[string]string{
		"service":     "diam-gw",
		"environment": "production",
		"hostname":    hostname,
	}

	// Add custom labels
	if cfg.Labels != nil {
		for k, v := range cfg.Labels {
			labels[k] = v
		}
	}

	lokiConfig := &sink.LokiSinkConfig{
		Config:      createSinkConfig(cfg),
		URL:         cfg.LokiURL,
		TenantID:    cfg.TenantID,
		Labels:      labels,
		BearerToken: cfg.BearerToken,
	}

	return sink.NewLokiSink(lokiConfig)
}

// createHTTPSink creates a generic HTTP sink from configuration
func createHTTPSink(cfg *CentralizedConfig) (sink.Sink, error) {
	httpConfig := &sink.HTTPSinkConfig{
		Config:      createSinkConfig(cfg),
		URL:         cfg.HTTPURL,
		Method:      "POST",
		ContentType: "application/json",
		BearerToken: cfg.BearerToken,
	}

	return sink.NewHTTPSink(httpConfig)
}

// createSinkConfig creates a sink.Config from the centralized configuration
func createSinkConfig(cfg *CentralizedConfig) *sink.Config {
	hostname, _ := os.Hostname()

	sinkCfg := sink.DefaultConfig()
	sinkCfg.ServiceName = "diam-gw"
	sinkCfg.Environment = "production"
	sinkCfg.InstanceID = hostname

	// Apply custom buffer settings if provided
	if cfg.BufferSize > 0 {
		sinkCfg.BufferSize = cfg.BufferSize
	}
	if cfg.FlushInterval > 0 {
		sinkCfg.FlushInterval = time.Duration(cfg.FlushInterval) * time.Second
	}
	if cfg.MaxBatchSize > 0 {
		sinkCfg.MaxBatchSize = cfg.MaxBatchSize
	}

	return sinkCfg
}

// SetLevel sets the global log level
// Valid levels: "debug", "info", "warn", "error", "fatal"
func SetLevel(level string) {
	logger.SetLevel(level)
}

// WithFields creates a new logger with contextual fields
// Example: logger.WithFields("conn_id", "abc123", "state", "OPEN")
func WithFields(args ...any) logger.LoggerI {
	return Log.With(args...).(logger.LoggerI)
}

// Logger is an alias for the underlying logger interface
type Logger = logger.LoggerI

// New creates a new logger with a name and level
func New(name, level string) Logger {
	if level != "" {
		// Set level if provided
		logger.SetLevel(level)
	}
	return Log.With("mod", name).(logger.LoggerI)
}
