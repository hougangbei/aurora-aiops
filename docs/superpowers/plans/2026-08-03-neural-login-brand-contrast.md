# Aurora Aiops 登录品牌与对比度调整 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make the login page visibly identify Aurora AIOps and improve dark-theme form contrast without changing authentication behavior.

**Architecture:** Keep the existing LoginPage behavior and DOM flow. Update only visible copy in `LoginPage.tsx`, scoped visual tokens and responsive styles in `LoginPage.css`, and the structural test expectations in `LoginPage.test.tsx`.

**Tech Stack:** React, TypeScript, Ant Design, Vitest, CSS media queries.

---

### Task 1: Update the branded login copy test

**Files:**
- Modify: `web/src/pages/LoginPage.test.tsx`

- [x] **Step 1: Replace the title assertion**

Update the neural access structure test to require both visible heading lines:

```tsx
expect(screen.getByRole('heading', { name: /AURORA AIOPS ACCESS CONTROL/i })).toBeVisible();
```

Keep the existing node label, bilingual field labels, and primary button assertions unchanged.

- [x] **Step 2: Run the targeted test and confirm the expected red result**

Run: `cd web && npm test -- --run src/pages/LoginPage.test.tsx`

Expected: the structure test fails because the current heading still contains `AURORA ACCESS`.

- [x] **Step 3: Commit the test baseline**

```bash
git add web/src/pages/LoginPage.test.tsx
git commit -m "test(web): require Aurora AIOps login branding"
```

### Task 2: Apply branded copy and brighter visual tokens

**Files:**
- Modify: `web/src/pages/LoginPage.tsx:75-100`
- Modify: `web/src/pages/LoginPage.css:1-290`

- [x] **Step 1: Update the heading and subtitle copy**

Use the following structure while preserving the existing `login-title` id:

```tsx
<h1 className="login-title" id="login-title">
  AURORA AIOPS
  <br />
  ACCESS CONTROL
</h1>
<p className="login-subtitle">
  Aurora AIOps 智能运维平台安全访问入口。
  <br />
  Authenticate to continue into the Aurora control plane.
</p>
```

- [x] **Step 2: Increase contrast in the scoped login styles**

Update the existing selectors without changing layout behavior:

```css
.login-stage {
  background:
    radial-gradient(circle at var(--pointer-x) var(--pointer-y), rgba(155, 168, 255, 0.26), transparent 38%),
    #0f121a;
}

.login-field-shell .ant-form-item-control-input-content {
  border-color: rgba(198, 209, 255, 0.34);
  background: rgba(45, 54, 78, 0.62);
}

.login-input::placeholder {
  color: rgba(232, 238, 255, 0.62);
}

.login-demo {
  border: 1px solid rgba(198, 209, 255, 0.34) !important;
  background: rgba(45, 54, 78, 0.74) !important;
  color: #e7ecff !important;
}
```

Also raise the field label, field mark, password icon, and focused border colors consistently with these tokens. Keep the reduced-motion block limited to color/border transitions.

- [x] **Step 3: Run targeted tests and build**

Run: `cd web && npm test -- --run src/pages/LoginPage.test.tsx && npm run build`

Expected: 5 LoginPage tests pass and the Vite build succeeds with only the existing chunk-size warning.

- [x] **Step 4: Commit the implementation**

```bash
git add web/src/pages/LoginPage.tsx web/src/pages/LoginPage.css
git commit -m "feat(web): strengthen Aurora Aiops login branding"
```

### Task 3: Verify visual contrast and regression safety

**Files:**
- Verify: `web/src/pages/LoginPage.tsx`
- Verify: `web/src/pages/LoginPage.css`
- Verify: `web/src/pages/LoginPage.test.tsx`

- [x] **Step 1: Run the full test suite and diff checks**

Run: `cd web && npm test && npm run build`; then run `git diff --check` from the repository root.

Expected: 13 test files / 52 tests pass, build succeeds, and diff check is clean.

- [x] **Step 2: Check the live login page**

Open `/login` from the current worktree and verify:

- heading visibly reads `AURORA AIOPS` and `ACCESS CONTROL`;
- field labels, borders, placeholders, ID/KEY marks, and password icon are readable on the dark background;
- primary and demo buttons have clear hierarchy;
- 390px width keeps fields within the viewport;
- mouse glow, keyboard focus, and reduced-motion CSS behavior remain present.

- [x] **Step 3: Commit only if verification required a fix**

If verification finds a defect, patch the smallest scoped selector or copy change, rerun the checks above, and commit it with a message describing the fix. Otherwise leave the verification task without an empty commit.
