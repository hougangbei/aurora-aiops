# Aurora AIOps 神经访问登录页设计

日期：2026-08-03
状态：设计已确认，待实现计划

## 背景

当前登录页使用 Ant Design 卡片和左右分栏，功能可用但视觉上偏普通后台表单。目标是在不改变认证行为的前提下，参考 [Neural Access Login](https://21st.dev/community/components/shivendra9795kumar/neural-access-login) 的单色科幻访问界面，重塑 Aurora AIOps 的登录入口。

## 设计方向

最终采用 A 方向：

- 配色：`Polar Signal` 冷蓝紫。背景使用深石墨黑，冷蓝紫作为唯一强调色，应用于光效、焦点态、边框和主按钮。
- 结构：单列聚焦式登录面板，保留 Aurora A 标志、AIOps 平台品牌和双语访问文案。
- 氛围：深色神经场、缓慢漂移的雾团、扫描纹理和鼠标跟随的局部光源。光效只服务于层次，不遮挡文字和控件。
- 输入框：`Signal Capsule`。用户名和密码各自是圆角胶囊节点，左侧显示 `ID` / `KEY` 标记，内部包含上方标签和输入值；聚焦时出现冷蓝描边、半透明底色和轻微上浮。
- 主按钮：默认圆角矩形，悬停/聚焦时平滑过渡到全圆胶囊；文字字号略放大、字距拉开，增加亮带扫过、下方光晕和轻微上浮。按钮标签为 `INITIALIZE SESSION`。

## 页面结构

```text
登录页
└── 神经场背景（固定层）
    ├── Aurora 光源（指针跟随）
    ├── 缓慢漂移雾团
    └── 低对比扫描纹理
└── 主登录面板
    ├── 节点标识：SYSTEM NODE: AURORA-CORE
    ├── Aurora A 标志
    ├── AURORA ACCESS 标题
    ├── 一句中文说明
    ├── Signal Capsule：用户身份
    ├── Signal Capsule：序列密钥
    ├── INITIALIZE SESSION 主按钮
    ├── 进入演示模式次级动作
    └── 加密通道 / 版本信息
```

## 行为与状态

- 账号密码登录仍调用现有 `loginWithPassword`，成功后清理 query cache、写入 session 并跳转 `/cluster/overview`。
- 登录中继续使用现有 mutation loading 状态，按钮禁用并显示 Ant Design 的操作反馈；不把操作级 loading 替换成页面级 loader。
- 登录错误继续使用现有 `AxiosError` 信息映射和错误 Alert，错误放在表单上方，不改变错误文案来源。
- 演示模式仍只更新本地 app store，不调用登录接口，跳转行为保持不变。
- 鼠标移动仅更新背景光源位置；指针离开登录面板后光源缓慢回到默认中心。触摸设备不显示跟随光源。
- `prefers-reduced-motion: reduce` 下关闭雾团漂移、光源过渡、亮带扫过和按钮上浮，只保留颜色/边框状态变化。

## 响应式与可访问性

- 桌面端主面板宽度约 440–520px，登录内容在首屏内完成；移动端改为单列全宽、收窄内边距。
- 用户名和密码使用真实 `<label>` 与 `htmlFor` 关联，保留 `autocomplete="username"` 和 `autocomplete="current-password"`。
- 输入焦点必须具备清晰的冷蓝边框和光晕，同时满足键盘可见焦点；按钮文本与背景保持 WCAG AA 对比度。
- 光效层使用 `pointer-events: none`，不影响表单点击；纯装饰层标记为 `aria-hidden`。
- 不能只依赖颜色表达错误或焦点状态，错误文本和边框均有文字/结构信号。

## 实现边界

需要修改：

- `web/src/pages/LoginPage.tsx`：登录页面结构和样式。
- `web/src/pages/LoginPage.test.tsx`：补充可访问标签、Signal Capsule 和保持行为的断言。
- `web/src/styles/index.css` 或登录页局部样式：背景光效、扫描纹理、减少动态效果规则。

不在本次范围：

- 登录 API、session cookie、路由和 Zustand 状态模型。
- 已完成的页面级 `CoreSpinLoader`。
- 登录后的 Overview 首页内容重做。
- 引入新的动画依赖或完整 shadcn 组件库。

## 验收标准

1. `/login` 首屏显示 Aurora AIOps 神经访问登录界面，旧的左右分栏卡片被移除。
2. 鼠标在登录面板内移动时，Polar Signal 光源平滑跟随；离开后淡回，光效不遮挡表单。
3. 用户名和密码均为 Signal Capsule 样式，聚焦态清晰且可键盘操作。
4. 主按钮悬停/键盘聚焦时完成圆角、字距、字号、亮带和光晕过渡。
5. 正确账号密码、错误账号密码、演示模式的既有测试和行为全部保持通过。
6. `prefers-reduced-motion` 下无持续漂移和强制跟随动效，页面仍可完整使用。
7. `npm test` 与 `npm run build` 通过。
