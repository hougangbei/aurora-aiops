package catalog

import (
	"errors"
	"testing"

	"github.com/hougangbei/aurora-aiops/server/internal/deployment"
)

func TestKubeadmPreflightSupportedMatrix(t *testing.T) {
	base := PreflightInput{OSRelease: "ID=ubuntu\nVERSION_ID=\"22.04\"\n", KernelVersion: "5.15.0-100", CPUs: 2, MemoryBytes: 4 << 30, RootFreeBytes: 30 << 30, DefaultRoute: true, DNS: true, SudoNonInteractive: true, SwapDisablePersistent: true}
	for _, fixture := range []string{"ID=ubuntu\nVERSION_ID=\"22.04\"\n", "ID=ubuntu\nVERSION_ID=\"24.04\"\n", "ID=debian\nVERSION_ID=\"12\"\n"} {
		base.OSRelease = fixture
		if _, err := ValidateKubeadmPreflight(base); err != nil {
			t.Fatalf("fixture %q: %v", fixture, err)
		}
	}
}

func TestKubeadmPreflightRejectsUnsupportedConditions(t *testing.T) {
	base := PreflightInput{OSRelease: "ID=ubuntu\nVERSION_ID=\"22.04\"\n", KernelVersion: "5.15.0", CPUs: 2, MemoryBytes: 4 << 30, RootFreeBytes: 30 << 30, DefaultRoute: true, DNS: true, SudoNonInteractive: true, SwapDisablePersistent: true}
	cases := []struct {
		name   string
		mutate func(*PreflightInput)
	}{
		{"old os", func(v *PreflightInput) { v.OSRelease = "ID=ubuntu\nVERSION_ID=\"20.04\"\n" }},
		{"new os", func(v *PreflightInput) { v.OSRelease = "ID=ubuntu\nVERSION_ID=\"26.04\"\n" }},
		{"kernel", func(v *PreflightInput) { v.KernelVersion = "5.9.0" }},
		{"cpu", func(v *PreflightInput) { v.CPUs = 1 }},
		{"memory", func(v *PreflightInput) { v.MemoryBytes = 3 << 30 }},
		{"disk", func(v *PreflightInput) { v.RootFreeBytes = 29 << 30 }},
		{"route", func(v *PreflightInput) { v.DefaultRoute = false }},
		{"dns", func(v *PreflightInput) { v.DNS = false }},
		{"sudo", func(v *PreflightInput) { v.SudoNonInteractive = false }},
		{"swap", func(v *PreflightInput) { v.SwapDisablePersistent = false }},
		{"port", func(v *PreflightInput) { v.OccupiedPorts = map[int]bool{6443: true} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			tc.mutate(&input)
			if _, err := ValidateKubeadmPreflight(input); err == nil {
				t.Fatal("preflight unexpectedly passed")
			}
		})
	}
}

func TestKubeadmPreflightRequiresAdoptionForExistingCluster(t *testing.T) {
	input := PreflightInput{OSRelease: "ID=ubuntu\nVERSION_ID=\"22.04\"\n", KernelVersion: "5.15.0", CPUs: 2, MemoryBytes: 4 << 30, RootFreeBytes: 30 << 30, DefaultRoute: true, DNS: true, SudoNonInteractive: true, SwapDisablePersistent: true, ExistingAdminConfig: true, ExistingVersion: "v1.34.0", ExistingNodes: []string{"node-1"}}
	_, err := ValidateKubeadmPreflight(input)
	var adoption *deployment.AdoptionRequiredError
	if !errors.As(err, &adoption) || adoption.Summary.Version != "v1.34.0" || len(adoption.Summary.Nodes) != 1 {
		t.Fatalf("err=%v adoption=%+v", err, adoption)
	}
}
