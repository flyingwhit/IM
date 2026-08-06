// Package logging initializes structured logging via Go's standard library slog.
//
// slog was added in Go 1.21 and provides structured key=value logging with
// levels (Debug, Info, Warn, Error) and pluggable handlers (JSON, text).
// It replaces the ad-hoc log.Printf pattern with machine-parseable output
// that log aggregation systems (Loki, ELK, CloudWatch) can index.
//
// Usage after Init():
//
//	slog.Info("message sent", "msg_id", id, "sender", sender)
//	slog.Warn("ws read error", "user", userID, "err", err)
//	slog.Error("kafka publish failed", "msg_id", id, "err", err)
//
// The global logger is configured — no need to pass a *slog.Logger around.
// For context propagation (future trace IDs), use slog.InfoContext(ctx, ...).
package logging

import (
	"log/slog"
	"os"
)

// Init configures the global slog logger.
//
// level is one of: "debug", "info", "warn", "error" (default "info").
// format is "json" or "text" (default "text" for readability).
//
// In production, use JSON format so log aggregators can parse structured fields.
// In development, text format with colors is more readable.
func Init(level, format string) {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: logLevel,
		// AddSource adds file:line to each log entry — useful in development,
		// but adds allocation overhead in production.
		AddSource: logLevel == slog.LevelDebug,
	}

	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	slog.SetDefault(slog.New(handler))
}
