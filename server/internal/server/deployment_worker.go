package server

import (
	"context"
	"errors"
	"io"
	"io/fs"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
	"github.com/hougangbei/aurora-aiops/server/internal/deployment"
)

// unavailableDeploymentTargetProvider keeps the worker lifecycle safe until a
// built-in installer registers a target execution adapter. It never exposes
// credentials or accepts arbitrary commands from HTTP.
type unavailableDeploymentTargetProvider struct{}

func (unavailableDeploymentTargetProvider) ExecutionContext(context.Context, string, string) (assets.Server, deployment.ExecutionContext, error) {
	return assets.Server{}, nil, errors.New("deployment target execution is unavailable")
}

type unavailableDeploymentCipher struct{}

func (unavailableDeploymentCipher) Seal(string, string, []byte) (deployment.SealedSecret, error) {
	return deployment.SealedSecret{}, deployment.ErrEncryptionUnavailable
}
func (unavailableDeploymentCipher) Open(string, string, deployment.SealedSecret) ([]byte, error) {
	return nil, deployment.ErrEncryptionUnavailable
}

// Keep the worker's narrow execution surface explicit even for the disabled
// provider; this prevents accidental shell access through future adapters.
type unavailableDeploymentContext struct{}

func (unavailableDeploymentContext) Server() assets.Server { return assets.Server{} }
func (unavailableDeploymentContext) Run(context.Context, string, int64) (assets.CommandResult, error) {
	return assets.CommandResult{}, errors.New("deployment target execution is unavailable")
}
func (unavailableDeploymentContext) Upload(context.Context, io.Reader, int64, string, fs.FileMode) error {
	return errors.New("deployment target execution is unavailable")
}
func (unavailableDeploymentContext) Log(string) {}
func (unavailableDeploymentContext) SetValue(string, string) error {
	return errors.New("deployment target execution is unavailable")
}
func (unavailableDeploymentContext) Value(string) (string, bool) { return "", false }
