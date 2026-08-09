package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/heihuzicity-tech/kubejojo/server/internal/aiops"
	"github.com/heihuzicity-tech/kubejojo/server/internal/policy"
	"github.com/heihuzicity-tech/kubejojo/server/internal/remediation"
	"github.com/heihuzicity-tech/kubejojo/server/internal/response"
)

type createIncidentRequest struct {
	Summary      string `json:"summary"`
	Severity     string `json:"severity"`
	Namespace    string `json:"namespace"`
	ResourceKind string `json:"resourceKind"`
	ResourceName string `json:"resourceName"`
}

// aiopsRoutesDeps carries the services the aiops routes need. workflow, events
// and remediation are optional: when absent the corresponding routes are not
// registered, keeping the group usable for incident-only tests.
type aiopsRoutesDeps struct {
	svc         *aiops.Service
	workflow    *aiops.Workflow
	events      *aiops.EventStore
	remediation *remediation.Service
}

type aiopsReadinessResponse struct {
	ModelConfigured             bool   `json:"modelConfigured"`
	Model                       string `json:"model"`
	ConfigurationSource         string `json:"configurationSource"`
	RuntimeMutable              bool   `json:"runtimeMutable"`
	DeterministicRolesAvailable bool   `json:"deterministicRolesAvailable"`
	RemediationAvailable        bool   `json:"remediationAvailable"`
}

// sseHeartbeat keeps event-stream clients alive through idle periods.
const sseHeartbeat = 15 * time.Second

// registerAIOpsRoutes registers the aiops incident routes on the given group.
// The group is expected to already carry the /api/v1 prefix and, in production,
// the RequireSession middleware. read routes accept any authenticated role;
// reanalyze additionally requires operator/admin.
func registerAIOpsRoutes(group *gin.RouterGroup, deps aiopsRoutesDeps) {
	group.GET("/aiops/readiness", handleAIOpsReadiness(deps))

	incidents := group.Group("/aiops/incidents")
	{
		incidents.POST("", RequireOperator(), handleCreateIncident(deps.svc, deps.workflow))
		incidents.GET("", handleListIncidents(deps.svc))
		incidents.GET("/:id", handleGetIncident(deps.svc))
		if deps.workflow != nil {
			incidents.GET("/:id/evidence", handleGetIncidentEvidence(deps.svc, deps.workflow))
			incidents.GET("/:id/runs", handleGetIncidentRuns(deps.svc, deps.workflow))
			incidents.POST("/:id/reanalyze",
				RequireOperator(),
				handleReanalyzeIncident(deps.svc, deps.workflow))
		}
		if deps.events != nil {
			incidents.GET("/:id/events", handleIncidentEvents(deps.svc, deps.events))
		}
		if deps.remediation != nil {
			incidents.POST("/:id/approve-remediation",
				RequireOperator(),
				handleApproveRemediation(deps.svc, deps.remediation))
			incidents.POST("/:id/reject-remediation",
				RequireOperator(),
				handleRejectRemediation(deps.svc, deps.remediation))
			incidents.POST("/:id/execute-remediation",
				RequireAdmin(),
				handleExecuteRemediation(deps.svc, deps.remediation))
			incidents.POST("/:id/rollback",
				RequireAdmin(),
				handleRollbackRemediation(deps.svc, deps.remediation))
		}
	}
}

func handleAIOpsReadiness(deps aiopsRoutesDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		modelConfigured, model := deps.workflow.ModelStatus()
		c.JSON(http.StatusOK, response.Success(aiopsReadinessResponse{
			ModelConfigured:             modelConfigured,
			Model:                       model,
			ConfigurationSource:         "environment",
			RuntimeMutable:              false,
			DeterministicRolesAvailable: true,
			RemediationAvailable:        deps.remediation != nil,
		}))
	}
}

func handleCreateIncident(svc *aiops.Service, wf *aiops.Workflow) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createIncidentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, response.Failure("INVALID_INCIDENT_REQUEST", "请求体格式不正确"))
			return
		}

		ctx := c.Request.Context()
		incident, err := svc.Create(ctx, aiops.CreateIncidentInput{
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

		// Creating an incident kicks off the diagnostic workflow. Role failures
		// are recorded on the incident itself; only infrastructure errors are
		// logged here.
		if wf != nil {
			if runErr := wf.Run(ctx, incident.ID); runErr != nil && !errors.Is(runErr, aiops.ErrWorkflowLocked) {
				log.Printf("INCIDENT_RUN_FAILED: %v", runErr)
			}
			if updated, getErr := svc.Get(ctx, incident.ID); getErr == nil {
				incident = updated
			}
		}

		c.JSON(http.StatusCreated, response.Success(incident))
	}
}

func handleListIncidents(svc *aiops.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		items, err := svc.List(c.Request.Context(), aiops.IncidentFilter{
			Status:    aiops.Status(strings.TrimSpace(c.Query("status"))),
			Namespace: strings.TrimSpace(c.Query("namespace")),
		})
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

func handleGetIncidentEvidence(svc *aiops.Service, wf *aiops.Workflow) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		ctx := c.Request.Context()
		if _, err := svc.Get(ctx, id); err != nil {
			if errors.Is(err, aiops.ErrIncidentNotFound) {
				c.JSON(http.StatusNotFound, response.Failure("INCIDENT_NOT_FOUND", "事件不存在"))
				return
			}
			log.Printf("GET_INCIDENT_FAILED: %v", err)
			c.JSON(http.StatusInternalServerError, response.Failure("GET_INCIDENT_FAILED", "获取事件失败"))
			return
		}

		nodes, edges, err := wf.EvidenceFor(ctx, id)
		if err != nil {
			log.Printf("GET_EVIDENCE_FAILED: %v", err)
			c.JSON(http.StatusInternalServerError, response.Failure("GET_EVIDENCE_FAILED", "查询证据失败"))
			return
		}
		c.JSON(http.StatusOK, response.Success(gin.H{"nodes": nodes, "edges": edges}))
	}
}

func handleGetIncidentRuns(svc *aiops.Service, wf *aiops.Workflow) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		ctx := c.Request.Context()
		if _, err := svc.Get(ctx, id); err != nil {
			if errors.Is(err, aiops.ErrIncidentNotFound) {
				c.JSON(http.StatusNotFound, response.Failure("INCIDENT_NOT_FOUND", "事件不存在"))
				return
			}
			log.Printf("GET_INCIDENT_FAILED: %v", err)
			c.JSON(http.StatusInternalServerError, response.Failure("GET_INCIDENT_FAILED", "获取事件失败"))
			return
		}

		runs, err := wf.RunsFor(ctx, id)
		if err != nil {
			log.Printf("GET_RUNS_FAILED: %v", err)
			c.JSON(http.StatusInternalServerError, response.Failure("GET_RUNS_FAILED", "查询诊断记录失败"))
			return
		}
		c.JSON(http.StatusOK, response.Success(gin.H{"runs": runs}))
	}
}

func handleReanalyzeIncident(svc *aiops.Service, wf *aiops.Workflow) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		ctx := c.Request.Context()
		if _, err := svc.Get(ctx, id); err != nil {
			if errors.Is(err, aiops.ErrIncidentNotFound) {
				c.JSON(http.StatusNotFound, response.Failure("INCIDENT_NOT_FOUND", "事件不存在"))
				return
			}
			log.Printf("GET_INCIDENT_FAILED: %v", err)
			c.JSON(http.StatusInternalServerError, response.Failure("GET_INCIDENT_FAILED", "获取事件失败"))
			return
		}

		if err := wf.Reanalyze(ctx, id); err != nil {
			switch {
			case errors.Is(err, aiops.ErrReanalyzeNotAllowed):
				c.JSON(http.StatusBadRequest, response.Failure("REANALYZE_NOT_ALLOWED", "当前状态不允许重新诊断"))
			case errors.Is(err, aiops.ErrWorkflowLocked):
				c.JSON(http.StatusConflict, response.Failure("WORKFLOW_BUSY", "诊断正在进行中"))
			default:
				log.Printf("REANALYZE_FAILED: %v", err)
				c.JSON(http.StatusInternalServerError, response.Failure("REANALYZE_FAILED", "重新诊断失败"))
			}
			return
		}

		incident, err := svc.Get(ctx, id)
		if err != nil {
			log.Printf("GET_INCIDENT_FAILED: %v", err)
			c.JSON(http.StatusInternalServerError, response.Failure("GET_INCIDENT_FAILED", "获取事件失败"))
			return
		}
		c.JSON(http.StatusOK, response.Success(incident))
	}
}

func handleIncidentEvents(svc *aiops.Service, events *aiops.EventStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		ctx := c.Request.Context()
		if _, err := svc.Get(ctx, id); err != nil {
			if errors.Is(err, aiops.ErrIncidentNotFound) {
				c.JSON(http.StatusNotFound, response.Failure("INCIDENT_NOT_FOUND", "事件不存在"))
				return
			}
			log.Printf("GET_INCIDENT_FAILED: %v", err)
			c.JSON(http.StatusInternalServerError, response.Failure("GET_INCIDENT_FAILED", "获取事件失败"))
			return
		}

		lastEventID, _ := strconv.ParseInt(c.Query("lastEventId"), 10, 64)

		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")

		// Replay persisted history, then subscribe for live events. History is
		// always replayed before the live feed so a reconnect never gaps.
		historical, err := events.ListAfter(ctx, id, lastEventID)
		if err != nil {
			log.Printf("EVENTS_LIST_FAILED: %v", err)
			return
		}
		flusher, _ := c.Writer.(http.Flusher)
		for _, ev := range historical {
			writeSSEEvent(c.Writer, ev)
		}
		if flusher != nil {
			flusher.Flush()
		}

		ch, unsubscribe := events.Subscribe(id)
		defer unsubscribe()
		ticker := time.NewTicker(sseHeartbeat)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-ch:
				writeSSEEvent(c.Writer, ev)
				if flusher != nil {
					flusher.Flush()
				}
			case <-ticker.C:
				_, _ = io.WriteString(c.Writer, ": heartbeat\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}
}

// writeSSEEvent writes one SSE frame. Event data is pre-serialized JSON.
func writeSSEEvent(w io.Writer, ev aiops.Event) {
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, ev.Data)
}

type remediationReasonRequest struct {
	Reason string `json:"reason"`
}

const minRemediationReasonLength = 8

func normalizeRemediationReason(raw string) (string, bool) {
	reason := strings.TrimSpace(raw)
	return reason, utf8.RuneCountInString(reason) >= minRemediationReasonLength
}

func handleApproveRemediation(svc *aiops.Service, remediationService *remediation.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		handleRemediationDecision(c, svc, remediationService, "approve")
	}
}

func handleRejectRemediation(svc *aiops.Service, remediationService *remediation.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		handleRemediationDecision(c, svc, remediationService, "reject")
	}
}

func handleRemediationDecision(c *gin.Context, svc *aiops.Service, remediationService *remediation.Service, kind string) {
	id := c.Param("id")
	actor, _ := ActorFromContext(c)
	var req remediationReasonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, response.Failure("INVALID_REMEDIATION_REQUEST", "请求体格式不正确"))
		return
	}
	reason, valid := normalizeRemediationReason(req.Reason)
	if !valid {
		c.JSON(http.StatusBadRequest, response.Failure("INVALID_REMEDIATION_REQUEST", "处置理由至少需要 8 个字符"))
		return
	}
	var err error
	if kind == "approve" {
		err = remediationService.Approve(c.Request.Context(), id, actor.Username, reason)
	} else {
		err = remediationService.Reject(c.Request.Context(), id, actor.Username, reason)
	}
	if err != nil {
		respondRemediationError(c, err)
		return
	}
	incident, _ := svc.Get(c.Request.Context(), id)
	c.JSON(http.StatusOK, response.Success(incident))
}

func handleExecuteRemediation(svc *aiops.Service, remediationService *remediation.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		actor, _ := ActorFromContext(c)
		if err := remediationService.Execute(c.Request.Context(), id, actor.Username); err != nil {
			respondRemediationError(c, err)
			return
		}
		incident, _ := svc.Get(c.Request.Context(), id)
		c.JSON(http.StatusOK, response.Success(incident))
	}
}

func handleRollbackRemediation(svc *aiops.Service, remediationService *remediation.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		actor, _ := ActorFromContext(c)
		var req struct {
			SnapshotID string `json:"snapshotId"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.SnapshotID == "" {
			c.JSON(http.StatusBadRequest, response.Failure("INVALID_ROLLBACK_REQUEST", "缺少 snapshotId"))
			return
		}
		if err := remediationService.Rollback(c.Request.Context(), id, req.SnapshotID, actor.Username); err != nil {
			respondRemediationError(c, err)
			return
		}
		incident, _ := svc.Get(c.Request.Context(), id)
		c.JSON(http.StatusOK, response.Success(incident))
	}
}

func respondRemediationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, aiops.ErrIncidentNotFound):
		c.JSON(http.StatusNotFound, response.Failure("INCIDENT_NOT_FOUND", "事件不存在"))
	case errors.Is(err, remediation.ErrInvalidStatus):
		c.JSON(http.StatusBadRequest, response.Failure("REMEDIATION_NOT_ALLOWED", "当前状态不允许该操作"))
	case errors.Is(err, remediation.ErrNotApproved):
		c.JSON(http.StatusBadRequest, response.Failure("REMEDIATION_NOT_APPROVED", "事件尚未审批通过"))
	case errors.Is(err, remediation.ErrNoExecutableActions):
		c.JSON(http.StatusBadRequest, response.Failure("NO_EXECUTABLE_ACTIONS", "修复方案无可执行的结构化动作"))
	case errors.Is(err, remediation.ErrAlreadyExecuted):
		c.JSON(http.StatusConflict, response.Failure("ALREADY_EXECUTED", "该动作已执行，请人工确认后再处理"))
	case errors.Is(err, remediation.ErrActionNotAllowed):
		c.JSON(http.StatusForbidden, response.Failure("ACTION_NOT_ALLOWED", "修复动作未通过确定性策略校验"))
	case errors.Is(err, policy.ErrActionDenied):
		c.JSON(http.StatusForbidden, response.Failure("ACTION_DENIED", "动作被策略拒绝"))
	case errors.Is(err, remediation.ErrSnapshotNotFound):
		c.JSON(http.StatusNotFound, response.Failure("SNAPSHOT_NOT_FOUND", "快照不存在"))
	case errors.Is(err, aiops.ErrStateTransitionConflict):
		c.JSON(http.StatusConflict, response.Failure("STATE_TRANSITION_CONFLICT", "事件状态已被其他操作更新"))
	default:
		log.Printf("REMEDIATION_FAILED: %v", err)
		c.JSON(http.StatusInternalServerError, response.Failure("REMEDIATION_FAILED", "执行失败"))
	}
}
