package usecase

import (
	"context"
	"runtime"
	"time"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/domain/evaluation"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
)

type EvaluationSearcher interface {
	Search(context.Context, string, string, retrieval.Options) ([]retrieval.Result, error)
}

type EvaluateUseCase struct { Searcher EvaluationSearcher; Version string; EmbeddingModel string }

func (u *EvaluateUseCase) Run(ctx context.Context, dataset evaluation.Dataset, cfg evaluation.Config) (evaluation.Run, error) {
	if err:=dataset.Validate(); err!=nil{return evaluation.Run{},err}
	start:=time.Now().UTC()
	run:=evaluation.Run{SchemaVersion:evaluation.SchemaVersion,Tool:"forgeai",RunID:uuid.NewString(),DatasetID:dataset.ID,DatasetVersion:dataset.Version,StartedAt:start,Config:cfg,Metrics:map[string]evaluation.Metric{},Queries:make([]evaluation.QueryEvidence,0,len(dataset.Cases)),Environment:evaluation.Environment{ForgeAIVersion:u.Version,GoVersion:runtime.Version(),OS:runtime.GOOS,Arch:runtime.GOARCH}}
	if run.Config.Retrieval==""{run.Config.Retrieval="hybrid_rrf"};if len(run.Config.TopK)==0{run.Config.TopK=[]int{1,3,5,10}};if run.Config.EmbeddingModel==""{run.Config.EmbeddingModel=u.EmbeddingModel}
	maxK:=0;for _,k:=range run.Config.TopK{if k>maxK{maxK=k}}
	latencies:=make([]int64,0,len(dataset.Cases))
	for _,c:=range dataset.Cases{
		qstart:=time.Now();results,err:=u.Searcher.Search(ctx,dataset.KnowledgeBaseID,c.Query,retrieval.Options{TopK:maxK,Rerank:cfg.Rerank});ms:=time.Since(qstart).Milliseconds();latencies=append(latencies,ms)
		if err!=nil{run.Queries=append(run.Queries,evaluation.QueryEvidence{QueryID:c.ID,Query:c.Query,Expected:c,Status:evaluation.QueryError,Metrics:map[string]evaluation.Metric{},LatencyMS:ms,Error:err.Error(),FailureReasons:[]string{"retrieval error"}});run.Errors=append(run.Errors,evaluation.EvidenceMessage{QueryID:c.ID,Field:"retrieval",Message:err.Error()});continue}
		q:=evaluation.EvaluateCase(c,results,run.Config.TopK);q.LatencyMS=ms;run.Queries=append(run.Queries,q)
	}
	run.Metrics=evaluation.Aggregate(run.Queries);run.Latency=evaluation.SummarizeLatency(latencies);run.FinishedAt=time.Now().UTC()
	run.Unknowns=[]evaluation.EvidenceMessage{{Field:"phase_latency",Message:"current retrieval trace does not expose deterministic per-phase timing in evaluation artifacts"},{Field:"token_cost",Message:"retrieval-only evaluation does not invoke answer generation; provider token/cost evidence is unavailable"}}
	return run,nil
}
