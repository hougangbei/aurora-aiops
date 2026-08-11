package catalog

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
)

const (
	kubernetesVersion        = "v1.35.6"
	kubernetesPackage        = "1.35.6-1.1"
	ciliumVersion            = "1.19.4"
	ciliumCLIVersion         = "0.19.2"
	kubernetesPodCIDR        = "10.244.0.0/16"
	containerdSocket         = "unix:///run/containerd/containerd.sock"
	containerdConfig         = "/etc/containerd/config.toml"
	kubeadmConfigPath        = "/etc/kubernetes/kubeadm-config.yaml"
	kubeadmConfigStagingPath = "/tmp/aurora-aiops/kubeadm-config.yaml"
	kubeadmKeyring           = "/etc/apt/keyrings/kubernetes-archive-keyring.gpg"
	kubeadmSource            = "/etc/apt/sources.list.d/kubernetes.list"
)

var kubeadmHostnamePattern = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)(?:\.(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?))*$`)

func kubeadmCommands(server assets.Server) ([]string, error) {
	if !kubeadmHostnamePattern.MatchString(server.Name) || len(server.Name) > 253 {
		return nil, fmt.Errorf("invalid kubeadm hostname")
	}
	if server.Username == "" {
		return nil, fmt.Errorf("target username is missing")
	}
	priv := func(command string) string { return kubeadmPrivilege(server, command) }
	return []string{
		priv("install -d -m 0755 /etc/modules-load.d /etc/sysctl.d"),
		priv("printf '%s\\n' overlay br_netfilter | install -m 0644 /dev/stdin /etc/modules-load.d/kubernetes.conf"),
		priv("modprobe overlay && modprobe br_netfilter"),
		priv("test -e /etc/fstab.aurora-kubeadm.bak || cp -a /etc/fstab /etc/fstab.aurora-kubeadm.bak && sed -i -E '/^[[:space:]]*[^#].*[[:space:]]swap[[:space:]]/ s/^[[:space:]]*/# /' /etc/fstab"),
		priv("swapoff -a"),
		priv("printf '%s\\n' 'net.bridge.bridge-nf-call-iptables = 1' 'net.bridge.bridge-nf-call-ip6tables = 1' 'net.ipv4.ip_forward = 1' | install -m 0644 /dev/stdin /etc/sysctl.d/99-kubernetes-cri.conf.tmp && mv -f /etc/sysctl.d/99-kubernetes-cri.conf.tmp /etc/sysctl.d/99-kubernetes-cri.conf"),
		priv("sysctl --system"),
		priv("install -d -m 0755 /etc/containerd && containerd config default > /etc/containerd/config.toml.tmp && sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml.tmp && grep -F 'SystemdCgroup = true' /etc/containerd/config.toml.tmp && mv -f /etc/containerd/config.toml.tmp " + containerdConfig),
		priv("systemctl enable --now containerd && test -S /run/containerd/containerd.sock"),
		priv("install -d -m 0755 /etc/apt/keyrings && test -s " + kubeadmKeyring),
		priv("printf '%s\\n' 'deb [signed-by=" + kubeadmKeyring + "] https://pkgs.k8s.io/core:/stable:/v1.35/deb/ /' | install -m 0644 /dev/stdin " + kubeadmSource + ".tmp && mv -f " + kubeadmSource + ".tmp " + kubeadmSource),
		priv("apt-get update"),
		priv("apt-get install -y containerd kubelet=" + kubernetesPackage + " kubeadm=" + kubernetesPackage + " kubectl=" + kubernetesPackage),
		priv("apt-mark hold kubelet kubeadm kubectl"),
		priv("test -s " + kubeadmConfigPath),
		priv("kubeadm init --config " + kubeadmConfigPath + " --kubernetes-version " + kubernetesVersion + " --pod-network-cidr " + kubernetesPodCIDR + " --cri-socket " + containerdSocket),
		priv("install -d -m 0700 /root/.kube && install -m 0600 /etc/kubernetes/admin.conf /root/.kube/config"),
		priv("kubectl --kubeconfig /etc/kubernetes/admin.conf taint nodes --all node-role.kubernetes.io/control-plane-"),
		priv("KUBECONFIG=/etc/kubernetes/admin.conf cilium install --version " + ciliumVersion + " --set ipam.mode=kubernetes"),
		priv("KUBECONFIG=/etc/kubernetes/admin.conf cilium status --wait --wait-duration 15m"),
	}, nil
}

func kubeadmPrivilege(server assets.Server, command string) string {
	if server.Username == "root" {
		return command
	}
	return "sudo -n -- sh -c " + commandPath(command)
}

func kubeadmConfigYAML(hostname string) (string, error) {
	if !kubeadmHostnamePattern.MatchString(hostname) || len(hostname) > 253 {
		return "", fmt.Errorf("invalid kubeadm hostname")
	}
	return "apiVersion: kubeadm.k8s.io/v1beta4\nkind: InitConfiguration\nlocalAPIEndpoint:\n  advertiseAddress: 0.0.0.0\n  bindPort: 6443\nnodeRegistration:\n  criSocket: " + containerdSocket + "\n  name: " + hostname + "\n---\napiVersion: kubeadm.k8s.io/v1beta4\nkind: ClusterConfiguration\nkubernetesVersion: " + kubernetesVersion + "\nnetworking:\n  podSubnet: " + kubernetesPodCIDR + "\n", nil
}

func kubeadmPackageSource(osFamily string) (string, error) {
	if osFamily != "ubuntu" && osFamily != "debian" {
		return "", fmt.Errorf("unsupported operating system")
	}
	return "deb [signed-by=" + kubeadmKeyring + "] https://pkgs.k8s.io/core:/stable:/v1.35/deb/ /", nil
}

func containsForbiddenKubeadmCommand(commands []string) bool {
	joined := strings.ToLower(strings.Join(commands, "\n"))
	for _, forbidden := range []string{"kubeadm reset", "latest", "apt-key", "curl | sh", "apt-get upgrade", "apt upgrade"} {
		if strings.Contains(joined, forbidden) {
			return true
		}
	}
	return false
}
