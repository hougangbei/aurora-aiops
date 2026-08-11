package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
	"github.com/hougangbei/aurora-aiops/server/internal/audit"
)

// ServerLookup is the narrow asset dependency needed by deployment APIs. It
// intentionally returns the public asset model and never exposes credentials.
type ServerLookup func(context.Context, string) (assets.Server, error)

type InstallRequest struct {
	ServerID      string
	Version       string
	Configuration json.RawMessage
}

type Service struct {
	repo    *Repository
	catalog *Catalog
	cipher  SecretCipher
	servers ServerLookup
	audit   audit.Repository
	events  *EventStore
	now     func() time.Time
}

func NewService(repo *Repository, catalog *Catalog, cipher SecretCipher, servers ServerLookup, auditRepo audit.Repository, events *EventStore, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, catalog: catalog, cipher: cipher, servers: servers, audit: auditRepo, events: events, now: now}
}

func (s *Service) ListProjects() []Project {
	if s == nil || s.catalog == nil {
		return []Project{}
	}
	return s.catalog.List()
}

// Events exposes the live notifier for the SSE adapter. Durable history is
// still read through the repository-backed EventStore.
func (s *Service) Events() *EventStore {
	if s == nil {
		return nil
	}
	return s.events
}

func (s *Service) GetProject(id string) (Project, error) {
	if s == nil || s.catalog == nil {
		return Project{}, ErrUnknownProject
	}
	p, ok := s.catalog.Get(id)
	if !ok {
		return Project{}, ErrUnknownProject
	}
	return p, nil
}

func (s *Service) Install(ctx context.Context, actor, projectID string, req InstallRequest) (Task, error) {
	if s == nil || s.catalog == nil || actor == "" || req.ServerID == "" || req.Version == "" {
		return Task{}, ErrInvalidInput
	}
	installer, ok := s.catalog.Installer(projectID)
	if !ok {
		return s.installFailure(ctx, actor, projectID, req.ServerID, "unknown_project", ErrUnknownProject)
	}
	project := installer.Project()
	if !contains(project.Versions, req.Version) {
		return s.installFailure(ctx, actor, projectID, req.ServerID, "invalid_version", ErrInvalidInput)
	}
	if s.servers == nil {
		return s.installFailure(ctx, actor, projectID, req.ServerID, "target_unavailable", ErrNotFound)
	}
	server, err := s.servers(ctx, req.ServerID)
	if err != nil {
		return s.installFailure(ctx, actor, projectID, req.ServerID, "target_unavailable", mapAssetError(err))
	}
	if !supportedTarget(project, server) {
		return s.installFailure(ctx, actor, projectID, req.ServerID, "unsupported_target", ErrUnsupportedTarget)
	}

	raw := req.Configuration
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	normalized, err := installer.NormalizeConfiguration(raw)
	if err != nil || !json.Valid(normalized) {
		return s.installFailure(ctx, actor, projectID, req.ServerID, "invalid_configuration", ErrInvalidInput)
	}
	if s.cipher == nil || s.repo == nil {
		return s.installFailure(ctx, actor, projectID, req.ServerID, "encryption_unavailable", ErrEncryptionUnavailable)
	}
	task := Task{ID: uuid.NewString(), ServerID: req.ServerID, ProjectID: projectID, Version: req.Version, Actor: actor, Action: TaskActionInstall}
	plan, err := installer.BuildPlan(task, server, normalized)
	if err != nil {
		return s.installFailure(ctx, actor, projectID, req.ServerID, "plan_invalid", err)
	}
	sealed, err := s.cipher.Seal(TaskConfigScope, task.ID, normalized)
	if err != nil {
		return s.installFailure(ctx, actor, projectID, req.ServerID, "encryption_unavailable", ErrEncryptionUnavailable)
	}
	created, err := s.repo.CreateTask(ctx, task, sealed, plan)
	if err != nil {
		if errors.Is(err, ErrActiveTask) {
			return s.installFailure(ctx, actor, projectID, req.ServerID, "active_task", err)
		}
		return s.installFailure(ctx, actor, projectID, req.ServerID, "create_failed", err)
	}
	if err := s.auditRecord(ctx, actor, created, "deployment.install", "success"); err != nil {
		return Task{}, err
	}
	if s.events != nil {
		s.events.Wake(created.ID)
	}
	return created, nil
}

func (s *Service) GetTask(ctx context.Context, id string) (Task, []Step, error) {
	task, err := s.repo.GetTask(ctx, id)
	if err != nil {
		return Task{}, nil, err
	}
	steps, err := s.repo.ListSteps(ctx, id)
	if err != nil {
		return Task{}, nil, err
	}
	return task, steps, nil
}

func (s *Service) ListServerTasks(ctx context.Context, serverID string) ([]Task, error) {
	return s.repo.ListServerTasks(ctx, serverID)
}

// ListInstallations returns completed install records for a server. Task rows
// remain the source of truth; encrypted configuration is never selected.
func (s *Service) ListInstallations(ctx context.Context, serverID string) ([]Task, error) {
	tasks, err := s.repo.ListServerTasks(ctx, serverID)
	if err != nil {
		return nil, err
	}
	out := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		if task.Action == TaskActionInstall && task.Status == TaskSucceeded {
			out = append(out, task)
		}
	}
	return out, nil
}

func (s *Service) Cancel(ctx context.Context, actor, id string) (Task, error) {
	task, err := s.repo.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if task.Status != TaskQueued && task.Status != TaskRunning {
		return Task{}, ErrInvalidTransition
	}
	if err := s.repo.RequestCancel(ctx, id, EventInput{Type: "cancel_requested"}); err != nil {
		return Task{}, err
	}
	task, err = s.repo.GetTask(ctx, id)
	if err == nil {
		_ = s.auditRecord(ctx, actor, task, "deployment.cancel", "success")
		if s.events != nil {
			s.events.Wake(id)
		}
	}
	return task, err
}

func (s *Service) Retry(ctx context.Context, actor, id string) (Task, error) {
	if s == nil || s.repo == nil || s.catalog == nil || s.servers == nil || s.cipher == nil {
		return Task{}, ErrInvalidInput
	}
	old, err := s.repo.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if old.Status != TaskSucceeded && old.Status != TaskFailed && old.Status != TaskCancelled {
		return Task{}, ErrNotRetryable
	}
	installer, ok := s.catalog.Installer(old.ProjectID)
	if !ok {
		return Task{}, ErrUnknownProject
	}
	server, err := s.servers(ctx, old.ServerID)
	if err != nil {
		return Task{}, mapAssetError(err)
	}
	config, err := s.repo.OpenTaskConfiguration(ctx, id, s.cipher)
	if err != nil {
		return Task{}, err
	}
	newID := uuid.NewString()
	plan, err := installer.BuildPlan(Task{ID: newID, ServerID: old.ServerID, ProjectID: old.ProjectID, Version: old.Version, Actor: actor, Action: old.Action}, server, config)
	if err != nil {
		return Task{}, err
	}
	task, err := s.repo.Retry(ctx, id, newID, actor, plan)
	if err == nil {
		_ = s.auditRecord(ctx, actor, task, "deployment.retry", "success")
		if s.events != nil {
			s.events.Wake(task.ID)
		}
	}
	return task, err
}

func (s *Service) installFailure(ctx context.Context, actor, projectID, serverID, result string, err error) (Task, error) {
	_ = s.auditRecordRaw(ctx, actor, "deployment.install", serverID, result, projectID, "")
	return Task{}, err
}

func (s *Service) auditRecord(ctx context.Context, actor string, task Task, action, result string) error {
	return s.auditRecordRaw(ctx, actor, action, task.ServerID, result, task.ProjectID, task.ID)
}

func (s *Service) auditRecordRaw(ctx context.Context, actor, action, serverID, result, projectID, taskID string) error {
	if s.audit == nil {
		return nil
	}
	payload, _ := json.Marshal(map[string]string{"taskId": taskID, "serverId": serverID, "projectId": projectID, "action": action, "result": result})
	_, err := s.audit.Append(ctx, audit.Record{Actor: actor, Action: action, Target: taskID, Result: result, Payload: string(payload), Timestamp: s.now().UTC()})
	return err
}

func mapAssetError(err error) error {
	if errors.Is(err, assets.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func supportedTarget(project Project, server assets.Server) bool {
	return contains(project.SupportedOSFamilies, server.OSFamily) && contains(project.SupportedArchitectures, server.Architecture)
}
