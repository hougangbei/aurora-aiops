# CoreSpinLoader 页面级加载 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将页面首次数据加载统一为 AIOps CoreSpinLoader，同时保留错误、空状态和操作级按钮 loading。

**Architecture:** 新增一个无外部依赖的 Tailwind React 状态组件，使用定时器循环运维文案并通过可访问状态播报加载进度。列表页面通过共享 `ResourceListPage` 集成；概览、拓扑、证据图、系统更新、AIOps 页面和资源详情页只替换首次加载分支，不改变查询、错误或空数据流程。

**Tech Stack:** React 19、TypeScript、Tailwind CSS v4、Ant Design、Vitest、Testing Library、Vite。

---

### Task 1: 创建 CoreSpinLoader 组件

**Files:**
- Create: `web/src/components/ui/core-spin-loader.tsx`
- Create: `web/src/components/ui/core-spin-loader.test.tsx`

- [ ] **Step 1: 写失败测试锁定可访问性、文案和尺寸**

```tsx
import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from '../../test/render';
import { CoreSpinLoader } from './core-spin-loader';

describe('CoreSpinLoader', () => {
  it('renders a live status with the initial loading text and requested min height', () => {
    vi.useFakeTimers();
    renderWithProviders(<CoreSpinLoader minHeight="180px" />);

    const status = screen.getByRole('status');
    expect(status).toHaveAttribute('aria-live', 'polite');
    expect(status).toHaveStyle({ minHeight: '180px' });
    expect(screen.getByText('Initializing')).toBeVisible();
    expect(status.querySelectorAll('[aria-hidden="true"]')).not.toHaveLength(0);

    vi.useRealTimers();
  });

  it('cycles through the provided loading states', () => {
    vi.useFakeTimers();
    renderWithProviders(<CoreSpinLoader />);

    vi.advanceTimersByTime(1000);
    expect(screen.getByText('Loading...')).toBeVisible();
    vi.advanceTimersByTime(1000);
    expect(screen.getByText('Fetching Data..')).toBeVisible();

    vi.useRealTimers();
  });
});
```

- [ ] **Step 2: 运行测试确认当前组件不存在**

Run: `npm test -- --run src/components/ui/core-spin-loader.test.tsx` from `web/`.

Expected: FAIL with a module-not-found or missing component error.

- [ ] **Step 3: 实现 CoreSpinLoader**

Use this component structure in `core-spin-loader.tsx`:

```tsx
import { useEffect, useState } from 'react';

type CoreSpinLoaderProps = {
  className?: string;
  minHeight?: string;
};

const loadingStates = [
  'Initializing',
  'Loading...',
  'Fetching Data..',
  'Syncing...',
  'Processing..',
  'Optimizing...',
];

export function CoreSpinLoader({
  className = '',
  minHeight = '220px',
}: CoreSpinLoaderProps) {
  const [loadingText, setLoadingText] = useState(loadingStates[0]);

  useEffect(() => {
    let index = 0;
    const interval = window.setInterval(() => {
      index = (index + 1) % loadingStates.length;
      setLoadingText(loadingStates[index]);
    }, 1000);

    return () => window.clearInterval(interval);
  }, []);

  return (
    <div
      role="status"
      aria-live="polite"
      className={['flex min-h-[200px] flex-col items-center justify-center gap-8', className].join(' ')}
      style={{ minHeight }}
    >
      <div className="relative flex h-20 w-20 items-center justify-center" aria-hidden="true">
        <div className="absolute inset-0 animate-pulse rounded-full bg-emerald-400/15 blur-xl dark:bg-cyan-500/10" />
        <div className="absolute inset-0 animate-[spin_10s_linear_infinite] rounded-full border border-dashed border-emerald-500/40 dark:border-cyan-500/20" />
        <div className="absolute inset-1 animate-[spin_2s_linear_infinite] rounded-full border-2 border-transparent border-t-emerald-500 shadow-[0_0_6px_rgba(16,185,129,0.5)] dark:border-t-cyan-400 dark:shadow-[0_0_10px_rgba(34,211,238,0.4)]" />
        <div className="absolute inset-3 animate-[spin_3s_linear_infinite_reverse] rounded-full border-2 border-transparent border-b-green-600 shadow-[0_0_6px_rgba(22,163,74,0.4)] dark:border-b-purple-500 dark:shadow-[0_0_10px_rgba(168,85,247,0.4)]" />
        <div className="absolute inset-5 animate-[spin_1s_ease-in-out_infinite] rounded-full border border-transparent border-l-green-700/60 dark:border-l-white/50" />
        <div className="absolute inset-0 animate-[spin_4s_linear_infinite]">
          <div className="absolute left-1/2 top-0 h-1 w-1 -translate-x-1/2 rounded-full bg-emerald-600 shadow-[0_0_4px_rgba(16,185,129,0.9)] dark:bg-cyan-400 dark:shadow-[0_0_6px_rgba(34,211,238,0.8)]" />
        </div>
        <div className="absolute h-2 w-2 animate-pulse rounded-full bg-emerald-700 shadow-[0_0_6px_rgba(16,185,129,0.6)] dark:bg-white dark:shadow-[0_0_10px_rgba(255,255,255,0.8)]" />
      </div>
      <span key={loadingText} className="h-8 animate-pulse text-[10px] font-medium uppercase tracking-[0.3em] text-emerald-700 dark:text-cyan-200/70">
        {loadingText}
      </span>
    </div>
  );
}
```

- [ ] **Step 4: 运行组件测试确认通过**

Run: `npm test -- --run src/components/ui/core-spin-loader.test.tsx` from `web/`.

Expected: PASS with two tests.

- [ ] **Step 5: 提交组件**

```bash
git add web/src/components/ui/core-spin-loader.tsx web/src/components/ui/core-spin-loader.test.tsx
git commit -m "feat(web): add AIOps core spin loader"
```

### Task 2: 接入共享资源列表和 AIOps Incident 列表

**Files:**
- Modify: `web/src/components/resource-list/ResourceListPage.tsx`
- Modify: `web/src/modules/aiops/components/IncidentTable.tsx`
- Create: `web/src/components/resource-list/ResourceListPage.test.tsx`

- [ ] **Step 1: 写共享列表 loading 测试**

Add `ResourceListPage.test.tsx` with this deterministic fixture:

```tsx
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { renderWithProviders } from '../../test/render';
import { ResourceListPage } from './ResourceListPage';

describe('ResourceListPage loading state', () => {
  it('uses CoreSpinLoader while keeping the page header and refresh action', () => {
    renderWithProviders(
      <ResourceListPage
        title="Pods"
        description="当前集群中的 Pod"
        dataSource={[]}
        columns={[]}
        rowKey="name"
        loading
        onRefresh={() => undefined}
      />,
    );

    expect(screen.getByRole('status')).toBeVisible();
    expect(screen.getByText('Pods')).toBeVisible();
    expect(screen.getByRole('button', { name: '刷新' })).toBeVisible();
  });
});
```

- [ ] **Step 2: 运行测试确认共享列表当前没有 CoreSpinLoader**

Run: `npm test -- --run src/components/resource-list/ResourceListPage.test.tsx` from `web/`.

Expected: FAIL because the current implementation delegates to ProTable's default spinner.

- [ ] **Step 3: 替换共享列表的首次加载区域**

Import `CoreSpinLoader` and replace the `ProTable` body with:

```tsx
{loading ? (
  <CoreSpinLoader minHeight="220px" />
) : (
  <ProTable<T>
    rowKey={rowKey}
    columns={columns}
    dataSource={filteredData}
    loading={false}
    search={false}
    options={false}
    toolBarRender={false}
    tableAlertRender={false}
    tableAlertOptionRender={false}
    cardBordered={false}
    dateFormatter="string"
    pagination={{
      defaultPageSize: paginationPageSize,
      showSizeChanger: true,
      pageSizeOptions: [10, 20, 50],
    }}
    locale={{
      emptyText: (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span className="text-sm text-slate-500">{emptyDescription}</span>} />
      ),
    }}
    scroll={{ x: 'max-content' }}
    onRow={onRow}
  />
)}
```

Keep the card header, search, refresh button, error handling in callers, and empty state unchanged.

- [ ] **Step 4: Replace IncidentTable's first-load spinner**

Use `CoreSpinLoader minHeight="220px"` around the incident table only when `dataMode === 'live' && incidentsQuery.isLoading`; keep the existing error Alert and demo data path unchanged.

- [ ] **Step 5: Run shared list tests**

Run: `npm test -- --run src/components/resource-list/ResourceListPage.test.tsx src/modules/aiops/components/IncidentTable.test.tsx` from `web/`.

Expected: PASS.

- [ ] **Step 6: Commit shared integrations**

```bash
git add web/src/components/resource-list/ResourceListPage.tsx web/src/components/resource-list/ResourceListPage.test.tsx web/src/modules/aiops/components/IncidentTable.tsx
git commit -m "feat(web): use core loader for resource lists"
```

### Task 3: Replace independent page-level loading states

**Files:**
- Modify: `web/src/pages/OverviewPage.tsx`
- Modify: `web/src/pages/TopologyPage.tsx`
- Modify: `web/src/pages/EvidenceGraphPage.tsx`
- Modify: `web/src/pages/SystemUpdatesPage.tsx`
- Modify: `web/src/pages/AIOpsApprovalsPage.tsx`
- Modify: `web/src/pages/AIOpsExperimentsPage.tsx`
- Modify: `web/src/pages/AIOpsOverviewPage.tsx`
- Modify: `web/src/pages/IncidentsPage.tsx`

- [ ] **Step 1: Replace overview Skeleton**

Change the `OverviewPage` branch `if (enabled && summaryQuery.isLoading && nodesQuery.isLoading)` from `<Skeleton active paragraph={{ rows: 12 }} />` to `<CoreSpinLoader minHeight="260px" />`; keep demo mode and partial refetch behavior unchanged.

- [ ] **Step 2: Replace topology Skeleton**

Change the `topologyQuery.isLoading` canvas branch from the padded Skeleton to `<CoreSpinLoader minHeight="320px" className="p-5 pt-20" />`; keep viewport controls mounted and preserve the existing empty graph branch.

- [ ] **Step 3: Replace evidence graph Spin**

Replace the evidence query `<Spin size="large" />` block with `<CoreSpinLoader minHeight="260px" />`; leave the separate `layouting` indicator as an operation-local state unless it is already a page-level blocking branch.

- [ ] **Step 4: Replace system and AIOps page Skeleton/Spin branches**

Use `<CoreSpinLoader minHeight="240px" />` for `SystemUpdatesPage` initial build/status loading, `AIOpsApprovalsPage`, `AIOpsExperimentsPage`, `AIOpsOverviewPage`, and `IncidentsPage` first-load branches. Do not change mutation button `loading` props or error Alerts.

- [ ] **Step 5: Run affected tests**

Run: `npm test -- --run src/modules/aiops src/pages/LoginPage.test.tsx src/test/smoke.test.tsx` from `web/`.

Expected: PASS with no new failures.

- [ ] **Step 6: Commit independent page integrations**

```bash
git add web/src/pages/OverviewPage.tsx web/src/pages/TopologyPage.tsx web/src/pages/EvidenceGraphPage.tsx web/src/pages/SystemUpdatesPage.tsx web/src/pages/AIOpsApprovalsPage.tsx web/src/pages/AIOpsExperimentsPage.tsx web/src/pages/AIOpsOverviewPage.tsx web/src/pages/IncidentsPage.tsx
git commit -m "feat(web): standardize page loading states"
```

### Task 4: Replace resource detail first-load branches

**Files:**
- Modify: `web/src/pages/ClusterRoleBindingDetailsPage.tsx`
- Modify: `web/src/pages/ClusterRoleDetailsPage.tsx`
- Modify: `web/src/pages/ConfigMapDetailsPage.tsx`
- Modify: `web/src/pages/CronJobDetailsPage.tsx`
- Modify: `web/src/pages/DaemonSetDetailsPage.tsx`
- Modify: `web/src/pages/DeploymentDetailsPage.tsx`
- Modify: `web/src/pages/EndpointDetailsPage.tsx`
- Modify: `web/src/pages/HPADetailsPage.tsx`
- Modify: `web/src/pages/IngressClassDetailsPage.tsx`
- Modify: `web/src/pages/IngressDetailsPage.tsx`
- Modify: `web/src/pages/JobDetailsPage.tsx`
- Modify: `web/src/pages/LimitRangeDetailsPage.tsx`
- Modify: `web/src/pages/NetworkPolicyDetailsPage.tsx`
- Modify: `web/src/pages/PersistentVolumeClaimDetailsPage.tsx`
- Modify: `web/src/pages/PersistentVolumeDetailsPage.tsx`
- Modify: `web/src/pages/PodDetailsPage.tsx`
- Modify: `web/src/pages/ReplicaSetDetailsPage.tsx`
- Modify: `web/src/pages/ResourceQuotaDetailsPage.tsx`
- Modify: `web/src/pages/RoleBindingDetailsPage.tsx`
- Modify: `web/src/pages/RoleDetailsPage.tsx`
- Modify: `web/src/pages/SecretDetailsPage.tsx`
- Modify: `web/src/pages/ServiceAccountDetailsPage.tsx`
- Modify: `web/src/pages/ServiceDetailsPage.tsx`
- Modify: `web/src/pages/StatefulSetDetailsPage.tsx`
- Modify: `web/src/pages/StorageClassDetailsPage.tsx`
- Modify: `web/src/pages/VPADetailsPage.tsx`

- [ ] **Step 1: Replace each plain-text first-load return**

In every listed detail page, import `CoreSpinLoader` and replace the existing `if (...isLoading)` return body that says `正在加载 <resource> 详情...` with:

```tsx
if (allowLiveAccess && resourceQuery.isLoading) {
  return <CoreSpinLoader minHeight="220px" />;
}
```

Keep each page's existing `enabled`/`allowLiveAccess` condition and query variable name; only the returned loading UI changes. Do not alter YAML fetch, save, mutation, error, or empty branches.

- [ ] **Step 2: Search for remaining page-level loading placeholders**

Run: `rg -n "正在加载 .*详情|<Skeleton|<Spin size=\"large\"" web/src/pages web/src/components/resource-list web/src/modules/aiops/components` from the repository root.

Expected: only operation-local or intentionally retained loading states remain; no initial page data branch renders the old plain-text/Skeleton/large Spin UI.

- [ ] **Step 3: Commit detail integrations**

```bash
git add web/src/pages/*DetailsPage.tsx
git commit -m "feat(web): use core loader for detail pages"
```

### Task 5: Full verification and browser acceptance

**Files:**
- Modify: none
- Test: `web/src/components/ui/core-spin-loader.test.tsx`, `web/src/components/resource-list/ResourceListPage.test.tsx`, existing page suites

- [ ] **Step 1: Run the complete frontend test suite**

Run: `npm test` from `web/`.

Expected: all test files pass.

- [ ] **Step 2: Run the production build**

Run: `npm run build` from `web/`.

Expected: TypeScript compilation and Vite build pass; existing chunk-size warning is informational only.

- [ ] **Step 3: Verify the running app in the browser**

Open the local app, sign in with the existing preview credentials, and check Overview, a resource list, a resource detail, Topology, and Evidence Graph. Confirm the CoreSpinLoader is centered, the page title/back path remains visible, error Alerts and Empty states are unchanged, and button-level loading remains compact.

- [ ] **Step 4: Record final status**

Run `git status --short` and confirm only pre-existing untracked preview directories remain; report the implementation commits and verification commands.
