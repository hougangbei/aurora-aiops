package aiops

import (
	"testing"
)

func TestBuildEffectiveRiskReview(t *testing.T) {
	// Case 1: the model says low/approved, but policy denies on a namespace
	// mismatch. The deterministic gate must win: high and not approvable.
	t.Run("policy_denies_overrides_model", func(t *testing.T) {
		model := RiskReviewOutput{RiskLevel: "low", Approved: true}
		remediation := RemediationOutput{Actions: []RemediationAction{{
			Command: "restart deployment", Reason: "r", Risk: "low",
			Kind: "restart_deployment", Namespace: "other",
			ResourceKind: "Deployment", ResourceName: "api-0",
		}}}
		got := BuildEffectiveRiskReview(model, remediation, "default")
		if got.EffectiveRisk != "high" {
			t.Errorf("effectiveRisk=%q want high", got.EffectiveRisk)
		}
		if got.Approvable {
			t.Error("approvable=true want false")
		}
		if !got.ModelReview.Approved || got.ModelReview.RiskLevel != "low" {
			t.Errorf("model review not preserved nested: %+v", got.ModelReview)
		}
	})

	// Case 2: the model says critical/not-approved, but policy permits a
	// suspend_cronjob. The deterministic gate permits it: low and approvable.
	// The model output is retained nested and its blockers shown as advisory.
	t.Run("policy_permits_overrides_model", func(t *testing.T) {
		model := RiskReviewOutput{RiskLevel: "critical", Approved: false, Blockers: []string{"model unsure"}}
		remediation := RemediationOutput{Actions: []RemediationAction{{
			Command: "suspend cronjob", Reason: "r", Risk: "high",
			Kind: "suspend_cronjob", Namespace: "default",
			ResourceKind: "CronJob", ResourceName: "job-0",
		}}}
		got := BuildEffectiveRiskReview(model, remediation, "default")
		if got.EffectiveRisk != "low" {
			t.Errorf("effectiveRisk=%q want low", got.EffectiveRisk)
		}
		if !got.Approvable {
			t.Error("approvable=false want true")
		}
		found := false
		for _, b := range got.Blockers {
			if b == "model: model unsure" {
				found = true
			}
		}
		if !found {
			t.Errorf("model advisory blocker missing: %v", got.Blockers)
		}
	})

	// Case 3: a single display-only action without structured fields is denied.
	t.Run("display_only_action_denied", func(t *testing.T) {
		model := RiskReviewOutput{RiskLevel: "low", Approved: true}
		remediation := RemediationOutput{Actions: []RemediationAction{{
			Command: "investigate manually", Reason: "r", Risk: "low",
		}}}
		got := BuildEffectiveRiskReview(model, remediation, "default")
		if got.EffectiveRisk != "high" {
			t.Errorf("effectiveRisk=%q want high", got.EffectiveRisk)
		}
		if got.Approvable {
			t.Error("approvable=true want false")
		}
	})

	// Case 4: no actions at all is not approvable and carries an explicit blocker.
	t.Run("empty_actions_not_approvable", func(t *testing.T) {
		model := RiskReviewOutput{RiskLevel: "low", Approved: true}
		got := BuildEffectiveRiskReview(model, RemediationOutput{}, "default")
		if got.Approvable {
			t.Error("approvable=true want false")
		}
		if got.EffectiveRisk != "high" {
			t.Errorf("effectiveRisk=%q want high", got.EffectiveRisk)
		}
		found := false
		for _, b := range got.Blockers {
			if b == "policy: remediation has no actions" {
				found = true
			}
		}
		if !found {
			t.Errorf("missing empty-actions blocker: %v", got.Blockers)
		}
	})

	// Case 5: two allowed actions, low plus medium, aggregate to medium and approvable.
	t.Run("two_allowed_low_and_medium", func(t *testing.T) {
		model := RiskReviewOutput{RiskLevel: "low", Approved: true}
		remediation := RemediationOutput{Actions: []RemediationAction{
			{Command: "suspend cronjob", Reason: "r", Risk: "low", Kind: "suspend_cronjob", Namespace: "default", ResourceKind: "CronJob", ResourceName: "job-0"},
			{Command: "restart deployment", Reason: "r", Risk: "medium", Kind: "restart_deployment", Namespace: "default", ResourceKind: "Deployment", ResourceName: "api-0"},
		}}
		got := BuildEffectiveRiskReview(model, remediation, "default")
		if got.EffectiveRisk != "medium" {
			t.Errorf("effectiveRisk=%q want medium", got.EffectiveRisk)
		}
		if !got.Approvable {
			t.Error("approvable=false want true")
		}
	})
}
