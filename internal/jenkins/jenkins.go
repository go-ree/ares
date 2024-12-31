package jenkins

import (
	"context"
	"github.com/bndr/gojenkins"
	"log/slog"
)

var Jenkins *gojenkins.Jenkins

func Init() error {
	var err error
	ctx := context.Background()
	//Jenkins := gojenkins.CreateJenkins(nil, config.Main.Jenkins.Address, config.Main.Jenkins.Token)
	Jenkins = gojenkins.CreateJenkins(nil, "http://172.16.2.88:8080", "admin", "hello@ttpai")
	_, err = Jenkins.Init(ctx)
	if err != nil {
		slog.Error("init jenkins error", slog.Any("error", err))
		return err
	}
	slog.Info("init jenkins success")
	return nil
}
