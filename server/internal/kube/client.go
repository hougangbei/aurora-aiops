package kube

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	appconfig "github.com/hougangbei/aurora-aiops/server/internal/config"
)

const (
	runtimeDirEnv           = "AURORA_AIOPS_RUNTIME_DIR"
	serviceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	serviceAccountCAPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

type Client struct {
	Kubernetes  kubernetes.Interface
	Metrics     metricsclient.Interface
	RESTConfig  *rest.Config
	ConfigPath  string
	AccessToken string
	AuthMode    string
	RawConfig   clientcmdapiConfig
}

// Options holds connection parameters applied to the shared client's REST
// configuration. A zero value leaves the corresponding field untouched.
type Options struct {
	Timeout time.Duration
	QPS     float32
	Burst   int
}

type clientcmdapiConfig struct {
	CurrentContext string
	AuthInfoName   string
}

// clientConfigLoaders supplies the identity sources so tests can inject fakes
// without a real cluster. Production uses rest.InClusterConfig and os.UserHomeDir.
type clientConfigLoaders struct {
	inCluster func() (*rest.Config, error)
	homeDir   func() (string, error)
}

// NewSharedClient builds the single cluster client whose identity resolves in
// this order: an explicit kubeconfig path, the in-cluster service account
// identity, then the user's default kubeconfig. It is created once at process
// start and reused for every request; no request-level token overrides apply.
func NewSharedClient(configPath string, options Options) (*Client, error) {
	return newSharedClientWithLoaders(configPath, options, clientConfigLoaders{
		inCluster: rest.InClusterConfig,
		homeDir:   os.UserHomeDir,
	})
}

// newSharedClientWithLoaders selects the identity source. An explicit path is
// authoritative; otherwise in-cluster identity is preferred so a deployment
// uses its ServiceAccount without mounting a kubeconfig.
func newSharedClientWithLoaders(path string, options Options, loaders clientConfigLoaders) (*Client, error) {
	if strings.TrimSpace(path) != "" {
		client, err := newKubeconfigClient(path, options)
		if err != nil {
			return nil, fmt.Errorf("explicit kubeconfig unavailable: %w", redactConfigPath(err, path))
		}
		return client, nil
	}

	config, err := loaders.inCluster()
	if err == nil {
		applyOptions(config, options)
		configPath, writeErr := writeInClusterKubeconfig(config)
		if writeErr != nil {
			return nil, fmt.Errorf("write in-cluster runtime kubeconfig: %w", writeErr)
		}
		return newClient(config, configPath, "in-cluster", clientcmdapiConfig{
			CurrentContext: "in-cluster",
			AuthInfoName:   "in-cluster",
		})
	}
	inClusterErr := redactConfigPaths(err, serviceAccountTokenPath, serviceAccountCAPath)

	home, homeErr := loaders.homeDir()
	if homeErr != nil {
		return nil, fmt.Errorf("in-cluster config: %w; resolve default kubeconfig: %w", inClusterErr, homeErr)
	}
	fallback := filepath.Join(home, ".kube", "config")
	client, fileErr := newKubeconfigClient(fallback, options)
	if fileErr != nil {
		return nil, fmt.Errorf("in-cluster config unavailable: %w; default kubeconfig unavailable: %w", inClusterErr, redactConfigPath(fileErr, fallback))
	}
	return client, nil
}

// writeInClusterKubeconfig materializes only the information kubectl needs.
// It references the projected ServiceAccount token instead of copying the
// bearer token value, and the generated file is private to the server process.
func writeInClusterKubeconfig(config *rest.Config) (string, error) {
	tokenFile := strings.TrimSpace(config.BearerTokenFile)
	if tokenFile == "" {
		return "", fmt.Errorf("service account token file is required")
	}

	runtimeDir := strings.TrimSpace(appconfig.RuntimeDir())
	if runtimeDir == "" {
		runtimeDir = os.TempDir()
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return "", fmt.Errorf("prepare runtime directory: %w", redactConfigPath(err, runtimeDir))
	}

	raw := clientcmdapi.NewConfig()
	raw.Clusters["in-cluster"] = &clientcmdapi.Cluster{
		Server:                   config.Host,
		CertificateAuthority:     config.TLSClientConfig.CAFile,
		CertificateAuthorityData: config.TLSClientConfig.CAData,
		InsecureSkipTLSVerify:    config.TLSClientConfig.Insecure,
	}
	raw.AuthInfos["in-cluster"] = &clientcmdapi.AuthInfo{TokenFile: tokenFile}
	raw.Contexts["in-cluster"] = &clientcmdapi.Context{
		Cluster:  "in-cluster",
		AuthInfo: "in-cluster",
	}
	raw.CurrentContext = "in-cluster"
	content, err := clientcmd.Write(*raw)
	if err != nil {
		return "", fmt.Errorf("encode runtime kubeconfig: %w", err)
	}
	return writeRuntimeKubeconfigFile(content, func(dir, pattern string) (runtimeKubeconfigFile, error) {
		return os.CreateTemp(dir, pattern)
	}, runtimeDir)
}

type runtimeKubeconfigFile interface {
	Name() string
	Chmod(mode os.FileMode) error
	Write(p []byte) (int, error)
	Close() error
}

func writeRuntimeKubeconfigFile(
	content []byte,
	createTemp func(dir, pattern string) (runtimeKubeconfigFile, error),
	runtimeDir string,
) (string, error) {
	file, err := createTemp(runtimeDir, "aurora-aiops-kubeconfig-*")
	if err != nil {
		return "", fmt.Errorf("create runtime kubeconfig: %w", redactConfigPath(err, runtimeDir))
	}
	path := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return "", fmt.Errorf("secure runtime kubeconfig: %w", redactConfigPath(err, path))
	}
	if _, err := file.Write(content); err != nil {
		return "", fmt.Errorf("write runtime kubeconfig: %w", redactConfigPath(err, path))
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close runtime kubeconfig: %w", redactConfigPath(err, path))
	}
	keep = true
	return path, nil
}

// newKubeconfigClient builds a client whose identity comes entirely from the
// kubeconfig at configPath.
func newKubeconfigClient(configPath string, options Options) (*Client, error) {
	rawConfig, err := clientcmd.LoadFromFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	restConfig, err := clientcmd.BuildConfigFromFlags("", configPath)
	if err != nil {
		return nil, fmt.Errorf("build rest config: %w", err)
	}

	applyOptions(restConfig, options)

	contextName, authInfoName := rawConfig.CurrentContext, ""
	if current, ok := rawConfig.Contexts[contextName]; ok {
		authInfoName = current.AuthInfo
	}

	return newClient(restConfig, configPath, "shared-kubeconfig", clientcmdapiConfig{
		CurrentContext: contextName,
		AuthInfoName:   authInfoName,
	})
}

// applyOptions applies non-zero connection parameters to the REST config.
func applyOptions(config *rest.Config, options Options) {
	if options.Timeout != 0 {
		config.Timeout = options.Timeout
	}
	if options.QPS != 0 {
		config.QPS = options.QPS
	}
	if options.Burst != 0 {
		config.Burst = options.Burst
	}
}

// redactConfigPath returns a new error preserving the wrapped category while
// replacing every occurrence of the resolved path with [redacted] so failures
// never leak the local filesystem layout.
func redactConfigPath(err error, path string) error {
	return redactConfigPaths(err, path)
}

type redactedPathError struct {
	err   error
	paths []string
}

func (e *redactedPathError) Error() string {
	message := e.err.Error()
	for _, path := range e.paths {
		if path != "" {
			message = strings.ReplaceAll(message, path, "[redacted]")
		}
	}
	return message
}

func (e *redactedPathError) Unwrap() error { return e.err }

func redactConfigPaths(err error, paths ...string) error {
	if err == nil {
		return nil
	}
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		if path != "" {
			filtered = append(filtered, path)
		}
	}
	if len(filtered) == 0 {
		return err
	}
	return &redactedPathError{err: err, paths: filtered}
}

func newClient(
	config *rest.Config,
	configPath string,
	authMode string,
	rawConfig clientcmdapiConfig,
) (*Client, error) {
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	metricsClient, err := metricsclient.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create metrics client: %w", err)
	}

	return &Client{
		Kubernetes:  kubeClient,
		Metrics:     metricsClient,
		RESTConfig:  rest.CopyConfig(config),
		ConfigPath:  configPath,
		AccessToken: strings.TrimSpace(config.BearerToken),
		AuthMode:    authMode,
		RawConfig:   rawConfig,
	}, nil
}
