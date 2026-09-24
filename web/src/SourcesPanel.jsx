import { useCallback, useEffect, useState } from 'react'

// Services the product plans to support but that this build cannot connect
// to yet. They stay visibly disabled so the UI never implies a connection.
const UPCOMING_SOURCES = [
  { id: 'slack', name: 'Slack', summary: 'チャンネルやスレッドの会話を取り込む', examples: ['チャンネル', 'スレッド'] },
  { id: 'jira', name: 'Jira / Confluence', summary: '課題・コメント・ページを取り込む', examples: ['課題', 'ページ'] },
  { id: 'drive', name: 'Google Drive / Microsoft 365', summary: '共有フォルダの資料を継続的に取り込む', examples: ['Docs', 'SharePoint'] },
]

const AUTH_LABEL = {
  not_required: null,
  authorization_required: '認可が必要',
  authorized: '認可済み',
  reauthorization_required: '再認可が必要',
}

const JOB_LABEL = {
  queued: '待機中',
  running: '同期中',
  pause_requested: '一時停止中…',
  paused: '一時停止',
  cancel_requested: '中止中…',
  cancelled: '中止',
  completed: '完了',
  failed: '失敗',
}

const ACTIVE_JOB = new Set(['queued', 'running', 'pause_requested', 'cancel_requested'])

async function api(path, options = {}) {
  const resp = await fetch(path, {
    ...options,
    headers: options.body ? { 'Content-Type': 'application/json' } : undefined,
  })
  const text = await resp.text()
  if (!resp.ok) throw new Error(text.trim() || `HTTP ${resp.status}`)
  return text ? JSON.parse(text) : null
}

function formatTime(value) {
  if (!value) return ''
  return new Date(value).toLocaleString('ja-JP', { dateStyle: 'short', timeStyle: 'short' })
}

function splitPatterns(value) {
  return value
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}

function isActive(conn) {
  return (conn.latest_job && ACTIVE_JOB.has(conn.latest_job.job.status)) || false
}

export default function SourcesPanel({ selectedId, uploading, onUpload }) {
  const [catalog, setCatalog] = useState(null)
  const [connections, setConnections] = useState([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')

  const load = useCallback(async () => {
    if (!selectedId) {
      setConnections([])
      return
    }
    const list = await api(`/api/v1/source-connections?knowledge_base_id=${selectedId}`)
    setConnections(list || [])
  }, [selectedId])

  useEffect(() => {
    api('/api/v1/source-catalog').then(setCatalog).catch((err) => setError(err.message))
  }, [])

  useEffect(() => {
    load().catch((err) => setError(err.message))
  }, [load])

  // Poll while a job runs so progress is visible without reloading.
  const polling = connections.some(isActive) || busy === 'sync'
  useEffect(() => {
    if (!polling) return undefined
    const timer = setInterval(() => load().catch(() => {}), 2000)
    return () => clearInterval(timer)
  }, [polling, load])

  async function run(label, fn) {
    setError('')
    setBusy(label)
    try {
      await fn()
      await load()
    } catch (err) {
      setError(err.message)
    } finally {
      setBusy('')
    }
  }

  const actions = {
    sync: (conn) => run('sync', () => api(`/api/v1/source-connections/${conn.id}/sync`, { method: 'POST' })),
    job: (conn, action) =>
      run(action, () => api(`/api/v1/ingestion-jobs/${conn.latest_job.job.id}/${action}`, { method: 'POST' })),
    enable: (conn, enabled) =>
      run('enable', () => api(`/api/v1/source-connections/${conn.id}/${enabled ? 'enable' : 'disable'}`, { method: 'POST' })),
    authorize: (conn) =>
      run('authorize', async () => {
        const body = await api(`/api/v1/source-connections/${conn.id}/authorize`, { method: 'POST' })
        window.open(body.authorization_url, '_blank', 'noopener')
      }),
    disconnect: (conn) => {
      if (!window.confirm(`「${conn.name}」を切断します。このソースから同期した資料と保存済みの認可情報も削除されます。元のデータは削除されません。`)) return
      run('disconnect', () => api(`/api/v1/source-connections/${conn.id}`, { method: 'DELETE' }))
    },
  }

  return (
    <main className="sources-panel">
      <section className="sources-intro">
        <div>
          <span className="section-kicker">DATA CONNECTIONS</span>
          <h2>情報がある場所をつなぐ</h2>
          <p>ブラウザでは接続先と範囲を決めるだけです。取り込みと更新はサーバーが行うので、この画面を閉じても同期は続きます。</p>
        </div>
        <ol className="source-steps" aria-label="データ連携の流れ">
          <li><span>1</span><strong>接続先を追加</strong><small>フォルダやサービス</small></li>
          <li><span>2</span><strong>範囲を確認</strong><small>許可した場所だけ読む</small></li>
          <li><span>3</span><strong>同期を実行</strong><small>進捗と失敗を確認</small></li>
        </ol>
      </section>

      {!selectedId && <p className="source-notice">最初に上のメニューからナレッジベースを作成してください。</p>}
      {error && <p className="kb-error">{error}</p>}

      {selectedId && (
        <section className="conn-section" aria-label="接続済みのソース">
          <h3 className="conn-heading">接続済みのソース</h3>
          {connections.length === 0 && <p className="empty">まだ接続はありません。下のフォームから追加できます。</p>}
          <ul className="conn-list">
            {connections.map((conn) => (
              <ConnectionCard key={conn.id} conn={conn} busy={busy} actions={actions} />
            ))}
          </ul>
        </section>
      )}

      {selectedId && catalog && (
        <section className="conn-section" aria-label="ソースを追加">
          <h3 className="conn-heading">ソースを追加</h3>
          <div className="source-grid">
            <article className="source-card source-file available">
              <div className="source-card-head">
                <FileLogo />
                <span className="source-status available">今すぐ使える</span>
              </div>
              <div>
                <h3>ファイル</h3>
                <strong>手元の資料を 1 件ずつ登録する</strong>
                <p>PDF、テキスト、CSV などを選ぶだけで検索対象にできます。</p>
              </div>
              <button type="button" onClick={onUpload} disabled={uploading}>
                {uploading ? '登録中…' : 'ファイルを選ぶ'}
              </button>
            </article>
            <AddFolderCard
              catalog={catalog}
              onCreate={(body) => run('create', () => api('/api/v1/source-connections', { method: 'POST', body: JSON.stringify({ ...body, knowledge_base_id: selectedId }) }))}
              busy={busy === 'create'}
            />
            {catalog.oauth_connectors.length > 0 && catalog.oauth_providers.length > 0 && (
              <AddOAuthCard
                catalog={catalog}
                onCreate={(body) => run('create', () => api('/api/v1/source-connections', { method: 'POST', body: JSON.stringify({ ...body, knowledge_base_id: selectedId }) }))}
                busy={busy === 'create'}
              />
            )}
            {UPCOMING_SOURCES.map((s) => (
              <article className={`source-card source-${s.id}`} key={s.id}>
                <div className="source-card-head">
                  <ServiceLogo id={s.id} />
                  <span className="source-status">準備中</span>
                </div>
                <div>
                  <h3>{s.name}</h3>
                  <strong>{s.summary}</strong>
                  <p>このバージョンでは接続できません。</p>
                </div>
                <ul className="source-tags">{s.examples.map((e) => <li key={e}>{e}</li>)}</ul>
                <button type="button" disabled aria-label={`${s.name}連携は準備中です`}>連携機能を準備中</button>
              </article>
            ))}
          </div>
        </section>
      )}

      <p className="source-safety">
        <span aria-hidden="true">✓</span>
        認可情報はサーバー内に暗号化して保存され、ブラウザや接続設定には残りません。フォルダは管理者が許可した場所だけを登録できます。
      </p>
    </main>
  )
}

function ConnectionCard({ conn, busy, actions }) {
  const auth = AUTH_LABEL[conn.auth_state]
  const job = conn.latest_job
  const sync = conn.latest_sync
  const needsAuth = conn.auth_state === 'authorization_required' || conn.auth_state === 'reauthorization_required'
  const active = isActive(conn)
  const status = job ? job.job.status : sync ? sync.status : null

  return (
    <li className={`conn-card ${conn.enabled ? '' : 'conn-disabled'}`}>
      <div className="conn-head">
        <div>
          <strong className="conn-name">{conn.name}</strong>
          <span className="conn-provider">{conn.provider === 'filesystem' ? 'フォルダ' : conn.provider}</span>
        </div>
        <div className="conn-badges">
          <span className={`conn-badge ${conn.enabled ? 'ok' : 'muted'}`}>{conn.enabled ? '有効' : '無効'}</span>
          {auth && <span className={`conn-badge ${conn.auth_state === 'authorized' ? 'ok' : 'warn'}`}>{auth}</span>}
          {status && <span className={`conn-badge ${status === 'failed' ? 'warn' : ''}`}>{JOB_LABEL[status] || status}</span>}
        </div>
      </div>

      <dl className="conn-scope">
        {conn.filesystem && (
          <>
            <dt>場所</dt>
            <dd><code>{conn.filesystem.root}</code></dd>
            {conn.filesystem.include && <><dt>対象</dt><dd>{conn.filesystem.include.join(', ')}</dd></>}
            {conn.filesystem.exclude && <><dt>除外</dt><dd>{conn.filesystem.exclude.join(', ')}</dd></>}
          </>
        )}
        {conn.oauth_provider && (
          <>
            <dt>認可先</dt>
            <dd>{conn.oauth_provider}</dd>
            {conn.scope && <><dt>範囲</dt><dd><code>{JSON.stringify(conn.scope)}</code></dd></>}
          </>
        )}
      </dl>

      {job && (
        <div className="conn-stats" aria-label="最新ジョブの件数">
          <span>見つかった {job.counts.discovered}</span>
          <span>取り込み {job.counts.completed}</span>
          <span>変更なし {job.counts.skipped}</span>
          <span>削除 {job.job.deleted}</span>
          <span className={job.counts.failed ? 'bad' : ''}>失敗 {job.counts.failed}</span>
          {active && <span>残り {job.counts.pending + job.counts.processing}</span>}
          {job.job.scan && job.job.scan.excluded_sensitive > 0 && <span>秘密情報らしいファイルを除外 {job.job.scan.excluded_sensitive}</span>}
          <small>{job.job.finished_at ? `最終同期 ${formatTime(job.job.finished_at)}` : `開始 ${formatTime(job.job.created_at)}`}</small>
        </div>
      )}
      {sync && (
        <div className="conn-stats" aria-label="最新の同期の件数">
          <span>追加 {sync.created}</span>
          <span>更新 {sync.updated}</span>
          <span>削除 {sync.deleted}</span>
          <span>変更なし {sync.skipped}</span>
          <small>{sync.finished_at ? `最終同期 ${formatTime(sync.finished_at)}` : `開始 ${formatTime(sync.started_at)}`}</small>
        </div>
      )}
      {job && job.failures.length > 0 && (
        <details className="conn-failures">
          <summary>失敗したファイル（{job.failures.length}）</summary>
          <ul>{job.failures.map((f) => <li key={f.path}><code>{f.path}</code> {f.error}</li>)}</ul>
        </details>
      )}
      {(conn.last_error || (job && job.job.last_error) || (sync && sync.error)) && (
        <p className="conn-error">{describeError(conn.last_error || (job && job.job.last_error) || sync.error)}</p>
      )}

      <div className="conn-actions">
        {needsAuth && (
          <button type="button" onClick={() => actions.authorize(conn)} disabled={!!busy}>
            {conn.auth_state === 'reauthorization_required' ? '再認可する' : '認可する'}
          </button>
        )}
        {!active && (
          <button type="button" onClick={() => actions.sync(conn)} disabled={!!busy || !conn.enabled || needsAuth}>
            同期を実行
          </button>
        )}
        {job && ['queued', 'running'].includes(job.job.status) && (
          <button type="button" onClick={() => actions.job(conn, 'pause')} disabled={!!busy}>一時停止</button>
        )}
        {job && job.job.status === 'paused' && (
          <button type="button" onClick={() => actions.job(conn, 'resume')} disabled={!!busy}>再開</button>
        )}
        {job && ['queued', 'running', 'paused'].includes(job.job.status) && (
          <button type="button" onClick={() => actions.job(conn, 'cancel')} disabled={!!busy}>中止</button>
        )}
        <button type="button" className="secondary" onClick={() => actions.enable(conn, !conn.enabled)} disabled={!!busy}>
          {conn.enabled ? '無効にする' : '有効にする'}
        </button>
        <button type="button" className="danger" onClick={() => actions.disconnect(conn)} disabled={!!busy}>切断</button>
      </div>
    </li>
  )
}

function AddFolderCard({ catalog, onCreate, busy }) {
  const [root, setRoot] = useState('')
  const [name, setName] = useState('')
  const [exclude, setExclude] = useState('')

  if (!catalog.filesystem_enabled) {
    return (
      <article className="source-card">
        <div className="source-card-head">
          <FileLogo />
          <span className="source-status">サーバー設定が必要</span>
        </div>
        <div>
          <h3>フォルダ / NAS</h3>
          <strong>大量の資料をフォルダごと取り込む</strong>
          <p>管理者が <code>sources.filesystem.allowed_roots</code> を設定すると、サブフォルダを含めて一括で取り込めます。</p>
        </div>
      </article>
    )
  }

  function submit(e) {
    e.preventDefault()
    onCreate({ provider: 'filesystem', root: root.trim(), name: name.trim(), exclude: splitPatterns(exclude) })
    setRoot('')
    setName('')
    setExclude('')
  }

  return (
    <article className="source-card available">
      <div className="source-card-head">
        <FileLogo />
        <span className="source-status available">今すぐ使える</span>
      </div>
      <form className="source-form" onSubmit={submit}>
        <h3>フォルダ / NAS</h3>
        <label>
          場所（サーバー上のパス）
          <input value={root} onChange={(e) => setRoot(e.target.value)} placeholder={catalog.allowed_roots[0] || '/srv/share'} required />
        </label>
        <small>登録できる場所: {catalog.allowed_roots.join(', ')}</small>
        <label>
          名前
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="例: 社内規程" />
        </label>
        <label>
          除外（カンマ区切り）
          <input value={exclude} onChange={(e) => setExclude(e.target.value)} placeholder="drafts/, *.tmp" />
        </label>
        <button type="submit" disabled={busy || !root.trim()}>フォルダを追加</button>
      </form>
    </article>
  )
}

function AddOAuthCard({ catalog, onCreate, busy }) {
  const [connector, setConnector] = useState(catalog.oauth_connectors[0])
  const [provider, setProvider] = useState(catalog.oauth_providers[0])
  const [name, setName] = useState('')
  const [scope, setScope] = useState('{}')
  const [scopeError, setScopeError] = useState('')

  function submit(e) {
    e.preventDefault()
    let parsed
    try {
      parsed = JSON.parse(scope || '{}')
    } catch {
      setScopeError('範囲は JSON で入力してください')
      return
    }
    setScopeError('')
    onCreate({ provider: connector, oauth_provider: provider, name: name.trim() || connector, scope: parsed })
    setName('')
  }

  return (
    <article className="source-card available">
      <div className="source-card-head">
        <span className="source-logo" aria-hidden="true" />
        <span className="source-status available">認可して使う</span>
      </div>
      <form className="source-form" onSubmit={submit}>
        <h3>外部サービス</h3>
        <label>
          サービス
          <select value={connector} onChange={(e) => setConnector(e.target.value)}>
            {catalog.oauth_connectors.map((c) => <option key={c} value={c}>{c}</option>)}
          </select>
        </label>
        <label>
          認可先
          <select value={provider} onChange={(e) => setProvider(e.target.value)}>
            {catalog.oauth_providers.map((p) => <option key={p} value={p}>{p}</option>)}
          </select>
        </label>
        <label>
          名前
          <input value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <label>
          読み取る範囲（JSON）
          <textarea value={scope} onChange={(e) => setScope(e.target.value)} rows={3} />
        </label>
        {scopeError && <small className="bad">{scopeError}</small>}
        <button type="submit" disabled={busy}>追加して認可へ進む</button>
      </form>
    </article>
  )
}

// describeError turns known server messages into Japanese; anything else is
// shown as-is so no detail is hidden from the operator.
function describeError(message) {
  const failed = /^(\d+) file\(s\) failed$/.exec(message)
  if (failed) return `${failed[1]} 件のファイルを取り込めませんでした。下の「失敗したファイル」を確認してください。`
  if (message.startsWith('authorization expired or was revoked')) return '認可の期限が切れたか取り消されました。「再認可する」から認可し直してください。'
  return message
}

function ServiceLogo({ id }) {
  const bars = { slack: 4, jira: 2, drive: 3 }[id] || 0
  return (
    <span className={`source-logo ${id}-logo`} aria-hidden="true">
      {Array.from({ length: bars }, (_, i) => <i key={i} />)}
    </span>
  )
}

function FileLogo() {
  return (
    <span className="source-logo file-logo" aria-hidden="true">
      <svg viewBox="0 0 24 24"><path d="M6 3h8l4 4v14H6z" /><path d="M14 3v5h5M9 13h6M9 17h4" /></svg>
    </span>
  )
}
