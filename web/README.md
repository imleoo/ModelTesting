# 模型自测台 · Web 控制台

`api-server`（Go，`internal/api`）的前端，Next.js 15 App Router + Tailwind CSS。页面范围与路由映射见 [`docs/模型自测台设计方案.md`](../docs/模型自测台设计方案.md) 第 10.3 节。

## 页面

| 路由 | 用途 |
|---|---|
| `/admin/providers` | 供应商 / 模型登记（CAPABILITY_PROFILE 录入） |
| `/admin/suites` | 套件管理：列出 / 创建 / 克隆 |
| `/admin/test-runs/new`、`/admin/test-runs/[id]` | 发起测试任务、查看结果详情 |
| `/admin/compare` | 多个已完成 TestRun 的功能结果并排对比 |
| `/admin/reports/[runId]` | HTML 报告展示，浏览器打印导出 |

## 本地开发

```bash
npm install
cp .env.example .env.local   # NEXT_PUBLIC_API_BASE_URL，默认 http://localhost:8090
npm run dev                  # http://localhost:3000
```

```bash
npm run lint
npm run typecheck
npm run build
```

Docker 单容器部署（前后端同源，`/api/*` 由 `next.config.js` 的 rewrites 转发到容器内 api-server）见仓库根目录 [`README.md`](../README.md)。

## 目录

```
src/app/            路由与布局（App Router）
src/components/     card / footer / link / navbar / sidebar
src/routes.tsx      侧边栏导航项
src/utils/          apiClient（后端接口封装）、navigation
src/fonts/          DM Sans（next/font/local 自托管，OFL 许可）
src/styles/         全局样式（Tailwind 入口）
```

## 致谢

页面骨架与 Tailwind 主题色板派生自 [Horizon UI](https://github.com/horizon-ui)（MIT），原许可证见 [`LICENSE-horizon-ui.txt`](./LICENSE-horizon-ui.txt)。模板自带的示例页、组件与素材已移除。
