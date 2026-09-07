import { useState } from 'react'
import './App.css'
import Chat from './Chat'
import Knowledge from './Knowledge'
import Prompts from './Prompts'
import Traces from './Traces'
import Evaluation from './Evaluation'
import ServiceIcon from './ServiceIcon'

const TABS = [
  {
    id: 'chat',
    label: 'AI回答',
    english: 'Chat',
    description: '登録した資料を根拠に質問へ回答',
    purpose: '質問する',
    Component: Chat,
  },
  {
    id: 'knowledge',
    label: '資料管理',
    english: 'Knowledge',
    description: 'PDFや文書を登録して検索可能にする',
    purpose: '知識を入れる',
    Component: Knowledge,
  },
  {
    id: 'prompts',
    label: '回答ルール',
    english: 'Prompts',
    description: 'AIへの指示を版管理して切り替える',
    purpose: '振る舞いを決める',
    Component: Prompts,
  },
  {
    id: 'traces',
    label: '実行履歴',
    english: 'Traces',
    description: '処理時間・コスト・エラーを追跡',
    purpose: '動きを調べる',
    Component: Traces,
  },
  {
    id: 'evaluation',
    label: '品質評価',
    english: 'Evaluation',
    description: '検索品質の改善と悪化を数字で証明',
    purpose: '品質を証明する',
    Component: Evaluation,
  },
]

function App() {
  const [tab, setTab] = useState('chat')
  const activeService = TABS.find((t) => t.id === tab)
  const Active = activeService.Component

  return (
    <div className="app">
      <header className="header">
        <div className="brand">
          <div className="brand-mark" aria-hidden="true">
            <span />
            <span />
            <span />
          </div>
          <div>
            <h1>ForgeAI</h1>
            <p>RAG Quality Engineering</p>
          </div>
        </div>
        <span className="local-badge"><i /> Local-first</span>
      </header>

      <nav className="service-nav" aria-label="ForgeAI services">
          {TABS.map((t) => (
            <button
              key={t.id}
              className={`service-tab service-${t.id} ${tab === t.id ? 'active' : ''}`}
              onClick={() => setTab(t.id)}
              aria-current={tab === t.id ? 'page' : undefined}
            >
              <span className="service-icon"><ServiceIcon name={t.id} /></span>
              <span className="service-copy">
                <strong>{t.label}</strong>
                <small>{t.english}</small>
                <em>{t.description}</em>
              </span>
              <span className="service-arrow" aria-hidden="true">›</span>
            </button>
          ))}
      </nav>

      <div className={`active-service-bar service-${activeService.id}`}>
        <span className="active-service-icon"><ServiceIcon name={activeService.id} /></span>
        <div>
          <small>{activeService.english}</small>
          <strong>{activeService.purpose}</strong>
        </div>
        <p>{activeService.description}</p>
      </div>

      <Active />
    </div>
  )
}

export default App
