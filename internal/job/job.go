package job

import (
	"log/slog"

	"ares/internal/config"

	"github.com/robfig/cron/v3"
)

var jobMap = make(map[string]func())

func Init() error {
	c := cron.New()

	for name, job := range config.Main.Job {
		_, err := c.AddFunc(job.Cron, jobMap[name])
		if err != nil {
			slog.Error("AddFunc error", slog.Any("error", err))
			return err
		}
	}

	c.Start()
	return nil
}

func Register(name string, fn func()) {
	jobMap[name] = fn
}
