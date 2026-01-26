package cli

import (
	"flag"
)

// ConfigFilePath 默认读取的配置文件路径
var ConfigFilePath = "default.yaml"

func Init() {
	// 获取当前执行文件的路径
	//execPath, err := os.Executable()
	//if err != nil {
	//	panic(err)
	//}
	//execDir := filepath.Dir(execPath)

	// 自定义配置文件路径，会覆盖默认的配置
	//defaultConfigPath := filepath.Join(execDir, "config/default.yaml")
	flag.StringVar(&ConfigFilePath, "config", "config/default.yaml", "配置文件路径")
	flag.Parse()
}
