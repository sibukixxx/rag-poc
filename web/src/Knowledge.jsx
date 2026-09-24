import { useEffect, useState, useCallback, useRef } from 'react'
import SourcesPanel from './SourcesPanel.jsx'

const STATUS_LABEL = {
  pending: '処理中…',
  ready: '利用可能',
  failed: '失敗',
}

function Knowledge() {
  const [knowledgeBases, setKnowledgeBases] = useState([])
  const [selectedId, setSelectedId] = useState('')
  const [newKbName, setNewKbName] = useState('')
  const [documents, setDocuments] = useState([])
  const [uploading, setUploading] = useState(false)
  const [error, setError] = useState('')
  const [subTab, setSubTab] = useState('documents')
  const fileInputRef = useRef(null)

  const loadKnowledgeBases = useCallback(async () => {
    const resp = await fetch('/api/v1/knowledge-bases')
    if (!resp.ok) throw new Error(await resp.text())
    const kbs = await resp.json()
    setKnowledgeBases(kbs || [])
    return kbs || []
  }, [])

  const loadDocuments = useCallback(async (kbId) => {
    if (!kbId) {
      setDocuments([])
      return
    }
    const resp = await fetch(`/api/v1/knowledge-bases/${kbId}/documents`)
    if (!resp.ok) throw new Error(await resp.text())
    setDocuments((await resp.json()) || [])
  }, [])

  useEffect(() => {
    loadKnowledgeBases()
      .then((kbs) => {
        if (kbs.length > 0) setSelectedId(kbs[0].id)
      })
      .catch((err) => setError(String(err.message || err)))
  }, [loadKnowledgeBases])

  useEffect(() => {
    loadDocuments(selectedId).catch((err) => setError(String(err.message || err)))
  }, [selectedId, loadDocuments])

  async function createKnowledgeBase(e) {
    e.preventDefault()
    const name = newKbName.trim()
    if (!name) return
    setError('')
    try {
      const resp = await fetch('/api/v1/knowledge-bases', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name }),
      })
      if (!resp.ok) throw new Error(await resp.text())
      const kb = await resp.json()
      setNewKbName('')
      const kbs = await loadKnowledgeBases()
      setSelectedId(kb.id || (kbs[0] && kbs[0].id) || '')
    } catch (err) {
      setError(String(err.message || err))
    }
  }

  async function uploadFile(e) {
    const file = e.target.files && e.target.files[0]
    e.target.value = ''
    if (!file || !selectedId) return

    setUploading(true)
    setError('')
    try {
      const form = new FormData()
      form.append('file', file)
      const resp = await fetch(`/api/v1/knowledge-bases/${selectedId}/documents`, {
        method: 'POST',
        body: form,
      })
      const body = await resp.json().catch(() => null)
      if (!resp.ok) throw new Error((body && body.error) || `HTTP ${resp.status}`)
      await loadDocuments(selectedId)
      if (body && body.status === 'failed') {
        setError(`${file.name}: ${body.error || 'ingestion failed'}`)
      }
    } catch (err) {
      setError(String(err.message || err))
    } finally {
      setUploading(false)
    }
  }

  return (
    <div className="view knowledge-view">
      <div className="view-toolbar knowledge-toolbar">
        <select value={selectedId} onChange={(e) => setSelectedId(e.target.value)}>
          <option value="" disabled>
            {knowledgeBases.length === 0 ? '保存先がまだありません' : '保存先を選ぶ'}
          </option>
          {knowledgeBases.map((kb) => (
            <option key={kb.id} value={kb.id}>
              {kb.name}
            </option>
          ))}
        </select>

        <form className="kb-create" onSubmit={createKnowledgeBase}>
          <input
            value={newKbName}
            onChange={(e) => setNewKbName(e.target.value)}
            placeholder="保存先の名前"
          />
          <button type="submit" disabled={!newKbName.trim()}>
            作成
          </button>
        </form>

        <nav className="sub-tabs">
          <button className={`sub-tab ${subTab === 'documents' ? 'active' : ''}`} onClick={() => setSubTab('documents')}>
            登録済み資料
          </button>
          <button className={`sub-tab ${subTab === 'sources' ? 'active' : ''}`} onClick={() => setSubTab('sources')}>
            データ連携
          </button>
          <button className={`sub-tab ${subTab === 'search' ? 'active' : ''}`} onClick={() => setSubTab('search')}>
            検索テスト
          </button>
        </nav>

        {subTab === 'documents' && (
          <button className="upload-button" type="button" onClick={() => fileInputRef.current?.click()} disabled={!selectedId || uploading}>
            {uploading ? '登録中…' : 'ファイルを追加'}
          </button>
        )}
        <input ref={fileInputRef} type="file" onChange={uploadFile} disabled={!selectedId || uploading} hidden />
      </div>

      {error && <p className="kb-error">{error}</p>}

      {subTab === 'documents' && (
        <DocumentsPanel selectedId={selectedId} documents={documents} />
      )}
      {subTab === 'sources' && (
        <SourcesPanel
          selectedId={selectedId}
          uploading={uploading}
          onUpload={() => fileInputRef.current?.click()}
        />
      )}
      {subTab === 'search' && (
        <SearchPanel selectedId={selectedId} onError={(msg) => setError(msg)} />
      )}
    </div>
  )
}

function DocumentsPanel({ selectedId, documents }) {
  return (
    <main className="kb-documents">
      {!selectedId && <p className="empty">最初に保存先を作成または選択してください。</p>}
      {selectedId && documents.length === 0 && (
        <p className="empty">登録済みの資料はありません。「ファイルを追加」から登録できます。</p>
      )}
      {documents.length > 0 && (
        <table className="doc-table">
          <thead>
            <tr>
              <th>ファイル名</th>
              <th>状態</th>
              <th>分割数</th>
              <th>サイズ</th>
            </tr>
          </thead>
          <tbody>
            {documents.map((d) => (
              <tr key={d.id} className={`doc-row doc-${d.status}`}>
                <td>{d.filename}</td>
                <td title={d.error || ''}>{STATUS_LABEL[d.status] || d.status}</td>
                <td>{d.chunk_count}</td>
                <td>{(d.size_bytes / 1024).toFixed(1)} KB</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </main>
  )
}

function SearchPanel({ selectedId, onError }) {
  const [query, setQuery] = useState('')
  const [rerank, setRerank] = useState(false)
  const [results, setResults] = useState(null)
  const [searching, setSearching] = useState(false)

  async function runSearch(e) {
    e.preventDefault()
    if (!query.trim() || !selectedId) return

    setSearching(true)
    onError('')
    try {
      const resp = await fetch(`/api/v1/knowledge-bases/${selectedId}/search`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ query, top_k: 10, rerank }),
      })
      const body = await resp.json().catch(() => null)
      if (!resp.ok) throw new Error((body && body.error) || `HTTP ${resp.status}`)
      setResults(body.results || [])
    } catch (err) {
      onError(String(err.message || err))
      setResults(null)
    } finally {
      setSearching(false)
    }
  }

  return (
    <main className="kb-search">
      <form className="search-form" onSubmit={runSearch}>
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search this knowledge base (vector + keyword)"
          disabled={!selectedId}
        />
        <label className="rerank-toggle">
          <input type="checkbox" checked={rerank} onChange={(e) => setRerank(e.target.checked)} />
          Rerank
        </label>
        <button type="submit" disabled={!selectedId || !query.trim() || searching}>
          {searching ? 'Searching…' : 'Search'}
        </button>
      </form>

      {!selectedId && <p className="empty">Select a knowledge base first.</p>}
      {selectedId && results !== null && results.length === 0 && <p className="empty">No results.</p>}

      {results && results.length > 0 && (
        <ul className="search-results">
          {results.map((r) => (
            <li key={r.chunk_id} className="search-result">
              <div className="search-result-meta">
                <span className="search-result-file">{r.filename}</span>
                {r.page != null && <span> · page {r.page}</span>}
                <span className="search-result-score"> · score {r.score.toFixed(4)}</span>
              </div>
              <p className="search-result-text">{r.text}</p>
            </li>
          ))}
        </ul>
      )}
    </main>
  )
}

export default Knowledge
