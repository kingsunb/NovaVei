# web-next · NovaVei 控制台

> NovaVei 控制台前端（v0.2.0+）。
> 设计依据见 [DESIGN.md](./DESIGN.md)。

## 状态

| 阶段 | 状态 | 说明 |
|---|---|---|
| **v0.2.x 视觉重构 | ✅ | macOS 磨砂玻璃简白风（SF 字体栈 / 玻璃面 / Apple 阴影三层级） |

## 快速开始

```bash
cd web-next
pnpm install
pnpm dev          # http://localhost:5174
pnpm build        # 生产构建
pnpm preview      # 预览构建产物
```

开发态把 `/api/*` 代理到 `http://127.0.0.1:8080`（见 `vite.config.ts`），需后端同机启动。

## 常用命令

```bash
pnpm typecheck     # TypeScript 编译检查
pnpm lint          # ESLint flat config（含 react-hooks/exhaustive-deps）
pnpm test          # vitest 单测
pnpm test:cov      # 同上 + 覆盖率（lib/ui/charts：lines 88 / functions 80 / branches 85 / statements 88）
pnpm test:watch    # 监听模式开发
pnpm exec playwright test   # Playwright E2E + a11y
pnpm size          # size-limit bundle 预算检查
```

## Pre-commit Hook（可选）

仓库根 `lefthook.yml` 配置了 pre-commit 钩子（提交前跑 typecheck + lint）。

启用方式（开发者本地一次）：

```bash
pnpm dlx lefthook install
```

启用后 `git commit` 会在提交前自动跑 `pnpm typecheck` + `pnpm lint`（仅 web-next/src 下变更的文件）。
未启用不影响提交；CI 仍跑完整套验证。

## 部署

**推荐方式：Go 内嵌前端。** 仓库根 `scripts/build.sh` 会 `pnpm build` 产出到 `../static/out`，
由 `static/static.go` 的 `//go:embed all:out` 打包进后端二进制。生产只需部署单个
`novavei` 容器（见 `../docker-compose.yml`），控制台由后端直接提供，无需 nginx 或 web-next 镜像。

**可选：独立前端部署（灰度 / A-B 路由）。** 用 `docker compose --profile web-next up` 启动
`web-next` + `web-router` 两个额外容器。需先构建并指定 `NOVAVEI_WEB_NEXT_IMAGE`：

```bash
docker build -t novavei-web-next:<ver> web-next/
NOVAVEI_WEB_NEXT_IMAGE=novavei-web-next:<ver> \
NOVAVEI_IMAGE=ghcr.io/kingsunb/novavei-api:<ver> \
docker compose --profile web-next up -d
```

> ⚠️ 独立部署是高级可选路径；默认场景请用 Go 内嵌单容器。

## 技术栈

- **Vite 5** + **React 18** + **TypeScript 5**
- **Tailwind 3** + shadcn/ui 风格原语（自维护 `components/ui/`）
- **Radix UI**（Dialog / Slot / Switch）
- **react-router-dom 6** 路由（页面级 lazy chunk）
- **@tanstack/react-query 5** 数据获取
- **@tanstack/react-virtual 3** 虚拟化（日志实时表）
- **sonner** Toast · **lucide-react** 图标

设计令牌：见 [src/index.css](./src/index.css) 的 `:root[data-theme]` 段；HSL 变量，亮暗主题切换零样式覆盖。

## 目录结构

```
web-next/
├── DESIGN.md                # 设计稿（调研 / IA / 令牌 / 线框）
├── Dockerfile               # 多阶段构建：node:22 → nginx:1.27
├── nginx.conf               # web-next 静态服务 + /api 反代
├── router.nginx.conf        # A/B 路由网关（仓库根 docker-compose 引用）
├── __flags.runtime.json.example  # 运维挂载的 flags 模板
├── playwright.config.ts      # E2E 配置
├── .size-limit.json          # bundle 预算
├── e2e/                     # Playwright E2E + a11y
└── src/
    ├── main.tsx
    ├── App.tsx              # 路由表 + 灰度判定 + 401/403 全局事件监听
    ├── index.css            # 设计令牌 + 玻璃工具类 + Tailwind 三层
    ├── lib/
    │   ├── api.ts           # /api/v1/* 封装（信封拆包 / APIError / 归一化）
    │   ├── types.ts         # 与后端 json tag 一一对应
    │   ├── flags.ts         # 运行时灰度开关（核心开关）
    │   ├── sse.ts           # EventSource 包装（指数退避重连）
    │   └── utils.ts         # cn / format / debounce / validateField
    ├── store/
    │   └── auth.tsx         # 认证上下文（JWT cookie 探活）
    ├── components/
    │   ├── ui/              # 原语：button / card / input / pill / switch / dialog / skeleton ...
    │   ├── layout/          # AppShell / Sidebar / Topbar / CommandPalette / ErrorBoundary / ThemeProvider
    │   └── charts/          # TokenTrendChart（SVG，数据为示意占位）
    └── pages/               # 6 个核心页
        ├── Login / Dashboard / Channels / Groups / Keys / Logs / Settings
        └── channels/        # channel-editor（渠道编辑 Sheet）
```

## 设计系统（v0.2.x · macOS 磨砂玻璃简白风）

- **色彩**：Light `#F5F5F7` 冷白 + Apple 蓝 `#0071E3`；Dark `#0E0E10` + 亮化蓝
- **字体**：SF Pro 字体栈（`-apple-system` 优先，Inter 兜底）+ SF Mono（等宽/数字）
- **圆角**：卡片 12px / 控件 8px / pill 全圆
- **材质**：`.glass-panel/.glass-sidebar/.glass-topbar/.glass-overlay` 磨砂玻璃 + 内高光
- **阴影**：`shadow-apple-sm/md/lg` 三层级
- **状态色**：enabled 绿 / disabled 锌 / cooling 琥珀 / affinity 青 / failed 红

## Mock 与真实 API

页面全部走 `useQuery + api.ts` 请求真实后端；后端未启动时页面显示错误/空态。
唯一例外：Dashboard 的 Token 趋势图为确定性示意曲线（后端暂无时间分桶接口）。

## 兼容与约束

- 浏览器：Chrome ≥ 100 / Edge ≥ 100 / Safari ≥ 16 / Firefox ≥ 100
- 主题切换无过渡动画防止闪烁
- 全部交互尊重 `prefers-reduced-motion`
- 键盘可达：Tab 顺序合理 / `⌘K` 命令面板 / `Esc` 关闭覆盖层

## 文档

| 文档 | 用途 |
|---|---|
| [DESIGN.md](./DESIGN.md) | 设计调研 / IA / 设计令牌 / 线框 |
