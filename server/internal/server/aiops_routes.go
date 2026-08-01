package server

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/heihuzicity-tech/kubejojo/server/internal/aiops"
	"github.com/heihuzicity-tech/kubejojo/server/internal/response"
)

type createIncidentRequest struct {
	Summary      string `json:"summary"`
	Severity     string `json:"severity"`
	Namespace    string `json:"namespace"`
	ResourceKind string `json:"resourceKind"`
	ResourceName string `json:"resourceName"`
}

// registerAIOpsRoutes registers the aiops incident routes on the given group.
// The group is expected to already carry the /api/v1 prefix.
func registerAIOpsRoutes(group *gin.RouterGroup, svc *aiops.Service) {
	incidents := group.Group("/aiops/incidents")
	{
		incidents.POST("", handleCreateIncident(svc))
		incidents.GET("", handleListIncidents(svc))
		incidents.GET("/:id", handleGetIncident(svc))
	}
}

func handleCreateIncident(svc *aiops.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createIncidentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, response.Failure("INVALID_INCIDENT_REQUEST", "请求体格式不正确"))
			return
		}

		incident, err := svc.Create(c.Request.Context(), aiops.CreateIncidentInput{
			Summary:      req.Summary,
			Severity:     req.Severity,
			Namespace:    req.Namespace,
			ResourceKind: req.ResourceKind,
			ResourceName: req.ResourceName,
		})
		if err != nil {
			if errors.Is(err, aiops.ErrInvalidIncidentRequest) {
				c.JSON(http.StatusBadRequest, response.Failure("INVALID_INCIDENT_REQUEST", "事件请求参数无效"))
				return
			}
			log.Printf("CREATE_INCIDENT_FAILED: %v", err)
			c.JSON(http.StatusInternalServerError, response.Failure("CREATE_INCIDENT_FAILED", "创建事件失败"))
			return
		}

		c.JSON(http.StatusCreated, response.Success(incident))
	}
}

func handleListIncidents(svc *aiops.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		items, err := svc.List(c.Request.Context(), aiops.IncidentFilter{})
		if err != nil {
			log.Printf("LIST_INCIDENTS_FAILED: %v", err)
			c.JSON(http.StatusInternalServerError, response.Failure("LIST_INCIDENTS_FAILED", "查询事件列表失败"))
			return
		}

		c.JSON(http.StatusOK, response.Success(items))
	}
}

func handleGetIncident(svc *aiops.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		incident, err := svc.Get(c.Request.Context(), id)
		if err != nil {
			if errors.Is(err, aiops.ErrIncidentNotFound) {
				c.JSON(http.StatusNotFound, response.Failure("INCIDENT_NOT_FOUND", "事件不存在"))
				return
			}
			log.Printf("GET_INCIDENT_FAILED: %v", err)
			c.JSON(http.StatusInternalServerError, response.Failure("GET_INCIDENT_FAILED", "获取事件失败"))
			return
		}

		c.JSON(http.StatusOK, response.Success(incident))
	}
}
