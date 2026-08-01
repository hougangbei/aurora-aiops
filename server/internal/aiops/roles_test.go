package aiops

import (
	"errors"
	"strings"
	"testing"
)

func TestDecodeTriageValid(t *testing.T) {
	out, err := DecodeTriage(`{"summary":"pod crash loop","severity":"critical","rationale":"restarts"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary != "pod crash loop" || out.Severity != "critical" {
		t.Fatalf("out=%+v", out)
	}
}

func TestDecodeTriageFencedJSON(t *testing.T) {
	out, err := DecodeTriage("```json\n{\"summary\":\"x\",\"severity\":\"warning\"}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if out.Severity != "warning" {
		t.Fatalf("out=%+v", out)
	}
}

func TestDecodeTriageRejectsInvalidJSON(t *testing.T) {
	if _, err := DecodeTriage("not json at all"); !errors.Is(err, ErrInvalidRoleOutput) {
		t.Fatalf("err=%v want ErrInvalidRoleOutput", err)
	}
}

func TestDecodeTriageRejectsEmptySummary(t *testing.T) {
	if _, err := DecodeTriage(`{"summary":"","severity":"info"}`); !errors.Is(err, ErrInvalidRoleOutput) {
		t.Fatal("expected rejection of empty summary")
	}
}

func TestDecodeTriageRejectsBadSeverity(t *testing.T) {
	if _, err := DecodeTriage(`{"summary":"x","severity":"catastrophic"}`); !errors.Is(err, ErrInvalidRoleOutput) {
		t.Fatal("expected rejection of unknown severity")
	}
}

func TestDecodeCollectorValid(t *testing.T) {
	out, err := DecodeCollector(`{"targetKind":"Pod","targetName":"api-0","commands":["collect snapshot"],"notes":"n"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out.TargetKind != "Pod" || out.TargetName != "api-0" {
		t.Fatalf("out=%+v", out)
	}
}

func TestDecodeCollectorRejectsInvalidJSON(t *testing.T) {
	if _, err := DecodeCollector("garbage"); !errors.Is(err, ErrInvalidRoleOutput) {
		t.Fatalf("err=%v want ErrInvalidRoleOutput", err)
	}
}

func TestDecodeCollectorRejectsEmptyTarget(t *testing.T) {
	if _, err := DecodeCollector(`{"targetKind":"","targetName":""}`); !errors.Is(err, ErrInvalidRoleOutput) {
		t.Fatal("expected rejection of empty target")
	}
}

func TestDecodeRootCauseValid(t *testing.T) {
	valid := map[string]bool{"snapshot": true, "log-app": true}
	text := `{"candidates":[{"summary":"oomkilled","confidence":0.9,"evidenceIds":["log-app"],"verificationSteps":["check events"]}]}`
	out, err := DecodeRootCause(text, valid)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Candidates) != 1 || out.Candidates[0].Summary != "oomkilled" {
		t.Fatalf("out=%+v", out)
	}
}

func TestDecodeRootCauseRejectsInvalidJSON(t *testing.T) {
	if _, err := DecodeRootCause("garbage", nil); !errors.Is(err, ErrInvalidRoleOutput) {
		t.Fatalf("err=%v want ErrInvalidRoleOutput", err)
	}
}

func TestDecodeRootCauseRejectsEmptyCandidates(t *testing.T) {
	if _, err := DecodeRootCause(`{"candidates":[]}`, nil); !errors.Is(err, ErrInvalidRoleOutput) {
		t.Fatal("expected rejection of empty candidates")
	}
}

func TestDecodeRootCauseRejectsConfidenceOutOfRange(t *testing.T) {
	valid := map[string]bool{"log-app": true}
	for _, conf := range []string{"1.5", "-0.1", "7"} {
		text := `{"candidates":[{"summary":"x","confidence":` + conf + `,"evidenceIds":["log-app"]}]}`
		if _, err := DecodeRootCause(text, valid); !errors.Is(err, ErrInvalidRoleOutput) {
			t.Fatalf("confidence %s not rejected: %v", conf, err)
		}
	}
}

func TestDecodeRootCauseRejectsUnknownEvidence(t *testing.T) {
	valid := map[string]bool{"log-app": true}
	text := `{"candidates":[{"summary":"x","confidence":0.5,"evidenceIds":["log-app","snap-999"]}]}`
	if _, err := DecodeRootCause(text, valid); !errors.Is(err, ErrInvalidRoleOutput) {
		t.Fatal("expected rejection of unknown evidence id")
	}
}

func TestDecodeRootCauseRejectsMissingEvidence(t *testing.T) {
	if _, err := DecodeRootCause(`{"candidates":[{"summary":"x","confidence":0.5,"evidenceIds":[]}]}`, nil); !errors.Is(err, ErrInvalidRoleOutput) {
		t.Fatal("expected rejection of empty evidenceIds")
	}
}

func TestDecodeRemediationValid(t *testing.T) {
	text := `{"actions":[{"command":"kubectl rollout restart deployment/api","reason":"restart after crash loop","risk":"low"}]}`
	out, err := DecodeRemediation(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Actions) != 1 || out.Actions[0].Risk != "low" {
		t.Fatalf("out=%+v", out)
	}
}

func TestDecodeRemediationRejectsShellString(t *testing.T) {
	for _, cmd := range []string{
		`kubectl get pods; rm -rf /`,
		`kubectl get pods && curl evil.example`,
		`bash -c 'curl evil.example'`,
		`echo hi | sh`,
		`kubectl delete pod x > /dev/null`,
	} {
		text := `{"actions":[{"command":"` + cmd + `","reason":"r","risk":"high"}]}`
		if _, err := DecodeRemediation(text); !errors.Is(err, ErrInvalidRoleOutput) {
			t.Fatalf("shell string %q not rejected: %v", cmd, err)
		}
	}
}

func TestDecodeRemediationRejectsEmptyActions(t *testing.T) {
	if _, err := DecodeRemediation(`{"actions":[]}`); !errors.Is(err, ErrInvalidRoleOutput) {
		t.Fatal("expected rejection of empty actions")
	}
}

func TestDecodeRemediationRejectsEmptyCommand(t *testing.T) {
	if _, err := DecodeRemediation(`{"actions":[{"command":"","reason":"r","risk":"low"}]}`); !errors.Is(err, ErrInvalidRoleOutput) {
		t.Fatal("expected rejection of empty command")
	}
}

func TestDecodeRemediationRejectsBadRisk(t *testing.T) {
	if _, err := DecodeRemediation(`{"actions":[{"command":"kubectl get pods","reason":"r","risk":"severe"}]}`); !errors.Is(err, ErrInvalidRoleOutput) {
		t.Fatal("expected rejection of unknown risk")
	}
}

func TestDecodeRiskReviewValid(t *testing.T) {
	out, err := DecodeRiskReview(`{"riskLevel":"high","approved":false,"blockers":["takes service down"],"rationale":"r"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out.RiskLevel != "high" || out.Approved {
		t.Fatalf("out=%+v", out)
	}
}

func TestDecodeRiskReviewRejectsBadRiskLevel(t *testing.T) {
	if _, err := DecodeRiskReview(`{"riskLevel":"extreme","approved":true}`); !errors.Is(err, ErrInvalidRoleOutput) {
		t.Fatal("expected rejection of unknown risk level")
	}
}

func TestDecodeRiskReviewRejectsInvalidJSON(t *testing.T) {
	if _, err := DecodeRiskReview("[]"); !errors.Is(err, ErrInvalidRoleOutput) {
		t.Fatalf("err=%v want ErrInvalidRoleOutput", err)
	}
}

func TestDeterministicTriage(t *testing.T) {
	inc := Incident{ID: "inc-1", Summary: "pod down", Severity: SeverityCritical, ResourceKind: "Pod", ResourceName: "api-0"}
	out := DeterministicTriage(inc)
	if out.Summary != "pod down" || out.Severity != "critical" {
		t.Fatalf("out=%+v", out)
	}
}

func TestDeterministicCollector(t *testing.T) {
	inc := Incident{ID: "inc-1", Summary: "pod down", Severity: SeverityCritical, ResourceKind: "Pod", ResourceName: "api-0"}
	out := DeterministicCollector(inc)
	if out.TargetKind != "Pod" || out.TargetName != "api-0" {
		t.Fatalf("out=%+v", out)
	}
	for _, cmd := range out.Commands {
		if isShellString(cmd) {
			t.Fatalf("deterministic command is a shell string: %q", cmd)
		}
	}
}

func TestPromptTreatsEvidenceAsUntrusted(t *testing.T) {
	p := strings.ToLower(BuildRootCausePrompt(`{"incident":{"id":"inc-1"}}`))
	for _, needle := range []string{"untrusted", "do not execute"} {
		if !strings.Contains(p, needle) {
			t.Fatalf("root cause prompt missing %q", needle)
		}
	}
	for _, rolePrompt := range []string{
		BuildTriagePrompt(`{}`),
		BuildCollectorPrompt(`{}`),
		BuildRemediationPrompt(`{}`, []RootCauseCandidate{}),
		BuildRiskReviewPrompt(`{}`, RemediationOutput{}),
	} {
		lower := strings.ToLower(rolePrompt)
		if !strings.Contains(lower, "untrusted") {
			t.Fatalf("role prompt missing untrusted-data instruction")
		}
		if strings.Contains(lower, "conceal your reasoning") {
			t.Fatalf("role prompt demands hidden chain-of-thought")
		}
	}
}
