package deployment

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound              = errors.New("deployment task not found")
	ErrActiveTask            = errors.New("server has an active deployment task")
	ErrInvalidTransition     = errors.New("invalid deployment task transition")
	ErrUnknownProject        = errors.New("unknown deployment project")
	ErrUnsupportedTarget     = errors.New("unsupported deployment target")
	ErrNotRetryable          = errors.New("deployment task is not retryable")
	ErrEncryptionUnavailable = errors.New("deployment secret encryption is not configured")
	ErrLeaseLost             = errors.New("deployment task lease lost")
	ErrInvalidInput          = errors.New("invalid deployment input")
	ErrAdoptionRequired      = errors.New("existing Kubernetes cluster requires adoption")
)

type AdoptionSummary struct {
	Version string
	Nodes   []string
}

type AdoptionRequiredError struct {
	Summary AdoptionSummary
}

func (e *AdoptionRequiredError) Error() string {
	if e == nil {
		return ErrAdoptionRequired.Error()
	}
	return fmt.Sprintf("%s: version=%s nodes=%d", ErrAdoptionRequired, e.Summary.Version, len(e.Summary.Nodes))
}

func (e *AdoptionRequiredError) Unwrap() error { return ErrAdoptionRequired }
