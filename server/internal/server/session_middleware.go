package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/heihuzicity-tech/kubejojo/server/internal/auth"
	"github.com/heihuzicity-tech/kubejojo/server/internal/response"
	"github.com/heihuzicity-tech/kubejojo/server/internal/service"
)

const (
	sessionCookieName = "kubejojo_session"
	actorContextKey   = "authUser"
	// sessionTTL bounds platform session lifetime. It is also passed to
	// auth.NewService so the cookie MaxAge and the server-side expiry agree.
	sessionTTL = 8 * time.Hour
)

// RequireSession authenticates the HttpOnly session cookie, puts the platform
// user and the shared ClusterService into the request context, and aborts with
// 401 UNAUTHORIZED when the session is missing or invalid.
func RequireSession(authService *auth.Service, clusterService *service.ClusterService) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := c.Cookie(sessionCookieName)
		if err != nil || raw == "" {
			c.JSON(http.StatusUnauthorized, response.Failure("UNAUTHORIZED", "未登录或会话已过期"))
			c.Abort()
			return
		}

		user, err := authService.Authenticate(c.Request.Context(), raw)
		if err != nil {
			c.JSON(http.StatusUnauthorized, response.Failure("UNAUTHORIZED", "未登录或会话已过期"))
			c.Abort()
			return
		}

		c.Set(actorContextKey, user)
		c.Set(clusterServiceContextKey, clusterService)
		c.Next()
	}
}

// RequireRoles guards a route so that only actors holding at least one of the
// given roles may proceed. It must run after RequireSession.
func RequireRoles(roles ...auth.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
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

// ActorFromContext returns the authenticated platform user stored by
// RequireSession.
func ActorFromContext(c *gin.Context) (auth.User, bool) {
	value, ok := c.Get(actorContextKey)
	if !ok {
		return auth.User{}, false
	}
	user, ok := value.(auth.User)
	return user, ok
}
