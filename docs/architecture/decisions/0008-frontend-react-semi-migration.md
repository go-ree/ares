# ADR-0008：前端改用 React + Semi Design

## 1. 状态与决策背景

2026-09-16 提出，**待评审**（本 ADR 合并前不构成已生效决策）。迁移方向由维护者确定：前端后续使用 Semi Design 开发。本文固定路径、批次与门禁；具体迁移实现按批次分别提交中文 PR。

同日维护者进一步决定：**本迁移为当前最高优先级，全速推进；W11-B3 独立运行上下文与 W11-C 产物能力暂缓**，待迁移完成后按原验收门禁恢复。顺序调整已先写入[总进度看板](../../plans/open-source-production-roadmap.md)与[W11 实施计划](../../plans/ci-artifact-cd-roadmap.md)。

触发原因是一个无法回避的约束：**Semi Design 没有官方 Vue 版本**。官方组件库只有 React 版 `@douyinfe/semi-ui`（React 19 用 `@douyinfe/semi-ui-19`），官方 FAQ 明确"暂无计划"提供其它技术栈，生态页也未收录 Vue 方案。因此"改用 Semi"等价于"前端换框架"，不是一个纯组件库替换。

当前前端（`frontend/`）的事实，用于评估工作量：

- 技术栈 Vue 3.5 + Element Plus 2.14 + Pinia 3 + vue-router 4 + axios + Vite 8 + TypeScript 5.7。
- 代码位于 `frontend/app/web/`；`frontend/src/` 只是遗留脚手架（仅 `App.vue`、`HelloWorld.vue`），不承载业务。
- 规模 31 个 `.vue`、46 个 `.ts`、约 16.1k 行；**40 种** `el-*` 标签、**579 处**标签使用；`v-loading` 用在生产代码中。
- 21 个 spec，其中**仅 6 个**依赖 `@vue/test-utils`（MainLayout、LogDetail、useFrozenReleaseNavigationGuard、AppInfo、Login、Users）。
- Vue API 面很窄：只用 `ref/computed/reactive/shallowRef/watch/nextTick` 与生命周期钩子；应用代码没有 `provide/inject`，没有自定义指令。
- 单一入口 `frontend/index.html` → `/app/web/main.ts`，挂载 `#app`；`main.ts` 里安装 Pinia、Element Plus（`zh_CN` locale），并用 `configureApiAuth` + `watch(authStore.status)` 实现 401 全局跳转。CI 前端门禁是 `make frontend-check`。

## 2. 采用的方案

1. **框架与组件库**：React 19 + `@douyinfe/semi-ui-19`（该包 peer 为 `react ^19.0.0`；`@douyinfe/semi-ui` 面向 React <19）+ `@douyinfe/semi-icons`，主题与暗黑沿用 Semi 的 Design Token 与 `theme-mode`（`document.body.setAttribute('theme-mode','dark')`）。
2. **不做运行时双框架共存**。新栈在 `frontend/app/web-react/` 平行开发，验收时用第二入口和 `/next` 前缀访问，对外不可见；全部页面完成后**只改 `frontend/index.html` 的一行入口**完成切换，Dockerfile 与 nginx 不动。理由是三条硬约束：认证是模块单例（`configureApiAuth` + `watch(status)` 的全局 401 跳转）、页面直接依赖 Pinia 与 `onBeforeRouteLeave` 的冻结发布守卫、nginx 是单 root + SPA fallback；跨 SPA 边界会丢内存态（发布页的 SSE 与冻结提交当场失效），要保住就得维护两套等价逻辑，代价高于一次性切换。
3. **状态与数据层**：认证用 Zustand（vanilla store + `useStore`，可在 React 之外读写，与 Pinia 能力对齐），`stores/auth.ts` 的会话世代号、过期定时器、401/403 分支机械平移。`services/`、`config/api.ts`、`utils/`、`models/`、`types/` 是纯 TS，**零改动复用**（含 CSRF 拦截器）。composables 先做无框架抽取（`boundLogText`/`log-stream` 分帧传输/`useReleaseComposer` 状态机/`calculateTaskProgress` 与其 spec 原样保留），再补 React 外壳；`useFrozenReleaseNavigationGuard` 改为 `useBlocker`。不引入 Redux Toolkit；TanStack Query 只用于 W11-D 的新列表页。
4. **路由**：react-router **v7**（`7.18.4`，`createBrowserRouter` data router；v8 在提出时仅有 9 个发布，作为基础设施先钉成熟大版本），把 `router.beforeEach` 拆成 loader——根 loader 做 `ensureSession` + `requiresAuth`，子 loader 做 `requiredPermissions` 并跳 `/forbidden`。`normalizeReturnTo` 的防开放重定向逻辑与 `publicOnly` 行为原样保留。
5. **测试**：21 个 spec 中 15 个（services/config/utils/纯逻辑）直接复用，6 个重写。React 侧用 jsdom + `@testing-library/react` + `user-event` + `jest-dom`（不用 happy-dom），通信层继续用 `axios-mock-adapter`；`Toast/Modal` 命令式 API 以 mock 断言，不断言 DOM。setup 保留 ResizeObserver/matchMedia stub。
6. **工程与 CI**：`@vitejs/plugin-react` 替换 `@vitejs/plugin-vue`，`manualChunks` 改 semi/react 分组，`vue-tsc` → `tsc --noEmit`；ESLint 去 `eslint-plugin-vue`、加 `react-hooks`/`react-refresh`。过渡期用 vitest projects 分 vue/react 两个 project，新增 `frontend-check-react`，`frontend-check` 保留到最后一批才删。

### 2.1 批次与门禁

| 批次 | 内容 | 门禁 |
| --- | --- | --- |
| B0 骨架 | React 入口、Semi + `zh_CN`、Zustand auth、loader 守卫、NotFound/Forbidden、共享层 `git mv`、双 vitest project | `frontend-check-react` 绿、旧栈不退化、returnTo/401/403 用例 1:1 通过 |
| B1 外壳 | Login、MainLayout（权限菜单）、Home | 权限菜单与守卫行为对拍 |
| B2 只读页 | Version、Monitor、AppList（含 AppTable/AppAdvancedSearch/AppDetailDialog）、AppInfo | 同上 |
| B3 配置重页 | AppConfigDetail、AppDomains、Settings、Users、AppApply | 同上 |
| B4 发布链路（最难，最后做） | useLog/log-stream 适配、LogQuery/LogDetail/DeployTool/DeployingList/ServiceDeploy → Deploy/Merge/Log/AppDetail/AppPods | 同上 |
| B5 切换 | 改 `index.html` 入口、删 Vue 依赖与 `frontend/src/` 遗留脚手架、并回单 vitest project、下线 `frontend-check` | 生产入口切换后全量回归 |

每批通用门禁：新栈检查全绿；spec 数量不减（被重写的 6 个文件必须补齐等价行为用例）；该批路由的 URL/权限/请求序列与旧栈一致；gzip 产物劣化不超过 10%。迁移窗口内旧栈只接受 bugfix，且当天同步到新栈；每批"迁完即删"对应 `.vue` 与 Element Plus 引用，不留半迁移悬空。

### 2.2 与 W11-D 的顺序

**W11-D 的新页面直接在 React + Semi 上写，不等迁移完成。** 其 `services/`、`types/`、`models/` 是纯 TS，B0 之前就可以写；组件在 B0/B1 之后写，避免新页面按旧壳结构落地再返工。这与[W11 实施计划](../../plans/ci-artifact-cd-roadmap.md)的 D 阶段范围不冲突：D 的范围仍是"新模型页面与联调"，本 ADR 只决定它构建在哪套栈上。

### 2.3 B0 落地时实测到的集成约束

以下为 2026-09-16 在 B0 骨架中实测确认的事实（非推断），后续批次必须遵守：

1. **React 19 必须引入适配器**：使用任何 Semi 组件前要先 `import '@douyinfe/semi-ui-19/react19-adapter'`，它把 `createRoot` 注入 Semi 全局配置，否则 Toast/Modal 等命令式弹层在 React 19 下无法挂载。React 版本对应关系：React 19 用 `@douyinfe/semi-ui-19`（peer `react ^19.0.0`），React <19 用 `@douyinfe/semi-ui`。
2. **官方文档的样式路径不可用**：`@douyinfe/semi-ui-19/dist/css/semi.min.css` 无法解析——该包的 `exports` 只暴露 `lib/**`，没有 `./dist/*`（普通包 `@douyinfe/semi-ui` 同样如此），Vite 8 会直接构建失败。因此 React 侧配置把该 specifier 别名到真实文件，而不是用脆弱的相对路径进 `node_modules`。
3. **locale 路径**是 `@douyinfe/semi-ui-19/lib/es/locale/source/zh_CN`。
4. **不能继承 Vue 的 tsconfig 基类**：`@vue/tsconfig` 设置了 `jsxImportSource: "vue"`，继承它会让每个 `.tsx` 编译成 Vue vnode，React 渲染时报 `Objects are not valid as a React child`。React 栈使用自包含 tsconfig（`app/web-react/tsconfig.json`），不 extends Vue 预设；两条栈的 type-check 范围互相排除。
5. **Semi 入口会拉入 `lottie-web`**：它在模块作用域构造 2D canvas 上下文（jsdom 需要桩，否则任何引入 Semi 组件的测试都在加载阶段失败），并使用直接 `eval`。当前 CSP 只有 `frame-ancestors 'none'`，不冲突；但后续若收紧 `script-src` 且不含 `unsafe-eval`，需要改为按组件深路径引入或排除插画组件。
6. **`redirect()` 没有 `replace` 选项**（签名是 `(url, init?: number | ResponseInit)`）：data router 的 loader 重定向本身就替换历史记录，等价于 Vue 守卫的 `replace: true`。
7. **实测规模与复用比例**：共享层搬迁（`services/config/utils/models/types` → `app/shared/`）后，Vue 栈仍是 21 个 spec / 198 个测试全绿；21 个 spec 中确实只有 6 个依赖 `@vue/test-utils`，与 ADR 的拆分一致。React 项目新增 17 个测试（认证 store 7 + 路由守卫 10）。
8. **产物体积**：React 栈首次构建为 semi 167 kB（gzip 48 kB）+ 样式 677 kB（gzip 77 kB）+ react 312 kB（gzip 98 kB）。样式体积主要来自整包引入，B1 应评估按组件引入。

## 3. 不采用的方案

- **社区 Vue 3 移植版 `@kousum/semi-ui-vue`**：改动最小，但单人维护、2025-04 后停更、比官方落后约 25 个 minor、无 Vue 文档站；把长期 UI 基建押在停更的移植版上，风险高于换框架。另一候选 `semi-design-vue3` 首发版本且无任何采用证据，不用于生产。
- **运行时双框架共存（iframe／微前端挂载两个 SPA）**：会话单例、冻结发布守卫与 nginx 单 root 三处都要额外改造，且整页刷新会打断发布链路的内存态；收益不匹配。
- **Web Components／只借鉴 Semi 设计规范自研组件**：官方只提到"与 Web Components 兼容的适配方案"，没有提供组件包；自研等于放弃 Semi 的组件与主题收益。
- **先在 Element Plus 上做完 W11-D 再迁移**：D 的新页面会白做一遍，且新页面会绑定旧壳结构（Pinia/指令/插槽式表格），迁移成本更高。
- **MobX/Redux Toolkit 全量替换状态层**：现有状态面很小（一个认证 store + 少量 composable），全量引入是过度设计。

## 4. 继承及替代范围

本 ADR 只决定前端框架与组件库，不改变产品模型。ADR-0002 的 RBAC 与可信主体、ADR-0003 的日志契约、ADR-0007 的应用类型/CI/CD 解耦模型继续有效；前端仍按既有权限串（`applications:read` 等）与 API 契约工作，`executable=false` 等"未实现能力"的呈现要求不变。

[W11 实施计划](../../plans/ci-artifact-cd-roadmap.md) 的 D 阶段范围不变，仅其实现栈由本 ADR 指定；W11-D 仍需连同权限、空状态、版本选择与来源链的联调一起验收。

## 5. 代价与边界

- 需要重写约 16.1k 行前端代码与 6 个组件级 spec，工作量以月为单位；迁移期间前端功能冻结（只接受 bugfix），这是明确接受的成本。
- 本 ADR 合并**不代表**迁移已开始或已完成；B0 之前不删除任何 Vue 依赖、不改生产入口、不宣称"已使用 Semi"。
- 不做同页面双运行时；过渡期的"共存"仅指构建期双入口与 `/next` 前缀验收，不是对外双栈。
- 品牌色与主题按 Semi Design Token 承载，不复制 Element Plus 的样式覆盖方式；暗黑模式与多语言随 Semi 机制实现。
- 组件映射的差异点（`v-loading` 无指令需改 `Spin` 包裹、`ElMessageBox.confirm` → `Modal.confirm`、`el-table-column` 插槽式列定义改 `columns` 数组 + `render`、`v-model` 全面改受控）在 B0/B1 落地时逐项验证，本 ADR 不把它们当作已验证结论。
