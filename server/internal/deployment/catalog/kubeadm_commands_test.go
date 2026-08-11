package catalog

import (
	"strings"
	"testing"
)

func TestKubeadmCommandsUseFixedVersionsSafePrivilegeAndPaths(t *testing.T) {
	for _, server := range []struct {
		name string
		user string
	}{
		{"root", "root"}, {"sudo", "ubuntu"},
	} {
		t.Run(server.name, func(t *testing.T) {
			s := kubeadmTestServer()
			s.Username = server.user
			cmds, err := kubeadmCommands(s)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(cmds, "\n")
			config, err := kubeadmConfigYAML(s.Name)
			if err != nil {
				t.Fatal(err)
			}
			joined += "\n" + config
			for _, want := range []string{"overlay", "br_netfilter", "SystemdCgroup = true", "1.35.6-1.1", "v1beta4", "v1.35.6", "10.244.0.0/16", "1.19.4"} {
				if !strings.Contains(joined, want) {
					t.Errorf("missing %q in commands: %s", want, joined)
				}
			}
			for _, forbidden := range []string{"kubeadm reset", "latest", "apt-key", "curl | sh", "apt upgrade", "apt-get upgrade"} {
				if strings.Contains(strings.ToLower(joined), strings.ToLower(forbidden)) {
					t.Errorf("forbidden %q in commands", forbidden)
				}
			}
			if server.user == "ubuntu" && strings.Contains(joined, "sudo -n -- sudo") {
				t.Fatal("double sudo prefix")
			}
		})
	}
}

func TestKubeadmCommandBuilderRejectsUnsafeHostnameAndTaskFragments(t *testing.T) {
	s := kubeadmTestServer()
	s.Name = "node; touch /tmp/pwned"
	if _, err := kubeadmCommands(s); err == nil {
		t.Fatal("unsafe hostname accepted")
	}
	if _, err := kubeadmConfigYAML("bad\nhost"); err == nil {
		t.Fatal("unsafe kubeadm hostname accepted")
	}
}
