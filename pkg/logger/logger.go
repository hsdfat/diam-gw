package logger

import (
	"github.com/hsdfat/go-zlog/logger"
	"go.uber.org/zap"
)

// Log is the global logger instance for the diam-gw project
var Log logger.LoggerI = logger.NewLogger()
func init(){ 
	Log.(*logger.Logger).SugaredLogger = Log.(*logger.Logger).SugaredLogger.WithOptions(zap.AddCallerSkip(1))
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
