package experiment

import (
	"math"
	"testing"
)

func makeRun(top1, top3, intercept bool, mttd float64, evidence float64, tokens int64) Run {
	return Run{
		Top1Correct:          top1,
		Top3Contains:         top3,
		HighRiskIntercepted:  intercept,
		MTTDSeconds:          mttd,
		EvidenceCompleteness: evidence,
		TokensUsed:           tokens,
	}
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestComputeMetricsRates(t *testing.T) {
	runs := []Run{
		makeRun(true, true, true, 10, 1.0, 100),
		makeRun(true, true, false, 20, 0.8, 150),
		makeRun(false, true, true, 30, 0.9, 120),
		makeRun(true, false, false, 40, 0.7, 90),
		makeRun(false, false, true, 50, 0.6, 110),
	}
	m := ComputeMetrics(GroupMultiAgent, runs)

	if m.SampleCount != 5 {
		t.Fatalf("sample count=%d", m.SampleCount)
	}
	if !almostEqual(m.Top1Rate, 3.0/5) {
		t.Fatalf("top1=%v want 0.6", m.Top1Rate)
	}
	if !almostEqual(m.Top3Rate, 3.0/5) {
		t.Fatalf("top3=%v want 0.6", m.Top3Rate)
	}
	if !almostEqual(m.HighRiskInterceptionRate, 3.0/5) {
		t.Fatalf("interception=%v want 0.6", m.HighRiskInterceptionRate)
	}
	if !almostEqual(m.EvidenceCompletenessRate, (1.0+0.8+0.9+0.7+0.6)/5) {
		t.Fatalf("evidence=%v", m.EvidenceCompletenessRate)
	}
	if !almostEqual(m.AvgMTTDSeconds, 30) {
		t.Fatalf("mttd=%v want 30", m.AvgMTTDSeconds)
	}
	if m.AvgTokens != 114 {
		t.Fatalf("tokens=%d want 114", m.AvgTokens)
	}
	if !m.InsufficientSamples {
		t.Fatal("expected insufficient for 5 samples")
	}
	if m.ConfidenceInterval != 0 {
		t.Fatalf("ci=%v want 0 below min sample", m.ConfidenceInterval)
	}
}

func TestComputeMetricsEmpty(t *testing.T) {
	m := ComputeMetrics(GroupRules, nil)
	if m.SampleCount != 0 || m.Top1Rate != 0 || m.Top3Rate != 0 {
		t.Fatalf("empty metrics: %+v", m)
	}
	if !m.InsufficientSamples {
		t.Fatal("empty must be insufficient")
	}
}

func TestComputeMetricsConfidenceInterval(t *testing.T) {
	runs := make([]Run, 0, 40)
	for i := 0; i < 40; i++ {
		runs = append(runs, makeRun(i%2 == 0, true, true, 5, 1.0, 50))
	}
	m := ComputeMetrics(GroupSingleLLM, runs)
	if m.InsufficientSamples {
		t.Fatal("40 samples must not be insufficient")
	}
	if !almostEqual(m.Top1Rate, 0.5) {
		t.Fatalf("top1=%v", m.Top1Rate)
	}
	if m.ConfidenceInterval <= 0 || m.ConfidenceInterval >= 0.3 {
		t.Fatalf("ci=%v out of reasonable range", m.ConfidenceInterval)
	}
}

func TestComputeMetricsPerfectInterception(t *testing.T) {
	runs := []Run{
		makeRun(true, true, true, 5, 1.0, 10),
		makeRun(true, true, true, 5, 1.0, 10),
	}
	m := ComputeMetrics(GroupRules, runs)
	if !almostEqual(m.HighRiskInterceptionRate, 1.0) {
		t.Fatalf("interception=%v want 1.0", m.HighRiskInterceptionRate)
	}
}
