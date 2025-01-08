package publish

import "fmt"

// PublishingEntry 所有类型项目的统一发布入口，由这里决定具体的发布动作
func PublishingEntry(appId int, env string) error {
	// 根据appid查询出应用的开发语言
	// 假设是一个sql的查询操作，查出来了是golang类型项目
	devLanguage := "golang"

	switch devLanguage {
	case "java":
		fmt.Println("java")
		BuildJAVA()
		return nil
	case "golang":
		BuildGolang()
		return nil
	case "python":
		fmt.Println("python")
		BuildPython()
		return nil
	case "nodejs":
		fmt.Println("nodejs")
		BuildNodeJS()
		return nil
	case "static":
		fmt.Println("static")
		BuildStatic()
		return nil
	default:
		fmt.Println("unknown")
		return nil
	}
}

func BuildGolang() {
	fmt.Println("golang")
}

func BuildJAVA() {
	fmt.Println("java")
}

func BuildPython() {
	fmt.Println("python")
}

func BuildNodeJS() {
	fmt.Println("nodejs")
}

func BuildStatic() {
	fmt.Println("static")
}
