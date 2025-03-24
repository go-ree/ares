package jenkins

import (
	"ares/internal/config"
	"context"
	"github.com/bndr/gojenkins"
	"log/slog"
)

var Jenkins *gojenkins.Jenkins

func Init() error {
	var err error
	ctx := context.Background()
	//Jenkins = gojenkins.CreateJenkins(nil, config.Main.Jenkins.Address, config.Main.Jenkins.UserName, config.Main.Jenkins.Password)
	// 这里发现使用token进行请求会提高响应效率，减少花费的时间
	Jenkins = gojenkins.CreateJenkins(nil, config.Main.Jenkins.Address, config.Main.Jenkins.UserName, config.Main.Jenkins.Token)
	_, err = Jenkins.Init(ctx)
	if err != nil {
		slog.Error("init jenkins error", slog.Any("error", err))
		return err
	}
	slog.Info("init jenkins success")
	return nil
}
