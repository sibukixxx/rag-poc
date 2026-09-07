// Package evaluation defines ForgeAI's deterministic RAG quality evidence.
// It deliberately contains measurements only; remediation and business
// interpretation are outside the public ForgeAI boundary.
package evaluation

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
)

const SchemaVersion = "1.0"

type Availability string

const (
	Observed      Availability = "OBSERVED"
	NotApplicable Availability = "NOT_APPLICABLE"
	Unknown       Availability = "UNKNOWN"
	Unavailable   Availability = "UNAVAILABLE"
	EvidenceError Availability = "ERROR"
)

type Metric struct {
	Status Availability `json:"status"`
	Value  *float64      `json:"value,omitempty"`
}

func observed(v float64) Metric { return Metric{Status: Observed, Value: &v} }

type Dataset struct {
	SchemaVersion   string `json:"schema_version"`
	ID              string `json:"id"`
	Version         string `json:"version"`
	Name            string `json:"name,omitempty"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	Cases           []Case `json:"cases"`
}

type Case struct {
	ID                  string   `json:"id"`
	Query               string   `json:"query"`
	RelevantDocumentIDs []string `json:"relevant_document_ids,omitempty"`
	RelevantChunkIDs    []string `json:"relevant_chunk_ids,omitempty"`
	ExpectedAnswer      string   `json:"expected_answer,omitempty"`
	Tags                []string `json:"tags,omitempty"`
	Category            string   `json:"category,omitempty"`
	Difficulty          string   `json:"difficulty,omitempty"`
	Notes               string   `json:"notes,omitempty"`
}

func (d Dataset) Validate() error {
	if d.SchemaVersion != "" && d.SchemaVersion != SchemaVersion { return fmt.Errorf("unsupported dataset schema_version %q", d.SchemaVersion) }
	if d.ID == "" || d.Version == "" || d.KnowledgeBaseID == "" { return fmt.Errorf("dataset id, version, and knowledge_base_id are required") }
	if len(d.Cases) == 0 { return fmt.Errorf("dataset must contain at least one case") }
	seen := map[string]bool{}
	for i, c := range d.Cases {
		if c.ID == "" || c.Query == "" { return fmt.Errorf("case %d: id and query are required", i) }
		if seen[c.ID] { return fmt.Errorf("duplicate case id %q", c.ID) }
		seen[c.ID] = true
	}
	return nil
}

type Config struct {
	TopK           []int          `json:"top_k"`
	Retrieval      string         `json:"retrieval"`
	Rerank         bool           `json:"rerank"`
	EmbeddingModel string         `json:"embedding_model,omitempty"`
	Chunking       map[string]any `json:"chunking,omitempty"`
}

type Environment struct {
	ForgeAIVersion string `json:"forgeai_version"`
	GoVersion      string `json:"go_version,omitempty"`
	OS             string `json:"os,omitempty"`
	Arch           string `json:"arch,omitempty"`
}

type Run struct {
	SchemaVersion string                 `json:"schema_version"`
	Tool          string                 `json:"tool"`
	RunID         string                 `json:"run_id"`
	DatasetID     string                 `json:"dataset_id"`
	DatasetVersion string                `json:"dataset_version"`
	StartedAt     time.Time              `json:"started_at"`
	FinishedAt    time.Time              `json:"finished_at"`
	Config        Config                 `json:"config"`
	Environment   Environment            `json:"environment"`
	Metrics       map[string]Metric      `json:"metrics"`
	Queries       []QueryEvidence        `json:"queries"`
	Latency       LatencySummary         `json:"latency"`
	Errors        []EvidenceMessage      `json:"errors"`
	Unknowns      []EvidenceMessage      `json:"unknowns"`
}

type EvidenceMessage struct { QueryID string `json:"query_id,omitempty"`; Field string `json:"field"`; Message string `json:"message"` }
type QueryStatus string
const (
	QueryPass QueryStatus = "PASS"; QueryFail QueryStatus = "FAIL"; QueryPartial QueryStatus = "PARTIAL"
	QueryNoRelevant QueryStatus = "NO_RELEVANT_RESULT"; QueryError QueryStatus = "ERROR"
)
type Retrieved struct { ChunkID string `json:"chunk_id"`; DocumentID string `json:"document_id"`; Rank int `json:"rank"`; FinalScore float64 `json:"final_score"`; Text string `json:"text,omitempty"` }
type QueryEvidence struct {
	QueryID string `json:"query_id"`; Query string `json:"query"`; Expected Case `json:"expected"`; Retrieved []Retrieved `json:"retrieved"`
	Metrics map[string]Metric `json:"metrics"`; Status QueryStatus `json:"status"`; FailureReasons []string `json:"failure_reasons,omitempty"`; LatencyMS int64 `json:"latency_ms"`; Error string `json:"error,omitempty"`
}
type LatencySummary struct { Status Availability `json:"status"`; Count int `json:"count"`; AverageMS float64 `json:"average_ms,omitempty"`; P50MS int64 `json:"p50_ms,omitempty"`; P95MS int64 `json:"p95_ms,omitempty"`; P99MS int64 `json:"p99_ms,omitempty"`; MaxMS int64 `json:"max_ms,omitempty"` }

func EvaluateCase(c Case, results []retrieval.Result, ks []int) QueryEvidence {
	e := QueryEvidence{QueryID:c.ID, Query:c.Query, Expected:c, Metrics:map[string]Metric{}, Retrieved:make([]Retrieved,0,len(results))}
	for i,r := range results { e.Retrieved=append(e.Retrieved, Retrieved{ChunkID:r.ChunkID,DocumentID:r.DocumentID,Rank:i+1,FinalScore:r.Score,Text:r.Text}) }
	targets, byChunk := relevantTargets(c)
	if len(targets)==0 {
		e.Status=QueryNoRelevant; e.FailureReasons=[]string{"golden case has no relevant chunk or document"}
		for _,k:=range normalizeK(ks) { for _,n:=range []string{"recall","precision","hit_rate","ndcg"} { e.Metrics[fmt.Sprintf("%s_at_%d",n,k)]=Metric{Status:NotApplicable} } }
		e.Metrics["reciprocal_rank"]=Metric{Status:NotApplicable}; return e
	}
	firstRank:=0; foundAll:=map[string]bool{}
	for i,r:=range results { key:=r.DocumentID; if byChunk { key=r.ChunkID }; if targets[key] && !foundAll[key] { foundAll[key]=true; if firstRank==0 { firstRank=i+1 } } }
	if firstRank==0 { e.Status=QueryFail; e.FailureReasons=[]string{"relevant result not found"} } else if len(foundAll)<len(targets) { e.Status=QueryPartial; e.FailureReasons=[]string{"only some relevant results were retrieved"} } else { e.Status=QueryPass }
	if firstRank==0 { e.Metrics["reciprocal_rank"]=observed(0) } else { e.Metrics["reciprocal_rank"]=observed(1/float64(firstRank)) }
	for _,k:=range normalizeK(ks) {
		hits:=0; seen:=map[string]bool{}; dcg:=0.0
		limit:=min(k,len(results)); for i:=0;i<limit;i++ { key:=results[i].DocumentID; if byChunk {key=results[i].ChunkID}; if targets[key] && !seen[key] { hits++; seen[key]=true; dcg += 1/math.Log2(float64(i+2)) } }
		e.Metrics[fmt.Sprintf("recall_at_%d",k)]=observed(float64(hits)/float64(len(targets)))
		e.Metrics[fmt.Sprintf("precision_at_%d",k)]=observed(float64(hits)/float64(k))
		hit:=0.0; if hits>0 {hit=1}; e.Metrics[fmt.Sprintf("hit_rate_at_%d",k)]=observed(hit)
		ideal:=0.0; for i:=0;i<min(k,len(targets));i++ { ideal += 1/math.Log2(float64(i+2)) }; ndcg:=0.0; if ideal>0 {ndcg=dcg/ideal}; e.Metrics[fmt.Sprintf("ndcg_at_%d",k)]=observed(ndcg)
	}
	return e
}

func Aggregate(queries []QueryEvidence) map[string]Metric {
	sums:=map[string]float64{}; counts:=map[string]int{}
	for _,q:=range queries { for n,m:=range q.Metrics { if m.Status==Observed && m.Value!=nil { sums[n]+=*m.Value; counts[n]++ } } }
	out:=map[string]Metric{}; for n,sum:=range sums { out[n]=observed(sum/float64(counts[n])) }; return out
}

func SummarizeLatency(values []int64) LatencySummary {
	if len(values)==0 {return LatencySummary{Status:Unavailable}}
	v:=append([]int64(nil),values...); sort.Slice(v,func(i,j int)bool{return v[i]<v[j]}); var sum int64; for _,x:=range v{sum+=x}
	p:=func(q float64)int64{return v[int(math.Ceil(q*float64(len(v))))-1]}
	return LatencySummary{Status:Observed,Count:len(v),AverageMS:float64(sum)/float64(len(v)),P50MS:p(.5),P95MS:p(.95),P99MS:p(.99),MaxMS:v[len(v)-1]}
}

func relevantTargets(c Case)(map[string]bool,bool){ ids:=c.RelevantDocumentIDs; byChunk:=false; if len(c.RelevantChunkIDs)>0 {ids=c.RelevantChunkIDs;byChunk=true}; m:=map[string]bool{};for _,id:=range ids{if id!=""{m[id]=true}};return m,byChunk }
func normalizeK(ks []int)[]int{m:=map[int]bool{};for _,k:=range ks{if k>0{m[k]=true}};if len(m)==0{m[5]=true};out:=make([]int,0,len(m));for k:=range m{out=append(out,k)};sort.Ints(out);return out}

type MetricDelta struct { Metric string `json:"metric"`; Baseline Metric `json:"baseline"`; Candidate Metric `json:"candidate"`; Delta *float64 `json:"delta,omitempty"` }
type QueryChange string
const(Improved QueryChange="IMPROVED";Regressed QueryChange="REGRESSED";Unchanged QueryChange="UNCHANGED";NotComparable QueryChange="NOT_COMPARABLE")
type QueryComparison struct { QueryID string `json:"query_id"`; Change QueryChange `json:"change"`; Baseline *float64 `json:"baseline_mrr,omitempty"`; Candidate *float64 `json:"candidate_mrr,omitempty"`; Delta *float64 `json:"delta,omitempty"` }
type Comparison struct { SchemaVersion string `json:"schema_version"`; Tool string `json:"tool"`; BaselineRunID string `json:"baseline_run_id"`; CandidateRunID string `json:"candidate_run_id"`; Metrics []MetricDelta `json:"metrics"`; Queries []QueryComparison `json:"queries"`; Improved int `json:"improved"`; Regressed int `json:"regressed"`; Unchanged int `json:"unchanged"` }

func Compare(b,c Run) Comparison {
	out:=Comparison{SchemaVersion:SchemaVersion,Tool:"forgeai",BaselineRunID:b.RunID,CandidateRunID:c.RunID}
	names:=map[string]bool{};for n:=range b.Metrics{names[n]=true};for n:=range c.Metrics{names[n]=true};ordered:=make([]string,0,len(names));for n:=range names{ordered=append(ordered,n)};sort.Strings(ordered)
	for _,n:=range ordered{bm,cm:=b.Metrics[n],c.Metrics[n];d:=MetricDelta{Metric:n,Baseline:bm,Candidate:cm};if bm.Value!=nil&&cm.Value!=nil&&bm.Status==Observed&&cm.Status==Observed{x:=*cm.Value-*bm.Value;d.Delta=&x};out.Metrics=append(out.Metrics,d)}
	bq:=map[string]QueryEvidence{};for _,q:=range b.Queries{bq[q.QueryID]=q};for _,q:=range c.Queries{old,ok:=bq[q.QueryID];qc:=QueryComparison{QueryID:q.QueryID,Change:NotComparable};if ok{bm,cm:=old.Metrics["reciprocal_rank"],q.Metrics["reciprocal_rank"];if bm.Value!=nil&&cm.Value!=nil{x:=*cm.Value-*bm.Value;qc.Baseline=bm.Value;qc.Candidate=cm.Value;qc.Delta=&x;if math.Abs(x)<1e-12{qc.Change=Unchanged;out.Unchanged++}else if x>0{qc.Change=Improved;out.Improved++}else{qc.Change=Regressed;out.Regressed++}}};out.Queries=append(out.Queries,qc)}
	return out
}
