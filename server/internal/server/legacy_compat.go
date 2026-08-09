package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const legacySessionCookieName = "kubejojo_session"

func readSessionCookie(c *gin.Context) (string, error) {
	raw, err := c.Cookie(sessionCookieName)
	if err == nil && strings.TrimSpace(raw) != "" {
		return raw, nil
	}
	return c.Cookie(legacySessionCookieName)
}

func clearSessionCookies(c *gin.Context) {
	for _, name := range []string{sessionCookieName, legacySessionCookieName} {
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			MaxAge:   -1,
			SameSite: http.SameSiteLaxMode,
		})
	}
}
