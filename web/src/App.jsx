import { useState } from 'react'
import './App.css'
import Chat from './Chat'
import Knowledge from './Knowledge'

const TABS = [
  { id: 'chat', label: 'Chat' },
  { id: 'knowledge', label: 'Knowledge' },
]

function App() {
  const [tab, setTab] = useState('chat')

  return (
    <div className="app">
      <header className="header">
        <h1>ForgeAI</h1>
        <nav className="tabs">
          {TABS.map((t) => (
            <button
              key={t.id}
              className={`tab ${tab === t.id ? 'active' : ''}`}
              onClick={() => setTab(t.id)}
            >
              {t.label}
            </button>
          ))}
        </nav>
      </header>

      {tab === 'chat' ? <Chat /> : <Knowledge />}
    </div>
  )
}

export default App
