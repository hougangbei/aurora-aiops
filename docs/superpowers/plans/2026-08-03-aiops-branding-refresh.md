# AIOps 平台品牌区刷新实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 将 Web 侧栏左上角品牌从 `aurora-aiops` 更新为 `AIOps 平台`，并落地已批准的 Aurora 节点图标。

**Architecture:** 保留现有 `BrandLogo` 组件接口和 `AppLayout` 的布局结构，仅把图片 logo 替换为可缩放的 CSS/HTML 品牌标记，并更新品牌文案。通过组件级可访问性测试锁定图标语义，最后用现有构建和浏览器预览确认布局没有回归。

**Tech Stack:** React 19、TypeScript、Tailwind CSS v4、Vitest、Testing Library、Vite。

---

### Task 1: 为 Aurora 品牌图标建立失败测试

**Files:**
- Create: `web/src/components/brand/BrandLogo.test.tsx`
- Test: `web/src/components/brand/BrandLogo.test.tsx`

- [ ] **Step 1: 写出图标语义和尺寸测试**

```tsx
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { BrandLogo } from './BrandLogo';
import { renderWithProviders } from '../../test/render';

describe('BrandLogo', () => {
  it('renders the AIOps Aurora mark with an accessible label and requested size', () => {
    renderWithProviders(<BrandLogo size={42} />);

    expect(screen.getByRole('img', { name: 'AIOps 平台 logo' })).toHaveStyle({
      width: '42px',
      height: '42px',
    });
    expect(screen.getByText('A')).toBeVisible();
  });
});
```

- [ ] **Step 2: 运行测试确认当前实现失败**

Run: `npm test -- --run web/src/components/brand/BrandLogo.test.tsx` from `web/`.

Expected: FAIL because the existing image logo has no `AIOps 平台 logo` role label and no visible `A` mark.

### Task 2: 实现 Aurora 节点图标并更新品牌文案

**Files:**
- Modify: `web/src/components/brand/BrandLogo.tsx`
- Modify: `web/src/layouts/AppLayout.tsx:41-53`

- [ ] **Step 1: 用内联标记替换图片 logo**

保留 `BrandLogoProps` 和 `size` 的动态尺寸接口，将根元素改为 `role="img" aria-label="AIOps 平台 logo"`，并使用以下 Aurora 结构：

```tsx
<div
  role="img"
  aria-label="AIOps 平台 logo"
  className={[
    'relative inline-flex shrink-0 items-center justify-center overflow-hidden rounded-[12px]',
    'border border-indigo-400/40 bg-gradient-to-br from-indigo-600 via-violet-600 to-purple-700',
    'shadow-[0_10px_24px_rgba(79,70,229,0.28)]',
    className,
  ].join(' ')}
  style={{ width: size, height: size }}
>
  <span aria-hidden className="relative z-10 text-[21px] font-extrabold leading-none tracking-[-0.08em] text-white">
    A
  </span>
  <span aria-hidden className="absolute right-1.5 top-1.5 h-1.5 w-1.5 rounded-full bg-cyan-300 shadow-[0_0_0_3px_rgba(103,232,249,0.16)]" />
  <span aria-hidden className="absolute bottom-1.5 left-1.5 h-1.5 w-1.5 rounded-full bg-violet-200 shadow-[0_0_0_3px_rgba(221,214,254,0.14)]" />
</div>
```

Remove the unused `logo.jpg` import. Keep the component free of external assets so the mark scales cleanly in the collapsed sidebar and mobile drawer.

- [ ] **Step 2: 更新左上角品牌名称**

In `NavigationPanel`, replace:

```tsx
<span className="text-lg font-bold text-gray-900">aurora-aiops</span>
```

with:

```tsx
<span className="text-lg font-bold text-gray-900">AIOps 平台</span>
```

Keep `VersionBadge` directly below the name and do not add a subtitle.

- [ ] **Step 3: 运行组件测试确认通过**

Run: `npm test -- --run web/src/components/brand/BrandLogo.test.tsx` from `web/`.

Expected: PASS with one test.

### Task 3: 完成构建、全量测试和浏览器验收

**Files:**
- Modify: none
- Test: `web/src/components/brand/BrandLogo.test.tsx`

- [ ] **Step 1: 运行前端全量测试**

Run: `npm test` from `web/`.

Expected: all Vitest suites pass.

- [ ] **Step 2: 运行生产构建**

Run: `npm run build` from `web/`.

Expected: TypeScript and Vite build complete successfully.

- [ ] **Step 3: 在已运行的预览中目视检查**

打开当前本地预览，确认桌面侧栏和移动端 Drawer 左上角均显示 Aurora 图标与 `AIOps 平台`，文字不溢出，版本徽章仍在名称下方，侧栏折叠后布局无错位。

- [ ] **Step 4: 提交实现**

```bash
git add web/src/components/brand/BrandLogo.tsx web/src/components/brand/BrandLogo.test.tsx web/src/layouts/AppLayout.tsx
git commit -m "feat(web): refresh AIOps platform branding"
```
