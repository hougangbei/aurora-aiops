package kube

import (
	"fmt"
	"os"
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

type Factory struct {
	configPath string
	rawConfig  clientcmdapiConfig
	baseConfig *rest.Config
}

type clientcmdapiConfig struct {
	CurrentContext string
	AuthInfoName   string
}

// NewSharedClient builds a single cluster client whose Kubernetes identity
// comes entirely from the shared kubeconfig. It is created once at process
// start and reused for every request; no request-level token overrides apply.
func NewSharedClient(configPath string, options Options) (*Client, error) {
	if configPath == "" {
		return nil, fmt.Errorf("kubeconfig path is empty")
	}

	rawConfig, err := clientcmd.LoadFromFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	restConfig, err := clientcmd.BuildConfigFromFlags("", configPath)
	if err != nil {
		return nil, fmt.Errorf("build rest config: %w", err)
	}

	if options.Timeout != 0 {
		restConfig.Timeout = options.Timeout
	}
	if options.QPS != 0 {
		restConfig.QPS = options.QPS
	}
	if options.Burst != 0 {
		restConfig.Burst = options.Burst
	}

	contextName, authInfoName := rawConfig.CurrentContext, ""
	if current, ok := rawConfig.Contexts[contextName]; ok {
		authInfoName = current.AuthInfo
	}

	return newClient(restConfig, configPath, "shared-kubeconfig", clientcmdapiConfig{
		CurrentContext: contextName,
		AuthInfoName:   authInfoName,
	})
}

func NewFactory(configPath string) (*Factory, error) {
	if configPath == "" {
		return nil, fmt.Errorf("kubeconfig path is empty")
	}

	if _, err := os.Stat(configPath); err != nil {
		return nil, fmt.Errorf("stat kubeconfig: %w", err)
	}

	rawConfig, err := clientcmd.LoadFromFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	restConfig, err := clientcmd.BuildConfigFromFlags("", configPath)
	if err != nil {
		return nil, fmt.Errorf("build rest config: %w", err)
	}

	authInfoName := ""
	if context, ok := rawConfig.Contexts[rawConfig.CurrentContext]; ok {
		authInfoName = context.AuthInfo
	}

	return &Factory{
		configPath: configPath,
		baseConfig: restConfig,
		rawConfig: clientcmdapiConfig{
			CurrentContext: rawConfig.CurrentContext,
			AuthInfoName:   authInfoName,
		},
	}, nil
}

func (f *Factory) NewClientForToken(token string) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("token is empty")
	}

	config := rest.CopyConfig(f.baseConfig)
	config.BearerToken = strings.TrimSpace(token)
	config.BearerTokenFile = ""
	config.Username = ""
	config.Password = ""
	config.CertFile = ""
	config.KeyFile = ""
	config.CertData = nil
	config.KeyData = nil
	config.AuthProvider = nil
	config.ExecProvider = nil

	return newClient(config, f.configPath, "token", f.rawConfig)
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
