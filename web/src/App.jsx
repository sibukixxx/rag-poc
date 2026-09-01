import { useState } from 'react'
import './App.css'

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

function App() {
  const [alias, setAlias] = useState('normal')
  const [input, setInput] = useState('')
  const [messages, setMessages] = useState([])
  const [sending, setSending] = useState(false)

  async function sendMessage() {
    const text = input.trim()
    if (!text || sending) return

    const history = [...messages, { role: 'user', content: text }]
    setMessages([...history, { role: 'assistant', content: '' }])
    setInput('')
    setSending(true)

    const apiMessages = history
      .filter((m) => m.role === 'user' || m.role === 'assistant')
      .map((m) => ({ role: m.role, content: m.content }))

    let content = ''
    const setAssistant = (patch) =>
      setMessages((prev) => {
        const next = [...prev]
        next[next.length - 1] = { role: 'assistant', content, ...patch }
        return next
      })

    try {
      const resp = await fetch('/api/v1/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ alias, messages: apiMessages }),
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

  return (
    <div className="app">
      <header className="header">
        <h1>ForgeAI Playground</h1>
        <select value={alias} onChange={(e) => setAlias(e.target.value)} disabled={sending}>
          {ALIASES.map((a) => (
            <option key={a} value={a}>
              {a}
            </option>
          ))}
        </select>
      </header>

      <main className="messages">
        {messages.length === 0 && (
          <p className="empty">Ask something to try the LLM Router ({alias} alias).</p>
        )}
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

export default App
