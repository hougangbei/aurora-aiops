package assets

import "errors"

var (
	ErrNotFound              = errors.New("asset server not found")
	ErrNameConflict          = errors.New("asset server name already exists")
	ErrInvalidInput          = errors.New("invalid asset input")
	ErrEncryptionUnavailable = errors.New("asset credential encryption is not configured")
	ErrHostKeyUntrusted      = errors.New("ssh host key requires confirmation")
	ErrHostKeyChanged        = errors.New("ssh host key changed")
	ErrActiveTask            = errors.New("asset server has an active deployment task")
)
