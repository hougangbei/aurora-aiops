package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/heihuzicity-tech/aurora-aiops/server/internal/auth"
	"github.com/heihuzicity-tech/aurora-aiops/server/internal/response"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authUserResponse struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      auth.Role `json:"role"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// registerAuthRoutes registers the anonymous login route. me and logout live
// inside the session-protected group via registerSessionAuthRoutes.
func registerAuthRoutes(
	group *gin.RouterGroup,
	authService *auth.Service,
	sessionTTL time.Duration,
) {
	group.POST("/auth/login", handlePasswordLogin(authService, sessionTTL))
}

// registerSessionAuthRoutes registers routes that require a valid session.
func registerSessionAuthRoutes(group *gin.RouterGroup, authService *auth.Service) {
	group.GET("/auth/me", handleAuthMe())
	group.POST("/auth/logout", handleAuthLogout(authService))
}

func handlePasswordLogin(authService *auth.Service, sessionTTL time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req loginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, response.Failure("INVALID_LOGIN_REQUEST", "请求体格式不正确"))
			return
		}

		raw, user, err := authService.Login(c.Request.Context(), req.Username, req.Password)
		if err != nil {
			if errors.Is(err, auth.ErrUserDisabled) {
				c.JSON(http.StatusUnauthorized, response.Failure("INVALID_CREDENTIALS", "账号已被禁用"))
				return
			}
			c.JSON(http.StatusUnauthorized, response.Failure("INVALID_CREDENTIALS", "用户名或密码错误"))
			return
		}

		expiresAt := authService.SessionExpiry() // see comment in Login for why TTL is stored server-side
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     sessionCookieName,
			Value:    raw,
			Path:     "/",
			HttpOnly: true,
			Secure:   c.Request.TLS != nil,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(sessionTTL.Seconds()),
		})

		c.JSON(http.StatusOK, response.Success(authUserResponse{
			ID:        user.ID,
			Username:  user.Username,
			Role:      user.Role,
			ExpiresAt: expiresAt,
		}))
	}
}

func handleAuthMe() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := ActorFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, response.Failure("UNAUTHORIZED", "未登录或会话已过期"))
			return
		}
		c.JSON(http.StatusOK, response.Success(authUserResponse{
			ID:       user.ID,
			Username: user.Username,
			Role:     user.Role,
		}))
	}
}

// currentActorName returns the platform username recorded in the request
// context. It is used as the audit actor and for update ownership; it is not a
// Kubernetes identity.
func currentActorName(c *gin.Context) string {
	user, ok := ActorFromContext(c)
	if !ok {
		return ""
	}
	return user.Username
}

func handleAuthLogout(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := readSessionCookie(c)
		if err == nil && strings.TrimSpace(raw) != "" {
			_ = authService.Logout(c.Request.Context(), raw)
		}
		clearSessionCookies(c)
		c.JSON(http.StatusOK, response.Success(gin.H{"ok": true}))
	}
}
