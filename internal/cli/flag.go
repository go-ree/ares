package cli

import (
	"flag"
)

var ConfigFilePath = "config.yaml"

func Init() {
	flag.StringVar(&ConfigFilePath, "config", "config.yaml", "配置文件路径")
	flag.Parse()
}
