package deployment

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
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

// ExecutionContext exposes only fixed, installer-approved operations. It never
// exposes SSH credentials or arbitrary request-provided shell commands.
type ExecutionContext interface {
	Server() assets.Server
	Run(context.Context, string, int64) (assets.CommandResult, error)
	Upload(context.Context, io.Reader, int64, string, fs.FileMode) error
	Log(string)
	SetValue(string, string) error
	Value(string) (string, bool)
}

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
	// Owner is the worker identity returned by ClaimNext. It fences worker-only transitions.
	Owner string
}

type SealedSecret struct {
	Nonce      []byte
	Ciphertext []byte
	KeyVersion int
}
