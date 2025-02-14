package entity

// DeployStatus 定义部署相关的所有状态
const (
	// 初始状态
	StatusInit = "init" // 初始状态
	// 打包相关状态
	StatusPackaging     = "packaging"      // 打包中
	StatusPackaged      = "packaged"       // 打包成功
	StatusPackageFailed = "package_failed" // 打包失败

	// 部署相关状态
	StatusDeploying    = "deploying"     // 部署中
	StatusDeployed     = "deployed"      // 部署成功
	StatusDeployFailed = "deploy_failed" // 部署失败
)
