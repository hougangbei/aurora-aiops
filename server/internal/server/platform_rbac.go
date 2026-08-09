package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/hougangbei/aurora-aiops/server/internal/auth"
	"github.com/hougangbei/aurora-aiops/server/internal/response"
)

// operatorWritePaths is the explicit allowlist of mutations that an operator
// may perform. Every other unsafe request falls through to the admin default.
// Prefix matching is intentionally avoided: a newly registered mutation must
// land in the admin bucket until it is deliberately reviewed and added here.
var operatorWritePaths = map[string]struct{}{
	http.MethodPost + " /api/v1/aiops/incidents":                         {},
	http.MethodPost + " /api/v1/aiops/incidents/:id/reanalyze":           {},
	http.MethodPost + " /api/v1/aiops/incidents/:id/approve-remediation": {},
	http.MethodPost + " /api/v1/aiops/incidents/:id/reject-remediation":  {},
	http.MethodPost + " /api/v1/cluster/connection/test":                 {},
	http.MethodPost + " /api/v1/assets/servers/:id/test-connection":      {},
	http.MethodPost + " /api/v1/assets/servers/:id/collect":              {},
}

// requiredPlatformRoles returns the roles permitted to handle a request, or nil
// when the request is read-only or anonymous and therefore not gated here. The
// fullPath is the Gin route template (c.FullPath()), not the concrete URL.
func requiredPlatformRoles(method, fullPath string) []auth.Role {
	if method == http.MethodHead || method == http.MethodOptions {
		return nil
	}
	if method == http.MethodGet {
		if fullPath == "/api/v1/pods/:namespace/:name/exec/ws" ||
			fullPath == "/api/v1/secrets/:namespace/:name/yaml" {
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

// EnforcePlatformRBAC is the default-deny platform authorization layer. It runs
// after RequireSession and gates every unsafe route by the role policy above.
// Read-only and anonymous routes pass straight through; route-local guards
// remain as defense in depth and as auditable approvals of each registration.
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
