package evaluation_test

import (
	"math"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/domain/evaluation"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
)

func value(t *testing.T,m evaluation.Metric)float64{t.Helper();if m.Value==nil{t.Fatal("metric has no value")};return *m.Value}
func closeTo(a,b float64)bool{return math.Abs(a-b)<1e-9}

func TestMetricsHandCalculated(t *testing.T){
	c:=evaluation.Case{ID:"q",Query:"q",RelevantChunkIDs:[]string{"A","C"}}
	r:=[]retrieval.Result{{ChunkID:"B"},{ChunkID:"A"},{ChunkID:"D"},{ChunkID:"C"}}
	e:=evaluation.EvaluateCase(c,r,[]int{1,3,5})
	checks:=map[string]float64{"recall_at_1":0,"precision_at_1":0,"hit_rate_at_1":0,"recall_at_3":.5,"precision_at_3":1.0/3,"hit_rate_at_3":1,"recall_at_5":1,"precision_at_5":.4,"reciprocal_rank":.5}
	for n,want:=range checks{if got:=value(t,e.Metrics[n]);!closeTo(got,want){t.Errorf("%s=%v want %v",n,got,want)}}
	// DCG = 1/log2(3)+1/log2(5); ideal = 1+1/log2(3).
	wantNDCG:=(1/math.Log2(3)+1/math.Log2(5))/(1+1/math.Log2(3));if got:=value(t,e.Metrics["ndcg_at_5"]);!closeTo(got,wantNDCG){t.Errorf("ndcg=%v want %v",got,wantNDCG)}
}

func TestMetricsEdgeCases(t *testing.T){
	tests:=[]struct{name string;c evaluation.Case;r []retrieval.Result;k int;metric string;want float64;status evaluation.Availability}{
		{"empty retrieval",evaluation.Case{ID:"q",Query:"q",RelevantChunkIDs:[]string{"A"}},nil,5,"recall_at_5",0,evaluation.Observed},
		{"rank one",evaluation.Case{ID:"q",Query:"q",RelevantChunkIDs:[]string{"A"}},[]retrieval.Result{{ChunkID:"A"}},1,"reciprocal_rank",1,evaluation.Observed},
		{"outside k",evaluation.Case{ID:"q",Query:"q",RelevantChunkIDs:[]string{"A"}},[]retrieval.Result{{ChunkID:"B"},{ChunkID:"A"}},1,"hit_rate_at_1",0,evaluation.Observed},
		{"duplicate ignored",evaluation.Case{ID:"q",Query:"q",RelevantChunkIDs:[]string{"A"}},[]retrieval.Result{{ChunkID:"A"},{ChunkID:"A"}},2,"recall_at_2",1,evaluation.Observed},
		{"no relevant",evaluation.Case{ID:"q",Query:"q"},nil,5,"recall_at_5",0,evaluation.NotApplicable},
	}
	for _,tt:=range tests{t.Run(tt.name,func(t *testing.T){e:=evaluation.EvaluateCase(tt.c,tt.r,[]int{tt.k});m:=e.Metrics[tt.metric];if m.Status!=tt.status{t.Fatalf("status=%s want %s",m.Status,tt.status)};if tt.status==evaluation.Observed&&!closeTo(value(t,m),tt.want){t.Fatalf("value=%v want %v",value(t,m),tt.want)}})}
}

func TestCompareDetectsQueryRegression(t *testing.T){
	m:=func(v float64)evaluation.Metric{return evaluation.Metric{Status:evaluation.Observed,Value:&v}}
	b:=evaluation.Run{RunID:"b",Metrics:map[string]evaluation.Metric{"reciprocal_rank":m(.5)},Queries:[]evaluation.QueryEvidence{{QueryID:"q1",Metrics:map[string]evaluation.Metric{"reciprocal_rank":m(1)}},{QueryID:"q2",Metrics:map[string]evaluation.Metric{"reciprocal_rank":m(.5)}}}}
	c:=evaluation.Run{RunID:"c",Metrics:map[string]evaluation.Metric{"reciprocal_rank":m(.6)},Queries:[]evaluation.QueryEvidence{{QueryID:"q1",Metrics:map[string]evaluation.Metric{"reciprocal_rank":m(.5)}},{QueryID:"q2",Metrics:map[string]evaluation.Metric{"reciprocal_rank":m(1)}}}}
	got:=evaluation.Compare(b,c);if got.Regressed!=1||got.Improved!=1{t.Fatalf("unexpected comparison: %+v",got)}
}
