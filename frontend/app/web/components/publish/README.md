# ServiceDeploy 组件拆分方案

## 问题分析

原始的 `ServiceDeploy.vue` 文件过大（2448行），包含了太多功能，导致：
- 代码维护困难
- 组件职责不清晰
- 复用性差
- 测试困难

## 拆分方案

### 1. 组件结构

```
ServiceDeploy/
├── ServiceDeployRefactored.vue    # 主组件（重构后）
├── DeployTool.vue                 # 服务发布工具组件
├── DeployingList.vue              # 发布中服务列表组件
├── LogQuery.vue                   # 日志查询组件
├── LogDetailDialog.vue            # 日志详情对话框组件
└── README.md                      # 说明文档
```

### 2. 组合式函数（Composables）

```
composables/
├── useDeployStatus.ts             # 部署状态相关工具函数
├── useServiceManagement.ts        # 服务管理相关功能
└── useLogStreaming.ts             # 日志流式传输相关功能
```

## 组件职责划分

### ServiceDeployRefactored.vue（主组件）
- **职责**: 整体布局和组件协调
- **功能**: 
  - 标签页管理
  - 组件间通信
  - 事件处理

### DeployTool.vue（发布工具组件）
- **职责**: 服务发布的核心功能
- **功能**:
  - 环境选择
  - 服务选择表格
  - 批量操作
  - 发布状态管理

### DeployingList.vue（发布中服务列表）
- **职责**: 展示正在发布的服务
- **功能**:
  - 发布中服务列表展示
  - 状态刷新
  - 操作按钮（取消发布、查看日志）

### LogQuery.vue（日志查询组件）
- **职责**: 历史日志查询
- **功能**:
  - 日志筛选条件
  - 日志列表展示
  - 分页功能

### LogDetailDialog.vue（日志详情对话框）
- **职责**: 日志详情展示
- **功能**:
  - CI/CD日志流式传输
  - 日志内容展示
  - 滚动控制

## 组合式函数说明

### useDeployStatus.ts
提供部署状态相关的工具函数：
- `getEnvType()` - 获取环境标签类型
- `getEnvLabel()` - 获取环境显示名称
- `getStatusType()` - 获取状态标签类型
- `getProgressStatus()` - 获取进度条状态
- `getDeployStatus()` - 获取部署状态显示文本
- `calculateProgress()` - 根据状态计算进度
- `formatDateTime()` - 格式化日期时间
- `isServiceProcessing()` - 判断服务是否正在处理中

### useServiceManagement.ts
提供服务管理相关功能：
- `availableServices` - 可用服务列表
- `selectedServices` - 选中的服务列表
- `fetchAvailableServices()` - 获取可用服务列表
- `updateSelectedServiceStatus()` - 更新选中服务状态
- `refreshSelectedServicesStatus()` - 刷新所有选中服务状态

### useLogStreaming.ts
提供日志流式传输相关功能：
- `ciLog`, `cdLog` - 日志内容
- `ciLogLoading`, `cdLogLoading` - 加载状态
- `fetchLogs()` - 获取日志
- `fetchCiLogsStream()` - 获取CI日志流
- `fetchCdLogsStream()` - 获取CD日志流
- `cleanupEventSources()` - 清理SSE连接
- `manualScrollToBottom()` - 手动滚动到底部

## 优势

### 1. 代码组织更清晰
- 每个组件职责单一
- 代码结构更易理解
- 维护成本降低

### 2. 复用性提升
- 组合式函数可在其他组件中复用
- 子组件可独立使用
- 便于单元测试

### 3. 性能优化
- 组件按需加载
- 状态管理更精确
- 减少不必要的重渲染

### 4. 开发效率
- 多人协作更容易
- 代码审查更简单
- 问题定位更准确

## 使用方式

### 1. 替换原组件
将原来的 `ServiceDeploy.vue` 替换为 `ServiceDeployRefactored.vue`

### 2. 路由配置
```typescript
// 在路由配置中更新组件引用
{
  path: '/publish/deploy',
  name: 'ServiceDeploy',
  component: () => import('@/components/publish/ServiceDeployRefactored.vue')
}
```

### 3. 独立使用子组件
```vue
<template>
  <!-- 只使用发布工具 -->
  <DeployTool />
  
  <!-- 只使用日志查询 -->
  <LogQuery @view-log-detail="handleViewLogDetail" />
</template>
```

## 注意事项

1. **组合式函数依赖**: 确保所有组合式函数都已正确创建
2. **类型定义**: 保持接口类型的一致性
3. **事件通信**: 使用事件总线或props进行组件间通信
4. **状态管理**: 考虑使用Pinia进行全局状态管理

## 后续优化建议

1. **状态管理**: 将共享状态迁移到Pinia store
2. **错误处理**: 统一错误处理机制
3. **加载状态**: 优化加载状态的用户体验
4. **缓存策略**: 实现数据缓存机制
5. **单元测试**: 为每个组件和组合式函数编写测试 