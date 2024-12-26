package logger

import (
	"log/slog"
	"os"
	"strings"
)

func Init() {
	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		AddSource:   true,
		Level:       slog.LevelInfo,
		ReplaceAttr: nil,
	}))

	slog.SetDefault(l)

	slog.SetLogLoggerLevel(slog.LevelInfo)
}

func SetLevel(level string) (oldLevel string) {
	switch strings.ToUpper(level) {
	case slog.LevelDebug.String():
		return slog.SetLogLoggerLevel(slog.LevelDebug).String()
	case slog.LevelInfo.String():
		return slog.SetLogLoggerLevel(slog.LevelInfo).String()
	case slog.LevelWarn.String():
		return slog.SetLogLoggerLevel(slog.LevelWarn).String()
	case slog.LevelError.String():
		return slog.SetLogLoggerLevel(slog.LevelError).String()
	default:
		slog.Error("unknown log level: " + level)
		return ""
	}
}
