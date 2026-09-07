import { useMemo, useState } from 'react'

const STATUS_LABELS = {
  PASS: '取得成功',
  PARTIAL: '一部取得',
  FAIL: '取得失敗',
  NO_RELEVANT_RESULT: '正解未設定',
  ERROR: '実行エラー',
}

const observed = (value) => ({ status: 'OBSERVED', value })
const DEMO_RUN = {
  schema_version: '1.0',
  tool: 'forgeai',
  run_id: 'customer-demo',
  dataset_id: 'support-quality-demo',
  dataset_version: '1.0.0',
  metrics: { reciprocal_rank: observed(0.792), recall_at_5: observed(0.833), hit_rate_at_5: observed(0.75) },
  queries: [
    { query_id: 'q-001', query: '返品期限は何日ですか？', status: 'PASS', expected: { relevant_chunk_ids: ['returns-window'] }, retrieved: [{ chunk_id: 'returns-window', document_id: 'returns-policy', rank: 1, final_score: 0.9132 }], metrics: { reciprocal_rank: observed(1) } },
    { query_id: 'q-002', query: '配送日数と送料を教えてください', status: 'PARTIAL', expected: { relevant_chunk_ids: ['shipping-time', 'shipping-price'] }, retrieved: [{ chunk_id: 'shipping-time', document_id: 'shipping-guide', rank: 1, final_score: 0.8421 }, { chunk_id: 'other-faq', document_id: 'general-faq', rank: 2, final_score: 0.6314 }], metrics: { reciprocal_rank: observed(1) }, failure_reasons: ['only some relevant results were retrieved'] },
    { query_id: 'q-003', query: '製品保証はいつまで有効ですか？', status: 'PASS', expected: { relevant_chunk_ids: ['warranty-period'] }, retrieved: [{ chunk_id: 'general-support', document_id: 'general-faq', rank: 1, final_score: 0.711 }, { chunk_id: 'warranty-period', document_id: 'warranty', rank: 2, final_score: 0.6902 }], metrics: { reciprocal_rank: observed(0.5) } },
    { query_id: 'q-004', query: '海外返品の送料は誰が負担しますか？', status: 'FAIL', expected: { relevant_chunk_ids: ['international-return-cost'] }, retrieved: [{ chunk_id: 'domestic-return-cost', document_id: 'returns-policy', rank: 1, final_score: 0.6221 }], metrics: { reciprocal_rank: observed(0) }, failure_reasons: ['relevant result not found'] },
  ],
}

const DEMO_COMPARISON = {
  improved: 12,
  unchanged: 3,
  regressed: 2,
  metrics: [
    { metric: 'reciprocal_rank', delta: 0.08 },
    { metric: 'recall_at_5', delta: 0.06 },
    { metric: 'hit_rate_at_5', delta: 0.04 },
  ],
}

function metricValue(run, name) {
  const metric = run?.metrics?.[name]
  return metric?.status === 'OBSERVED' && typeof metric.value === 'number' ? metric.value : null
}

function percent(value) {
  return value == null ? '—' : `${(value * 100).toFixed(1)}%`
}

function score(value) {
  return value == null ? '—' : value.toFixed(3)
}

async function readJSONFile(file) {
  if (!file) return null
  return JSON.parse(await file.text())
}

function RAGQualityFlow() {
  return (
    <svg className="evaluation-flow" viewBox="0 0 920 180" role="img" aria-labelledby="flow-title flow-description">
      <title id="flow-title">ForgeAI evaluation flow</title>
      <desc id="flow-description">Documents become searchable chunks, questions are evaluated against correct chunks, and evidence shows improvements and regressions.</desc>
      <defs>
        <linearGradient id="flow-gradient" x1="0" x2="1">
          <stop offset="0" stopColor="#2563eb" />
          <stop offset="1" stopColor="#7c3aed" />
        </linearGradient>
        <marker id="arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
          <path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" />
        </marker>
      </defs>
      <g className="flow-arrow" markerEnd="url(#arrow)">
        <path d="M190 86 H250" />
        <path d="M425 86 H485" />
        <path d="M660 86 H720" />
      </g>
      <g transform="translate(20 30)">
        <rect width="170" height="112" rx="18" />
        <text x="85" y="43" textAnchor="middle" className="flow-number">1</text>
        <text x="85" y="70" textAnchor="middle" className="flow-title">RAG検索</text>
        <text x="85" y="92" textAnchor="middle" className="flow-copy">文書から候補を取得</text>
      </g>
      <g transform="translate(255 30)">
        <rect width="170" height="112" rx="18" />
        <text x="85" y="43" textAnchor="middle" className="flow-number">2</text>
        <text x="85" y="70" textAnchor="middle" className="flow-title">正解と照合</text>
        <text x="85" y="92" textAnchor="middle" className="flow-copy">Golden Dataset</text>
      </g>
      <g transform="translate(490 30)">
        <rect width="170" height="112" rx="18" />
        <text x="85" y="43" textAnchor="middle" className="flow-number">3</text>
        <text x="85" y="70" textAnchor="middle" className="flow-title">品質を測定</text>
        <text x="85" y="92" textAnchor="middle" className="flow-copy">Recall・MRR・順位</text>
      </g>
      <g transform="translate(725 30)" className="flow-evidence">
        <rect width="175" height="112" rx="18" />
        <text x="87.5" y="43" textAnchor="middle" className="flow-number">4</text>
        <text x="87.5" y="70" textAnchor="middle" className="flow-title">変化を証明</text>
        <text x="87.5" y="92" textAnchor="middle" className="flow-copy">改善と悪化をEvidence化</text>
      </g>
    </svg>
  )
}

function Evaluation() {
  const [dataset, setDataset] = useState(null)
  const [datasetName, setDatasetName] = useState('')
  const [run, setRun] = useState(null)
  const [running, setRunning] = useState(false)
  const [rerank, setRerank] = useState(false)
  const [error, setError] = useState('')
  const [baseline, setBaseline] = useState(null)
  const [candidate, setCandidate] = useState(null)
  const [comparison, setComparison] = useState(null)
  const [openQuery, setOpenQuery] = useState('')

  const failed = useMemo(() => run?.queries?.filter((query) => query.status !== 'PASS') || [], [run])

  async function loadDataset(file) {
    try {
      const value = await readJSONFile(file)
      setDataset(value)
      setDatasetName(file?.name || '')
      setError('')
    } catch (err) {
      setError(`Golden Datasetを読み込めません: ${err.message || err}`)
    }
  }

  async function loadEvidence(file) {
    try {
      setRun(await readJSONFile(file))
      setError('')
    } catch (err) {
      setError(`Evidenceを読み込めません: ${err.message || err}`)
    }
  }

  async function runEvaluation() {
    if (!dataset) return
    setRunning(true)
    setError('')
    try {
      const response = await fetch('/api/v1/evaluations/run', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          dataset,
          config: { top_k: [1, 3, 5, 10], retrieval: 'hybrid_rrf', rerank },
        }),
      })
      if (!response.ok) throw new Error(await response.text())
      setRun(await response.json())
    } catch (err) {
      setError(String(err.message || err))
    } finally {
      setRunning(false)
    }
  }

  async function compareRuns(nextBaseline = baseline, nextCandidate = candidate) {
    if (!nextBaseline || !nextCandidate) return
    setError('')
    try {
      const response = await fetch('/api/v1/evaluations/compare', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ baseline: nextBaseline, candidate: nextCandidate }),
      })
      if (!response.ok) throw new Error(await response.text())
      setComparison(await response.json())
    } catch (err) {
      setError(String(err.message || err))
    }
  }

  function downloadRun() {
    const blob = new Blob([`${JSON.stringify(run, null, 2)}\n`], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `${run.run_id || 'forgeai-evaluation'}.json`
    link.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="view evaluation-view">
      <main className="evaluation-scroll">
        <section className="evaluation-hero">
          <div>
            <p className="eyebrow">RAG QUALITY ENGINEERING</p>
            <h2>RAGの改善を、感覚ではなく数字で証明する。</h2>
            <p>
              正解データと実際の検索結果を照合し、見つかった情報・見落とした情報・順位の変化を
              再現可能なEvidenceとして残します。
            </p>
          </div>
          <div className="evidence-seal" aria-label="Evidence producer">
            <span>MEASURE</span>
            <strong>EVIDENCE</strong>
            <span>VERIFY</span>
          </div>
        </section>

        <RAGQualityFlow />

        <section className="evaluation-actions">
          <div className="action-card">
            <span className="action-step">STEP 1</span>
            <h3>品質を測る</h3>
            <p>正解の文書・チャンクを記録したGolden Datasetを読み込み、現在のRAG検索を実行します。</p>
            <div className="action-controls">
              <label className="file-button">
                {datasetName || 'Golden Datasetを選択'}
                <input type="file" accept="application/json,.json" onChange={(event) => loadDataset(event.target.files?.[0])} hidden />
              </label>
              <label className="rerank-toggle">
                <input type="checkbox" checked={rerank} onChange={(event) => setRerank(event.target.checked)} />
                Rerankを使用
              </label>
              <button className="primary-button" onClick={runEvaluation} disabled={!dataset || running}>
                {running ? '評価中…' : 'Evaluationを実行'}
              </button>
            </div>
          </div>

          <div className="action-card">
            <span className="action-step">VIEW</span>
            <h3>既存Evidenceを見る</h3>
            <p>CLIやCIで生成したschema v1 Artifactを読み込み、同じダッシュボードで確認できます。</p>
            <label className="file-button secondary">
              Evidence JSONを選択
              <input type="file" accept="application/json,.json" onChange={(event) => loadEvidence(event.target.files?.[0])} hidden />
            </label>
            <button className="demo-button" onClick={() => setRun(DEMO_RUN)}>サンプル結果を見る</button>
          </div>
        </section>

        {error && <p className="evaluation-error">{error}</p>}

        {run && (
          <>
            <section className="result-heading">
              <div>
                <p className="eyebrow">EVALUATION RESULT</p>
                <h2>{run.dataset_id} <span>v{run.dataset_version}</span></h2>
              </div>
              <button className="secondary-button" onClick={downloadRun}>JSON Evidenceを保存</button>
            </section>

            <section className="metric-grid">
              <MetricCard label="MRR" value={score(metricValue(run, 'reciprocal_rank'))} note="正解が上位に現れるほど1に近い" />
              <MetricCard label="Recall@5" value={percent(metricValue(run, 'recall_at_5'))} note="正解をどれだけ取りこぼさなかったか" />
              <MetricCard label="Hit Rate@5" value={percent(metricValue(run, 'hit_rate_at_5'))} note="5位以内に正解があった質問の割合" />
              <MetricCard label="要確認" value={`${failed.length}件`} note={`全${run.queries?.length || 0}件の質問を評価`} warning={failed.length > 0} />
            </section>

            <section className="evidence-panel">
              <div className="panel-title">
                <div>
                  <h3>質問ごとのEvidence</h3>
                  <p>平均値だけでなく、どの質問で何位に正解が出たかを確認できます。</p>
                </div>
                <div className="legend"><span className="dot pass" /> 成功 <span className="dot partial" /> 一部 <span className="dot fail" /> 失敗</div>
              </div>
              <div className="query-list">
                {run.queries?.map((query) => (
                  <article key={query.query_id} className={`query-card status-${query.status.toLowerCase()}`}>
                    <button className="query-summary" onClick={() => setOpenQuery(openQuery === query.query_id ? '' : query.query_id)}>
                      <span className={`status-pill status-${query.status.toLowerCase()}`}>{STATUS_LABELS[query.status] || query.status}</span>
                      <span className="query-text"><small>{query.query_id}</small>{query.query}</span>
                      <span className="query-rank">最初の正解 <strong>{firstRelevantRank(query) || '—'}位</strong></span>
                      <span className="chevron">{openQuery === query.query_id ? '−' : '+'}</span>
                    </button>
                    {openQuery === query.query_id && <QueryDetail query={query} />}
                  </article>
                ))}
              </div>
            </section>
          </>
        )}

        <ComparisonPanel
          comparison={comparison}
          onDemo={() => setComparison(DEMO_COMPARISON)}
          onBaseline={async (file) => { const value = await readJSONFile(file); setBaseline(value); compareRuns(value, candidate) }}
          onCandidate={async (file) => { const value = await readJSONFile(file); setCandidate(value); compareRuns(baseline, value) }}
        />

        <section className="boundary-note">
          <strong>ForgeAIが示すのは「何が起きたか」までです。</strong>
          <span>原因の仮説、直す順番、構成変更、見積・提案はこのPublic OSSの外で判断します。</span>
        </section>
      </main>
    </div>
  )
}

function MetricCard({ label, value, note, warning = false }) {
  return <article className={`metric-card ${warning ? 'warning' : ''}`}><span>{label}</span><strong>{value}</strong><small>{note}</small></article>
}

function firstRelevantRank(query) {
  const relevantChunks = new Set(query.expected?.relevant_chunk_ids || [])
  const relevantDocuments = new Set(query.expected?.relevant_document_ids || [])
  const useChunks = relevantChunks.size > 0
  return query.retrieved?.find((item) => useChunks ? relevantChunks.has(item.chunk_id) : relevantDocuments.has(item.document_id))?.rank
}

function QueryDetail({ query }) {
  const expected = query.expected?.relevant_chunk_ids?.length ? query.expected.relevant_chunk_ids : query.expected?.relevant_document_ids || []
  return (
    <div className="query-detail">
      <div className="expected-box"><span>期待した正解</span><div>{expected.length ? expected.map((id) => <code key={id}>{id}</code>) : '未設定'}</div></div>
      {query.failure_reasons?.length > 0 && <div className="failure-box"><span>観測した失敗</span><p>{query.failure_reasons.join(' / ')}</p></div>}
      <table className="ranking-table">
        <thead><tr><th>順位</th><th>取得チャンク</th><th>文書</th><th>Final score</th></tr></thead>
        <tbody>
          {query.retrieved?.length ? query.retrieved.map((item) => (
            <tr key={`${item.rank}-${item.chunk_id}`}><td>#{item.rank}</td><td><code>{item.chunk_id}</code></td><td>{item.document_id}</td><td>{item.final_score.toFixed(4)}</td></tr>
          )) : <tr><td colSpan="4">検索結果は空でした</td></tr>}
        </tbody>
      </table>
    </div>
  )
}

function ComparisonPanel({ comparison, onBaseline, onCandidate, onDemo }) {
  return (
    <section className="comparison-panel">
      <div className="panel-title">
        <div><p className="eyebrow">BEFORE / AFTER</p><h3>変更前と変更後を比較する</h3><p>全体が良くなっていても、一部の質問だけ悪化していないかを検出します。</p></div>
        <div className="compare-inputs">
          <button className="demo-button" onClick={onDemo}>比較サンプル</button>
          <label className="file-button secondary">Baseline<input type="file" accept="application/json,.json" onChange={(event) => onBaseline(event.target.files?.[0]).catch(() => {})} hidden /></label>
          <span>→</span>
          <label className="file-button">Candidate<input type="file" accept="application/json,.json" onChange={(event) => onCandidate(event.target.files?.[0]).catch(() => {})} hidden /></label>
        </div>
      </div>
      {comparison ? (
        <div className="comparison-result">
          <div className="change-stat improved"><strong>{comparison.improved}</strong><span>改善</span></div>
          <div className="change-stat unchanged"><strong>{comparison.unchanged}</strong><span>変化なし</span></div>
          <div className="change-stat regressed"><strong>{comparison.regressed}</strong><span>悪化</span></div>
          <div className="delta-list">
            {comparison.metrics?.filter((item) => item.delta != null && ['reciprocal_rank', 'recall_at_5', 'hit_rate_at_5'].includes(item.metric)).map((item) => (
              <div key={item.metric}><span>{item.metric}</span><strong className={item.delta >= 0 ? 'positive' : 'negative'}>{item.delta >= 0 ? '+' : ''}{item.delta.toFixed(3)}</strong></div>
            ))}
          </div>
        </div>
      ) : <p className="comparison-empty">2つのEvidence JSONを選ぶと、改善・悪化した質問と主要指標の差分がここに表示されます。</p>}
    </section>
  )
}

export default Evaluation
