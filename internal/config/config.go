package config

import (
	"ares/docs"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ares/internal/cli"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Log struct {
		Level          string `yaml:"level"`
		AccessLogfile  string `yaml:"accessLogfile"`
		RuntimeLogfile string `yaml:"runtimeLogfile"`
	} `yaml:"log"`
	Web struct {
		Address string `yaml:"address"`
	} `yaml:"web"`
	DB struct {
		ConnStr string `yaml:"conn_str"`
	} `yaml:"db"`
	Job map[string]struct {
		Cron string `yaml:"cron"`
	} `yaml:"job"`
	Jenkins struct {
		Address  string `yaml:"address"`
		Token    string `yaml:"token"`
		UserName string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"jenkins"`
	K8s struct {
		Clusters map[string]struct {
			Name        string `yaml:"name"`
			ConfigPath  string `yaml:"config_path"`
			Description string `yaml:"description"`
		} `yaml:"clusters"`
	} `yaml:"k8s"`
	Apollo struct {
		Enable    bool   `yaml:"enable"`
		Address   string `yaml:"address"`   // Apollo Config Service base URL, e.g. http://apollo-config-service:8080
		AppID     string `yaml:"app_id"`    // e.g. ares
		Cluster   string `yaml:"cluster"`   // e.g. default
		Namespace string `yaml:"namespace"` // e.g. application
		Key       string `yaml:"key"`       // e.g. config_file_name (value is "default.yaml"/"test.yaml"...)
		TimeoutMS int    `yaml:"timeout_ms"`
	} `yaml:"apollo"`
}

var Main = &Config{}

func Init() error {
	// 先读取默认配置文件（兜底），再尝试从 Apollo 获取“配置文件名”并二次加载覆盖
	bootstrapPath := cli.ConfigFilePath
	slog.Info("config bootstrap start", "bootstrap_path", bootstrapPath)
	yamlData, err := os.ReadFile(bootstrapPath)
	if err != nil {
		slog.Error("read config file error", "path", bootstrapPath, slog.Any("error", err))
		return err
	}

	err = yaml.Unmarshal(yamlData, Main)
	if err != nil {
		slog.Error("yaml unmarshal error", "path", bootstrapPath, slog.Any("error", err))
		return err
	}

	loadedPath := bootstrapPath

	// Apollo：只读取“配置文件名”，决定二次加载哪个 config/*.yaml
	{
		if Main == nil || !Main.Apollo.Enable {
			slog.Info("apollo config disabled, skip")
		} else {
			base := strings.TrimRight(strings.TrimSpace(Main.Apollo.Address), "/")
			appID := strings.TrimSpace(Main.Apollo.AppID)
			cluster := strings.TrimSpace(Main.Apollo.Cluster)
			namespace := strings.TrimSpace(Main.Apollo.Namespace)
			key := strings.TrimSpace(Main.Apollo.Key)
			timeoutMS := Main.Apollo.TimeoutMS
			if timeoutMS <= 0 {
				timeoutMS = 1200
			}

			slog.Info("apollo config resolve start",
				"address", base,
				"app_id", appID,
				"cluster", cluster,
				"namespace", namespace,
				"key", key,
				"timeout_ms", timeoutMS,
			)
		}
	}
	if p, ok, apolloErr := resolveConfigPathFromApollo(Main, bootstrapPath); apolloErr != nil {
		slog.Warn("apollo config resolve failed, fallback to bootstrap config",
			"bootstrap_path", bootstrapPath,
			"error", apolloErr.Error(),
		)
	} else if !ok {
		slog.Info("apollo config resolve skipped/no value, keep bootstrap config",
			"bootstrap_path", bootstrapPath,
		)
	} else if ok && p != "" && p != bootstrapPath {
		slog.Info("apollo selected config file", "selected_path", p)
		if b, err := os.ReadFile(p); err != nil {
			slog.Warn("read apollo-selected config file failed, fallback to bootstrap config",
				"selected_path", p,
				"bootstrap_path", bootstrapPath,
				"error", err.Error(),
			)
		} else if err := yaml.Unmarshal(b, Main); err != nil {
			slog.Warn("yaml unmarshal apollo-selected config failed, fallback to bootstrap config",
				"selected_path", p,
				"bootstrap_path", bootstrapPath,
				"error", err.Error(),
			)
		} else {
			loadedPath = p
			slog.Info("apollo selected config loaded successfully", "path", loadedPath)
		}
	}

	// 注意：不要把完整配置打印到日志（包含 token/密码等敏感信息）
	slog.Info("load config successfully",
		"path", loadedPath,
		"web.address", Main.Web.Address,
	)
	return nil
}

func InitSwagger() {
	docs.SwaggerInfo.Title = "Ares"
	docs.SwaggerInfo.Version = "v1.x"
	docs.SwaggerInfo.Description = "天天拍车发布引擎"
	docs.SwaggerInfo.Schemes = []string{"http", "https"}
	docs.SwaggerInfo.Host = ""
	docs.SwaggerInfo.BasePath = ""
	slog.Info("swagger config successfully")
}

type apolloConfigResponse struct {
	Configurations map[string]string `json:"configurations"`
}

func resolveConfigPathFromApollo(cfg *Config, bootstrapPath string) (path string, ok bool, err error) {
	if cfg == nil || !cfg.Apollo.Enable {
		return "", false, nil
	}
	if strings.TrimSpace(cfg.Apollo.Address) == "" {
		return "", false, nil
	}
	appID := strings.TrimSpace(cfg.Apollo.AppID)
	cluster := strings.TrimSpace(cfg.Apollo.Cluster)
	namespace := strings.TrimSpace(cfg.Apollo.Namespace)
	key := strings.TrimSpace(cfg.Apollo.Key)
	if appID == "" || cluster == "" || namespace == "" || key == "" {
		return "", false, nil
	}

	timeout := time.Duration(cfg.Apollo.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 1200 * time.Millisecond
	}
	base := strings.TrimRight(cfg.Apollo.Address, "/")
	// Apollo Config Service: GET /configs/{appId}/{cluster}/{namespace}
	url := base + "/configs/" + appID + "/" + cluster + "/" + namespace

	slog.Debug("apollo http request",
		"url", url,
		"key", key,
		"timeout_ms", int(timeout.Milliseconds()),
	)
	client := &http.Client{Timeout: timeout}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, fmt.Errorf("apollo http status: %s", resp.Status)
	}
	var r apolloConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", false, err
	}
	if r.Configurations == nil {
		return "", false, fmt.Errorf("apollo response missing configurations")
	}
	name := strings.TrimSpace(r.Configurations[key])
	if name == "" {
		slog.Info("apollo key not found/empty, keep bootstrap config",
			"key", key,
			"bootstrap_path", bootstrapPath,
		)
		return "", false, nil
	}

	// 允许 value 为 "default.yaml" / "test.yaml" / "test"（自动补 .yaml）
	if !strings.HasSuffix(strings.ToLower(name), ".yaml") && !strings.HasSuffix(strings.ToLower(name), ".yml") {
		name = name + ".yaml"
	}

	// 仅允许读取 config/ 目录下的文件，防止路径穿越
	name = strings.TrimPrefix(name, "/")
	clean := filepath.Clean(name)
	if strings.Contains(clean, "..") || strings.HasPrefix(clean, "../") {
		return "", false, fmt.Errorf("invalid config file name from apollo: %s", name)
	}
	if strings.Contains(clean, string(filepath.Separator)) {
		// 不允许子目录：强制只允许文件名
		return "", false, fmt.Errorf("invalid config file name (no subdir allowed): %s", name)
	}
	selected := filepath.Join("config", clean)
	// 与 bootstrap 同目录策略保持一致：如果 bootstrap 不是 config/xxx，则仍强制选 config/xxx
	_ = bootstrapPath
	slog.Info("apollo resolved config file name",
		"key", key,
		"value", name,
		"selected_path", selected,
	)
	return selected, true, nil
}
