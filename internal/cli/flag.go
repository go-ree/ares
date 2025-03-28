package cli

import (
	"flag"
)

// ConfigFilePath 默认读取的配置文件路径
var ConfigFilePath = "default.yaml"

func Init() {
	// 自定义配置文件路径，会覆盖默认的配置
	flag.StringVar(&ConfigFilePath, "config", "config/default.yaml", "配置文件路径")
	flag.Parse()
}
