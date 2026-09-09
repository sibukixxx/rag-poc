import { useEffect, useState, useCallback, useRef } from 'react'

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

const SOURCE_OPTIONS = [
  {
    id: 'slack',
    name: 'Slack',
    summary: 'チャンネルやスレッドの会話を取り込む',
    detail: '必要なチャンネルだけを選び、過去の質問や回答を検索できます。',
    examples: ['チャンネル', 'スレッド', '添付資料'],
    status: '次に対応',
  },
  {
    id: 'jira',
    name: 'Jira',
    summary: '課題・コメント・対応履歴を取り込む',
    detail: '選択したプロジェクトの課題や判断の経緯をまとめて検索できます。',
    examples: ['課題', 'コメント', '対応履歴'],
    status: '準備中',
  },
  {
    id: 'drive',
    name: 'Google Drive',
    summary: '共有フォルダの資料を継続的に取り込む',
    detail: '指定フォルダだけを対象にして、更新された資料を同期できます。',
    examples: ['Google Docs', 'PDF', 'スプレッドシート'],
    status: '準備中',
  },
]

function SourcesPanel({ selectedId, uploading, onUpload }) {
  return (
    <main className="sources-panel">
      <section className="sources-intro">
        <div>
          <span className="section-kicker">DATA CONNECTIONS</span>
          <h2>情報がある場所を選ぶ</h2>
          <p>難しい設定は後回しで大丈夫です。まず、ForgeAIで検索したい情報がどこにあるかを選びます。</p>
        </div>
        <ol className="source-steps" aria-label="データ連携の流れ">
          <li><span>1</span><strong>サービスを選ぶ</strong><small>情報の保存場所</small></li>
          <li><span>2</span><strong>範囲を確認</strong><small>読む場所だけ許可</small></li>
          <li><span>3</span><strong>同期を開始</strong><small>更新も自動反映</small></li>
        </ol>
      </section>

      {!selectedId && <p className="source-notice">最初に上のメニューからナレッジベースを作成してください。</p>}

      <section className="source-grid" aria-label="連携できるデータソース">
        <article className="source-card source-file available">
          <div className="source-card-head">
            <SourceLogo name="file" />
            <span className="source-status available">今すぐ使える</span>
          </div>
          <div>
            <h3>ファイル</h3>
            <strong>手元の資料をそのまま登録する</strong>
            <p>PDF、テキスト、CSVなどを選ぶだけで検索対象にできます。</p>
          </div>
          <ul className="source-tags"><li>PDF</li><li>CSV</li><li>文書</li></ul>
          <button type="button" onClick={onUpload} disabled={!selectedId || uploading}>
            {uploading ? '登録中…' : 'ファイルを選ぶ'}
          </button>
        </article>

        {SOURCE_OPTIONS.map((source) => (
          <article className={`source-card source-${source.id}`} key={source.id}>
            <div className="source-card-head">
              <SourceLogo name={source.id} />
              <span className={`source-status ${source.id === 'slack' ? 'next' : ''}`}>{source.status}</span>
            </div>
            <div>
              <h3>{source.name}</h3>
              <strong>{source.summary}</strong>
              <p>{source.detail}</p>
            </div>
            <ul className="source-tags">
              {source.examples.map((example) => <li key={example}>{example}</li>)}
            </ul>
            <button type="button" disabled aria-label={`${source.name}連携は準備中です`}>連携機能を準備中</button>
          </article>
        ))}
      </section>

      <p className="source-safety">
        <span aria-hidden="true">✓</span>
        接続時は、管理者が許可したチャンネル・プロジェクト・フォルダだけを読み取ります。
      </p>
    </main>
  )
}

function SourceLogo({ name }) {
  if (name === 'slack') {
    return <span className="source-logo slack-logo" aria-hidden="true"><i /><i /><i /><i /></span>
  }
  if (name === 'jira') {
    return <span className="source-logo jira-logo" aria-hidden="true"><i /><i /></span>
  }
  if (name === 'drive') {
    return <span className="source-logo drive-logo" aria-hidden="true"><i /><i /><i /></span>
  }
  return (
    <span className="source-logo file-logo" aria-hidden="true">
      <svg viewBox="0 0 24 24"><path d="M6 3h8l4 4v14H6z" /><path d="M14 3v5h5M9 13h6M9 17h4" /></svg>
    </span>
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
