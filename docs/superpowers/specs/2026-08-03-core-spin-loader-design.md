# CoreSpinLoader 页面级加载设计

**状态：** 已批准，等待实施计划
**日期：** 2026-08-03
**范围：** Web 端页面级数据加载状态

## 1. 目标

将项目中首次加载页面数据时的 Skeleton、Ant Design Spin 和纯文本等待提示统一为 AIOps 平台的 CoreSpinLoader。统一后的加载状态使用多层环形动画和动态运维文案，保持页面品牌一致，同时不改变错误提示和操作级按钮 loading。

## 2. 当前技术背景

Web 项目位于 `web/`，已经使用 React、TypeScript、Tailwind CSS v4 和 Ant Design，但没有标准 shadcn/ui 目录和依赖。项目不需要引入 shadcn CLI 或新的动画依赖；可复用 UI 组件统一放在 `web/src/components/ui/`，由现有 Tailwind 配置提供样式。

## 3. 组件设计

新建 `web/src/components/ui/core-spin-loader.tsx`，实现以下结构：

- 外层居中容器，默认最小高度 220px，支持 `className` 和 `minHeight` 覆盖。
- 20px 级别的环形核心区域：基础光晕、外层虚线环、主弧、反向弧、内层快速环、轨道圆点和中心核心。
- 动画使用 CSS `animate-pulse` 和 Tailwind arbitrary animation，分别控制 10 秒外环、2 秒主弧、3 秒反向弧、1 秒内环和 4 秒轨道点。
- 颜色沿用提供代码的 emerald/green 语义，保留 dark 模式下的 cyan/purple 备用色。
- 文案按以下序列每秒循环：`Initializing`、`Loading...`、`Fetching Data..`、`Syncing...`、`Processing..`、`Optimizing...`。
- 根元素使用 `role="status"` 与 `aria-live="polite"`；动画装饰元素全部 `aria-hidden`，动态文案作为可读状态输出。

组件接口：

```tsx
type CoreSpinLoaderProps = {
  className?: string;
  minHeight?: string;
};
```

默认状态直接渲染动态文案，不提供静态 label，避免页面之间出现第二套文案体系。

## 4. 页面集成范围

### 4.1 共享容器

- `web/src/components/resource-list/ResourceListPage.tsx`：首次列表数据加载时，在内容卡片中渲染 CoreSpinLoader；保留列表标题、搜索框和刷新入口。
- 资源详情页共用的加载分支：统一替换“正在加载 X 详情...”纯文本，保留页面返回路径和错误 Alert。

### 4.2 独立页面

替换以下页面级等待视图：

- `OverviewPage.tsx`
- `TopologyPage.tsx`
- `EvidenceGraphPage.tsx`
- `SystemUpdatesPage.tsx`
- `AIOpsApprovalsPage.tsx`
- `AIOpsExperimentsPage.tsx`
- `AIOpsOverviewPage.tsx`
- `IncidentsPage.tsx` 及 AIOps IncidentTable 首次加载分支

所有其它资源列表页和详情页沿用共享容器或同一加载分支规则，不单独设计新的动画。

## 5. 明确不改动的 loading

以下状态继续使用现有控件，避免把短时操作反馈变成大面积页面阻塞：

- 登录、保存、删除、重启、扩缩容、审批、拒绝等按钮的 `loading` / `confirmLoading`。
- YAML 编辑器的读取、保存和刷新按钮 loading。
- 版本徽章刷新图标和 Agent 时间线中的小型处理标记。
- 页面已经显示有效数据时的后台 refetch，不清空旧内容，也不替换成全屏加载。

## 6. 错误与空状态

- 查询错误优先显示原有 Alert 或错误面板，不能被 CoreSpinLoader 覆盖。
- 查询成功但无数据时继续显示现有 Empty 组件。
- 演示模式不触发页面级加载动画，继续显示演示数据或原有说明。
- 组件卸载时清理动态文案定时器，避免测试和页面切换产生泄漏。

## 7. 测试与验收

1. 新增 `CoreSpinLoader.test.tsx`，验证可访问角色、默认文案、装饰节点隐藏和 `minHeight` 样式。
2. 现有页面测试继续通过；对共享资源列表增加 loading 状态渲染断言。
3. `npm test` 全量通过。
4. `npm run build` 通过。
5. 浏览器目视检查概览、资源列表、资源详情、拓扑和证据图：页面结构不跳动，动画居中，标题/返回路径保留，错误和空状态不受影响。
