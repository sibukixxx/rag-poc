function ServiceIcon({ name }) {
  const common = {
    width: 24,
    height: 24,
    viewBox: '0 0 24 24',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 1.8,
    strokeLinecap: 'round',
    strokeLinejoin: 'round',
    'aria-hidden': true,
  }

  if (name === 'chat') {
    return (
      <svg {...common}>
        <path d="M20 11.5a7.6 7.6 0 0 1-8 7.2 9.2 9.2 0 0 1-3.2-.6L4 20l1.4-4A6.8 6.8 0 0 1 4 11.8C4 7.5 7.6 4 12 4s8 3.3 8 7.5Z" />
        <path d="M8.2 11.5h.1M11.9 11.5h.1M15.6 11.5h.1" />
      </svg>
    )
  }

  if (name === 'knowledge') {
    return (
      <svg {...common}>
        <path d="M5 4.5h10.5A2.5 2.5 0 0 1 18 7v12H7.5A2.5 2.5 0 0 1 5 16.5v-12Z" />
        <path d="M18 7h1a1 1 0 0 1 1 1v12H8a3 3 0 0 1-3-3" />
        <path d="M8.5 9h6M8.5 12h6" />
      </svg>
    )
  }

  if (name === 'prompts') {
    return (
      <svg {...common}>
        <path d="M4 7h10M18 7h2M4 17h2M10 17h10M4 12h4M12 12h8" />
        <circle cx="16" cy="7" r="2" />
        <circle cx="8" cy="17" r="2" />
        <circle cx="10" cy="12" r="2" />
      </svg>
    )
  }

  if (name === 'traces') {
    return (
      <svg {...common}>
        <path d="M3 12h4l2.2-6 4.1 12 2.2-6H21" />
        <path d="M4 4v16M20 4v16" opacity=".35" />
      </svg>
    )
  }

  return (
    <svg {...common}>
      <path d="M4 19V9M10 19V5M16 19v-7M22 19V3" />
      <path d="m3 14 6-5 6 2 7-7" />
      <path d="m18 4 4-1-1 4" />
    </svg>
  )
}

export default ServiceIcon
