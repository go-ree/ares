# ChaosCanvas 开发指南

## 脚本说明

### 开发相关

- `npm run dev` / `npm start` - 启动开发服务器
- `npm run type-check` - TypeScript 类型检查
- `npm run type-check:watch` - 监听模式的类型检查

### 构建相关

- `npm run build` - 生产环境构建（默认）
- `npm run build:dev` - 开发环境构建
- `npm run build:prod` - 生产环境构建（完整流程：清理+检查+构建）
- `npm run preview` - 预览构建结果
- `npm run serve` - 在生产模式下预览

### 代码质量

- `npm run lint` - 代码格式化 + ESLint 检查
- `npm run eslint` - ESLint 检查并自动修复
- `npm run eslint:check` - 仅检查 ESLint 规则，不修复
- `npm run prettier` - Prettier 格式化
- `npm run prettier:check` - 仅检查格式，不修复

### 工具相关

- `npm run clean` - 清理构建目录
- `npm run clean:deps` - 清理依赖（删除 node_modules）
- `npm run analyze` - 构建并分析包大小

## 开发工作流

### 日常开发

```bash
# 启动开发服务器
npm run dev

# 或者使用 start（等同于 dev）
npm start
```

### 提交代码前

项目已配置 Git hooks，会自动执行：

- **pre-commit**: 运行 lint-staged（代码格式化和 ESLint 检查）
- **pre-push**: 运行类型检查

手动执行：

```bash
# 代码格式化和检查
npm run lint

# 类型检查
npm run type-check
```

### 构建部署

```bash
# 开发环境构建
npm run build:dev

# 生产环境构建（推荐）
npm run build:prod

# 预览构建结果
npm run preview
```

### 包大小分析

```bash
# 构建并生成分析报告
npm run analyze
```

会在 `dist/stats.html` 生成可视化分析报告。

## 配置文件说明

### 代码质量配置

- `.eslintrc.js` - ESLint 配置
- `.prettierrc.js` - Prettier 配置
- `tsconfig.json` - TypeScript 配置

### 构建配置

- `vite.config.ts` - Vite 构建配置
- 支持环境变量配置，可创建 `.env` 系列文件

### Git Hooks

- `.husky/pre-commit` - 提交前钩子
- `.husky/pre-push` - 推送前钩子

## 环境变量

可创建以下环境变量文件：

- `.env` - 通用配置
- `.env.development` - 开发环境配置
- `.env.production` - 生产环境配置

支持的环境变量：

```bash
# 应用信息
VITE_APP_TITLE=应用标题
VITE_APP_DESCRIPTION=应用描述

# API 配置
VITE_API_BASE_URL=API地址
VITE_API_TIMEOUT=超时时间

# 构建分析
ANALYZE=true  # 启用构建分析
```

## 开发建议

1. **提交前检查**: 确保代码通过 lint 和类型检查
2. **构建验证**: 重要更改后运行生产构建验证
3. **包大小监控**: 定期运行 `npm run analyze` 检查包大小
4. **类型安全**: 启用了严格的 TypeScript 检查，建议遵循类型约束

## 故障排除

### 类型检查失败

```bash
# 查看详细错误信息
npm run type-check

# 监听模式，实时查看错误
npm run type-check:watch
```

### ESLint 错误

```bash
# 自动修复能修复的问题
npm run eslint

# 仅查看问题，不自动修复
npm run eslint:check
```

### 构建失败

```bash
# 清理后重新构建
npm run clean
npm run build
```

### 依赖问题

```bash
# 重新安装依赖
npm run clean:deps
npm install
```
