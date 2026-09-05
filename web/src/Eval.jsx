import { useEffect, useState, useCallback, useRef } from 'react'

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
  const [starting, setStarting] = useState(false)

  async function startRun(e) {
    e.preventDefault()
    setStarting(true)
    onError('')
    try {
      const resp = await fetch('/api/v1/evaluations', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dataset_id: datasetId, top_k: Number(topK) || 10, rerank }),
      })
      const body = await resp.json().catch(() => null)
      if (!resp.ok) throw new Error((body && body.error) || `HTTP ${resp.status}`)
      onStarted(body.id)
    } catch (err) {
      onError(String(err.message || err))
    } finally {
      setStarting(false)
    }
  }

  return (
    <main className="kb-documents">
      <form className="search-form" onSubmit={startRun}>
        <input
          type="number"
          min="1"
          value={topK}
          onChange={(e) => setTopK(e.target.value)}
          style={{ width: '5rem' }}
        />
        <label className="rerank-toggle">
          <input type="checkbox" checked={rerank} onChange={(e) => setRerank(e.target.checked)} />
          Rerank
        </label>
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
              <th>Top K</th>
              <th>Rerank</th>
              <th>Recall@K</th>
              <th>Precision@K</th>
              <th>MRR</th>
              <th>Hit Rate</th>
              <th>Started</th>
            </tr>
          </thead>
          <tbody>
            {runs.map((r) => (
              <tr key={r.id} className={`doc-row trace-row ${r.status === 'failed' ? 'doc-failed' : 'doc-ready'}`} title={r.error || ''}>
                <td>{STATUS_LABEL[r.status] || r.status}</td>
                <td>{r.top_k}</td>
                <td>{r.rerank ? 'on' : 'off'}</td>
                <td>{r.recall_at_k.toFixed(3)}</td>
                <td>{r.precision_at_k.toFixed(3)}</td>
                <td>{r.mrr.toFixed(3)}</td>
                <td>{r.hit_rate.toFixed(3)}</td>
                <td>{new Date(r.started_at).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </main>
  )
}

export default Eval
