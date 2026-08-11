package deployment

import "errors"

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
)
