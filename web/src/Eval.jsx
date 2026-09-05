import { Fragment, useEffect, useState, useCallback, useRef } from 'react'

const STATUS_LABEL = {
  pending: 'Pending…',
  running: 'Running…',
  done: 'Done',
  failed: 'Failed',
}

function Eval() {
  const [knowledgeBases, setKnowledgeBases] = useState([])
  const [datasets, setDatasets] = useState([])
  const [selectedId, setSelectedId] = useState('')
  const [newDatasetName, setNewDatasetName] = useState('')
  const [newDatasetKbId, setNewDatasetKbId] = useState('')
  const [runs, setRuns] = useState([])
  const [error, setError] = useState('')
  const [importing, setImporting] = useState(false)
  const pollRef = useRef(null)

  const loadKnowledgeBases = useCallback(async () => {
    const resp = await fetch('/api/v1/knowledge-bases')
    if (!resp.ok) throw new Error(await resp.text())
    return (await resp.json()) || []
  }, [])

  const loadDatasets = useCallback(async () => {
    const resp = await fetch('/api/v1/datasets')
    if (!resp.ok) throw new Error(await resp.text())
    const ds = (await resp.json()) || []
    setDatasets(ds)
    return ds
  }, [])

  const loadRuns = useCallback(async (datasetId) => {
    if (!datasetId) {
      setRuns([])
      return
    }
    const resp = await fetch(`/api/v1/evaluations?dataset_id=${datasetId}`)
    if (!resp.ok) throw new Error(await resp.text())
    setRuns((await resp.json()) || [])
  }, [])

  useEffect(() => {
    loadKnowledgeBases()
      .then((kbs) => {
        setKnowledgeBases(kbs)
        if (kbs.length > 0) setNewDatasetKbId(kbs[0].id)
      })
      .catch((err) => setError(String(err.message || err)))
    loadDatasets()
      .then((ds) => {
        if (ds.length > 0) setSelectedId(ds[0].id)
      })
      .catch((err) => setError(String(err.message || err)))
  }, [loadKnowledgeBases, loadDatasets])

  useEffect(() => {
    loadRuns(selectedId).catch((err) => setError(String(err.message || err)))
  }, [selectedId, loadRuns])

  useEffect(() => () => clearInterval(pollRef.current), [])

  async function createDataset(e) {
    e.preventDefault()
    const name = newDatasetName.trim()
    if (!name || !newDatasetKbId) return
    setError('')
    try {
      const resp = await fetch('/api/v1/datasets', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, knowledge_base_id: newDatasetKbId }),
      })
      if (!resp.ok) throw new Error(await resp.text())
      const ds = await resp.json()
      setNewDatasetName('')
      await loadDatasets()
      setSelectedId(ds.id)
    } catch (err) {
      setError(String(err.message || err))
    }
  }

  async function importCases(e) {
    const file = e.target.files && e.target.files[0]
    e.target.value = ''
    if (!file || !selectedId) return

    setImporting(true)
    setError('')
    try {
      let resp
      if (file.name.toLowerCase().endsWith('.csv')) {
        const form = new FormData()
        form.append('file', file)
        resp = await fetch(`/api/v1/datasets/${selectedId}/cases`, { method: 'POST', body: form })
      } else {
        const text = await file.text()
        resp = await fetch(`/api/v1/datasets/${selectedId}/cases`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: text,
        })
      }
      if (!resp.ok) throw new Error(await resp.text())
      const body = await resp.json()
      setError(`Imported ${(body.cases || []).length} case(s).`)
    } catch (err) {
      setError(String(err.message || err))
    } finally {
      setImporting(false)
    }
  }

  function pollRun(runId) {
    clearInterval(pollRef.current)
    pollRef.current = setInterval(async () => {
      try {
        const resp = await fetch(`/api/v1/evaluations/${runId}`)
        if (!resp.ok) throw new Error(await resp.text())
        const body = await resp.json()
        setRuns((prev) => {
          const others = prev.filter((r) => r.id !== body.run.id)
          return [body.run, ...others]
        })
        if (body.run.status === 'done' || body.run.status === 'failed') {
          clearInterval(pollRef.current)
        }
      } catch (err) {
        clearInterval(pollRef.current)
        setError(String(err.message || err))
      }
    }, 1000)
  }

  return (
    <div className="view knowledge-view">
      <div className="view-toolbar knowledge-toolbar">
        <select value={selectedId} onChange={(e) => setSelectedId(e.target.value)}>
          <option value="" disabled>
            {datasets.length === 0 ? 'No datasets yet' : 'Select a dataset'}
          </option>
          {datasets.map((d) => (
            <option key={d.id} value={d.id}>
              {d.name}
            </option>
          ))}
        </select>

        <form className="kb-create" onSubmit={createDataset}>
          <input
            value={newDatasetName}
            onChange={(e) => setNewDatasetName(e.target.value)}
            placeholder="New dataset name"
          />
          <select value={newDatasetKbId} onChange={(e) => setNewDatasetKbId(e.target.value)}>
            <option value="" disabled>
              Knowledge base
            </option>
            {knowledgeBases.map((kb) => (
              <option key={kb.id} value={kb.id}>
                {kb.name}
              </option>
            ))}
          </select>
          <button type="submit" disabled={!newDatasetName.trim() || !newDatasetKbId}>
            Create
          </button>
        </form>

        <label className="upload-button">
          {importing ? 'Importing…' : 'Import cases (.json/.csv)'}
          <input type="file" accept=".json,.csv" onChange={importCases} disabled={!selectedId || importing} hidden />
        </label>
      </div>

      {error && <p className="kb-error">{error}</p>}

      {selectedId ? (
        <RunPanel datasetId={selectedId} runs={runs} onStarted={pollRun} onError={(msg) => setError(msg)} />
      ) : (
        <main className="kb-documents">
          <p className="empty">Create or select a dataset to import cases and run an evaluation.</p>
        </main>
      )}
    </div>
  )
}

function RunPanel({ datasetId, runs, onStarted, onError }) {
  const [topK, setTopK] = useState(10)
  const [rerank, setRerank] = useState(false)
  const [judge, setJudge] = useState(false)
  const [alias, setAlias] = useState('normal')
  const [starting, setStarting] = useState(false)
  const [selectedRunId, setSelectedRunId] = useState('')

  useEffect(() => setSelectedRunId(''), [datasetId])

  async function startRun(e) {
    e.preventDefault()
    setStarting(true)
    onError('')
    try {
      const resp = await fetch('/api/v1/evaluations', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dataset_id: datasetId, top_k: Number(topK) || 10, rerank, judge, alias: judge ? alias : '' }),
      })
      const body = await resp.json().catch(() => null)
      if (!resp.ok) throw new Error((body && body.error) || `HTTP ${resp.status}`)
      onStarted(body.id)
      setSelectedRunId(body.id)
    } catch (err) {
      onError(String(err.message || err))
    } finally {
      setStarting(false)
    }
  }

  const hasJudgeRuns = runs.some((r) => r.judge)
  const selectedRun = runs.find((r) => r.id === selectedRunId)

  return (
    <main className="kb-documents">
      <form className="search-form" onSubmit={startRun}>
        <input
          type="number"
          min="1"
          value={topK}
          onChange={(e) => setTopK(e.target.value)}
          style={{ width: '5rem' }}
          title="top_k"
        />
        <label className="rerank-toggle">
          <input type="checkbox" checked={rerank} onChange={(e) => setRerank(e.target.checked)} />
          Rerank
        </label>
        <label className="rerank-toggle">
          <input type="checkbox" checked={judge} onChange={(e) => setJudge(e.target.checked)} />
          LLM Judge
        </label>
        {judge && (
          <select value={alias} onChange={(e) => setAlias(e.target.value)} title="answering alias">
            <option value="cheap">cheap</option>
            <option value="normal">normal</option>
            <option value="judge">judge</option>
          </select>
        )}
        <button type="submit" disabled={starting}>
          {starting ? 'Starting…' : 'Run evaluation'}
        </button>
      </form>

      {runs.length === 0 && <p className="empty">No runs yet for this dataset.</p>}

      {runs.length > 0 && (
        <table className="doc-table trace-table">
          <thead>
            <tr>
              <th>Status</th>
              <th>Config</th>
              <th>Recall@K</th>
              <th>MRR</th>
              <th>Hit Rate</th>
              {hasJudgeRuns && <th>Correct.</th>}
              {hasJudgeRuns && <th>Grounded.</th>}
              {hasJudgeRuns && <th>Relev.</th>}
              {hasJudgeRuns && <th>Cost</th>}
              <th>Started</th>
            </tr>
          </thead>
          <tbody>
            {runs.map((r) => (
              <tr
                key={r.id}
                className={`doc-row trace-row ${r.status === 'failed' ? 'doc-failed' : 'doc-ready'} ${selectedRunId === r.id ? 'selected' : ''}`}
                title={r.error || ''}
                onClick={() => setSelectedRunId(r.id)}
              >
                <td>{STATUS_LABEL[r.status] || r.status}</td>
                <td>
                  k={r.top_k}
                  {r.rerank ? ' · rerank' : ''}
                  {r.judge ? ` · judge (${r.alias})` : ''}
                </td>
                <td>{r.recall_at_k.toFixed(3)}</td>
                <td>{r.mrr.toFixed(3)}</td>
                <td>{r.hit_rate.toFixed(3)}</td>
                {hasJudgeRuns && <td>{r.judge ? r.correctness.toFixed(2) : '—'}</td>}
                {hasJudgeRuns && <td>{r.judge ? r.groundedness.toFixed(2) : '—'}</td>}
                {hasJudgeRuns && <td>{r.judge ? r.relevance.toFixed(2) : '—'}</td>}
                {hasJudgeRuns && <td>{r.judge ? `$${r.cost_usd.toFixed(4)}` : '—'}</td>}
                <td>{new Date(r.started_at).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {selectedRun && <RunDetail run={selectedRun} onError={onError} />}
    </main>
  )
}

// LOW_SCORE is the threshold at or below which a judged case is flagged;
// the "failed cases" filter shows only those (plus errored cases).
const LOW_SCORE = 0.5

function RunDetail({ run, onError }) {
  const [results, setResults] = useState([])
  const [onlyLow, setOnlyLow] = useState(false)
  const [openId, setOpenId] = useState('')

  useEffect(() => {
    let cancelled = false
    fetch(`/api/v1/evaluations/${run.id}`)
      .then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`)
        return r.json()
      })
      .then((body) => {
        if (!cancelled) setResults(body.results || [])
      })
      .catch((err) => onError(String(err.message || err)))
    return () => {
      cancelled = true
    }
  }, [run.id, run.status, onError])

  const isLow = (r) =>
    r.error || !r.hit || (run.judge && Math.min(r.correctness, r.groundedness, r.relevance) <= LOW_SCORE)
  const shown = onlyLow ? results.filter(isLow) : results
  const lowCount = results.filter(isLow).length

  return (
    <div className="trace-detail">
      <h3>
        Run detail — {results.length} case(s), {lowCount} flagged
        {run.judge && ` · judge prompt v${(results.find((r) => r.judge_prompt_version) || {}).judge_prompt_version || '?'}`}
      </h3>
      <label className="rerank-toggle">
        <input type="checkbox" checked={onlyLow} onChange={(e) => setOnlyLow(e.target.checked)} />
        Only failed / low-scoring cases
      </label>

      {results.length === 0 && <p className="empty">{run.status === 'done' ? 'No results.' : 'Waiting for results…'}</p>}

      {shown.length > 0 && (
        <table className="doc-table">
          <thead>
            <tr>
              <th>Query</th>
              <th>Hit</th>
              <th>RR</th>
              {run.judge && <th>Correct.</th>}
              {run.judge && <th>Grounded.</th>}
              {run.judge && <th>Relev.</th>}
              <th>ms</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((r) => (
              <Fragment key={r.case_id}>
                <tr
                  className={`doc-row trace-row ${isLow(r) ? 'doc-failed' : 'doc-ready'} ${openId === r.case_id ? 'selected' : ''}`}
                  onClick={() => setOpenId(openId === r.case_id ? '' : r.case_id)}
                  title={r.error || ''}
                >
                  <td>{r.query}</td>
                  <td>{r.error ? 'error' : r.hit ? 'yes' : 'no'}</td>
                  <td>{r.reciprocal_rank.toFixed(2)}</td>
                  {run.judge && <td>{r.correctness.toFixed(2)}</td>}
                  {run.judge && <td>{r.groundedness.toFixed(2)}</td>}
                  {run.judge && <td>{r.relevance.toFixed(2)}</td>}
                  <td>{r.duration_ms}</td>
                </tr>
                {openId === r.case_id && (
                  <tr className="eval-case-detail">
                    <td colSpan={run.judge ? 7 : 4}>
                      {r.error && <p className="kb-error">{r.error}</p>}
                      <p>
                        <strong>Expected:</strong> {r.expected_filenames.join(', ')}
                        {r.expected_answer && <> — {r.expected_answer}</>}
                      </p>
                      <p>
                        <strong>Retrieved:</strong> {r.retrieved_filenames.join(', ') || '(none)'}
                      </p>
                      {run.judge && r.answer && (
                        <p className="search-result-text">
                          <strong>Answer:</strong> {r.answer}
                        </p>
                      )}
                      {run.judge && r.judge_reason && (
                        <p className="search-result-text">
                          <strong>Judge ({r.judge_model}):</strong> {r.judge_reason}
                        </p>
                      )}
                    </td>
                  </tr>
                )}
              </Fragment>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

export default Eval
