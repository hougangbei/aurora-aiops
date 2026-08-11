package catalog

import (
	"fmt"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

type kubeconfigDocument struct {
	Clusters []struct {
		Cluster struct {
			Server string `yaml:"server"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Contexts []struct {
		Name string `yaml:"name"`
	} `yaml:"contexts"`
	CurrentContext string `yaml:"current-context"`
}

func validateAdminKubeconfig(data []byte) error {
	if len(data) == 0 || len(data) > 1<<20 {
		return fmt.Errorf("admin kubeconfig is empty or too large")
	}
	var document kubeconfigDocument
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("invalid admin kubeconfig")
	}
	if len(document.Clusters) != 1 || len(document.Contexts) != 1 || document.CurrentContext == "" || document.Contexts[0].Name != document.CurrentContext {
		return fmt.Errorf("admin kubeconfig must contain one current context")
	}
	endpoint, err := url.Parse(document.Clusters[0].Cluster.Server)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || strings.ContainsAny(document.Clusters[0].Cluster.Server, "\r\n\x00") {
		return fmt.Errorf("admin kubeconfig server must use HTTPS")
	}
	return nil
}
