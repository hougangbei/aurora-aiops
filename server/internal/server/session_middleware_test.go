package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/heihuzicity-tech/kubejojo/server/internal/auth"
	"github.com/heihuzicity-tech/kubejojo/server/internal/kube"
	"github.com/heihuzicity-tech/kubejojo/server/internal/response"
	"github.com/heihuzicity-tech/kubejojo/server/internal/service"
	"github.com/heihuzicity-tech/kubejojo/server/internal/store"
)

func seedUser(t *testing.T, repo auth.Repository, id, username, password string, role auth.Role) {
	t.Helper()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateUser(context.Background(), auth.User{
		ID:           id,
		Username:     username,
		PasswordHash: string(passwordHash),
		Role:         role,
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}
}

// newAuthTestRouter builds a router with platform auth routes plus one
// role-gated route so tests can exercise login, session, and RBAC.
func newAuthTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := store.Open(filepath.Join(t.TempDir(), "auth-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	repo := auth.NewRepository(db)
	authService := auth.NewService(repo, sessionTTL, time.Now)
	seedUser(t, repo, "user-admin", "admin", "correct-password", auth.RoleAdmin)
	seedUser(t, repo, "user-operator", "operator", "correct-password", auth.RoleOperator)
	seedUser(t, repo, "user-viewer", "viewer", "correct-password", auth.RoleViewer)

	kubeClient := kubefake.NewSimpleClientset(&corev1.Node{})
	clusterService := service.NewClusterService(&kube.Client{
		Kubernetes: kubeClient,
		Metrics:    metricsfake.NewSimpleClientset(),
		RESTConfig: &rest.Config{},
	})

	router := gin.New()
	api := router.Group("/api/v1")
	registerAuthRoutes(api, authService, sessionTTL)

	authorized := api.Group("/")
	authorized.Use(RequireSession(authService, clusterService))
	{
		registerSessionAuthRoutes(authorized, authService)
		authorized.GET("/test/exec",
			RequireRoles(auth.RoleOperator, auth.RoleAdmin),
			func(c *gin.Context) {
				c.JSON(http.StatusOK, response.Success(gin.H{"ok": true}))
			})
	}

	return router
}

func doLogin(t *testing.T, router *gin.Engine, username, password string) (*http.Response, *http.Cookie) {
	t.Helper()
	body := `{"username":"` + username + `","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	resp := rec.Result()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
		}
	}
	return resp, cookie
}

func TestPasswordLoginSetsHttpOnlySession(t *testing.T) {
	router := newAuthTestRouter(t)
	resp, cookie := doLogin(t, router, "admin", "correct-password")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, readBody(resp))
	}
	if cookie == nil {
		t.Fatal("no session cookie set")
	}
	if !cookie.HttpOnly {
		t.Fatalf("cookie not HttpOnly: %+v", cookie)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie SameSite=%v", cookie.SameSite)
	}
	if len(cookie.Value) < 40 {
		t.Fatalf("session value too short: %d", len(cookie.Value))
	}
}

func TestLoginRejectsInvalidCredentials(t *testing.T) {
	router := newAuthTestRouter(t)
	for _, password := range []string{"wrong-password", "short"} {
		resp, _ := doLogin(t, router, "admin", password)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("password=%q status=%d", password, resp.StatusCode)
		}
		if !strings.Contains(readBody(resp), `"code":"INVALID_CREDENTIALS"`) {
			t.Fatalf("password=%q body=%s", password, readBody(resp))
		}
	}
}

func TestMeRequiresSession(t *testing.T) {
	router := newAuthTestRouter(t)

	// No cookie -> 401.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	// With a valid session cookie -> 200 and the platform user.
	_, cookie := doLogin(t, router, "admin", "correct-password")
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"username":"admin"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"role":"admin"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	router := newAuthTestRouter(t)
	_, cookie := doLogin(t, router, "admin", "correct-password")

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutReq.AddCookie(cookie)
	logoutRec := httptest.NewRecorder()
	router.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout status=%d", logoutRec.Code)
	}

	// The same cookie value must no longer authenticate.
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.AddCookie(cookie)
	meRec := httptest.NewRecorder()
	router.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusUnauthorized {
		t.Fatalf("status after logout=%d body=%s", meRec.Code, meRec.Body.String())
	}
}

func TestViewerCannotAccessExecApi(t *testing.T) {
	router := newAuthTestRouter(t)
	_, cookie := doLogin(t, router, "viewer", "correct-password")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test/exec", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"FORBIDDEN"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestOperatorCanAccessExecApi(t *testing.T) {
	router := newAuthTestRouter(t)
	_, cookie := doLogin(t, router, "operator", "correct-password")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test/exec", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func readBody(resp *http.Response) string {
	if resp.Body == nil {
		return ""
	}
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	resp.Body.Close()
	return string(buf[:n])
}
