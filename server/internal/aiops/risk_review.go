package aiops

import (
	"github.com/heihuzicity-tech/kubejojo/server/internal/policy"
)

// BuildEffectiveRiskReview is the only source of approvability. ModelReview is
// retained for explanation but never changes EffectiveRisk or Approvable: those
// two fields are derived entirely from policy.Evaluate over the remediation
// plan. A denied action is recorded as high risk and clears Approvable.
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
			Kind:         proposed.Kind,
			Namespace:    proposed.Namespace,
			ResourceKind: proposed.ResourceKind,
			ResourceName: proposed.ResourceName,
			Parameters:   proposed.Parameters,
		}, incidentNamespace)
		risk := string(evaluation.Risk)
		if !evaluation.Allowed {
			risk = string(policy.RiskHigh)
			review.Approvable = false
			review.Blockers = append(review.Blockers, "policy: "+evaluation.Reason)
		}
		review.Actions = append(review.Actions, PolicyActionReview{
			Kind:         proposed.Kind,
			ResourceKind: proposed.ResourceKind,
			ResourceName: proposed.ResourceName,
			Allowed:      evaluation.Allowed,
			Risk:         risk,
			Reason:       evaluation.Reason,
		})
		if riskRank(risk) > riskRank(review.EffectiveRisk) {
			review.EffectiveRisk = risk
		}
	}
	if review.EffectiveRisk == string(policy.RiskHigh) {
		review.Approvable = false
	}
	// The model's blockers are carried as advisory context only.
	for _, blocker := range model.Blockers {
		review.Blockers = append(review.Blockers, "model: "+blocker)
	}
	return review
}

// riskRank orders risk levels so the effective review takes the maximum across
// all actions. Unknown values are treated as high so an unexpected risk string
// can never lower the effective risk.
func riskRank(risk string) int {
	switch risk {
	case string(policy.RiskLow):
		return 0
	case string(policy.RiskMedium):
		return 1
	default:
		return 2
	}
}
