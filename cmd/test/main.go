package main

import (
	"log/slog"
	"os"
)

func main() {
	slog.Debug("debug1")
	slog.Info("info1")

	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		AddSource:   true,
		Level:       slog.LevelInfo,
		ReplaceAttr: nil,
	}))

	slog.SetDefault(l)

	slog.SetLogLoggerLevel(slog.LevelInfo)
	slog.Debug("debug2")
	slog.Info("info2")
	slog.SetLogLoggerLevel(slog.LevelInfo)
	slog.Debug("debug3")
	slog.Info("info3")
}
