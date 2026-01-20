# Jenkins 构建指南

## 项目结构说明

本项目已经调整为与Jenkins构建流程兼容的结构：

### 关键文件

- `package.json` - 包含 `start:dev` 和 `start:prod` 脚本
- `app.js` - 应用入口文件，用于Docker容器启动
- `.npmrc` - npm配置文件
- `config/vite.config.ts` - Vite配置文件，包含健康检测接口 ✅ **已移动到config目录**
- `index.html` - 应用入口HTML文件

### 目录结构

```
chaoscanvas/
├── app/                    # 应用代码目录
│   ├── web/               # 前端代码
│   ├── controller/        # 控制器
│   ├── service/           # 服务层
│   └── middleware/        # 中间件
├── config/                # 配置文件目录
│   ├── vite.config.ts     # Vite配置 ✅ **已移动到这里**
│   ├── dev.ts             # 开发环境配置
│   ├── prod.ts            # 生产环境配置
│   └── index.ts           # 配置入口
├── dist/                  # 构建输出目录
├── public/                # 静态资源目录
├── src/                   # 源代码目录
├── package.json           # 项目配置
├── app.js                 # 应用入口文件
├── .npmrc                 # npm配置
└── index.html             # 应用入口HTML
```

## Jenkins构建流程兼容性

### 1. 构建阶段

- **打包编译解压**: 执行 `npm run build` 生成dist目录
- **文件复制**: 将关键文件和目录复制到code目录
- **生产包构建**: 在code目录执行 `npm install --production`

### 2. 关键脚本

- `start:dev`: 开发环境启动命令，监听8080端口
- `start:prod`: 生产环境启动命令，使用vite preview
- `build`: 生产环境构建命令

### 3. 健康检测接口

- 路径: `/ttpai/inside/checkup`
- 返回: JSON格式的健康状态信息
- 支持: CORS跨域访问

### 4. Docker容器启动

- 基础镜像: `harbor.ttpai.work/publish-system/ttpai_nodejs_basev2`
- 工作目录: `/opt/htdocs/chaoscanvas`
- 启动命令: `npm run start:${env}`

## 构建参数

Jenkins构建时需要提供以下参数：

- `app_name`: 应用名称 (chaoscanvas)
- `branch`: 发布分支
- `env`: 环境 (dev/prod)
- `git_url`: Git仓库地址
- `image`: 镜像名称

## ✅ 问题已解决

### 健康检测接口404错误 - 已修复

**解决方案**: 将 `vite.config.ts` 文件移动到 `config/` 目录下，这样Jenkins构建流程会自动包含该文件。

**变更内容**:

1. `vite.config.ts` → `config/vite.config.ts`
2. 更新了package.json中的所有脚本，使用 `--config config/vite.config.ts`
3. 调整了vite.config.ts中的路径引用（`__dirname` 相对路径）
4. 将必要的依赖（vite、@vitejs/plugin-vue、terser、sass）移到dependencies

**优势**:

- ✅ Jenkins构建流程无需修改
- ✅ 健康检测接口正常工作
- ✅ 配置文件集中管理
- ✅ 符合项目结构规范
- ✅ 生产环境依赖完整

## 注意事项

1. **端口配置**: 应用固定监听8080端口
2. **网络访问**: 配置了 `--host 0.0.0.0` 支持网络访问
3. **健康检测**: 内置健康检测接口用于K8s健康检查
4. **环境变量**: 支持通过NODE_ENV设置环境
5. **文件复制**: Jenkins会复制dist、config、app、public等关键目录
6. **✅ vite.config.ts**: 现在位于config目录，会被自动包含
7. **✅ 依赖完整**: 所有生产环境需要的依赖都在dependencies中

## 本地测试

```bash
# 开发环境
npm run start:dev

# 生产环境构建
npm run build

# 生产环境预览
npm run start:prod

# 健康检测测试
curl http://localhost:8080/ttpai/inside/checkup

# 简化版启动（无健康检测）
npm run start:dev-simple
npm run start:prod-simple
```

## 故障排查

### 健康检测接口404

1. ✅ 确认 `config/vite.config.ts` 文件存在
2. ✅ 确认Jenkins构建流程包含了config目录
3. 查看Vite启动日志是否有错误信息
4. 使用简化版启动脚本作为临时解决方案
