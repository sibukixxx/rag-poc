package usecase

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/eval"
)

// RunSummary is one run's aggregate view for comparison: the quality
// metrics already on the run plus latency and cost statistics derived
// from its per-case results (docs/V0.1_SPEC.md §8: "品質 / 平均・P95
// レイテンシ / 合計・平均コスト を並記").
type RunSummary struct {
	Run          eval.Run
	Cases        int
	Errors       int
	AvgLatencyMS float64
	P95LatencyMS int64
	TotalCostUSD float64
	AvgCostUSD   float64
}

// MetricDelta is one row of the Before/After table.
type MetricDelta struct {
	Name   string // e.g. "Hit Rate"
	Key    string // machine key, e.g. "hit_rate"
	A      float64
	B      float64
	Delta  float64 // B - A
	Winner string  // "a", "b", or "tie"
	// HigherIsBetter is false for latency/cost, so Winner and the UI's
	// coloring don't have to special-case them.
	HigherIsBetter bool
	// Available is false when the metric doesn't apply to both runs
	// (e.g. judge scores when only one run was judged); the row is kept
	// so the table shape is stable, but shouldn't decide a winner.
	Available bool
}

// CaseDelta pairs one case's result in A with its result in B.
type CaseDelta struct {
	CaseID  string
	Query   string
	A       *eval.CaseResult
	B       *eval.CaseResult
	Changed bool   // any compared metric differs
	Verdict string // "improved", "regressed", "same", or "mixed"
}

// Comparison is the Before/After of two runs over the same dataset.
type Comparison struct {
	Dataset   eval.Dataset
	A         RunSummary
	B         RunSummary
	Metrics   []MetricDelta
	Cases     []CaseDelta
	Winner    string // "a", "b", or "tie"
	Rationale string // one sentence on how Winner was decided
	Improved  int
	Regressed int
	Judged    bool // both runs were judge runs
	CreatedAt time.Time
}

// tieEpsilon is how close two metric values can be and still count as a
// tie, so a 0.001 difference in MRR doesn't crown a winner.
const tieEpsilon = 0.005

// CompareUseCase builds Comparisons from stored runs (docs/ROADMAP.md W9).
type CompareUseCase struct {
	Datasets eval.Store
}

func NewCompareUseCase(datasets eval.Store) *CompareUseCase {
	return &CompareUseCase{Datasets: datasets}
}

// Compare loads runs a and b, which must belong to the same dataset and
// both be finished ("done"), and produces their Before/After.
func (u *CompareUseCase) Compare(ctx context.Context, aID, bID string) (*Comparison, error) {
	if aID == bID {
		return nil, fmt.Errorf("compare: a and b are the same run")
	}
	runA, err := u.Datasets.GetRun(ctx, aID)
	if err != nil {
		return nil, fmt.Errorf("compare: loading run a: %w", err)
	}
	runB, err := u.Datasets.GetRun(ctx, bID)
	if err != nil {
		return nil, fmt.Errorf("compare: loading run b: %w", err)
	}
	if runA.DatasetID != runB.DatasetID {
		return nil, fmt.Errorf("compare: runs belong to different datasets (%s vs %s)", runA.DatasetID, runB.DatasetID)
	}
	if runA.Status != eval.RunStatusDone || runB.Status != eval.RunStatusDone {
		return nil, fmt.Errorf("compare: both runs must be done (a: %s, b: %s)", runA.Status, runB.Status)
	}

	dataset, err := u.Datasets.GetDataset(ctx, runA.DatasetID)
	if err != nil {
		return nil, fmt.Errorf("compare: loading dataset: %w", err)
	}
	cases, err := u.Datasets.ListCases(ctx, dataset.ID)
	if err != nil {
		return nil, fmt.Errorf("compare: loading cases: %w", err)
	}
	resultsA, err := u.Datasets.ListCaseResults(ctx, runA.ID)
	if err != nil {
		return nil, fmt.Errorf("compare: loading results for a: %w", err)
	}
	resultsB, err := u.Datasets.ListCaseResults(ctx, runB.ID)
	if err != nil {
		return nil, fmt.Errorf("compare: loading results for b: %w", err)
	}

	return BuildComparison(*dataset, *runA, resultsA, *runB, resultsB, cases), nil
}

// BuildComparison is the pure part of Compare, exposed so it can be unit
// tested without a store.
func BuildComparison(dataset eval.Dataset, runA eval.Run, resultsA []eval.CaseResult, runB eval.Run, resultsB []eval.CaseResult, cases []eval.Case) *Comparison {
	c := &Comparison{
		Dataset:   dataset,
		A:         summarizeRun(runA, resultsA),
		B:         summarizeRun(runB, resultsB),
		Judged:    runA.Judge && runB.Judge,
		CreatedAt: time.Now(),
	}
	c.Metrics = buildMetricDeltas(c.A, c.B, c.Judged)
	c.Cases = buildCaseDeltas(cases, resultsA, resultsB, c.Judged)
	for _, cd := range c.Cases {
		switch cd.Verdict {
		case "improved":
			c.Improved++
		case "regressed":
			c.Regressed++
		}
	}
	c.Winner, c.Rationale = decideWinner(c.Metrics, c.A, c.B)
	return c
}

func summarizeRun(run eval.Run, results []eval.CaseResult) RunSummary {
	s := RunSummary{Run: run, Cases: len(results)}
	if len(results) == 0 {
		return s
	}
	durations := make([]int64, 0, len(results))
	var totalLatency int64
	for _, r := range results {
		if r.Error != "" {
			s.Errors++
		}
		durations = append(durations, r.DurationMS)
		totalLatency += r.DurationMS
		s.TotalCostUSD += r.CostUSD
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	s.AvgLatencyMS = float64(totalLatency) / float64(len(results))
	s.P95LatencyMS = percentile(durations, 0.95)
	s.AvgCostUSD = s.TotalCostUSD / float64(len(results))
	return s
}

// percentile uses the nearest-rank method on an ascending-sorted slice.
func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

func buildMetricDeltas(a, b RunSummary, judged bool) []MetricDelta {
	type spec struct {
		name, key      string
		get            func(RunSummary) float64
		higherIsBetter bool
		available      bool
	}
	specs := []spec{
		{"Hit Rate", "hit_rate", func(s RunSummary) float64 { return s.Run.HitRate }, true, true},
		{"Recall@K", "recall_at_k", func(s RunSummary) float64 { return s.Run.RecallAtK }, true, true},
		{"Precision@K", "precision_at_k", func(s RunSummary) float64 { return s.Run.PrecisionAtK }, true, true},
		{"MRR", "mrr", func(s RunSummary) float64 { return s.Run.MRR }, true, true},
		{"Correctness", "correctness", func(s RunSummary) float64 { return s.Run.Correctness }, true, judged},
		{"Groundedness", "groundedness", func(s RunSummary) float64 { return s.Run.Groundedness }, true, judged},
		{"Relevance", "relevance", func(s RunSummary) float64 { return s.Run.Relevance }, true, judged},
		{"Avg latency (ms)", "avg_latency_ms", func(s RunSummary) float64 { return s.AvgLatencyMS }, false, true},
		{"P95 latency (ms)", "p95_latency_ms", func(s RunSummary) float64 { return float64(s.P95LatencyMS) }, false, true},
		{"Total cost (USD)", "total_cost_usd", func(s RunSummary) float64 { return s.TotalCostUSD }, false, true},
		{"Avg cost / case (USD)", "avg_cost_usd", func(s RunSummary) float64 { return s.AvgCostUSD }, false, true},
	}
	out := make([]MetricDelta, 0, len(specs))
	for _, sp := range specs {
		va, vb := sp.get(a), sp.get(b)
		md := MetricDelta{Name: sp.name, Key: sp.key, A: va, B: vb, Delta: vb - va, HigherIsBetter: sp.higherIsBetter, Available: sp.available}
		md.Winner = pickWinner(va, vb, sp.higherIsBetter, metricEpsilon(sp.key))
		out = append(out, md)
	}
	return out
}

// metricEpsilon widens the tie band for latency (milliseconds) and cost
// (absolute USD is tiny), where tieEpsilon's 0.005 would be meaningless.
func metricEpsilon(key string) float64 {
	switch key {
	case "avg_latency_ms", "p95_latency_ms":
		return 1 // 1ms
	case "total_cost_usd", "avg_cost_usd":
		return 1e-6
	}
	return tieEpsilon
}

func pickWinner(a, b float64, higherIsBetter bool, eps float64) string {
	if math.Abs(a-b) <= eps {
		return "tie"
	}
	if (b > a) == higherIsBetter {
		return "b"
	}
	return "a"
}

// decideWinner picks the overall winner on quality first (the mean of the
// available higher-is-better metrics), then cost as the tie-breaker:
// a change that doesn't move quality but is cheaper still wins.
func decideWinner(metrics []MetricDelta, a, b RunSummary) (string, string) {
	var sumA, sumB float64
	var n int
	for _, m := range metrics {
		if !m.HigherIsBetter || !m.Available {
			continue
		}
		sumA += m.A
		sumB += m.B
		n++
	}
	if n == 0 {
		return "tie", "no quality metrics to compare"
	}
	qa, qb := sumA/float64(n), sumB/float64(n)
	switch pickWinner(qa, qb, true, tieEpsilon) {
	case "a":
		return "a", fmt.Sprintf("A has higher mean quality (%.3f vs %.3f)", qa, qb)
	case "b":
		return "b", fmt.Sprintf("B has higher mean quality (%.3f vs %.3f)", qb, qa)
	}
	switch pickWinner(a.TotalCostUSD, b.TotalCostUSD, false, metricEpsilon("total_cost_usd")) {
	case "a":
		return "a", fmt.Sprintf("quality is tied (%.3f); A is cheaper ($%.6f vs $%.6f)", qa, a.TotalCostUSD, b.TotalCostUSD)
	case "b":
		return "b", fmt.Sprintf("quality is tied (%.3f); B is cheaper ($%.6f vs $%.6f)", qa, b.TotalCostUSD, a.TotalCostUSD)
	}
	return "tie", fmt.Sprintf("quality (%.3f) and cost are tied", qa)
}

func buildCaseDeltas(cases []eval.Case, resultsA, resultsB []eval.CaseResult, judged bool) []CaseDelta {
	byA := make(map[string]*eval.CaseResult, len(resultsA))
	for i := range resultsA {
		byA[resultsA[i].CaseID] = &resultsA[i]
	}
	byB := make(map[string]*eval.CaseResult, len(resultsB))
	for i := range resultsB {
		byB[resultsB[i].CaseID] = &resultsB[i]
	}
	out := make([]CaseDelta, 0, len(cases))
	for _, c := range cases {
		cd := CaseDelta{CaseID: c.ID, Query: c.Query, A: byA[c.ID], B: byB[c.ID]}
		cd.Verdict, cd.Changed = caseVerdict(cd.A, cd.B, judged)
		out = append(out, cd)
	}
	return out
}

// caseVerdict compares one case across runs on hit, reciprocal rank and
// (for judged pairs) the mean judge score. A missing result on one side
// (e.g. the case was added after that run) is reported as "same" with
// Changed=false, since there's nothing to diff.
func caseVerdict(a, b *eval.CaseResult, judged bool) (string, bool) {
	if a == nil || b == nil {
		return "same", false
	}
	better, worse := 0, 0
	track := func(va, vb float64, eps float64) {
		switch pickWinner(va, vb, true, eps) {
		case "b":
			better++
		case "a":
			worse++
		}
	}
	track(boolScore(a.Hit && a.Error == ""), boolScore(b.Hit && b.Error == ""), 0)
	track(a.ReciprocalRank, b.ReciprocalRank, tieEpsilon)
	if judged {
		track(judgeMean(a), judgeMean(b), tieEpsilon)
	}
	switch {
	case better > 0 && worse > 0:
		return "mixed", true
	case better > 0:
		return "improved", true
	case worse > 0:
		return "regressed", true
	}
	return "same", false
}

func boolScore(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func judgeMean(r *eval.CaseResult) float64 {
	if r == nil || r.Error != "" {
		return 0
	}
	return (r.Correctness + r.Groundedness + r.Relevance) / 3
}

// RenderComparisonMarkdown formats a Comparison as a customer-facing
// Before/After report (docs/ROADMAP.md W9: "顧客向け成果報告の種").
func RenderComparisonMarkdown(c *Comparison) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Evaluation comparison: %s\n\n", c.Dataset.Name)
	fmt.Fprintf(&sb, "Generated %s\n\n", c.CreatedAt.Format("2006-01-02 15:04 MST"))

	sb.WriteString("## Runs\n\n")
	sb.WriteString("| | A (before) | B (after) |\n|---|---|---|\n")
	fmt.Fprintf(&sb, "| Run ID | `%s` | `%s` |\n", c.A.Run.ID, c.B.Run.ID)
	fmt.Fprintf(&sb, "| Started | %s | %s |\n", c.A.Run.StartedAt.Format(time.RFC3339), c.B.Run.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&sb, "| Config | %s | %s |\n", describeConfig(c.A.Run), describeConfig(c.B.Run))
	fmt.Fprintf(&sb, "| Cases (errors) | %d (%d) | %d (%d) |\n\n", c.A.Cases, c.A.Errors, c.B.Cases, c.B.Errors)

	sb.WriteString("## Metrics\n\n")
	sb.WriteString("| Metric | A | B | Δ (B−A) | Better |\n|---|---:|---:|---:|:---:|\n")
	for _, m := range c.Metrics {
		if !m.Available {
			continue
		}
		fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s |\n",
			m.Name, formatMetric(m.Key, m.A), formatMetric(m.Key, m.B), formatDelta(m.Key, m.Delta), winnerLabel(m.Winner))
	}
	fmt.Fprintf(&sb, "\n**Winner: %s** — %s\n\n", winnerLabel(c.Winner), c.Rationale)
	fmt.Fprintf(&sb, "Per case: %d improved, %d regressed, %d unchanged.\n\n", c.Improved, c.Regressed, len(c.Cases)-c.Improved-c.Regressed)

	var changed []CaseDelta
	for _, cd := range c.Cases {
		if cd.Changed {
			changed = append(changed, cd)
		}
	}
	if len(changed) > 0 {
		sb.WriteString("## Changed cases\n\n")
		if c.Judged {
			sb.WriteString("| Verdict | Query | Hit A→B | RR A→B | Judge mean A→B |\n|---|---|:---:|---:|---:|\n")
		} else {
			sb.WriteString("| Verdict | Query | Hit A→B | RR A→B |\n|---|---|:---:|---:|\n")
		}
		for _, cd := range changed {
			row := fmt.Sprintf("| %s | %s | %s→%s | %.2f→%.2f |",
				cd.Verdict, escapePipes(cd.Query), hitLabel(cd.A), hitLabel(cd.B), cd.A.ReciprocalRank, cd.B.ReciprocalRank)
			if c.Judged {
				row += fmt.Sprintf(" %.2f→%.2f |", judgeMean(cd.A), judgeMean(cd.B))
			}
			sb.WriteString(row + "\n")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func describeConfig(r eval.Run) string {
	parts := []string{fmt.Sprintf("top_k=%d", r.TopK)}
	if r.Rerank {
		parts = append(parts, "rerank")
	}
	if r.Judge {
		parts = append(parts, "judge", "alias="+r.Alias)
	}
	return strings.Join(parts, ", ")
}

func formatMetric(key string, v float64) string {
	switch key {
	case "avg_latency_ms", "p95_latency_ms":
		return fmt.Sprintf("%.0f", v)
	case "total_cost_usd", "avg_cost_usd":
		return fmt.Sprintf("$%.6f", v)
	}
	return fmt.Sprintf("%.3f", v)
}

func formatDelta(key string, d float64) string {
	s := formatMetric(key, math.Abs(d))
	switch {
	case d > 0:
		return "+" + s
	case d < 0:
		return "−" + s
	}
	return "±0"
}

func winnerLabel(w string) string {
	switch w {
	case "a":
		return "A"
	case "b":
		return "B"
	}
	return "tie"
}

func hitLabel(r *eval.CaseResult) string {
	if r == nil {
		return "-"
	}
	if r.Error != "" {
		return "err"
	}
	if r.Hit {
		return "✓"
	}
	return "✗"
}

func escapePipes(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "|", "\\|")
}
