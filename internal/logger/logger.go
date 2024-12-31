package logger

import (
	"gitlab.ttpai.work/sre/pipeline/ares/internal/config"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var AccessFile *os.File
var RuntimeFile *os.File
var initLogLevel = slog.LevelDebug

func Init() error {
	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		AddSource:   true,
		Level:       initLogLevel,
		ReplaceAttr: nil,
	}))

	slog.SetDefault(l)

	return nil
}

func Init2(logLevel string) error {
	var err error
	AccessFile, err = createLogFile(config.Main.Log.AccessLogfile)
	if err != nil {
		return err
	}
	RuntimeFile, err = createLogFile(config.Main.Log.RuntimeLogfile)
	if err != nil {
		return err
	}

	l := slog.New(slog.NewTextHandler(RuntimeFile, &slog.HandlerOptions{
		AddSource:   true,
		Level:       Level2Level(logLevel),
		ReplaceAttr: nil,
	}))

	slog.SetDefault(l)

	return nil
}

func createLogFile(file string) (*os.File, error) {
	ex, err := os.Executable()
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(ex)

	//确保存放日志文件的目录始终存在
	logPathDir := path.Dir(file)                                 //返回路径中除去最后一个元素的剩余部分，也就是路径最后一个元素所在的目录
	if err := os.MkdirAll(logPathDir, os.ModePerm); err != nil { //创建目录类似于（mkdir -p /aaa/bbb的效果）
		slog.Error("创建日志目录失败", slog.Any("error", err))
		return nil, err
	}
	slog.Debug("日志路径为", slog.Any("logPathDir", logPathDir))

	slog.Debug("当前路径为：", slog.Any("dir", dir))

	file1, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		slog.Error("打开日志文件失败", slog.Any("error", err))
		return nil, err
	}
	return file1, err
}

func Level2Level(level string) slog.Level {
	slog.Debug("日志等级", slog.Any("level", level))
	switch strings.ToUpper(level) {
	case slog.LevelDebug.String():
		//return slog.SetLogLoggerLevel(slog.LevelDebug).String()
		return slog.LevelDebug
	case slog.LevelInfo.String():
		slog.Debug("解析后的日志等级", slog.Any("level", level))
		//return slog.SetLogLoggerLevel(slog.LevelInfo).String()
		return slog.LevelInfo
	case slog.LevelWarn.String():
		//return slog.SetLogLoggerLevel(slog.LevelWarn).String()
		return slog.LevelWarn
	case slog.LevelError.String():
		//return slog.SetLogLoggerLevel(slog.LevelError).String()
		return slog.LevelError
	default:
		slog.Error("unknown log level: " + level + "现在用的是info类型")
		return slog.LevelInfo
	}
}
