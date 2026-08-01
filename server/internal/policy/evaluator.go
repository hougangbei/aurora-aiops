package policy

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrActionDenied = errors.New("action denied by policy")

// Evaluation is the deterministic outcome of checking an action against the
// allowlist and fixed rules.
type Evaluation struct {
	Allowed bool
	Risk    RiskLevel
	Reason  string
}

// Evaluate checks a single action against the allowlist. incidentNamespace is
// the target namespace of the owning Incident; an action that targets any other
// namespace (a cross-incident write) is denied. Risk is assigned by these rules,
// not by the model.
func Evaluate(action Action, incidentNamespace string) Evaluation {
	if action.Namespace == "" || action.Namespace != incidentNamespace {
		return denied("action namespace must match the incident namespace")
	}
	if isWildcard(action.Namespace) || isWildcard(action.ResourceName) {
		return denied("wildcard targets are not allowed")
	}
	if action.ResourceKind == "Secret" {
		return denied("secret access is not allowed")
	}

	switch action.Kind {
	case ActionRestartDeployment:
		if len(action.Parameters) > 0 {
			return denied("restart_deployment does not accept parameters")
		}
		if action.ResourceKind != "Deployment" || action.ResourceName == "" {
			return denied("restart_deployment requires a Deployment target")
		}
		return allowed(RiskMedium, "restart deployment")

	case ActionScaleDeployment:
		replicas, ok := action.Parameters["replicas"]
		if !ok || replicas == "" {
			return denied("scale_deployment requires the replicas parameter")
		}
		n, err := strconv.Atoi(replicas)
		if err != nil || n < 0 || n > 100 {
			return denied("scale_deployment replicas must be an integer between 0 and 100")
		}
		if action.ResourceKind != "Deployment" || action.ResourceName == "" {
			return denied("scale_deployment requires a Deployment target")
		}
		return allowed(RiskMedium, "scale deployment")

	case ActionSuspendCronJob:
		if len(action.Parameters) > 0 {
			return denied("suspend_cronjob does not accept parameters")
		}
		if action.ResourceKind != "CronJob" || action.ResourceName == "" {
			return denied("suspend_cronjob requires a CronJob target")
		}
		return allowed(RiskLow, "suspend cronjob")

	default:
		return denied(fmt.Sprintf("action kind %q is not in the allowlist", action.Kind))
	}
}

func allowed(risk RiskLevel, reason string) Evaluation {
	return Evaluation{Allowed: true, Risk: risk, Reason: reason}
}

func denied(reason string) Evaluation {
	return Evaluation{Allowed: false, Reason: reason}
}

func isWildcard(value string) bool {
	return value == "*" || strings.Contains(value, "*")
}
