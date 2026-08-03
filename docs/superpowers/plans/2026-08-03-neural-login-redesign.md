# Aurora AIOps 神经访问登录页 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `/login` 重做为 Polar Signal 冷蓝紫神经访问界面，使用 Signal Capsule 输入框和鼠标跟随光效，同时保持现有认证、演示模式、错误和 loading 行为。

**Architecture:** 保留 `LoginPage.tsx` 中现有 React Query mutation 和路由流程，只替换 JSX 结构并抽离登录页专属 CSS 到 `LoginPage.css`。背景指针位置通过舞台元素的 CSS 自定义属性更新，所有装饰层使用 `pointer-events: none`；`prefers-reduced-motion` 在局部 CSS 中关闭持续动画和过渡。

**Tech Stack:** React 19、TypeScript、Ant Design 5、React Query、React Router、Tailwind CSS 4、Vitest、Testing Library。

---

### Task 1: 固化登录页可访问结构与行为测试

**Files:**
- Modify: `web/src/pages/LoginPage.test.tsx`
- Target: `web/src/pages/LoginPage.tsx`

- [ ] **Step 1: 写结构失败测试**

在现有 `describe('LoginPage')` 中加入：

```tsx
it('renders the neural access structure with labelled Signal Capsule fields', () => {
  renderLoginPage();
  expect(screen.getByRole('heading', { name: /AURORA ACCESS/i })).toBeVisible();
  expect(screen.getByText('SYSTEM NODE: AURORA-CORE')).toBeVisible();
  expect(screen.getByLabelText('用户身份 / User Identity')).toBeVisible();
  expect(screen.getByLabelText('序列密钥 / Sequence Key')).toBeVisible();
  expect(screen.getByRole('button', { name: /INITIALIZE SESSION/i })).toBeVisible();
});
```

- [ ] **Step 2: 运行并确认失败**

Run: `cd web && npm test -- --run src/pages/LoginPage.test.tsx`
Expected: 新结构测试因旧页面没有节点标题和双语 label 而失败，既有登录、token、cookie、演示测试仍通过。

- [ ] **Step 3: 提交测试基线**

```bash
git add web/src/pages/LoginPage.test.tsx
git commit -m "test(web): specify neural login structure"
```

### Task 2: 创建 Polar Signal 登录页样式层

**Files:**
- Create: `web/src/pages/LoginPage.css`

- [ ] **Step 1: 添加局部 CSS**

必须实现下列视觉契约（完整规则写在同一文件，避免污染全局样式）：

```css
.login-page { min-height: 100vh; background: #07080a; color: #f0f4fa; }
.login-stage { --pointer-x: 50%; --pointer-y: 42%; position: relative; isolation: isolate; overflow: hidden; min-height: 100vh; background: radial-gradient(circle at var(--pointer-x) var(--pointer-y), rgba(155,168,255,.2), transparent 38%), #0b0d11; }
.login-stage::before, .login-stage::after { content: ''; position: absolute; z-index: -2; border-radius: 50%; filter: blur(38px); opacity: .3; animation: login-drift 16s ease-in-out infinite alternate; }
.login-cursor-glow { position: absolute; z-index: -1; left: var(--pointer-x); top: var(--pointer-y); width: 210px; height: 210px; border-radius: 50%; pointer-events: none; transform: translate(-50%, -50%); background: radial-gradient(circle, rgba(185,197,255,.2), rgba(112,145,255,.08) 38%, transparent 72%); filter: blur(10px); transition: left .28s ease, top .28s ease, opacity .45s ease; }
.login-panel { width: min(calc(100% - 32px), 520px); margin: 0 auto; padding: 56px 0 42px; }
.login-field-shell { display: flex; align-items: center; gap: 10px; min-height: 58px; padding: 9px 12px; border: 1px solid rgba(226,235,255,.17); border-radius: 15px; background: rgba(255,255,255,.045); transition: border-color .35s, background .35s, box-shadow .35s, transform .35s; }
.login-field-shell:focus-within { border-color: #9ba8ff; background: rgba(255,255,255,.09); box-shadow: 0 0 25px rgba(112,145,255,.2); transform: translateY(-1px); }
.login-field-mark { display: inline-flex; width: 28px; height: 28px; flex: 0 0 auto; align-items: center; justify-content: center; border: 1px solid rgba(155,168,255,.62); border-radius: 50%; color: #b7c0ff; font-size: 9px; }
.login-input { width: 100%; border: 0 !important; padding: 0 !important; background: transparent !important; color: #f0f4fa !important; }
.login-submit { border-radius: 8px; transition: border-radius .58s cubic-bezier(.22,1,.36,1), letter-spacing .42s ease, font-size .42s ease, transform .42s ease, box-shadow .5s ease; }
.login-submit:hover, .login-submit:focus-visible { border-radius: 999px; letter-spacing: .2em; font-size: 12px; transform: translateY(-2px) scale(1.018); box-shadow: 0 0 34px rgba(112,145,255,.4); }
```

同时加入 `@keyframes login-drift`、扫描纹理、按钮亮带伪元素、移动端 `max-width: 640px` 规则，以及 `@media (prefers-reduced-motion: reduce)`：禁用漂移、cursor glow 过渡、亮带和 transform，只保留边框/颜色焦点态。

- [ ] **Step 2: 提交样式层**

```bash
git add web/src/pages/LoginPage.css
git commit -m "feat(web): add polar signal login styles"
```

### Task 3: 重构 LoginPage JSX 并接入指针光效

**Files:**
- Modify: `web/src/pages/LoginPage.tsx`
- Use: `web/src/pages/LoginPage.css`

- [ ] **Step 1: 保留认证逻辑并新增指针事件**

保留 `loginWithPassword`、`handleFinish`、`handleDemoEnter`、`errorMessage` 和所有 React Query/Zustand 行为；新增 `useRef` 和 `PointerEvent`：

```tsx
const stageRef = useRef<HTMLDivElement>(null);
const handlePointerMove = (event: PointerEvent<HTMLDivElement>) => {
  if (event.pointerType === 'touch') return;
  const rect = event.currentTarget.getBoundingClientRect();
  event.currentTarget.style.setProperty('--pointer-x', `${event.clientX - rect.left}px`);
  event.currentTarget.style.setProperty('--pointer-y', `${event.clientY - rect.top}px`);
};
const handlePointerLeave = () => {
  stageRef.current?.style.setProperty('--pointer-x', '50%');
  stageRef.current?.style.setProperty('--pointer-y', '42%');
};
```

- [ ] **Step 2: 替换返回 JSX**

使用 `<main className="login-page">`、带 `ref/onPointerMove/onPointerLeave` 的 `.login-stage`、`aria-hidden` 的光效层和 `.login-panel`。标题使用 `AURORA<br />ACCESS`，节点标识为 `SYSTEM NODE: AURORA-CORE`；两个 `Form.Item` 内分别放 `ID` / `KEY` 标记、真实 `<label htmlFor>` 和带 `id` 的 `Input` / `Input.Password`，保留 `autocomplete="username"`、`autocomplete="current-password"`。主按钮使用 `className="login-submit"`、`htmlType="submit"`、`loading={loginMutation.isPending}`；演示按钮继续调用 `handleDemoEnter` 并在 mutation pending 时禁用；error Alert 继续使用 `errorMessage`。

- [ ] **Step 3: 运行登录测试**

Run: `cd web && npm test -- --run src/pages/LoginPage.test.tsx`
Expected: 所有登录页测试通过，`getByLabelText` 命中真实输入框，密码可见性按钮不会替代密码输入框。

- [ ] **Step 4: 提交页面实现**

```bash
git add web/src/pages/LoginPage.tsx web/src/pages/LoginPage.css
git commit -m "feat(web): redesign login as neural access surface"
```

### Task 4: 浏览器验收与全量验证

**Files:**
- Verify: `web/src/pages/LoginPage.tsx`
- Verify: `web/src/pages/LoginPage.css`
- Verify: `web/src/pages/LoginPage.test.tsx`

- [ ] **Step 1: 浏览器检查桌面与移动布局**

Run: `cd web && npm run dev -- --host 127.0.0.1`
检查 `/login` 首屏、鼠标光源跟随/离开回中、Signal Capsule 聚焦态、按钮胶囊过渡、键盘焦点、触摸设备和移动宽度不溢出。

- [ ] **Step 2: 检查减少动态效果**

在浏览器模拟 `prefers-reduced-motion: reduce`，确认无持续漂移、强制光源跟随、亮带扫过或按钮上浮，但表单和焦点边框仍可用。

- [ ] **Step 3: 运行全量测试与构建**

```bash
cd web && npm test
cd web && npm run build
```

Expected: 全部 Vitest 测试通过，TypeScript 与 Vite 构建通过。

- [ ] **Step 4: 检查并提交验收**

```bash
git diff --check
git status --short
git add web/src/pages/LoginPage.tsx web/src/pages/LoginPage.css web/src/pages/LoginPage.test.tsx
git commit -m "test(web): verify neural login acceptance"
```

仅允许本任务文件和既有 `.dev-kubeconfig/`、`.run/`、`.superpowers/` 未跟踪目录存在；不得修改登录 API、session、路由或已完成的 `CoreSpinLoader`。
