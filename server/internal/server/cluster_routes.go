package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/heihuzicity-tech/kubejojo/server/internal/auth"
	"github.com/heihuzicity-tech/kubejojo/server/internal/cluster"
	"github.com/heihuzicity-tech/kubejojo/server/internal/response"
	"github.com/heihuzicity-tech/kubejojo/server/internal/service"
)

const probeCacheTTL = 30 * time.Second

// probeCache caches the latest cluster probe result for a short TTL and merges
// concurrent probes into a single in-flight run.
type probeCache struct {
	mu        sync.Mutex
	result    cluster.ProbeResult
	checkedAt time.Time
	running   chan struct{}
}

// get returns a fresh-enough probe result, running a new probe when the cache
// is empty or older than probeCacheTTL. Concurrent callers share one probe.
func (c *probeCache) get(ctx context.Context, probe *cluster.Probe) cluster.ProbeResult {
	c.mu.Lock()
	if !c.checkedAt.IsZero() && time.Since(c.checkedAt) < probeCacheTTL {
		result := c.result
		c.mu.Unlock()
		return result
	}
	if c.running != nil {
		done := c.running
		c.mu.Unlock()
		<-done
		c.mu.Lock()
		result := c.result
		c.mu.Unlock()
		return result
	}
	c.running = make(chan struct{})
	c.mu.Unlock()

	result := probe.Run(ctx)

	c.mu.Lock()
	c.result = result
	c.checkedAt = time.Now()
	close(c.running)
	c.running = nil
	c.mu.Unlock()
	return result
}

// registerClusterRoutes registers the shared cluster connection and node
// discovery routes. They require a session; the probe is bounded and the node
// list is a projection of the Kubernetes Node API.
func registerClusterRoutes(
	group *gin.RouterGroup,
	probe *cluster.Probe,
	clusterService *service.ClusterService,
) {
	cache := &probeCache{}

	group.GET("/cluster/connection", handleGetConnection(cache, probe))
	group.POST("/cluster/connection/test",
		RequireRoles(auth.RoleOperator, auth.RoleAdmin),
		handleTestConnection(cache, probe),
	)
	group.GET("/nodes", handleListNodes(clusterService))
}

func handleGetConnection(cache *probeCache, probe *cluster.Probe) gin.HandlerFunc {
	return func(c *gin.Context) {
		result := cache.get(c.Request.Context(), probe)
		if result.State == cluster.StateUnreachable {
			c.JSON(http.StatusServiceUnavailable, response.Failure(probeFailureCode(result), "集群不可达"))
			return
		}
		c.JSON(http.StatusOK, response.Success(result))
	}
}

func handleTestConnection(cache *probeCache, probe *cluster.Probe) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Force a fresh probe by expiring the cache first, then reuse the
		// merged cache.get so concurrent tests collapse into one run.
		cache.mu.Lock()
		cache.checkedAt = time.Time{}
		cache.mu.Unlock()

		result := cache.get(c.Request.Context(), probe)
		if result.State == cluster.StateUnreachable {
			c.JSON(http.StatusServiceUnavailable, response.Failure(probeFailureCode(result), "集群不可达"))
			return
		}
		c.JSON(http.StatusOK, response.Success(result))
	}
}

func handleListNodes(clusterService *service.ClusterService) gin.HandlerFunc {
	return func(c *gin.Context) {
		nodes, err := clusterService.ListPlatformNodes(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, response.Failure("CLUSTER_UNREACHABLE", "节点列表不可用"))
			return
		}
		c.JSON(http.StatusOK, response.Success(nodes))
	}
}

// probeFailureCode distinguishes permission failures from generic reachability
// failures so clients can tell "kubeconfig lacks RBAC" apart from "cluster down".
func probeFailureCode(result cluster.ProbeResult) string {
	for _, check := range result.Checks {
		if check.Code == "NODES_FORBIDDEN" || check.Code == "AUTH_FAILED" {
			return "CLUSTER_PERMISSION_DENIED"
		}
	}
	return "CLUSTER_UNREACHABLE"
}
