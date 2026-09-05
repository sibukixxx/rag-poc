import { useEffect, useState } from 'react'

const ALIASES = ['cheap', 'normal', 'judge']

function parseSSEBuffer(buffer, onEvent) {
  let idx
  while ((idx = buffer.indexOf('\n\n')) >= 0) {
    const rawEvent = buffer.slice(0, idx)
    buffer = buffer.slice(idx + 2)
    const line = rawEvent.split('\n').find((l) => l.startsWith('data: '))
    if (!line) continue
    let evt
    try {
      evt = JSON.parse(line.slice(6))
    } catch {
      // Ignore malformed chunks rather than aborting the whole stream.
      continue
    }
    // Let onEvent throw (e.g. on evt.error) — the caller's try/catch
    // around the read loop is what surfaces it as an error bubble.
    onEvent(evt)
  }
  return buffer
}

function Chat() {
  const [alias, setAlias] = useState('normal')
  const [knowledgeBases, setKnowledgeBases] = useState([])
  const [kbId, setKbId] = useState('')
  const [input, setInput] = useState('')
  const [messages, setMessages] = useState([])
  const [sending, setSending] = useState(false)
  const [expanded, setExpanded] = useState(() => new Set())

  useEffect(() => {
    fetch('/api/v1/knowledge-bases')
      .then((r) => (r.ok ? r.json() : []))
      .then((kbs) => setKnowledgeBases(kbs || []))
      .catch(() => setKnowledgeBases([]))
  }, [])

  function toggleCitation(key) {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  async function sendMessage() {
    const text = input.trim()
    if (!text || sending) return

    const history = [...messages, { role: 'user', content: text }]
    setMessages([...history, { role: 'assistant', content: '' }])
    setInput('')
    setSending(true)

    let content = ''
    const setAssistant = (patch) =>
      setMessages((prev) => {
        const next = [...prev]
        next[next.length - 1] = { role: 'assistant', content, ...patch }
        return next
      })

    try {
      const useRAG = Boolean(kbId)
      const url = useRAG ? `/api/v1/knowledge-bases/${kbId}/chat` : '/api/v1/chat'
      const body = useRAG
        ? { alias, query: text }
        : {
            alias,
            messages: history
              .filter((m) => m.role === 'user' || m.role === 'assistant')
              .map((m) => ({ role: m.role, content: m.content })),
          }

      const resp = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (!resp.ok) {
        throw new Error((await resp.text()) || `HTTP ${resp.status}`)
      }

      const reader = resp.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        buffer = parseSSEBuffer(buffer, (evt) => {
          if (evt.error) throw new Error(evt.error)
          if (evt.delta) {
            content += evt.delta
            setAssistant({})
          }
          if (evt.done) {
            setAssistant({
              meta: { traceId: evt.trace_id, usage: evt.usage, costUsd: evt.cost_usd },
              citations: evt.citations || [],
              noContext: evt.no_context || false,
            })
          }
        })
      }
    } catch (err) {
      setMessages((prev) => {
        const next = [...prev]
        next[next.length - 1] = { role: 'error', content: String(err.message || err) }
        return next
      })
    } finally {
      setSending(false)
    }
  }

  function handleKeyDown(e) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      sendMessage()
    }
  }

  const modeLabel = kbId
    ? `RAG chat over ${(knowledgeBases.find((k) => k.id === kbId) || {}).name || 'knowledge base'}`
    : `plain chat (${alias} alias)`

  return (
    <div className="view chat-view">
      <div className="view-toolbar">
        <select value={alias} onChange={(e) => setAlias(e.target.value)} disabled={sending}>
          {ALIASES.map((a) => (
            <option key={a} value={a}>
              {a}
            </option>
          ))}
        </select>
        <select value={kbId} onChange={(e) => setKbId(e.target.value)} disabled={sending}>
          <option value="">No knowledge base (plain chat)</option>
          {knowledgeBases.map((kb) => (
            <option key={kb.id} value={kb.id}>
              {kb.name}
            </option>
          ))}
        </select>
      </div>

      <main className="messages">
        {messages.length === 0 && <p className="empty">Ask something to try {modeLabel}.</p>}
        {messages.map((m, i) => (
          <div key={i} className={`bubble ${m.role}`}>
            <div className="bubble-content">{m.content || (sending && i === messages.length - 1 ? '…' : '')}</div>
            {m.meta && (
              <div className="bubble-meta">
                {m.meta.usage && (
                  <span>
                    {m.meta.usage.input_tokens}&rarr;{m.meta.usage.output_tokens} tok
                  </span>
                )}
                {typeof m.meta.costUsd === 'number' && <span> · ${m.meta.costUsd.toFixed(6)}</span>}
                {m.meta.traceId && <span> · trace {m.meta.traceId.slice(0, 8)}</span>}
              </div>
            )}
            {m.noContext && <div className="bubble-meta">No matching context found in the knowledge base.</div>}
            {m.citations && m.citations.length > 0 && (
              <div className="citations">
                {m.citations.map((c) => {
                  const key = `${i}:${c.index}`
                  const isOpen = expanded.has(key)
                  return (
                    <div key={key} className="citation">
                      <button className="citation-chip" onClick={() => toggleCitation(key)}>
                        [{c.index}] {c.filename}
                        {c.page != null ? ` p.${c.page}` : ''}
                      </button>
                      {isOpen && <p className="citation-text">{c.text}</p>}
                    </div>
                  )
                })}
              </div>
            )}
          </div>
        ))}
      </main>

      <footer className="composer">
        <textarea
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="Type a message... (Enter to send, Shift+Enter for newline)"
          rows={2}
          disabled={sending}
        />
        <button onClick={sendMessage} disabled={sending || !input.trim()}>
          {sending ? 'Sending…' : 'Send'}
        </button>
      </footer>
    </div>
  )
}

export default Chat
