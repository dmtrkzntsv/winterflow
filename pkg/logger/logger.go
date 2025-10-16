package logger

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

type Logger struct {
	LogLevel string
	Service  string
	Logger   *slog.Logger
}

type LoggerConfiguration struct {
	LogLevel string
	Service  string
}

func NewLogger(cfg LoggerConfiguration) *Logger {
	defaultLevel := slog.LevelDebug
	level := defaultLevel
	if cfg.LogLevel != "" {
		level = parseLogLevel(cfg.LogLevel, defaultLevel)
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	logger := slog.New(handler)
	if cfg.Service != "" {
		logger = logger.With("service", cfg.Service)
	}

	return &Logger{
		LogLevel: cfg.LogLevel,
		Service:  cfg.Service,
		Logger:   logger,
	}
}

func (l *Logger) Debug(msg string, args ...any) { l.Logger.Debug(msg, args...) }
func (l *Logger) Info(msg string, args ...any)  { l.Logger.Info(msg, args...) }
func (l *Logger) Warn(msg string, args ...any)  { l.Logger.Warn(msg, args...) }
func (l *Logger) Error(msg string, args ...any) { l.Logger.Error(msg, args...) }
func (l *Logger) Fatalf(format string, args ...any) {
	l.Logger.Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}
func (l *Logger) Fatal(args ...any) {
	l.Logger.Error(fmt.Sprint(args...))
	os.Exit(1)
}

func parseLogLevel(levelStr string, defaultLevel slog.Level) slog.Level {
	switch strings.ToLower(levelStr) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return defaultLevel
	}
}
