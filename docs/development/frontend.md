# 前端开发指南

## 1. 目录与职责

前端位于 `frontend/`，使用 Vue 3、TypeScript、Vite、Pinia 和 Element Plus。发布相关页面主要位于：

- `frontend/app/web/components/publish/`：发布、运行列表和任务详情组件；
- `frontend/app/web/composables/`：动态环境、发布和日志状态组合逻辑；
- `frontend/app/web/views/application/detail/`：AppConfig、域名及环境专属工作流编辑；
- `frontend/app/web/views/system/Settings.vue`：动态环境与可选集成配置。

环境代码是服务端目录数据，不得在 TypeScript 类型、选项或条件分支中新增固定环境枚举。Git 分支/ref 是每次发布的独立输入，不得根据环境名称自动改写。

## 2. 本地运行

```bash
cd frontend
npm ci
npm run dev
```

开发服务默认监听 `0.0.0.0:8080`。完整 Compose 环境和反向代理方式见 [部署指南](../operations/deployment.md)。

## 3. 提交前验证

```bash
npm run type-check
npm run build
```

`npm run lint` 会执行带自动修复的格式化与 ESLint；准备提交时应检查 diff，避免格式化无关文件。当前仓库没有有效的前端单元测试套件，涉及交互的改动还需在 Compose 页面中人工验收。

## 4. 发布界面约束

- 发布环境来自 `GET /api/v1/environments`；选择器只展示启用项，历史详情可展示停用或未知环境。
- AppConfig 与工作流按环境独立维护，步骤可新增、删除和排序。
- 工作流读取和保存需要系统管理员令牌；令牌只保存在当前页面内存中。
- 任务详情优先展示通用步骤时间线；旧 Jenkins CI/CD 标签只在兼容字段存在时显示。
- 前端不得伪造取消、重试或“仅重发”等后端尚未实现的动作。
