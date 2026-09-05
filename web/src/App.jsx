import { useState } from 'react'
import './App.css'
import Chat from './Chat'
import Knowledge from './Knowledge'
import Prompts from './Prompts'
import Traces from './Traces'

const TABS = [
  { id: 'chat', label: 'Chat', Component: Chat },
  { id: 'knowledge', label: 'Knowledge', Component: Knowledge },
  { id: 'prompts', label: 'Prompts', Component: Prompts },
  { id: 'traces', label: 'Traces', Component: Traces },
]

function App() {
  const [tab, setTab] = useState('chat')
  const Active = TABS.find((t) => t.id === tab).Component

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

      <Active />
    </div>
  )
}

export default App
