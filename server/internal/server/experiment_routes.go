package server

import (
	"bytes"
	"encoding/csv"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/heihuzicity-tech/aurora-aiops/server/internal/experiment"
	"github.com/heihuzicity-tech/aurora-aiops/server/internal/response"
)

// registerExperimentRoutes exposes experiment run recording and metrics export.
// The group is expected to carry the /api/v1 prefix and RequireSession.
func registerExperimentRoutes(group *gin.RouterGroup, repo experiment.RunRepository) {
	experiments := group.Group("/experiments")
	{
		experiments.POST("/runs", RequireAdmin(), handleAddExperimentRun(repo))
		experiments.GET("/metrics", handleExperimentMetrics(repo))
	}
}

type experimentRunRequest struct {
	ID                   string  `json:"id"`
	Group                string  `json:"group"`
	Seed                 int64   `json:"seed"`
	Scenario             string  `json:"scenario"`
	ExpectedRootCause    string  `json:"expectedRootCause"`
	Top1Correct          bool    `json:"top1Correct"`
	Top3Contains         bool    `json:"top3Contains"`
	MTTDSeconds          float64 `json:"mttdSeconds"`
	EvidenceCompleteness float64 `json:"evidenceCompleteness"`
	HighRiskIntercepted  bool    `json:"highRiskIntercepted"`
	TokensUsed           int64   `json:"tokensUsed"`
}

func handleAddExperimentRun(repo experiment.RunRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req experimentRunRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, response.Failure("INVALID_EXPERIMENT_RUN", "请求体格式不正确"))
			return
		}
		group := experiment.Group(req.Group)
		if !validGroup(group) {
			c.JSON(http.StatusBadRequest, response.Failure("INVALID_EXPERIMENT_GROUP", "未知实验分组"))
			return
		}
		run := experiment.Run{
			ID:                   req.ID,
			Group:                group,
			Seed:                 req.Seed,
			Scenario:             req.Scenario,
			ExpectedRootCause:    req.ExpectedRootCause,
			Top1Correct:          req.Top1Correct,
			Top3Contains:         req.Top3Contains,
			MTTDSeconds:          req.MTTDSeconds,
			EvidenceCompleteness: req.EvidenceCompleteness,
			HighRiskIntercepted:  req.HighRiskIntercepted,
			TokensUsed:           req.TokensUsed,
		}
		stored, err := repo.Add(c.Request.Context(), run)
		if err != nil {
			log.Printf("ADD_EXPERIMENT_RUN_FAILED: %v", err)
			c.JSON(http.StatusInternalServerError, response.Failure("ADD_EXPERIMENT_RUN_FAILED", "记录实验失败"))
			return
		}
		c.JSON(http.StatusCreated, response.Success(stored))
	}
}

func validGroup(group experiment.Group) bool {
	return group == experiment.GroupRules ||
		group == experiment.GroupSingleLLM ||
		group == experiment.GroupMultiAgent
}

var metricGroups = []experiment.Group{
	experiment.GroupRules,
	experiment.GroupSingleLLM,
	experiment.GroupMultiAgent,
}

func handleExperimentMetrics(repo experiment.RunRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		metrics := make([]experiment.Metrics, 0, len(metricGroups))
		for _, group := range metricGroups {
			runs, err := repo.ListByGroup(ctx, group)
			if err != nil {
				log.Printf("LIST_EXPERIMENT_RUNS_FAILED: %v", err)
				c.JSON(http.StatusInternalServerError, response.Failure("EXPERIMENT_METRICS_FAILED", "查询实验结果失败"))
				return
			}
			metrics = append(metrics, experiment.ComputeMetrics(group, runs))
		}

		if c.Query("format") == "csv" {
			writeMetricsCSV(c, metrics)
			return
		}
		c.JSON(http.StatusOK, response.Success(gin.H{"metrics": metrics}))
	}
}

func writeMetricsCSV(c *gin.Context, metrics []experiment.Metrics) {
	var buf bytes.Buffer
	// UTF-8 BOM：Excel 打开 CSV 时正确识别中文。
	buf.WriteString("\xEF\xBB\xBF")
	writer := csv.NewWriter(&buf)
	_ = writer.Write([]string{
		"group", "sampleCount", "top1Rate", "top3Rate", "avgMttdSeconds",
		"evidenceCompletenessRate", "highRiskInterceptionRate", "avgTokens",
		"confidenceInterval", "insufficientSamples",
	})
	for _, m := range metrics {
		_ = writer.Write([]string{
			string(m.Group),
			strconv.Itoa(m.SampleCount),
			strconv.FormatFloat(m.Top1Rate, 'f', 4, 64),
			strconv.FormatFloat(m.Top3Rate, 'f', 4, 64),
			strconv.FormatFloat(m.AvgMTTDSeconds, 'f', 2, 64),
			strconv.FormatFloat(m.EvidenceCompletenessRate, 'f', 4, 64),
			strconv.FormatFloat(m.HighRiskInterceptionRate, 'f', 4, 64),
			strconv.FormatInt(m.AvgTokens, 10),
			strconv.FormatFloat(m.ConfidenceInterval, 'f', 4, 64),
			strconv.FormatBool(m.InsufficientSamples),
		})
	}
	writer.Flush()

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="experiment-metrics.csv"`)
	c.String(http.StatusOK, buf.String())
}
