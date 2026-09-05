import { useEffect, useState, useCallback } from 'react'

function Traces() {
  const [traces, setTraces] = useState([])
  const [selectedId, setSelectedId] = useState('')
  const [detail, setDetail] = useState(null)
  const [error, setError] = useState('')

  const loadTraces = useCallback(async () => {
    const resp = await fetch('/api/v1/traces?limit=50')
    if (!resp.ok) throw new Error(await resp.text())
    setTraces((await resp.json()) || [])
  }, [])

  useEffect(() => {
    loadTraces().catch((err) => setError(String(err.message || err)))
  }, [loadTraces])

  useEffect(() => {
    if (!selectedId) {
      setDetail(null)
      return
    }
    fetch(`/api/v1/traces/${selectedId}`)
      .then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`)
        return r.json()
      })
      .then(setDetail)
      .catch((err) => setError(String(err.message || err)))
  }, [selectedId])

  return (
    <div className="view knowledge-view">
      <div className="view-toolbar">
        <button className="link-button" onClick={() => loadTraces().catch((err) => setError(String(err.message || err)))}>
          Refresh
        </button>
      </div>

      {error && <p className="kb-error">{error}</p>}

      <main className="kb-documents traces-view">
        {traces.length === 0 && <p className="empty">No traces recorded yet. Chat, search, or ingest something first.</p>}

        {traces.length > 0 && (
          <table className="doc-table trace-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Status</th>
                <th>Duration</th>
                <th>Cost</th>
                <th>Started</th>
              </tr>
            </thead>
            <tbody>
              {traces.map((t) => (
                <tr
                  key={t.id}
                  className={`doc-row trace-row ${t.status === 'error' ? 'doc-failed' : 'doc-ready'} ${selectedId === t.id ? 'selected' : ''}`}
                  onClick={() => setSelectedId(t.id)}
                >
                  <td>{t.name}</td>
                  <td>{t.status}</td>
                  <td>{t.duration_ms} ms</td>
                  <td>${t.cost_usd.toFixed(6)}</td>
                  <td>{new Date(t.started_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}

        {detail && (
          <div className="trace-detail">
            <h3>Spans for {detail.trace.name}</h3>
            <table className="doc-table">
              <thead>
                <tr>
                  <th>Kind</th>
                  <th>Name</th>
                  <th>Duration</th>
                  <th>Tokens</th>
                  <th>Cost</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {detail.spans.map((s) => (
                  <tr key={s.id} className={`doc-row ${s.status === 'error' ? 'doc-failed' : 'doc-ready'}`} title={s.error || ''}>
                    <td>{s.kind}</td>
                    <td>{s.name}</td>
                    <td>{s.duration_ms} ms</td>
                    <td>{s.input_tokens || s.output_tokens ? `${s.input_tokens}→${s.output_tokens}` : '—'}</td>
                    <td>{s.cost_usd ? `$${s.cost_usd.toFixed(6)}` : '—'}</td>
                    <td>{s.status}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </main>
    </div>
  )
}

export default Traces
