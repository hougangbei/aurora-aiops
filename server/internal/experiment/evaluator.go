package experiment

import "math"

// MinSampleForCI is the sample count required before a confidence interval is
// reported; below it the metrics are flagged insufficient.
const MinSampleForCI = 30

// ComputeMetrics aggregates runs into KPIs. It never fabricates percentages:
// an empty run set yields all-zero metrics flagged insufficient.
func ComputeMetrics(group Group, runs []Run) Metrics {
	metrics := Metrics{Group: group, SampleCount: len(runs)}
	if len(runs) == 0 {
		metrics.InsufficientSamples = true
		return metrics
	}

	var top1, top3, intercept int
	var mttdSum, evidenceSum float64
	var tokenSum int64
	for _, run := range runs {
		if run.Top1Correct {
			top1++
		}
		if run.Top3Contains {
			top3++
		}
		if run.HighRiskIntercepted {
			intercept++
		}
		mttdSum += run.MTTDSeconds
		evidenceSum += run.EvidenceCompleteness
		tokenSum += run.TokensUsed
	}

	n := float64(len(runs))
	metrics.Top1Rate = float64(top1) / n
	metrics.Top3Rate = float64(top3) / n
	metrics.AvgMTTDSeconds = mttdSum / n
	metrics.EvidenceCompletenessRate = evidenceSum / n
	metrics.HighRiskInterceptionRate = float64(intercept) / n
	metrics.AvgTokens = tokenSum / int64(len(runs))

	if len(runs) < MinSampleForCI {
		metrics.InsufficientSamples = true
		return metrics
	}
	metrics.ConfidenceInterval = wilsonCIHalfWidth(metrics.Top1Rate, n)
	return metrics
}

// wilsonCIHalfWidth returns the 95% Wilson score interval half-width for a
// proportion, which behaves better near 0/1 than the normal approximation.
func wilsonCIHalfWidth(p, n float64) float64 {
	if n <= 0 {
		return 0
	}
	z := 1.96
	denom := 1 + z*z/n
	center := (p + z*z/(2*n)) / denom
	margin := z * math.Sqrt((p*(1-p)+z*z/(4*n))/n) / denom
	return math.Abs(center - (center - margin))
}
