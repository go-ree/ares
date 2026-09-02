# Kubernetes 业务层操作系统

这是一个**面向业务**的Kubernetes多环境管理系统，采用**管理器模式**设计，**专注于您真正关心的问题**：环境是否可用、命名空间能否访问、应用状态如何等。

## 🎯 支持的环境

- ✅ **dev** - 开发环境 (支持别名: development)
- ✅ **test** - 测试环境 (支持别名: testing)  
- ✅ **moni** - 监控环境 (支持别名: monitor, staging)

## 🎯 您真正关心的问题

在实际工作中，您不需要关心底层的集群细节，您关心的是：

- ✅ **环境是否可用** - dev/test/moni环境能否正常访问？
- ✅ **命名空间是否可达** - 能否访问指定的命名空间？  
- ✅ **应用状态如何** - 应用是否正在运行？有多少副本？
- ✅ **部署是否成功** - 新版本是否部署成功？
- ✅ **监控和排查** - 快速了解环境整体状况
- ✅ **Pod管理** - 重启、日志查看、状态监控
- ✅ **Service管理** - 负载均衡、端点管理、服务暴露

## 🏗️ 管理器架构设计

新的设计采用**管理器模式**，将不同的K8s操作按业务领域分类：

```
┌─────────────────┬─────────────────┬─────────────────┐
│  ApplicationManager │    PodManager       │  ServiceManager     │
├─────────────────┼─────────────────┼─────────────────┤
│ • 应用部署        │ • Pod重启        │ • Service创建     │
│ • 应用更新        │ • 日志获取        │ • 负载均衡配置     │
│ • 应用删除        │ • 状态监控        │ • 端点管理        │
│ • 扩缩容         │ • 事件查看        │ • 服务暴露        │
│ • 状态查询        │ • 健康检查        │ • 连接测试        │
└─────────────────┴─────────────────┴─────────────────┘
                         ↑
                 统一的业务函数接口
                 k8s.DeployApp() 
                 k8s.RestartApp()
                 k8s.GetAppLogs()
```

## 🚀 新的管理器使用方式

### ✨ 应用管理器 (ApplicationManager)

```go
// 获取应用管理器
appManager := k8s.GetApplicationManager()

// 部署应用
appReq := &k8s.ApplicationRequest{
    AppName:       "demo-app",
    Namespace:     "default",
    Env:           "dev",
    Image:         "nginx:1.20",
    Replicas:      2,
    Port:          80,
    LimitsMemory:  "256Mi",
    LimitsCPU:     "200m",
    RequestMemory: "128Mi",
    RequestCPU:    "100m",
    Labels: map[string]string{
        "team": "platform",
        "env":  "dev",
    },
}

result, err := appManager.DeployApplication(ctx, appReq)
if err == nil {
    fmt.Printf("✅ 应用部署成功: %s\n", result.Message)
}

// 获取应用状态
status, err := appManager.GetApplicationStatus(ctx, "demo-app", "default", "dev")
if err == nil {
    fmt.Printf("应用状态: %s, 副本: %d/%d\n", 
        status.Message, status.ReadyReplicas, status.Replicas)
}

// 扩缩容应用
result, err = appManager.ScaleApplication(ctx, "demo-app", "default", "dev", 5)
if err == nil {
    fmt.Printf("✅ 扩容成功: %s\n", result.Message)
}

// 删除应用
result, err = appManager.DeleteApplication(ctx, "demo-app", "default", "dev")
```

### ✨ Pod管理器 (PodManager)

```go
// 获取Pod管理器
podManager := k8s.GetPodManager()

// 获取Pod列表
pods, err := podManager.GetPodsInNamespace(ctx, "default", "dev", "app=demo-app")
for _, pod := range pods {
    fmt.Printf("Pod: %s, 状态: %s, 重启次数: %d\n", 
        pod.Name, pod.Status, pod.RestartCount)
}

// 重启Pod
err = podManager.RestartPod(ctx, "demo-app-123", "default", "dev")

// 批量重启Pod (通过标签选择器)
restartedPods, err := podManager.RestartPodsBySelector(ctx, "default", "dev", "app=demo-app")
fmt.Printf("重启了 %d 个Pod\n", len(restartedPods))

// 获取Pod日志
tailLines := int64(100)
logReq := &k8s.PodLogRequest{
    PodName:   "demo-app-123",
    Namespace: "default",
    Env:       "dev",
    TailLines: &tailLines,
}
logs, err := podManager.GetPodLogs(ctx, logReq)

// 流式获取Pod日志
logChannel := make(chan string, 100)
go podManager.StreamPodLogs(ctx, logReq, logChannel)
for logLine := range logChannel {
    fmt.Println("日志:", logLine)
}

// 等待Pod就绪
err = podManager.WaitForPodReady(ctx, "demo-app-123", "default", "dev", 30*time.Second)

// 获取Pod事件
events, err := podManager.GetPodEvents(ctx, "demo-app-123", "default", "dev")
```

### ✨ Service管理器 (ServiceManager)

```go
// 获取Service管理器
serviceManager := k8s.GetServiceManager()

// 创建Service
serviceReq := &k8s.ServiceRequest{
    ServiceName: "demo-app",
    Namespace:   "default",
    Env:         "dev",
    Selector: map[string]string{
        "app": "demo-app",
    },
    Ports: []k8s.ServicePort{
        {
            Name: "http",
            Port: 80,
        },
    },
    ServiceType: corev1.ServiceTypeClusterIP,
}

result, err := serviceManager.CreateOrUpdateService(ctx, serviceReq)
if err == nil {
    fmt.Printf("✅ Service创建成功: %s\n", result.Message)
}

// 获取Service信息
serviceInfo, err := serviceManager.GetService(ctx, "demo-app", "default", "dev")
if err == nil {
    fmt.Printf("Service: %s, 类型: %s, ClusterIP: %s\n", 
        serviceInfo.Name, serviceInfo.Type, serviceInfo.ClusterIP)
}

// 获取Service端点
endpoints, err := serviceManager.GetServiceEndpoints(ctx, "demo-app", "default", "dev")
for _, endpoint := range endpoints {
    fmt.Printf("端点: %s:%d, 就绪: %t\n", 
        endpoint.IP, endpoint.Port, endpoint.Ready)
}

// 暴露Service为NodePort
result, err = serviceManager.ExposeService(ctx, "demo-app", "default", "dev", corev1.ServiceTypeNodePort)

// 删除Service
result, err = serviceManager.DeleteService(ctx, "demo-app", "default", "dev")
```

## 🎯 便捷业务函数

为了简化使用，我们提供了高级的便捷函数：

```go
// 部署应用（一键完成Deployment + Service）
appReq := &k8s.ApplicationRequest{
    AppName:   "web-app",
    Namespace: "demo",
    Env:       "dev",
    Image:     "nginx:alpine",
    Replicas:  2,
    Port:      80,
}
result, err := k8s.DeployApp(ctx, appReq)

// 删除应用
result, err := k8s.DeleteApp(ctx, "web-app", "demo", "dev")

// 扩缩容应用
result, err := k8s.ScaleApp(ctx, "web-app", "demo", "dev", 5)

// 重启应用的所有Pod
restartedPods, err := k8s.RestartApp(ctx, "web-app", "demo", "dev")

// 获取应用日志
tailLines := int64(50)
logs, err := k8s.GetAppLogs(ctx, "web-app", "demo", "dev", &tailLines)

// 为应用创建Service
result, err := k8s.CreateAppService(ctx, "web-app", "demo", "dev", 80)

// 获取应用的Service信息
serviceInfo, err := k8s.GetAppService(ctx, "web-app", "demo", "dev")
```

## 📝 实际业务场景

### 场景1：完整的应用部署流程

```go
func deployApplicationWorkflow(ctx context.Context) error {
    // 1. 部署应用
    appReq := &k8s.ApplicationRequest{
        AppName:       "production-app",
        Namespace:     "production",
        Env:           "moni",
        Image:         "nginx:1.21-alpine",
        Replicas:      3,
        Port:          80,
        LimitsMemory:  "512Mi",
        LimitsCPU:     "500m",
        RequestMemory: "256Mi",
        RequestCPU:    "200m",
        Labels: map[string]string{
            "app":     "production-app",
            "env":     "moni",
            "version": "v1.0.0",
            "tier":    "frontend",
        },
    }

    result, err := k8s.DeployApp(ctx, appReq)
    if err != nil {
        return fmt.Errorf("应用部署失败: %w", err)
    }
    log.Printf("✅ 应用部署成功: %s", result.Message)

    // 2. 创建Service
    serviceResult, err := k8s.CreateAppService(ctx, "production-app", "production", "moni", 80)
    if err != nil {
        return fmt.Errorf("Service创建失败: %w", err)
    }
    log.Printf("✅ Service创建成功: %s", serviceResult.Message)

    // 3. 等待应用就绪
    time.Sleep(15 * time.Second)

    // 4. 验证部署结果
    podManager := k8s.GetPodManager()
    pods, err := podManager.GetPodsInNamespace(ctx, "production", "moni", "app=production-app")
    if err != nil {
        return fmt.Errorf("获取Pod列表失败: %w", err)
    }

    readyCount := 0
    for _, pod := range pods {
        if strings.Contains(pod.Status, "Ready") {
            readyCount++
        }
    }

    if int32(readyCount) == appReq.Replicas {
        log.Printf("✅ 所有Pod已就绪，部署成功")
    } else {
        log.Printf("⚠️ 部分Pod未就绪: %d/%d", readyCount, appReq.Replicas)
    }

    return nil
}
```

### 场景2：应用故障排查

```go
func troubleshootApplication(ctx context.Context, appName, namespace, env string) {
    appManager := k8s.GetApplicationManager()
    podManager := k8s.GetPodManager()

    // 1. 检查应用状态
    status, err := appManager.GetApplicationStatus(ctx, appName, namespace, env)
    if err != nil {
        log.Printf("❌ 应用不存在: %v", err)
        return
    }

    log.Printf("📊 应用状态: %s, 副本: %d/%d", 
        status.Message, status.ReadyReplicas, status.Replicas)

    // 2. 检查Pod状态
    pods, err := podManager.GetPodsInNamespace(ctx, namespace, env, fmt.Sprintf("app=%s", appName))
    if err != nil {
        log.Printf("❌ 获取Pod列表失败: %v", err)
        return
    }

    for _, pod := range pods {
        log.Printf("Pod: %s, 状态: %s, 重启: %d次", 
            pod.Name, pod.Status, pod.RestartCount)

        // 如果Pod重启次数过多，获取日志
        if pod.RestartCount > 5 {
            tailLines := int64(20)
            logReq := &k8s.PodLogRequest{
                PodName:   pod.Name,
                Namespace: namespace,
                Env:       env,
                TailLines: &tailLines,
                Previous:  true, // 获取前一个容器的日志
            }

            logs, err := podManager.GetPodLogs(ctx, logReq)
            if err == nil {
                log.Printf("🔍 Pod %s 的错误日志:\n%s", pod.Name, logs)
            }

            // 获取Pod事件
            events, err := podManager.GetPodEvents(ctx, pod.Name, namespace, env)
            if err == nil {
                log.Printf("📋 Pod %s 的事件数量: %d", pod.Name, len(events))
            }
        }
    }

    // 3. 检查Service状态
    serviceManager := k8s.GetServiceManager()
    serviceInfo, err := serviceManager.GetService(ctx, appName, namespace, env)
    if err == nil {
        log.Printf("🌐 Service状态: %s, 类型: %s", serviceInfo.Name, serviceInfo.Type)

        // 检查Service端点
        endpoints, err := serviceManager.GetServiceEndpoints(ctx, appName, namespace, env)
        if err == nil {
            readyEndpoints := 0
            for _, endpoint := range endpoints {
                if endpoint.Ready {
                    readyEndpoints++
                }
            }
            log.Printf("🔗 Service端点: %d个总共, %d个就绪", len(endpoints), readyEndpoints)
        }
    } else {
        log.Printf("⚠️ Service不存在: %v", err)
    }
}
```

### 场景3：生产环境维护

```go
func productionMaintenance(ctx context.Context) {
    // 1. 检查所有环境状态
    allStatus := k8s.GetAllEnvsStatus()
    for envName, status := range allStatus {
        if status.Available {
            log.Printf("✅ %s环境正常 - 节点:%d Pod:%d", 
                envName, status.NodeCount, status.PodCount)
        } else {
            log.Printf("❌ %s环境异常 - %s", envName, status.Error)
        }
    }

    // 2. 应用重启（滚动更新）
    apps := []string{"frontend", "backend", "api-gateway"}
    for _, app := range apps {
        log.Printf("🔄 重启应用: %s", app)
        
        restartedPods, err := k8s.RestartApp(ctx, app, "production", "moni")
        if err != nil {
            log.Printf("❌ 重启失败: %v", err)
            continue
        }
        
        log.Printf("✅ 重启完成，影响Pod数量: %d", len(restartedPods))
        
        // 等待Pod就绪
        time.Sleep(30 * time.Second)
        
        // 验证应用状态
        appManager := k8s.GetApplicationManager()
        status, err := appManager.GetApplicationStatus(ctx, app, "production", "moni")
        if err == nil && status.ReadyReplicas == status.Replicas {
            log.Printf("✅ 应用 %s 重启后状态正常", app)
        } else {
            log.Printf("⚠️ 应用 %s 重启后状态异常", app)
        }
    }
}
```

## 📊 管理器数据结构

### ApplicationManager 相关类型

```go
// 应用部署请求
type ApplicationRequest struct {
    AppName       string            `json:"app_name"`
    Namespace     string            `json:"namespace"`
    Env           string            `json:"env"`
    Image         string            `json:"image"`
    Replicas      int32             `json:"replicas"`
    Port          int32             `json:"port"`
    LimitsMemory  string            `json:"limits_memory"`
    LimitsCPU     string            `json:"limits_cpu"`
    RequestMemory string            `json:"request_memory"`
    RequestCPU    string            `json:"request_cpu"`
    Labels        map[string]string `json:"labels"`
    EnvVars       []corev1.EnvVar   `json:"env_vars,omitempty"`
}

// 应用状态
type ApplicationStatus struct {
    AppName           string    `json:"app_name"`
    Namespace         string    `json:"namespace"`
    Env               string    `json:"env"`
    Exists            bool      `json:"exists"`
    Replicas          int32     `json:"replicas"`
    ReadyReplicas     int32     `json:"ready_replicas"`
    AvailableReplicas int32     `json:"available_replicas"`
    Image             string    `json:"image"`
    ServiceExists     bool      `json:"service_exists"`
    CreatedAt         time.Time `json:"created_at"`
    Message           string    `json:"message"`
}
```

### PodManager 相关类型

```go
// Pod信息
type PodInfo struct {
    Name         string            `json:"name"`
    Namespace    string            `json:"namespace"`
    Status       string            `json:"status"`
    Phase        corev1.PodPhase   `json:"phase"`
    RestartCount int32             `json:"restart_count"`
    NodeName     string            `json:"node_name"`
    PodIP        string            `json:"pod_ip"`
    HostIP       string            `json:"host_ip"`
    Labels       map[string]string `json:"labels"`
    CreatedAt    time.Time         `json:"created_at"`
    StartedAt    *time.Time        `json:"started_at,omitempty"`
    Message      string            `json:"message,omitempty"`
}

// Pod日志请求
type PodLogRequest struct {
    PodName       string `json:"pod_name"`
    Namespace     string `json:"namespace"`
    Env           string `json:"env"`
    Container     string `json:"container,omitempty"`
    TailLines     *int64 `json:"tail_lines,omitempty"`
    SinceSeconds  *int64 `json:"since_seconds,omitempty"`
    Follow        bool   `json:"follow,omitempty"`
    Previous      bool   `json:"previous,omitempty"`
}
```

### ServiceManager 相关类型

```go
// Service请求
type ServiceRequest struct {
    ServiceName   string                        `json:"service_name"`
    Namespace     string                        `json:"namespace"`
    Env           string                        `json:"env"`
    Selector      map[string]string             `json:"selector"`
    Ports         []ServicePort                 `json:"ports"`
    ServiceType   corev1.ServiceType            `json:"service_type,omitempty"`
    Labels        map[string]string             `json:"labels,omitempty"`
    Annotations   map[string]string             `json:"annotations,omitempty"`
}

// Service信息
type ServiceInfo struct {
    Name         string            `json:"name"`
    Namespace    string            `json:"namespace"`
    Type         string            `json:"type"`
    ClusterIP    string            `json:"cluster_ip"`
    ExternalIPs  []string          `json:"external_ips,omitempty"`
    LoadBalancerIP string          `json:"load_balancer_ip,omitempty"`
    Ports        []ServicePortInfo `json:"ports"`
    Selector     map[string]string `json:"selector"`
    Labels       map[string]string `json:"labels"`
    CreatedAt    time.Time         `json:"created_at"`
}
```

## 🛠️ 向后兼容的API

为了保持向后兼容，原有的业务函数依然可用：

### 快速检查类
```go
// 检查环境是否可用
IsEnvAvailable(env string) bool

// 检查命名空间是否可访问
CanAccessNamespace(env, namespace string) bool
```

### 状态查询类
```go
// 获取环境详细健康状态
CheckEnvHealth(env string) *EnvStatus

// 获取所有环境状态概览
GetAllEnvsStatus() map[string]*EnvStatus

// 获取环境下的命名空间列表
GetNamespacesInEnv(env string) ([]NamespaceInfo, error)
```

### 应用管理类
```go
// 获取命名空间下的应用列表
GetAppsInNamespace(env, namespace string) ([]AppInfo, error)

// 检查特定应用状态
CheckAppStatus(env, namespace, appName string) (*AppInfo, error)
```

## ⚙️ 配置说明

Kubernetes 不再从启动 YAML 或仓库内文件读取连接配置。Ares 启动后，由管理员在“系统设置 → 系统配置”中为 `dev`、`test`、`moni` 环境录入集群名称与 kubeconfig；连接验证通过后，运行时管理器会原子切换到新客户端。

## 🛡️ 最佳实践

### 1. 使用新的管理器模式
```go
// ✅ 推荐：使用管理器模式
appManager := k8s.GetApplicationManager()
result, err := appManager.DeployApplication(ctx, appReq)

// ✅ 或使用便捷函数
result, err := k8s.DeployApp(ctx, appReq)
```

### 2. 错误处理和超时控制
```go
// ✅ 正确：带超时的上下文
ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
defer cancel()

result, err := k8s.DeployApp(ctx, appReq)
if err != nil {
    log.Printf("部署失败: %v", err)
    return
}
```

### 3. 声明式操作
```go
// ✅ 推荐：声明式部署（自动创建或更新）
result, err := appManager.DeployApplication(ctx, appReq)

// ✅ 推荐：声明式Service管理
result, err := serviceManager.CreateOrUpdateService(ctx, serviceReq)
```

### 4. 环境别名支持
```go
// 支持多种环境名称写法
k8s.IsEnvAvailable("dev")         // ✅
k8s.IsEnvAvailable("development") // ✅  
k8s.IsEnvAvailable("test")        // ✅
k8s.IsEnvAvailable("testing")     // ✅
k8s.IsEnvAvailable("moni")        // ✅
k8s.IsEnvAvailable("staging")     // ✅
```

### 5. 监控集成
```go
// 在您的监控系统中使用
func healthCheckEndpoint(w http.ResponseWriter, r *http.Request) {
    allStatus := k8s.GetAllEnvsStatus()
    
    response := map[string]interface{}{
        "timestamp": time.Now(),
        "environments": allStatus,
    }
    
    json.NewEncoder(w).Encode(response)
}
```

## 🔄 设计演进

### 之前：直接调用K8s API
```go
// ❌ 复杂：需要了解K8s API细节
client := k8s.DefaultClient(k8s.EnvDev)
deployment := &appsv1.Deployment{...}
_, err := client.AppsV1().Deployments("default").Create(ctx, deployment, metav1.CreateOptions{})
```

### 现在：管理器模式 + 业务抽象
```go
// ✅ 简单：业务导向的管理器模式
appManager := k8s.GetApplicationManager()
result, err := appManager.DeployApplication(ctx, appReq)

// ✅ 更简单：一键便捷函数
result, err := k8s.DeployApp(ctx, appReq)
```

## 📈 性能和可靠性

- ✅ **无状态设计**: 管理器不缓存状态，确保数据一致性
- ✅ **声明式操作**: 自动检查当前状态并执行最小必要变更
- ✅ **环境隔离**: 多环境客户端完全隔离，避免误操作
- ✅ **错误处理**: 统一的错误处理和日志记录
- ✅ **超时控制**: 所有操作都有合理的超时设置
- ✅ **向后兼容**: 现有代码无需修改，新功能完全兼容

---

**总结**：新的管理器模式在保持原有简单易用特性的基础上，提供了更强大、更专业的K8s操作能力。您可以根据需要选择使用高级管理器功能或简单的便捷函数，系统会根据您的业务需求提供最合适的抽象层次！
