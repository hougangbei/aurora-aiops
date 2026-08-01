package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/heihuzicity-tech/kubejojo/server/internal/auth"
	"github.com/heihuzicity-tech/kubejojo/server/internal/cluster"
	"github.com/heihuzicity-tech/kubejojo/server/internal/kube"
	"github.com/heihuzicity-tech/kubejojo/server/internal/service"
	"github.com/heihuzicity-tech/kubejojo/server/internal/store"
)

func newClusterTestRouter(
	t *testing.T,
	kubeClient *kubefake.Clientset,
	metricsClient *metricsfake.Clientset,
) (*gin.Engine, *auth.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := store.Open(filepath.Join(t.TempDir(), "cluster-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	repo := auth.NewRepository(db)
	authService := auth.NewService(repo, sessionTTL, time.Now)
	seedUser(t, repo, "user-admin", "admin", "correct-password", auth.RoleAdmin)
	seedUser(t, repo, "user-viewer", "viewer", "correct-password", auth.RoleViewer)
	seedUser(t, repo, "user-operator", "operator", "correct-password", auth.RoleOperator)

	client := &kube.Client{
		Kubernetes: kubeClient,
		Metrics:    metricsClient,
		RESTConfig: &rest.Config{},
	}
	clusterService := service.NewClusterService(client)
	probe := cluster.NewProbe(
		kubeClient.Discovery(),
		kubeClient,
		metricsClient,
		"lab",
		"https://10.0.0.101:6443",
		10*time.Second,
	)

	router := gin.New()
	api := router.Group("/api/v1")
	registerAuthRoutes(api, authService, sessionTTL)

	authorized := api.Group("/")
	authorized.Use(RequireSession(authService, clusterService))
	{
		registerClusterRoutes(authorized, probe, clusterService)
	}
	return router, authService
}

func loginCookie(t *testing.T, router *gin.Engine, username string) *http.Cookie {
	t.Helper()
	_, cookie := doLogin(t, router, username, "correct-password")
	if cookie == nil {
		t.Fatal("login did not set a session cookie")
	}
	return cookie
}

func doGetWithCookie(router *gin.Engine, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestConnectionRequiresSession(t *testing.T) {
	router, _ := newClusterTestRouter(t, kubefake.NewSimpleClientset(), metricsfake.NewSimpleClientset())
	rec := doGetWithCookie(router, "/api/v1/cluster/connection", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestConnectionReportsConnected(t *testing.T) {
	kubeClient := kubefake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}})
	router, _ := newClusterTestRouter(t, kubeClient, metricsfake.NewSimpleClientset())
	cookie := loginCookie(t, router, "viewer")

	rec := doGetWithCookie(router, "/api/v1/cluster/connection", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"state":"connected"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestConnectionDegradesWithoutMetrics(t *testing.T) {
	kubeClient := kubefake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}})
	metricsClient := metricsfake.NewSimpleClientset()
	metricsClient.PrependReactor("list", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewServiceUnavailable("metrics down")
	})
	router, _ := newClusterTestRouter(t, kubeClient, metricsClient)
	cookie := loginCookie(t, router, "viewer")

	rec := doGetWithCookie(router, "/api/v1/cluster/connection", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"state":"degraded"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestConnectionUnreachableWhenNodesForbidden(t *testing.T) {
	kubeClient := kubefake.NewSimpleClientset()
	kubeClient.PrependReactor("list", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "nodes", context.DeadlineExceeded)
	})
	router, _ := newClusterTestRouter(t, kubeClient, metricsfake.NewSimpleClientset())
	cookie := loginCookie(t, router, "viewer")

	rec := doGetWithCookie(router, "/api/v1/cluster/connection", cookie)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"CLUSTER_PERMISSION_DENIED"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestListNodesReturnsProjection(t *testing.T) {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeHostName, Address: "worker-1"},
				{Type: corev1.NodeInternalIP, Address: "10.0.0.12"},
			},
			NodeInfo: corev1.NodeSystemInfo{
				OSImage:                 "Ubuntu 24.04.3 LTS",
				KernelVersion:           "6.8.0-45-generic",
				KubeletVersion:          "v1.30.14",
				ContainerRuntimeVersion: "containerd://2.0.0",
			},
		},
	}
	router, _ := newClusterTestRouter(t, kubefake.NewSimpleClientset(&node), metricsfake.NewSimpleClientset())
	cookie := loginCookie(t, router, "viewer")

	rec := doGetWithCookie(router, "/api/v1/nodes", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"name":"worker-1"`) {
		t.Fatalf("body=%s", body)
	}
	if !strings.Contains(body, `"internalAddress":{"address":"10.0.0.12","source":"NodeInternalIP","family":"ipv4"}`) {
		t.Fatalf("body=%s", body)
	}
}

func TestListNodesEmptyReturnsBrackets(t *testing.T) {
	router, _ := newClusterTestRouter(t, kubefake.NewSimpleClientset(), metricsfake.NewSimpleClientset())
	cookie := loginCookie(t, router, "viewer")

	rec := doGetWithCookie(router, "/api/v1/nodes", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestConnectionTestRequiresOperatorRole(t *testing.T) {
	router, _ := newClusterTestRouter(t, kubefake.NewSimpleClientset(), metricsfake.NewSimpleClientset())

	viewerCookie := loginCookie(t, router, "viewer")
	rec := doPostWithCookie(router, "/api/v1/cluster/connection/test", viewerCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer status=%d body=%s", rec.Code, rec.Body.String())
	}

	operatorCookie := loginCookie(t, router, "operator")
	rec = doPostWithCookie(router, "/api/v1/cluster/connection/test", operatorCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("operator status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func doPostWithCookie(router *gin.Engine, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}
