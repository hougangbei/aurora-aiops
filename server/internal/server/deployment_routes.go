package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hougangbei/aurora-aiops/server/internal/deployment"
	"github.com/hougangbei/aurora-aiops/server/internal/response"
)

type deploymentInstallRequest struct {
	ServerID      string          `json:"serverId"`
	Version       string          `json:"version"`
	Configuration json.RawMessage `json:"configuration,omitempty"`
}

type deploymentProjectDTO struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	Description            string   `json:"description"`
	Versions               []string `json:"versions"`
	SupportedOSFamilies    []string `json:"supportedOsFamilies"`
	SupportedArchitectures []string `json:"supportedArchitectures"`
}

func registerDeploymentRoutes(group *gin.RouterGroup, svc *deployment.Service) {
	if svc == nil {
		return
	}
	group.GET("/projects", func(c *gin.Context) {
		projects := svc.ListProjects()
		out := make([]deploymentProjectDTO, 0, len(projects))
		for _, p := range projects {
			out = append(out, projectDTO(p))
		}
		c.JSON(http.StatusOK, response.Success(out))
	})
	group.GET("/projects/:projectID", func(c *gin.Context) {
		p, err := svc.GetProject(c.Param("projectID"))
		if err != nil {
			respondDeploymentError(c, err)
			return
		}
		c.JSON(http.StatusOK, response.Success(projectDTO(p)))
	})
	group.POST("/projects/:projectID/install", RequireAdmin(), func(c *gin.Context) {
		var req deploymentInstallRequest
		if err := decodeAssetJSON(c, &req); err != nil {
			respondDeploymentError(c, deployment.ErrInvalidInput)
			return
		}
		task, err := svc.Install(c.Request.Context(), currentActorName(c), c.Param("projectID"), deployment.InstallRequest{ServerID: req.ServerID, Version: req.Version, Configuration: req.Configuration})
		if err != nil {
			respondDeploymentError(c, err)
			return
		}
		c.JSON(http.StatusCreated, response.Success(taskDTO(task)))
	})
	group.GET("/assets/servers/:serverID/tasks", func(c *gin.Context) {
		tasks, err := svc.ListServerTasks(c.Request.Context(), c.Param("serverID"))
		if err != nil {
			respondDeploymentError(c, err)
			return
		}
		c.JSON(http.StatusOK, response.Success(taskDTOs(tasks)))
	})
	group.GET("/assets/servers/:serverID/installations", func(c *gin.Context) {
		tasks, err := svc.ListInstallations(c.Request.Context(), c.Param("serverID"))
		if err != nil {
			respondDeploymentError(c, err)
			return
		}
		c.JSON(http.StatusOK, response.Success(taskDTOs(tasks)))
	})
	group.GET("/deployment-tasks/:taskID", func(c *gin.Context) {
		task, steps, err := svc.GetTask(c.Request.Context(), c.Param("taskID"))
		if err != nil {
			respondDeploymentError(c, err)
			return
		}
		c.JSON(http.StatusOK, response.Success(gin.H{"task": taskDTO(task), "steps": stepDTOs(steps)}))
	})
	group.GET("/deployment-tasks/:taskID/events", func(c *gin.Context) { streamDeploymentEvents(c, svc, c.Param("taskID")) })
	group.POST("/deployment-tasks/:taskID/cancel", RequireAdmin(), func(c *gin.Context) {
		task, err := svc.Cancel(c.Request.Context(), currentActorName(c), c.Param("taskID"))
		if err != nil {
			respondDeploymentError(c, err)
			return
		}
		c.JSON(http.StatusOK, response.Success(taskDTO(task)))
	})
	group.POST("/deployment-tasks/:taskID/retry", RequireAdmin(), func(c *gin.Context) {
		task, err := svc.Retry(c.Request.Context(), currentActorName(c), c.Param("taskID"))
		if err != nil {
			respondDeploymentError(c, err)
			return
		}
		c.JSON(http.StatusCreated, response.Success(taskDTO(task)))
	})
}

func projectDTO(p deployment.Project) deploymentProjectDTO {
	return deploymentProjectDTO{ID: p.ID, Name: p.Name, Description: p.Description, Versions: p.Versions, SupportedOSFamilies: p.SupportedOSFamilies, SupportedArchitectures: p.SupportedArchitectures}
}
func taskDTO(t deployment.Task) gin.H {
	return gin.H{"id": t.ID, "serverId": t.ServerID, "projectId": t.ProjectID, "version": t.Version, "actor": t.Actor, "retryOf": t.RetryOf, "action": t.Action, "status": t.Status, "currentStepId": t.CurrentStepID, "currentStepLabel": t.CurrentStepLabel, "errorCode": t.ErrorCode, "errorMessage": t.ErrorMessage, "percent": t.Percent, "cancelRequested": t.CancelRequested, "createdAt": t.CreatedAt, "updatedAt": t.UpdatedAt, "startedAt": t.StartedAt, "finishedAt": t.FinishedAt}
}
func taskDTOs(tasks []deployment.Task) []gin.H {
	out := make([]gin.H, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskDTO(t))
	}
	return out
}
func stepDTOs(steps []deployment.Step) []gin.H {
	out := make([]gin.H, 0, len(steps))
	for _, s := range steps {
		out = append(out, gin.H{"taskId": s.TaskID, "id": s.ID, "label": s.Label, "ordinal": s.Ordinal, "percent": s.Percent, "status": s.Status, "startedAt": s.StartedAt, "finishedAt": s.FinishedAt, "errorMessage": s.ErrorMessage})
	}
	return out
}

func respondDeploymentError(c *gin.Context, err error) {
	code, status := "DEPLOYMENT_FAILED", http.StatusInternalServerError
	message := "deployment request failed"
	switch {
	case errors.Is(err, deployment.ErrNotFound):
		code, status, message = "NOT_FOUND", http.StatusNotFound, "deployment task not found"
	case errors.Is(err, deployment.ErrUnknownProject):
		code, status, message = "DEPLOYMENT_UNKNOWN_PROJECT", http.StatusNotFound, "deployment project not found"
	case errors.Is(err, deployment.ErrActiveTask):
		code, status, message = "DEPLOYMENT_ACTIVE_TASK", http.StatusConflict, "server already has an active deployment"
	case errors.Is(err, deployment.ErrUnsupportedTarget):
		code, status, message = "DEPLOYMENT_UNSUPPORTED_TARGET", http.StatusUnprocessableEntity, "target does not support this project"
	case errors.Is(err, deployment.ErrAdoptionRequired):
		code, status, message = "KUBERNETES_ADOPTION_REQUIRED", http.StatusConflict, "an existing Kubernetes cluster requires read-only adoption"
	case errors.Is(err, deployment.ErrInvalidInput):
		code, status, message = "INVALID_ARGUMENT", http.StatusBadRequest, "invalid deployment request"
	case errors.Is(err, deployment.ErrInvalidTransition), errors.Is(err, deployment.ErrNotRetryable):
		code, status, message = "DEPLOYMENT_INVALID_TRANSITION", http.StatusConflict, "deployment task cannot perform that transition"
	case errors.Is(err, deployment.ErrEncryptionUnavailable):
		code, status, message = "DEPLOYMENT_ENCRYPTION_UNAVAILABLE", http.StatusServiceUnavailable, "deployment encryption is unavailable"
	}
	c.JSON(status, response.Failure(code, message))
}

func streamDeploymentEvents(c *gin.Context, svc *deployment.Service, taskID string) {
	if taskID == "" || svc == nil || svc.Events() == nil {
		c.JSON(http.StatusNotFound, response.Failure("NOT_FOUND", "deployment task not found"))
		return
	}
	if _, _, err := svc.GetTask(c.Request.Context(), taskID); err != nil {
		respondDeploymentError(c, err)
		return
	}
	after := int64(0)
	if raw := c.GetHeader("Last-Event-ID"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			c.JSON(http.StatusBadRequest, response.Failure("INVALID_ARGUMENT", "Last-Event-ID must be a non-negative integer"))
			return
		}
		after = parsed
	}
	if raw := c.Query("lastEventId"); raw != "" && c.GetHeader("Last-Event-ID") == "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			c.JSON(http.StatusBadRequest, response.Failure("INVALID_ARGUMENT", "lastEventId must be a non-negative integer"))
			return
		}
		after = parsed
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}
	events := svc.Events()
	sub, unsubscribe := events.Subscribe(taskID)
	defer unsubscribe()
	write := func(ev deployment.Event) {
		fmt.Fprintf(c.Writer, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, ev.Payload)
		flusher.Flush()
	}
	sendAfter := func() bool {
		list, err := events.ListAfter(c.Request.Context(), taskID, after)
		if err != nil {
			return false
		}
		for _, ev := range list {
			write(ev)
			after = ev.ID
		}
		return true
	}
	if !sendAfter() {
		return
	}
	if terminalTask(c, svc, taskID) {
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-sub:
			if !sendAfter() {
				return
			}
			if terminalTask(c, svc, taskID) {
				return
			}
		case <-ticker.C:
			fmt.Fprint(c.Writer, ": heartbeat\n\n")
			flusher.Flush()
			if !sendAfter() {
				return
			}
			if terminalTask(c, svc, taskID) {
				return
			}
		}
	}
}

func terminalTask(c *gin.Context, svc *deployment.Service, id string) bool {
	task, _, err := svc.GetTask(c.Request.Context(), id)
	return err == nil && (task.Status == deployment.TaskSucceeded || task.Status == deployment.TaskFailed || task.Status == deployment.TaskCancelled)
}
