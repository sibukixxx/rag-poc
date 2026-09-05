import { useEffect, useState, useCallback } from 'react'
import { diffLines } from 'diff'

function Prompts() {
  const [prompts, setPrompts] = useState([])
  const [selectedId, setSelectedId] = useState('')
  const [newName, setNewName] = useState('')
  const [versions, setVersions] = useState([])
  const [draft, setDraft] = useState('')
  const [diffAgainst, setDiffAgainst] = useState(null) // version number, or null
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const loadPrompts = useCallback(async () => {
    const resp = await fetch('/api/v1/prompts')
    if (!resp.ok) throw new Error(await resp.text())
    const list = (await resp.json()) || []
    setPrompts(list)
    return list
  }, [])

  const loadVersions = useCallback(async (id) => {
    if (!id) {
      setVersions([])
      return
    }
    const resp = await fetch(`/api/v1/prompts/${id}/versions`)
    if (!resp.ok) throw new Error(await resp.text())
    setVersions((await resp.json()) || [])
  }, [])

  useEffect(() => {
    loadPrompts()
      .then((list) => {
        if (list.length > 0) setSelectedId(list[0].id)
      })
      .catch((err) => setError(String(err.message || err)))
  }, [loadPrompts])

  useEffect(() => {
    loadVersions(selectedId).catch((err) => setError(String(err.message || err)))
    setDiffAgainst(null)
  }, [selectedId, loadVersions])

  const selectedPrompt = prompts.find((p) => p.id === selectedId)

  async function createPrompt(e) {
    e.preventDefault()
    const name = newName.trim()
    if (!name) return
    setError('')
    try {
      const resp = await fetch('/api/v1/prompts', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name }),
      })
      if (!resp.ok) throw new Error(await resp.text())
      const p = await resp.json()
      setNewName('')
      await loadPrompts()
      setSelectedId(p.id)
    } catch (err) {
      setError(String(err.message || err))
    }
  }

  async function addVersion(e) {
    e.preventDefault()
    if (!draft.trim() || !selectedId) return
    setBusy(true)
    setError('')
    try {
      const resp = await fetch(`/api/v1/prompts/${selectedId}/versions`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ content: draft }),
      })
      if (!resp.ok) throw new Error(await resp.text())
      setDraft('')
      await loadVersions(selectedId)
      await loadPrompts()
    } catch (err) {
      setError(String(err.message || err))
    } finally {
      setBusy(false)
    }
  }

  async function activate(version) {
    setBusy(true)
    setError('')
    try {
      const resp = await fetch(`/api/v1/prompts/${selectedId}/activate`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ version }),
      })
      if (!resp.ok) throw new Error(await resp.text())
      await loadPrompts()
    } catch (err) {
      setError(String(err.message || err))
    } finally {
      setBusy(false)
    }
  }

  const diffTarget = diffAgainst != null ? versions.find((v) => v.version === diffAgainst) : null
  const diffBase = diffTarget ? versions.find((v) => v.version === diffTarget.version - 1) : null
  const diffParts = diffTarget ? diffLines(diffBase ? diffBase.content : '', diffTarget.content) : null

  return (
    <div className="view knowledge-view">
      <div className="view-toolbar knowledge-toolbar">
        <select value={selectedId} onChange={(e) => setSelectedId(e.target.value)}>
          <option value="" disabled>
            {prompts.length === 0 ? 'No prompts yet' : 'Select a prompt'}
          </option>
          {prompts.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>

        <form className="kb-create" onSubmit={createPrompt}>
          <input value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="New prompt name" />
          <button type="submit" disabled={!newName.trim()}>
            Create
          </button>
        </form>
      </div>

      {error && <p className="kb-error">{error}</p>}

      <main className="kb-documents prompt-view">
        {!selectedId && <p className="empty">Create or select a prompt to manage its versions.</p>}

        {selectedId && (
          <>
            <form className="prompt-draft" onSubmit={addVersion}>
              <textarea
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                placeholder="Write a new version's content..."
                rows={5}
                disabled={busy}
              />
              <button type="submit" disabled={busy || !draft.trim()}>
                Save as new version
              </button>
            </form>

            <ul className="version-list">
              {versions.map((v) => {
                const isActive = selectedPrompt && selectedPrompt.active_version === v.version
                const isDiffing = diffAgainst === v.version
                return (
                  <li key={v.version} className={`version-row ${isActive ? 'active' : ''}`}>
                    <div className="version-row-header">
                      <span className="version-badge">v{v.version}</span>
                      {isActive && <span className="active-badge">active</span>}
                      <span className="version-date">{new Date(v.created_at).toLocaleString()}</span>
                      <div className="version-actions">
                        {v.version > 1 && (
                          <button className="link-button" onClick={() => setDiffAgainst(isDiffing ? null : v.version)}>
                            {isDiffing ? 'Hide diff' : 'Diff vs previous'}
                          </button>
                        )}
                        {!isActive && (
                          <button className="link-button" onClick={() => activate(v.version)} disabled={busy}>
                            Activate
                          </button>
                        )}
                      </div>
                    </div>

                    {isDiffing && diffParts ? (
                      <pre className="version-diff">
                        {diffParts.map((part, i) => (
                          <span
                            key={i}
                            className={part.added ? 'diff-added' : part.removed ? 'diff-removed' : 'diff-same'}
                          >
                            {part.value}
                          </span>
                        ))}
                      </pre>
                    ) : (
                      <pre className="version-content">{v.content}</pre>
                    )}
                  </li>
                )
              })}
            </ul>
          </>
        )}
      </main>
    </div>
  )
}

export default Prompts
