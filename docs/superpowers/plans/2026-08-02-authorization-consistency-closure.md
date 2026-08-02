# Authorization and Consistency Closure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the accepted P1 authorization, incident-transition consistency, deterministic risk-gating, and Kubernetes deployment-identity gaps without implementing the stage-two settings, audit UI, filtering, rollback UI, dependency, or experiment work.

**Architecture:** Add one default-deny platform RBAC middleware after session authentication, keep route-local guards as defense in depth, and make incident transitions compare-and-swap at the repository boundary. Convert model risk output into an advisory nested object while a deterministic policy builder produces the only approval gate; re-evaluate that policy immediately before approval. Prefer explicit kubeconfig, then in-cluster identity, then the user's default kubeconfig, and make the supplied deployment use ServiceAccount identity with separate read-only and opt-in executor roles.

**Tech Stack:** Go 1.25, Gin, SQLite, client-go, React 19, TypeScript, TanStack Query, Ant Design, Vitest, Kubernetes YAML, shell validation.

---

## Constraints and acceptance map

- Stage one only: do not implement model settings, audit browsing, incident-filter wiring, rollback UI request repair, dependency upgrades, browser-width QA, or the 6-by-10 real-cluster experiment matrix.
- Every mutation not explicitly assigned to `operator` defaults to `admin`.
- `viewer` is read-only; `operator` may create/reanalyze/approve/reject incidents and test the connection; `admin` additionally owns execution, rollback, experiments, Pod exec, raw manifests, resource mutation, and system lifecycle operations.
- Approval is fail-closed: the UI requires a valid stored effective review, while the backend independently rebuilds a fresh deterministic effective review from the remediation plan before every approval.
- Concurrent transitions must produce one winner and `409 STATE_TRANSITION_CONFLICT` for losers; losing decisions must not append audit records.
- The default Kubernetes deployment must not set `KUBEJOJO_KUBECONFIG` or mount a kubeconfig Secret.

## Task 1: Enforce platform HTTP RBAC with a default-admin write policy

**Files:**

- Create: `server/internal/server/platform_rbac.go`
- Create: `server/internal/server/platform_rbac_test.go`
- Modify: `server/internal/server/session_middleware.go`
- Modify: `server/internal/server/session_middleware_test.go`
- Modify: `server/internal/server/router.go:102-120`
- Modify: `server/internal/server/aiops_routes.go:46-75`
- Modify: `server/internal/server/experiment_routes.go:18-25`
- Modify: `server/internal/server/aiops_evidence_routes_test.go`
- Modify: `server/internal/server/experiment_routes_test.go`

- [ ] **Step 1: Write the role-policy table test.**

Add table cases to `platform_rbac_test.go` for all exceptional paths and default behavior:

```go
func TestRequiredPlatformRoles(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   []auth.Role
	}{
		{http.MethodGet, "/api/v1/nodes", nil},
		{http.MethodHead, "/api/v1/nodes", nil},
		{http.MethodOptions, "/api/v1/nodes", nil},
		{http.MethodPost, "/api/v1/auth/logout", nil},
		{http.MethodPost, "/api/v1/aiops/incidents", []auth.Role{auth.RoleOperator, auth.RoleAdmin}},
		{http.MethodPost, "/api/v1/aiops/incidents/:id/reanalyze", []auth.Role{auth.RoleOperator, auth.RoleAdmin}},
		{http.MethodPost, "/api/v1/aiops/incidents/:id/approve-remediation", []auth.Role{auth.RoleOperator, auth.RoleAdmin}},
		{http.MethodPost, "/api/v1/aiops/incidents/:id/reject-remediation", []auth.Role{auth.RoleOperator, auth.RoleAdmin}},
		{http.MethodPost, "/api/v1/cluster/connection/test", []auth.Role{auth.RoleOperator, auth.RoleAdmin}},
		{http.MethodGet, "/api/v1/pods/:namespace/:name/exec/ws", []auth.Role{auth.RoleAdmin}},
		{http.MethodPost, "/api/v1/experiments/runs", []auth.Role{auth.RoleAdmin}},
		{http.MethodPut, "/api/v1/deployments/:namespace/:name/yaml", []auth.Role{auth.RoleAdmin}},
		{http.MethodDelete, "/api/v1/pods/:namespace/:name", []auth.Role{auth.RoleAdmin}},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			if got := requiredPlatformRoles(tt.method, tt.path); !slices.Equal(got, tt.want) {
				t.Fatalf("roles=%v want=%v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the focused test and confirm the expected compile failure.**

Run: `cd server && go test ./internal/server -run TestRequiredPlatformRoles -count=1`

Expected: FAIL with `undefined: requiredPlatformRoles`.

- [ ] **Step 3: Implement the pure policy and middleware.**

Create `platform_rbac.go` with a fixed operator allowlist and an admin default for every other unsafe request:

```go
package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/heihuzicity-tech/kubejojo/server/internal/auth"
	"github.com/heihuzicity-tech/kubejojo/server/internal/response"
)

var operatorWritePaths = map[string]struct{}{
	http.MethodPost + " /api/v1/aiops/incidents":                           {},
	http.MethodPost + " /api/v1/aiops/incidents/:id/reanalyze":             {},
	http.MethodPost + " /api/v1/aiops/incidents/:id/approve-remediation":   {},
	http.MethodPost + " /api/v1/aiops/incidents/:id/reject-remediation":    {},
	http.MethodPost + " /api/v1/cluster/connection/test":                   {},
}

func requiredPlatformRoles(method, fullPath string) []auth.Role {
	if method == http.MethodHead || method == http.MethodOptions {
		return nil
	}
	if method == http.MethodGet {
		if fullPath == "/api/v1/pods/:namespace/:name/exec/ws" {
			return []auth.Role{auth.RoleAdmin}
		}
		return nil
	}
	if method == http.MethodPost && fullPath == "/api/v1/auth/logout" {
		return nil
	}
	if _, ok := operatorWritePaths[method+" "+fullPath]; ok {
		return []auth.Role{auth.RoleOperator, auth.RoleAdmin}
	}
	return []auth.Role{auth.RoleAdmin}
}

func EnforcePlatformRBAC() gin.HandlerFunc {
	return func(c *gin.Context) {
		roles := requiredPlatformRoles(c.Request.Method, c.FullPath())
		if len(roles) == 0 {
			c.Next()
			return
		}
		user, ok := ActorFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, response.Failure("UNAUTHORIZED", "未登录或会话已过期"))
			c.Abort()
			return
		}
		for _, role := range roles {
			if user.Role == role {
				c.Next()
				return
			}
		}
		c.JSON(http.StatusForbidden, response.Failure("FORBIDDEN", "当前角色无权限执行该操作"))
		c.Abort()
	}
}
```

Keep the exact operator path strings in one map. Do not use prefix matching, because a new mutation must fall into the admin default until deliberately reviewed.

- [ ] **Step 4: Add the approved reusable role combinators.**

Append to `session_middleware.go`:

```go
func RequireOperator() gin.HandlerFunc {
	return RequireRoles(auth.RoleOperator, auth.RoleAdmin)
}

func RequireAdmin() gin.HandlerFunc {
	return RequireRoles(auth.RoleAdmin)
}
```

Extend `session_middleware_test.go` to prove viewer/operator/admin behavior for both combinators. Keep `RequireRoles` for any future non-hierarchical role set, but route registration in this closure must use the named combinators.

- [ ] **Step 5: Install the middleware and preserve explicit route-local defense in depth.**

In `router.go`, place `authorized.Use(EnforcePlatformRBAC())` immediately after `RequireSession`. Change Pod exec's local guard to `RequireAdmin()`.

In `aiops_routes.go`, guard incident creation, reanalysis, approval, and rejection with `RequireOperator()`. Guard execution and rollback with `RequireAdmin()`.

In `cluster_routes.go`, replace the connection-test role list with `RequireOperator()`.

In `experiment_routes.go`, guard `POST /runs` with `RequireAdmin()`.

In `router.go`, attach `RequireAdmin()` as the first route-local handler to every remaining registered `POST`, `PUT`, `PATCH`, and `DELETE` route except `/auth/logout` and the operator routes above. This includes system update/restart/rollback, `/manifests`, resource YAML updates, scale/restart/suspend operations, and resource deletes. The group middleware is the future-route fail-safe; these local guards make the current route registrations auditable as approved.

- [ ] **Step 6: Add integration assertions for the role matrix and full route inventory.**

Extend the existing authenticated route test helpers so these cases use real session cookies:

```text
viewer   GET  /api/v1/aiops/incidents          -> 200
viewer   POST /api/v1/aiops/incidents          -> 403
operator POST /api/v1/aiops/incidents          -> 201
operator POST /api/v1/experiments/runs         -> 403
admin    POST /api/v1/experiments/runs         -> 201
viewer   GET  /api/v1/experiments/metrics      -> 200
viewer   PUT  /api/v1/test-resource            -> 403 (middleware harness)
admin    PUT  /api/v1/test-resource            -> handler reached
```

For the middleware harness, register `PUT /api/v1/test-resource` only in the test router and assert the default-admin branch. Do not invoke a real Kubernetes write.

Add `TestPlatformRBACClassifiesEveryRegisteredRoute` against `router.Routes()`. For every route under `/api/v1` other than anonymous login, assert that GET/HEAD/OPTIONS is read-only except the exact Pod exec path, logout is authenticated-only, the five operator paths resolve to operator/admin, and every other POST/PUT/PATCH/DELETE resolves to admin. Fail with method plus full path so a newly added route requires an explicit review of the operator allowlist.

- [ ] **Step 7: Run the server tests and commit.**

Run: `cd server && go test ./internal/server -count=1`

Expected: PASS.

Commit: `git add server/internal/server && git commit -m "fix(auth): enforce platform mutation roles"`

## Task 2: Make incident transitions atomic and return a stable conflict

**Files:**

- Modify: `server/internal/aiops/repository.go`
- Modify: `server/internal/aiops/repository_test.go`
- Modify: `server/internal/aiops/service.go`
- Modify: `server/internal/aiops/service_test.go`
- Create: `server/internal/remediation/service_test.go`
- Modify: `server/internal/server/aiops_routes.go:372-395`
- Modify: `server/internal/server/aiops_evidence_routes_test.go`

- [ ] **Step 1: Write repository CAS tests.**

Add a test that creates an incident in `awaiting_approval`, then asserts:

```go
if err := repo.TransitionStatus(ctx, id, StatusAwaitingApproval, StatusApproved, now); err != nil {
	t.Fatal(err)
}
if err := repo.TransitionStatus(ctx, id, StatusAwaitingApproval, StatusRejected, now); !errors.Is(err, ErrStateTransitionConflict) {
	t.Fatalf("err=%v want ErrStateTransitionConflict", err)
}
if err := repo.TransitionStatus(ctx, "missing", StatusAwaitingApproval, StatusRejected, now); !errors.Is(err, ErrIncidentNotFound) {
	t.Fatalf("err=%v want ErrIncidentNotFound", err)
}
```

- [ ] **Step 2: Write a concurrent service test.**

Make `fakeRepository` mutex-protected and change its transition method to compare `from` under the mutex. Launch approved and rejected transitions from one start channel. Assert exactly one nil result, exactly one `ErrStateTransitionConflict`, and a single stored terminal status.

- [ ] **Step 3: Run the tests and confirm failure.**

Run: `cd server && go test ./internal/aiops -run 'TestRepositoryTransitionStatusCompareAndSwap|TestAdvanceConcurrentDecisionHasOneWinner' -count=1`

Expected: FAIL because `TransitionStatus` and `ErrStateTransitionConflict` do not exist.

- [ ] **Step 4: Replace the repository update with compare-and-swap.**

Change the interface and SQL implementation to:

```go
var ErrStateTransitionConflict = errors.New("incident state transition conflict")

type IncidentRepository interface {
	Create(ctx context.Context, inc Incident) error
	Get(ctx context.Context, id string) (Incident, error)
	List(ctx context.Context, filter IncidentFilter) ([]Incident, error)
	TransitionStatus(ctx context.Context, id string, from, to Status, updatedAt time.Time) error
}

func (r *sqlIncidentRepository) TransitionStatus(
	ctx context.Context, id string, from, to Status, updatedAt time.Time,
) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE incidents SET status = ?, updated_at = ? WHERE id = ? AND status = ?`,
		string(to), updatedAt.UTC().Format(time.RFC3339Nano), id, string(from),
	)
	if err != nil {
		return fmt.Errorf("transition incident status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 1 {
		return nil
	}
	if _, err := r.Get(ctx, id); errors.Is(err, ErrIncidentNotFound) {
		return ErrIncidentNotFound
	} else if err != nil {
		return err
	}
	return ErrStateTransitionConflict
}
```

Update `Service.Advance` to retain the current validation read, then call `TransitionStatus(ctx, id, incident.Status, to, time.Now().UTC())`. The validation read preserves `ErrInvalidStateTransition`; CAS closes the race after that read.

- [ ] **Step 5: Map conflicts at the HTTP boundary and prove no losing audit.**

Add this branch before the generic remediation errors:

```go
case errors.Is(err, aiops.ErrStateTransitionConflict):
	c.JSON(http.StatusConflict, response.Failure("STATE_TRANSITION_CONFLICT", "事件状态已被其他操作更新"))
```

Add a route/service test that issues concurrent approve and reject decisions, then asserts one 200, one 409 with code `STATE_TRANSITION_CONFLICT`, and exactly one `approve-remediation` or `reject-remediation` audit record.

- [ ] **Step 6: Run and commit.**

Run: `cd server && go test -race ./internal/aiops ./internal/remediation ./internal/server -count=1`

Expected: PASS with no race report.

Commit: `git add server/internal/aiops server/internal/remediation server/internal/server && git commit -m "fix(aiops): make status transitions atomic"`

## Task 3: Produce an authoritative deterministic effective risk review

**Files:**

- Modify: `server/internal/aiops/roles.go`
- Create: `server/internal/aiops/risk_review.go`
- Create: `server/internal/aiops/risk_review_test.go`
- Modify: `server/internal/aiops/workflow.go:439-458`
- Modify: `server/internal/aiops/workflow_test.go`

- [ ] **Step 1: Define tests for model/policy separation.**

Cover these cases in `risk_review_test.go`:

1. Model says `low/approved`, policy denies a namespace mismatch: `effectiveRisk=high`, `approvable=false`.
2. Model says `critical/not approved`, policy permits `suspend_cronjob`: `effectiveRisk=low`, `approvable=true`; model output remains nested and its blockers are displayed as advisory blockers.
3. One display-only action without structured fields: denied, high, not approvable.
4. Empty actions: not approvable with an explicit blocker.
5. Two allowed actions low plus medium: effective risk is medium and approvable.

The second case is intentional: the model may advise but cannot control the deterministic approval bit.

- [ ] **Step 2: Run and confirm type/function failures.**

Run: `cd server && go test ./internal/aiops -run TestBuildEffectiveRiskReview -count=1`

Expected: FAIL with missing `BuildEffectiveRiskReview`/`EffectiveRiskReview`.

- [ ] **Step 3: Add explicit wire types.**

Append to `roles.go`:

```go
type PolicyActionReview struct {
	Kind         string `json:"kind"`
	ResourceKind string `json:"resourceKind"`
	ResourceName string `json:"resourceName"`
	Allowed      bool   `json:"allowed"`
	Risk         string `json:"risk"`
	Reason       string `json:"reason"`
}

type EffectiveRiskReview struct {
	EffectiveRisk string               `json:"effectiveRisk"`
	Approvable    bool                 `json:"approvable"`
	Blockers      []string             `json:"blockers"`
	Actions       []PolicyActionReview `json:"actions"`
	ModelReview   RiskReviewOutput     `json:"modelReview"`
}
```

- [ ] **Step 4: Implement the deterministic builder.**

In `risk_review.go`, import `policy`, convert every remediation action to `policy.Action`, and call `policy.Evaluate`. Use this exact behavior:

```go
// BuildEffectiveRiskReview is the only source of approvability. ModelReview is
// retained for explanation but never changes EffectiveRisk or Approvable.
func BuildEffectiveRiskReview(model RiskReviewOutput, remediation RemediationOutput, incidentNamespace string) EffectiveRiskReview {
	review := EffectiveRiskReview{
		EffectiveRisk: string(policy.RiskLow),
		Approvable:    len(remediation.Actions) > 0,
		ModelReview:   model,
	}
	if len(remediation.Actions) == 0 {
		review.EffectiveRisk = string(policy.RiskHigh)
		review.Blockers = append(review.Blockers, "policy: remediation has no actions")
	}
	for _, proposed := range remediation.Actions {
		evaluation := policy.Evaluate(policy.Action{
			Kind: proposed.Kind, Namespace: proposed.Namespace,
			ResourceKind: proposed.ResourceKind, ResourceName: proposed.ResourceName,
			Parameters: proposed.Parameters,
		}, incidentNamespace)
		risk := string(evaluation.Risk)
		if !evaluation.Allowed {
			risk = string(policy.RiskHigh)
			review.Approvable = false
			review.Blockers = append(review.Blockers, "policy: "+evaluation.Reason)
		}
		review.Actions = append(review.Actions, PolicyActionReview{
			Kind: proposed.Kind, ResourceKind: proposed.ResourceKind,
			ResourceName: proposed.ResourceName, Allowed: evaluation.Allowed,
			Risk: risk, Reason: evaluation.Reason,
		})
		if riskRank(risk) > riskRank(review.EffectiveRisk) {
			review.EffectiveRisk = risk
		}
	}
	if review.EffectiveRisk == string(policy.RiskHigh) {
		review.Approvable = false
	}
	for _, blocker := range model.Blockers {
		review.Blockers = append(review.Blockers, "model: "+blocker)
	}
	return review
}
```

Implement `riskRank` as an unexported switch for `low`, `medium`, and `high`; treat unknown values as high. Do not consult `RemediationAction.Risk`, `RiskReviewOutput.RiskLevel`, or `RiskReviewOutput.Approved` when calculating the two authoritative fields.

- [ ] **Step 5: Store effective output from the workflow.**

After decoding the model response in `runRiskReview`, build the effective review from the already loaded remediation and incident namespace:

```go
modelReview, err := DecodeRiskReview(resp.Text)
if err != nil {
	return w.failRun(ctx, run, err)
}
effective := BuildEffectiveRiskReview(modelReview, remediation, inc.Namespace)
applyModelUsage(run, resp)
run.Summary = fmt.Sprintf("effective risk %s, approvable=%t", effective.EffectiveRisk, effective.Approvable)
run.Output = mustJSON(effective)
```

Update workflow tests to decode `EffectiveRiskReview`, assert policy-over-model behavior, and assert the original model response is preserved in `ModelReview`.

- [ ] **Step 6: Run and commit.**

Run: `cd server && go test ./internal/aiops ./internal/policy -count=1`

Expected: PASS.

Commit: `git add server/internal/aiops && git commit -m "fix(aiops): derive risk gating from policy"`

## Task 4: Re-evaluate policy immediately before approval

**Files:**

- Modify: `server/internal/remediation/service.go`
- Modify: `server/internal/remediation/service_test.go`
- Modify: `server/internal/server/aiops_routes.go:372-395`
- Modify: `server/internal/server/aiops_evidence_routes_test.go`

- [ ] **Step 1: Write fail-closed approval tests.**

Add service tests for:

- an allowed structured `suspend_cronjob` plan advances and appends one audit record;
- a display-only plan returns `ErrActionNotAllowed`, stays `awaiting_approval`, and appends no audit;
- a namespace-mismatched plan does the same;
- a plan that was allowed at workflow time but whose stored output is now denied is rejected by the immediate re-check.

- [ ] **Step 2: Run and confirm current unsafe behavior.**

Run: `cd server && go test ./internal/remediation -run 'TestApprove.*Policy' -count=1`

Expected: FAIL because `Approve` currently transitions without reading the remediation plan.

- [ ] **Step 3: Refactor plan loading and add the approval gate.**

Add:

```go
var ErrActionNotAllowed = errors.New("remediation action not allowed")

func (s *Service) latestRemediation(ctx context.Context, incidentID string) (aiops.RemediationOutput, error) {
	runs, err := s.runs.ListRuns(ctx, incidentID)
	if err != nil {
		return aiops.RemediationOutput{}, err
	}
	var latest *aiops.AgentRun
	for i := range runs {
		if runs[i].Role != "remediation" || runs[i].Status != aiops.RunStatusSucceeded {
			continue
		}
		if latest == nil || runs[i].Attempt > latest.Attempt {
			copy := runs[i]
			latest = &copy
		}
	}
	if latest == nil {
		return aiops.RemediationOutput{}, ErrNoExecutableActions
	}
	return aiops.DecodeRemediation(latest.Output)
}
```

Use this helper in both `actionsFromPlan` and `Approve`. Before `Advance`:

```go
plan, err := s.latestRemediation(ctx, incidentID)
if err != nil {
	return err
}
effective := aiops.BuildEffectiveRiskReview(aiops.RiskReviewOutput{}, plan, incident.Namespace)
if !effective.Approvable {
	return fmt.Errorf("%w: %s", ErrActionNotAllowed, strings.Join(effective.Blockers, "; "))
}
```

This is a fresh policy calculation, not trust in the stored risk-review run. Keep the status CAS after this calculation, so concurrent approve/reject still has one winner and only the winner reaches audit append.

- [ ] **Step 4: Return the stable API error.**

Map `ErrActionNotAllowed` before `policy.ErrActionDenied`:

```go
case errors.Is(err, remediation.ErrActionNotAllowed):
	c.JSON(http.StatusForbidden, response.Failure("ACTION_NOT_ALLOWED", "修复动作未通过确定性策略校验"))
```

Assert the route response is 403 with `ACTION_NOT_ALLOWED` and the incident remains unchanged.

- [ ] **Step 5: Run and commit.**

Run: `cd server && go test -race ./internal/remediation ./internal/server -count=1`

Expected: PASS.

Commit: `git add server/internal/remediation server/internal/server && git commit -m "fix(remediation): recheck policy before approval"`

## Task 5: Make the approval UI consume the effective review

**Files:**

- Modify: `web/src/modules/aiops/types.ts`
- Modify: `web/src/modules/aiops/components/ApprovalDialog.tsx`
- Modify: `web/src/modules/aiops/components/ApprovalDialog.test.tsx`

- [ ] **Step 1: Replace fixture semantics in the component tests.**

Change the test helper to return both a remediation run and a succeeded risk-review run whose output is:

```ts
type ReviewFixture = {
  effectiveRisk: 'low' | 'medium' | 'high';
  approvable: boolean;
  blockers: string[];
  actions: Array<{
    kind: string;
    resourceKind: string;
    resourceName: string;
    allowed: boolean;
    risk: 'low' | 'medium' | 'high';
    reason: string;
  }>;
  modelReview: {
    riskLevel: 'low' | 'medium' | 'high' | 'critical';
    approved: boolean;
    blockers: string[];
    rationale: string;
  };
};
```

Test these decisions:

- remediation claims `low`, effective review says `high/false`: approve hidden, reject visible;
- remediation claims `high`, effective review says `medium/true`: approve visible and enabled after an eight-character reason;
- no succeeded risk-review run: approval hidden and an “有效风险评审缺失” error is visible;
- pending approve request still disables duplicate clicks.

- [ ] **Step 2: Run the test and confirm semantic failures.**

Run: `cd web && npm test -- --run src/modules/aiops/components/ApprovalDialog.test.tsx`

Expected: FAIL because the component still trusts `RemediationAction.risk`.

- [ ] **Step 3: Add TypeScript wire types and parsing.**

Add `PolicyActionReview`, `ModelRiskReview`, and `EffectiveRiskReview` to `types.ts`, matching the Go JSON tags exactly.

In `ApprovalDialog.tsx`:

- find the highest-attempt succeeded `risk_review` run;
- parse it into `EffectiveRiskReview`, returning `undefined` on invalid JSON or invalid field shapes;
- keep remediation parsing only for command/reason display;
- set `canApprove = review?.approvable === true`;
- show `review.effectiveRisk` as the authoritative risk tag;
- show every `review.blockers` entry;
- when `review` is absent, show a fail-closed error and never render the approve button.

Do not read `action.risk` for permission or button visibility.

- [ ] **Step 4: Run component and complete frontend verification.**

Run: `cd web && npm test -- --run src/modules/aiops/components/ApprovalDialog.test.tsx`

Expected: PASS.

Run: `cd web && npm test -- --run && npm run build`

Expected: all tests PASS and Vite build exits 0; the existing bundle-size warning may remain for stage two.

- [ ] **Step 5: Commit.**

Commit: `git add web/src/modules/aiops && git commit -m "fix(web): gate approval on effective risk review"`

## Task 6: Select explicit kubeconfig, then in-cluster identity, then default kubeconfig

**Files:**

- Modify: `server/internal/config/config.go`
- Modify: `server/internal/config/config_test.go`
- Modify: `server/internal/kube/client.go`
- Modify: `server/internal/kube/client_test.go`

- [ ] **Step 1: Write config-precedence tests.**

Use `t.Setenv` for both kubeconfig variables and assert:

```text
KUBEJOJO_KUBECONFIG=/explicit  and KUBECONFIG=/secondary -> /explicit
KUBEJOJO_KUBECONFIG empty and KUBECONFIG=/first:/second -> /first
both empty -> empty string (selection deferred to kube.NewSharedClient)
```

Do not assert a real home-directory path from `config.Load`.

- [ ] **Step 2: Write injectable client-source tests.**

Introduce tests around an unexported `newSharedClientWithLoaders` function. Provide fake functions, never a real cluster:

```go
type clientConfigLoaders struct {
	inCluster func() (*rest.Config, error)
	homeDir   func() (string, error)
}
```

Assert:

- explicit path never calls `inCluster` and yields `AuthMode=shared-kubeconfig`;
- empty path plus successful in-cluster config yields `AuthMode=in-cluster`, `ConfigPath=""`, `RawConfig.CurrentContext="in-cluster"`;
- empty path plus `rest.ErrNotInCluster` loads `<fake-home>/.kube/config`;
- if both in-cluster and fallback file fail, the error names both source types without printing the local path or bearer tokens.

- [ ] **Step 3: Run and confirm current failures.**

Run: `cd server && go test ./internal/config ./internal/kube -count=1`

Expected: FAIL because empty config currently resolves at config load time and `NewSharedClient` rejects it.

- [ ] **Step 4: Move fallback selection into the client factory.**

Change `kubeconfigPath()` to return only `KUBEJOJO_KUBECONFIG`, then the first non-empty `KUBECONFIG` entry, then `""`. Remove the now-unused `filepath` home fallback from the config package.

Implement this structure in `client.go`:

```go
func NewSharedClient(configPath string, options Options) (*Client, error) {
	return newSharedClientWithLoaders(configPath, options, clientConfigLoaders{
		inCluster: rest.InClusterConfig,
		homeDir:   os.UserHomeDir,
	})
}

func newSharedClientWithLoaders(path string, options Options, loaders clientConfigLoaders) (*Client, error) {
	if strings.TrimSpace(path) != "" {
		client, err := newKubeconfigClient(path, options)
		if err != nil {
			return nil, fmt.Errorf("explicit kubeconfig unavailable: %w", redactConfigPath(err, path))
		}
		return client, nil
	}
	if config, err := loaders.inCluster(); err == nil {
		applyOptions(config, options)
		return newClient(config, "", "in-cluster", clientcmdapiConfig{CurrentContext: "in-cluster"})
	} else {
		home, homeErr := loaders.homeDir()
		if homeErr != nil {
			return nil, fmt.Errorf("in-cluster config: %v; resolve default kubeconfig: %w", err, homeErr)
		}
		fallback := filepath.Join(home, ".kube", "config")
		client, fileErr := newKubeconfigClient(fallback, options)
		if fileErr != nil {
			return nil, fmt.Errorf("in-cluster config unavailable: %v; default kubeconfig unavailable: %w", err, redactConfigPath(fileErr, fallback))
		}
		return client, nil
	}
}
```

Extract `newKubeconfigClient` and `applyOptions` from the current function. Implement `redactConfigPath` so it preserves the wrapped error category while replacing every occurrence of the resolved path with `[redacted]`. Test that an explicit-path failure does not contain that path, and that the fallback error contains both `in-cluster config unavailable` and `default kubeconfig unavailable` without the fake home path. Preserve successful explicit kubeconfig behavior and never log `RESTConfig.BearerToken` or `Client.AccessToken`.

- [ ] **Step 5: Run and commit.**

Run: `cd server && go test ./internal/config ./internal/kube ./internal/server -count=1`

Expected: PASS.

Commit: `git add server/internal/config server/internal/kube && git commit -m "fix(kube): prefer in-cluster service account identity"`

## Task 7: Supply deployable least-privilege Kubernetes manifests and focused docs

**Files:**

- Create: `deploy/kubernetes/namespace.yaml`
- Modify: `deploy/kubernetes/rbac.yaml`
- Create: `deploy/kubernetes/rbac-executor.yaml`
- Create: `deploy/kubernetes/pvc.yaml`
- Modify: `deploy/kubernetes/deployment.yaml`
- Create: `deploy/kubernetes/README.md`
- Create: `scripts/verify-kubernetes-manifests.sh`
- Modify: `README.md`
- Modify: `docs/architecture/single-cluster-access.md`

- [ ] **Step 1: Add a manifest verification script that fails against the current files.**

Create an executable `scripts/verify-kubernetes-manifests.sh` with `set -euo pipefail`. It must:

1. Require `kubectl` with `command -v kubectl`.
2. Run `kubectl apply --dry-run=client --validate=false -f` on namespace, readonly RBAC, PVC, deployment, and service.
3. Run the same command on the opt-in executor RBAC separately.
4. Fail if `deployment.yaml` contains `KUBEJOJO_KUBECONFIG`, `kubejojo-kubeconfig`, or a kubeconfig volume.
5. Assert `rbac.yaml` contains `kind: ClusterRole` and `kind: ClusterRoleBinding`.
6. Assert `deployment.yaml` contains both bootstrap-admin Secret keys.

Use `rg -q` for assertions and print only the failed invariant, never Secret values.

Use this complete script body:

```sh
#!/usr/bin/env bash
set -euo pipefail

fail() {
  printf 'manifest verification failed: %s\n' "$1" >&2
  exit 1
}

command -v kubectl >/dev/null 2>&1 || fail "kubectl is required"
command -v rg >/dev/null 2>&1 || fail "rg is required"

readonly_files=(
  deploy/kubernetes/namespace.yaml
  deploy/kubernetes/rbac.yaml
  deploy/kubernetes/pvc.yaml
  deploy/kubernetes/deployment.yaml
  deploy/kubernetes/service.yaml
)
for file in "${readonly_files[@]}" deploy/kubernetes/rbac-executor.yaml; do
  [[ -f "$file" ]] || fail "missing $file"
done

kubectl apply --dry-run=client --validate=false \
  -f deploy/kubernetes/namespace.yaml \
  -f deploy/kubernetes/rbac.yaml \
  -f deploy/kubernetes/pvc.yaml \
  -f deploy/kubernetes/deployment.yaml \
  -f deploy/kubernetes/service.yaml >/dev/null
kubectl apply --dry-run=client --validate=false \
  -f deploy/kubernetes/rbac-executor.yaml >/dev/null
kubectl auth reconcile --dry-run=client \
  -f deploy/kubernetes/rbac.yaml >/dev/null
kubectl auth reconcile --dry-run=client \
  -f deploy/kubernetes/rbac-executor.yaml >/dev/null

if rg -q 'KUBEJOJO_KUBECONFIG|kubejojo-kubeconfig|name: kubeconfig' deploy/kubernetes/deployment.yaml; then
  fail "deployment must not mount or select kubeconfig"
fi
rg -q '^kind: ClusterRole$' deploy/kubernetes/rbac.yaml || fail "readonly ClusterRole missing"
rg -q '^kind: ClusterRoleBinding$' deploy/kubernetes/rbac.yaml || fail "readonly ClusterRoleBinding missing"
rg -q 'key: bootstrap-admin-user' deploy/kubernetes/deployment.yaml || fail "bootstrap admin user Secret key missing"
rg -q 'key: bootstrap-admin-password' deploy/kubernetes/deployment.yaml || fail "bootstrap admin password Secret key missing"

printf 'kubernetes manifests verified\n'
```

- [ ] **Step 2: Run it and confirm failure.**

Run: `./scripts/verify-kubernetes-manifests.sh`

Expected: FAIL because the namespace/PVC/executor files are missing and the deployment still mounts kubeconfig.

- [ ] **Step 3: Add Namespace and PVC.**

Create `namespace.yaml` exactly as:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: kubejojo
```

Create `pvc.yaml` exactly as:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: kubejojo-data
  namespace: kubejojo
spec:
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 5Gi
```

Do not set a cluster-specific storage class.

- [ ] **Step 4: Replace namespaced read RBAC with cluster-scoped read RBAC.**

In `rbac.yaml`, keep only the default `kubejojo-readonly` ServiceAccount, then define a `ClusterRole` and `ClusterRoleBinding`. Preserve read verbs for the resources currently listed, including cluster-scoped nodes, namespaces, persistentvolumes, storageclasses, ingressclasses, and the namespaced resources across all namespaces. Do not add any create/update/patch/delete verb and do not add `pods/exec`.

- [ ] **Step 5: Isolate the opt-in executor identity.**

In `rbac-executor.yaml`, create:

- ServiceAccount `kubejojo-executor`;
- a ClusterRoleBinding from that ServiceAccount to `kubejojo-readonly`;
- ClusterRole `kubejojo-executor-write` with only `get/list/watch/update/patch` on `apps/deployments` and `batch/cronjobs`;
- a ClusterRoleBinding from the executor ServiceAccount to that write role.

Do not grant Pod exec, Secret mutation, namespace mutation, wildcard resources, or wildcard verbs.

- [ ] **Step 6: Make Deployment use ServiceAccount identity and bootstrap secrets.**

Delete the `KUBEJOJO_KUBECONFIG` env, kubeconfig volume mount, and kubeconfig Secret volume. Keep `serviceAccountName: kubejojo-readonly`. Add:

```yaml
- name: KUBEJOJO_BOOTSTRAP_ADMIN_USER
  valueFrom:
    secretKeyRef:
      name: kubejojo-secrets
      key: bootstrap-admin-user
- name: KUBEJOJO_BOOTSTRAP_ADMIN_PASSWORD
  valueFrom:
    secretKeyRef:
      name: kubejojo-secrets
      key: bootstrap-admin-password
```

Keep the optional `llm-api-key` reference, data PVC, non-root user, read-only root filesystem, dropped capabilities, and resource bounds.

- [ ] **Step 7: Document exact install and executor opt-in.**

In `deploy/kubernetes/README.md`, document these commands in order:

```sh
kubectl apply -f deploy/kubernetes/namespace.yaml
kubectl -n kubejojo create secret generic kubejojo-secrets \
  --from-literal=bootstrap-admin-user="$KUBEJOJO_ADMIN_USER" \
  --from-literal=bootstrap-admin-password="$KUBEJOJO_ADMIN_PASSWORD"
kubectl apply -f deploy/kubernetes/rbac.yaml
kubectl apply -f deploy/kubernetes/pvc.yaml
kubectl apply -f deploy/kubernetes/deployment.yaml
kubectl apply -f deploy/kubernetes/service.yaml
```

Explain that default mode is cluster-wide read-only and therefore execution/rollback/Pod exec fail at Kubernetes authorization. For executor mode, apply `rbac-executor.yaml` and patch `serviceAccountName` to `kubejojo-executor`; execution remains limited to Deployment/CronJob writes and Pod exec remains denied.

Update the root README and single-cluster architecture doc only where they describe identity precedence, default install, env variables, and RBAC. Remove claims that the provided manifest requires a shared kubeconfig Secret. Do not rewrite unrelated stage-two documentation.

- [ ] **Step 8: Validate and commit.**

Run: `chmod +x scripts/verify-kubernetes-manifests.sh && ./scripts/verify-kubernetes-manifests.sh`

Expected: PASS with client-side dry-run success for every manifest group.

Run: `rg -n 'KUBEJOJO_KUBECONFIG|kubejojo-kubeconfig' deploy/kubernetes/deployment.yaml`

Expected: no output, exit 1.

Commit: `git add deploy/kubernetes scripts/verify-kubernetes-manifests.sh README.md docs/architecture/single-cluster-access.md && git commit -m "fix(deploy): use least-privilege service account identity"`

## Task 8: Run the stage-one acceptance gate and record evidence

**Files:**

- Create: `docs/aiops/acceptance/2026-08-02-authorization-consistency-closure.md`

- [ ] **Step 1: Run formatting and static checks.**

Run:

```sh
cd server
gofmt -w internal/server/platform_rbac.go internal/server/platform_rbac_test.go \
  internal/aiops/repository.go internal/aiops/repository_test.go \
  internal/aiops/service.go internal/aiops/service_test.go \
  internal/aiops/roles.go internal/aiops/risk_review.go internal/aiops/risk_review_test.go \
  internal/aiops/workflow.go internal/aiops/workflow_test.go \
  internal/remediation/service.go internal/remediation/service_test.go \
  internal/kube/client.go internal/kube/client_test.go \
  internal/config/config.go internal/config/config_test.go \
  internal/server/router.go internal/server/aiops_routes.go \
  internal/server/experiment_routes.go internal/server/aiops_evidence_routes_test.go \
  internal/server/experiment_routes_test.go
go vet ./...
go test -race ./...
go test -race ./internal/aiops ./internal/remediation ./internal/server -count=20
```

Expected: gofmt produces no subsequent diff, vet exits 0, every package passes under race detection, and the focused concurrency/authorization packages pass all 20 repetitions.

- [ ] **Step 2: Run frontend and release-build gates.**

Run:

```sh
cd web
npm ci
npm test -- --run
npm run build
cd ..
SKIP_NPM_INSTALL=1 ./scripts/build-release.sh
```

Expected: all frontend tests pass, Vite exits 0, and the release archive is created under `server/dist/release/`. Record the exact archive name. The known bundle-size warning is non-blocking and remains stage-two work.

- [ ] **Step 3: Run manifest checks and authorization simulation.**

Run the local manifest check:

```sh
./scripts/verify-kubernetes-manifests.sh
```

When a disposable validation cluster/context is explicitly selected, apply the manifests and run:

```sh
kubectl auth can-i list nodes --as=system:serviceaccount:kubejojo:kubejojo-readonly
kubectl auth can-i patch deployments -n default --as=system:serviceaccount:kubejojo:kubejojo-readonly
kubectl auth can-i delete namespaces --as=system:serviceaccount:kubejojo:kubejojo-readonly
kubectl auth can-i list nodes --as=system:serviceaccount:kubejojo:kubejojo-executor
kubectl auth can-i patch deployments -n default --as=system:serviceaccount:kubejojo:kubejojo-executor
kubectl auth can-i update cronjobs -n default --as=system:serviceaccount:kubejojo:kubejojo-executor
kubectl auth can-i create pods/exec -n default --as=system:serviceaccount:kubejojo:kubejojo-executor
kubectl auth can-i delete namespaces --as=system:serviceaccount:kubejojo:kubejojo-executor
```

Expected in order: `yes, no, no, yes, yes, yes, no, no`.

Do not apply manifests to the user's connected cluster without explicit authorization. If no disposable cluster is authorized, record the `kubectl auth can-i` block as `NOT RUN — external cluster mutation not authorized`; do not mark it passed.

- [ ] **Step 4: Record acceptance evidence.**

Create the acceptance file with:

- branch name and exact commit SHA;
- date and validation environment;
- each command above with `PASS`, `FAIL`, or `NOT RUN`;
- exact test counts and release archive path;
- role-matrix integration results;
- CAS concurrency result and audit-count result;
- policy-over-model test result;
- manifest dry-run result and, if authorized, the eight `can-i` values;
- remaining stage-two items listed verbatim from the constraints section.

Do not write `PASS` from expectation alone; paste only observed summaries and redact credentials/tokens.

- [ ] **Step 5: Check diff hygiene and commit evidence.**

Run:

```sh
git diff --check
git status --short
rg -n 'TODO|FIXME|example-token|changeme' \
  server/internal web/src/modules/aiops deploy/kubernetes \
  docs/aiops/acceptance/2026-08-02-authorization-consistency-closure.md
```

Expected: `git diff --check` has no output; only the new acceptance file is uncommitted; the placeholder scan has no unsafe placeholder credential or unfinished implementation marker. Documentation command examples may use shell variable names, but never literal passwords.

Commit: `git add docs/aiops/acceptance/2026-08-02-authorization-consistency-closure.md && git commit -m "docs: record authorization closure acceptance"`

- [ ] **Step 6: Final branch verification.**

Run:

```sh
git status --short --branch
git log --oneline --decorate -8
```

Expected: clean feature worktree, all eight task commits visible, and no work performed directly on `main`.

## Plan self-review checklist

- [ ] Every approved design item maps to a task: platform role matrix (Task 1), atomic state CAS (Task 2), deterministic risk output (Task 3), approval-time recheck (Task 4), frontend authority source (Task 5), identity precedence (Task 6), deployable RBAC/PVC/bootstrap configuration (Task 7), and evidence-based acceptance (Task 8).
- [ ] Stage-two work is explicitly excluded and not smuggled into implementation tasks.
- [ ] All new backend JSON fields have matching TypeScript fields and identical camelCase tags.
- [ ] All new repository and service errors have explicit HTTP mappings and tests.
- [ ] Every task begins with a failing test/check, implements the smallest behavior, re-runs the check, and commits a bounded change.
- [ ] No step contains an unresolved implementation marker, path, role, or literal credential.
- [ ] External-cluster mutation remains conditional on explicit authorization and cannot be misreported as passed.
