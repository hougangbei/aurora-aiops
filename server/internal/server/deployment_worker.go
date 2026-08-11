package server

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"sync"

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

// assetDeploymentTargetProvider is the only production target adapter. It
// resolves the already-confirmed asset credential through the asset service
// and exposes only the narrow command/upload surface required by installers.
type assetDeploymentTargetProvider struct {
	assets *assets.Service
	remote assets.RemoteTransport
}

func (p assetDeploymentTargetProvider) ExecutionContext(ctx context.Context, serverID, _ string) (assets.Server, deployment.ExecutionContext, error) {
	if p.assets == nil || p.remote == nil {
		return assets.Server{}, nil, errors.New("deployment target execution is unavailable")
	}
	server, target, secret, err := p.assets.DeploymentTarget(ctx, serverID)
	if err != nil {
		return assets.Server{}, nil, err
	}
	return server, &assetDeploymentContext{remote: p.remote, target: target, secret: secret, values: make(map[string]string)}, nil
}

type assetDeploymentContext struct {
	remote assets.RemoteTransport
	target assets.RemoteTarget
	secret assets.CredentialSecret
	mu     sync.RWMutex
	values map[string]string
}

func (c *assetDeploymentContext) Server() assets.Server {
	return assets.Server{Address: c.target.Address, Username: c.target.Username, SSHPort: c.target.Port, HostKeyFingerprint: c.target.ExpectedFingerprint}
}
func (c *assetDeploymentContext) Run(ctx context.Context, command string, limit int64) (assets.CommandResult, error) {
	return c.remote.Run(ctx, c.target, c.secret, command, limit)
}
func (c *assetDeploymentContext) Upload(ctx context.Context, source io.Reader, size int64, destination string, mode fs.FileMode) error {
	return c.remote.Upload(ctx, c.target, c.secret, source, size, destination, mode)
}
func (c *assetDeploymentContext) Log(string) {}
func (c *assetDeploymentContext) SetValue(key, value string) error {
	if key == "" || len(key) > 128 {
		return deployment.ErrInvalidInput
	}
	c.mu.Lock()
	c.values[key] = value
	c.mu.Unlock()
	return nil
}
func (c *assetDeploymentContext) Value(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	value, ok := c.values[key]
	return value, ok
}
func (c *assetDeploymentContext) Clear() {
	c.secret = assets.CredentialSecret{}
	c.mu.Lock()
	for key := range c.values {
		c.values[key] = ""
	}
	c.values = nil
	c.mu.Unlock()
}

type unavailableDeploymentCipher struct{}

func (unavailableDeploymentCipher) Seal(string, string, []byte) (deployment.SealedSecret, error) {
	return deployment.SealedSecret{}, deployment.ErrEncryptionUnavailable
}
func (unavailableDeploymentCipher) Open(string, string, deployment.SealedSecret) ([]byte, error) {
	return nil, deployment.ErrEncryptionUnavailable
}
func (unavailableDeploymentCipher) SealResource(string, string, string, []byte) (deployment.SealedSecret, error) {
	return deployment.SealedSecret{}, deployment.ErrEncryptionUnavailable
}
func (unavailableDeploymentCipher) OpenResource(string, string, string, deployment.SealedSecret) ([]byte, error) {
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
