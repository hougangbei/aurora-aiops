# Authorization and Consistency Closure — Stage-One Acceptance

**Branch:** `feat/authorization-consistency-closure`
**Head commit:** `2664430` (records the seven task commits below plus the run-ordering fix)
**Date:** 2026-08-03
**Validation environment:** macOS (darwin arm64), Go 1.26.5 toolchain (module declares `go 1.25.1`), SQLite, kubectl v1.30-era client against dev cluster `https://192.168.120.128:6443` (Kubernetes v1.30.14)

## Task commit map

| Task | Commit | Subject |
|------|--------|---------|
| T1 | `f860644` | fix(auth): enforce platform mutation roles |
| T2 | `ba0f3ad` | fix(aiops): make status transitions atomic |
| T3 | `4cb0e51` | fix(aiops): derive risk gating from policy |
| T4 | `7c33448` | fix(remediation): recheck policy before approval |
| T5 | `fa3c034` | fix(web): gate approval on effective risk review |
| T6 | `7fbc24b` | fix(kube): prefer in-cluster service account identity |
| T7 | `13ea9b4` | fix(deploy): use least-privilege service account identity |
| — | `2664430` | fix(aiops): order runs by insertion for stable latest selection (found during acceptance; regression test added) |

## Backend gates (Task 8 Step 1)

- `gofmt -w <plan files>` → **PASS** (no subsequent diff; working tree clean).
- `go vet ./...` → **PASS** (exit 0, no findings).
- `go test -race ./...` → **PASS**. All packages with tests passed under the race detector:
  `aiops`, `audit`, `auth`, `cluster`, `config`, `evidence`, `experiment`, `jsonx`, `kube`, `llm`, `policy`, `remediation`, `response`, `server`, `service`, `store`.
- `go test -race ./internal/aiops ./internal/remediation ./internal/server -count=20` →
  - `internal/aiops` **PASS** (20/20 repetitions, 27.9s).
  - `internal/remediation` **PASS** (20/20 repetitions, 10.7s).
  - `internal/server` **PASS** (20/20 repetitions, 1597.9s ≈ 26.6 min) when re-run with `-timeout=40m`.
  - **Finding:** the plan command as written (no `-timeout`) cannot finish 20 `server` repetitions inside Go's default 10m package timeout (the suite runs ~78 s per pass under `-race`); the default-timeout attempt aborted with `panic: test timed out after 10m0s` and no test-assertion or DATA RACE failure. Recorded below as a stage-two/known-issue note.

  Observed during the default-timeout attempt: the suite failed solely with `panic: test timed out after 10m0s`; **no test assertion failed and no DATA RACE report was produced** in any repetition. The `GET_INCIDENT_FAILED: sql: database is closed` log lines are the intended output of `TestGetIncidentRoute_InternalError`, which deliberately breaks the repository to exercise the 500 path.

## Frontend and release gates (Task 8 Step 2)

- `cd web && npm ci` → **PASS**.
- `npm test -- --run` → **PASS**: 10 test files, 45 tests, all green (includes `ApprovalDialog.test.tsx` 4 tests: fail-closed without risk review, approve hidden on high/non-approvable effective review, approve enabled after 8-char reason, disabled during pending request).
- `npm run build` → **PASS**: Vite build exited 0, 10 229 modules transformed. Known non-blocking bundle-size warning remains (`index-CVxeK7Pa.js` 4 763 kB / 1 396 kB gzip) — stage-two work.
- `SKIP_NPM_INSTALL=1 ./scripts/build-release.sh` → **PASS**. Release archive:
  `server/dist/release/aurora-aiops_0.1.1_darwin_arm64.tar.gz` (19 319 378 bytes), checksums at `server/dist/release/checksums.txt`.

## Manifest checks and authorization simulation (Task 8 Step 3)

- `./scripts/verify-kubernetes-manifests.sh` → **PASS** (exit 0) run with `KUBECONFIG` pointing at the dev cluster. All client-side dry-runs (`kubectl apply --dry-run=client --validate=false` for namespace/readonly RBAC/PVC/deployment/service, and separately for executor RBAC; `kubectl auth reconcile --dry-run=client` for both RBAC files) succeeded. Static assertions passed: no `AURORA_AIOPS_KUBECONFIG`/`aurora-aiops-kubeconfig`/`name: kubeconfig` in `deployment.yaml`; `rbac.yaml` contains `kind: ClusterRole` and `kind: ClusterRoleBinding`; `deployment.yaml` contains both `bootstrap-admin-user` and `bootstrap-admin-password` Secret keys. **No resource was applied to the cluster.**
- `kubectl auth can-i` eight-item simulation → **NOT RUN — external cluster mutation not authorized.** The simulation requires binding the RBAC manifests into a cluster; the only reachable cluster is the developer's connected cluster (`192.168.120.128`, not disposable), and no disposable validation cluster/context was explicitly selected. Not marked passed.

## Feature-level evidence

- Role-matrix integration: `TestPlatformRBACRoleMatrix` PASS (viewer read 200 / viewer create 403 / operator create 201 / operator experiment-run 403 / admin experiment-run 201 / viewer metrics 200); `TestRequiredPlatformRoles` PASS (12 route cases); `TestPlatformRBACClassifiesEveryRegisteredRoute` PASS (full `/api/v1` route inventory); `TestEnforcePlatformRBACDefaultAdmin` PASS (default-admin branch via `PUT /api/v1/test-resource` harness).
- CAS concurrency: `TestDecideConcurrentDecisionHasOneWinner` PASS (exactly one nil result, one `ErrStateTransitionConflict`, single stored terminal status); `TestConcurrentApproveRejectRouteConflict` PASS (route returns one 200 and one 409 `STATE_TRANSITION_CONFLICT`, exactly one audit record); `TestApproveRejectConcurrentDecisionSingleWinner` PASS (remediation approve/reject, one winner).
- Deterministic risk review / policy-over-model: `TestBuildEffectiveRiskReview` PASS (policy denies namespace mismatch → effective high, not approvable; model `critical/not approved` + policy permits suspend → effective low, approvable, model kept advisory; display-only action denied; empty actions blocked; two-allowed low+medium → effective medium).
- Approval-time recheck: `TestApproveAllowedStructuredPlanAdvancesAndAudits`, `TestApproveDisplayOnlyPlanRejected`, `TestApproveNamespaceMismatchRejected`, `TestApproveRechecksPolicyOnStoredPlan` all PASS (fresh policy calculation, stored output not trusted).
- Run ordering fix: `TestListRunsOrdersByInsertion` PASS; verified to fail against the pre-fix `ORDER BY started_at` (`[run-second run-first]`).

## Placeholder hygiene and diff checks (Task 8 Step 5)

- `git diff --check` → **PASS** (no whitespace errors).
- `git status --short` → only the new acceptance file uncommitted (see note below about commit state).
- Placeholder scan over `server/internal`, `web/src/modules/aiops`, `deploy/kubernetes`, and this file → **PASS** (no unsafe placeholder credential or unfinished implementation marker found).

## Known issue

- The plan's Step-1 command `go test -race ./internal/aiops ./internal/remediation ./internal/server -count=20` times out on the `server` package at Go's default 10m test timeout (~26 min of work). Evidence above uses `-timeout=40m`. Not a product defect — no assertion or data-race failure observed; flagged so the acceptance procedure is reproducible.

## Remaining stage-two items (verbatim from the plan constraints)

- Model settings.
- Audit browsing UI.
- Incident-filter wiring.
- Rollback UI request repair.
- Dependency upgrades.
- Browser-width QA.
- The 6-by-10 real-cluster experiment matrix.
- Bundle-size warning remediation (observed; non-blocking).
