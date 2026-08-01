package policy

import "testing"

func TestEvaluateAllowedActions(t *testing.T) {
	cases := []struct {
		name   string
		action Action
		want   RiskLevel
	}{
		{
			name:   "restart deployment",
			action: Action{Kind: ActionRestartDeployment, Namespace: "default", ResourceKind: "Deployment", ResourceName: "api"},
			want:   RiskMedium,
		},
		{
			name:   "scale deployment to 3",
			action: Action{Kind: ActionScaleDeployment, Namespace: "default", ResourceKind: "Deployment", ResourceName: "api", Parameters: map[string]string{"replicas": "3"}},
			want:   RiskMedium,
		},
		{
			name:   "scale deployment to 0",
			action: Action{Kind: ActionScaleDeployment, Namespace: "default", ResourceKind: "Deployment", ResourceName: "api", Parameters: map[string]string{"replicas": "0"}},
			want:   RiskMedium,
		},
		{
			name:   "scale deployment to 100",
			action: Action{Kind: ActionScaleDeployment, Namespace: "default", ResourceKind: "Deployment", ResourceName: "api", Parameters: map[string]string{"replicas": "100"}},
			want:   RiskMedium,
		},
		{
			name:   "suspend cronjob",
			action: Action{Kind: ActionSuspendCronJob, Namespace: "default", ResourceKind: "CronJob", ResourceName: "backup"},
			want:   RiskLow,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.action, "default")
			if !got.Allowed {
				t.Fatalf("expected allowed, got denied: %s", got.Reason)
			}
			if got.Risk != tc.want {
				t.Fatalf("risk=%s want %s", got.Risk, tc.want)
			}
		})
	}
}

func TestEvaluateDeniedActions(t *testing.T) {
	cases := []struct {
		name    string
		action  Action
		context string
	}{
		{
			name:    "unknown kind like a shell command",
			action:  Action{Kind: "shell", Namespace: "default", ResourceKind: "Pod", ResourceName: "api", Parameters: map[string]string{"command": "rm -rf /"}},
			context: "default",
		},
		{
			name:    "exec into pod",
			action:  Action{Kind: "exec", Namespace: "default", ResourceKind: "Pod", ResourceName: "api"},
			context: "default",
		},
		{
			name:    "delete namespace",
			action:  Action{Kind: "delete_namespace", Namespace: "default", ResourceKind: "Namespace", ResourceName: "default"},
			context: "default",
		},
		{
			name:    "read secret",
			action:  Action{Kind: "read_secret", Namespace: "default", ResourceKind: "Secret", ResourceName: "db-credentials"},
			context: "default",
		},
		{
			name:    "restart with parameters",
			action:  Action{Kind: ActionRestartDeployment, Namespace: "default", ResourceKind: "Deployment", ResourceName: "api", Parameters: map[string]string{"force": "true"}},
			context: "default",
		},
		{
			name:    "restart wrong resource kind",
			action:  Action{Kind: ActionRestartDeployment, Namespace: "default", ResourceKind: "Pod", ResourceName: "api"},
			context: "default",
		},
		{
			name:    "scale missing replicas",
			action:  Action{Kind: ActionScaleDeployment, Namespace: "default", ResourceKind: "Deployment", ResourceName: "api"},
			context: "default",
		},
		{
			name:    "scale replicas above 100",
			action:  Action{Kind: ActionScaleDeployment, Namespace: "default", ResourceKind: "Deployment", ResourceName: "api", Parameters: map[string]string{"replicas": "101"}},
			context: "default",
		},
		{
			name:    "scale replicas negative",
			action:  Action{Kind: ActionScaleDeployment, Namespace: "default", ResourceKind: "Deployment", ResourceName: "api", Parameters: map[string]string{"replicas": "-1"}},
			context: "default",
		},
		{
			name:    "scale replicas not a number",
			action:  Action{Kind: ActionScaleDeployment, Namespace: "default", ResourceKind: "Deployment", ResourceName: "api", Parameters: map[string]string{"replicas": "many"}},
			context: "default",
		},
		{
			name:    "wildcard target",
			action:  Action{Kind: ActionRestartDeployment, Namespace: "default", ResourceKind: "Deployment", ResourceName: "*"},
			context: "default",
		},
		{
			name:    "empty namespace",
			action:  Action{Kind: ActionRestartDeployment, Namespace: "", ResourceKind: "Deployment", ResourceName: "api"},
			context: "default",
		},
		{
			name:    "cross incident namespace",
			action:  Action{Kind: ActionRestartDeployment, Namespace: "other-ns", ResourceKind: "Deployment", ResourceName: "api"},
			context: "default",
		},
		{
			name:    "etcd access",
			action:  Action{Kind: "etcd_put", Namespace: "default", ResourceKind: "Etcd", ResourceName: "keys"},
			context: "default",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.action, tc.context)
			if got.Allowed {
				t.Fatalf("expected denied, got allowed: %+v", tc.action)
			}
			if got.Reason == "" {
				t.Fatal("denied action must carry a reason")
			}
		})
	}
}
