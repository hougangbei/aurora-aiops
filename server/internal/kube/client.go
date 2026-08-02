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
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
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
		return newClient(config, "", "in-cluster", clientcmdapiConfig{CurrentContext: "in-cluster"})
	}

	home, homeErr := loaders.homeDir()
	if homeErr != nil {
		return nil, fmt.Errorf("in-cluster config: %v; resolve default kubeconfig: %w", err, homeErr)
	}
	fallback := filepath.Join(home, ".kube", "config")
	client, fileErr := newKubeconfigClient(fallback, options)
	if fileErr != nil {
		return nil, fmt.Errorf("in-cluster config unavailable: %v; default kubeconfig unavailable: %w", err, redactConfigPath(fileErr, fallback))
	}
	return client, nil
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
	if err == nil {
		return nil
	}
	if path == "" {
		return err
	}
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), path, "[redacted]"))
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
