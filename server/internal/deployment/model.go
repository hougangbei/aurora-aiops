package deployment

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
)

type TaskStatus string

const (
	TaskQueued    TaskStatus = "queued"
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
)

type TaskAction string

const (
	TaskActionInstall TaskAction = "install"
	TaskActionAdopt   TaskAction = "adopt"
)

type Task struct {
	ID, ServerID, ProjectID, Version, Actor, RetryOf         string
	Action                                                   TaskAction
	Status                                                   TaskStatus
	CurrentStepID, CurrentStepLabel, ErrorCode, ErrorMessage string
	Percent                                                  int
	CancelRequested                                          bool
	CreatedAt, UpdatedAt                                     time.Time
	StartedAt, FinishedAt                                    *time.Time
}

type Project struct {
	ID, Name, Description                                 string
	Versions, SupportedOSFamilies, SupportedArchitectures []string
}

type ExecutionContext struct{}

type StepDefinition struct {
	ID, Label string
	Percent   int
	Timeout   time.Duration
	Probe     func(context.Context, ExecutionContext) (bool, error)
	Run       func(context.Context, ExecutionContext) error
	// ValueKeys declares the worker-only durable values a step may save.
	ValueKeys []string
}

type Installer interface {
	Project() Project
	NormalizeConfiguration(json.RawMessage) (json.RawMessage, error)
	BuildPlan(Task, assets.Server, json.RawMessage) ([]StepDefinition, error)
}

type StepStatus string

const (
	StepPending   StepStatus = "pending"
	StepRunning   StepStatus = "running"
	StepSucceeded StepStatus = "succeeded"
	StepFailed    StepStatus = "failed"
	StepSkipped   StepStatus = "skipped"
	StepCancelled StepStatus = "cancelled"
)

type Step struct {
	TaskID, ID, Label, ErrorMessage string
	Ordinal                         int
	Percent                         int
	Status                          StepStatus
	StartedAt, FinishedAt           *time.Time
}

type EventInput struct {
	Type    string
	Payload json.RawMessage
}

type SealedSecret struct {
	Nonce      []byte
	Ciphertext []byte
	KeyVersion int
}
