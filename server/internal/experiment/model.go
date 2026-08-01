package experiment

// Group identifies the diagnosis strategy under test. The grouping is fixed:
// rule-based, single-LLM, and the multi-agent pipeline.
type Group string

const (
	GroupRules      Group = "rules"
	GroupSingleLLM  Group = "single_llm"
	GroupMultiAgent Group = "multi_agent"
)

// Run is the outcome of one experiment run against one scenario instance.
type Run struct {
	ID                   string
	Group                Group
	Seed                 int64
	Scenario             string
	ExpectedRootCause    string
	Top1Correct          bool
	Top3Contains         bool
	MTTDSeconds          float64
	EvidenceCompleteness float64 // 0..1
	HighRiskIntercepted  bool
	TokensUsed           int64
}

// Metrics aggregates a group of runs into the KPIs used for comparison.
type Metrics struct {
	Group                    Group   `json:"group"`
	SampleCount              int     `json:"sampleCount"`
	Top1Rate                 float64 `json:"top1Rate"`
	Top3Rate                 float64 `json:"top3Rate"`
	AvgMTTDSeconds           float64 `json:"avgMttdSeconds"`
	EvidenceCompletenessRate float64 `json:"evidenceCompletenessRate"`
	HighRiskInterceptionRate float64 `json:"highRiskInterceptionRate"`
	AvgTokens                int64   `json:"avgTokens"`
	// ConfidenceInterval is the 95% CI half-width of the Top-1 rate; 0 when
	// the sample is too small.
	ConfidenceInterval  float64 `json:"confidenceInterval"`
	InsufficientSamples bool    `json:"insufficientSamples"`
}
