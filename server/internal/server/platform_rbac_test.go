package server

import (
	"net/http"
	"slices"
	"testing"

	"github.com/heihuzicity-tech/aurora-aiops/server/internal/auth"
)

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
		{http.MethodGet, "/api/v1/secrets/:namespace/:name/yaml", []auth.Role{auth.RoleAdmin}},
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
