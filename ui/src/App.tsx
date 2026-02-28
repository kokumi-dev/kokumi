import { useEffect, useState } from 'react'

interface Info {
  name: string
  version: string
}

function App() {
  const [info, setInfo] = useState<Info | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetch('/api/v1/info')
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        return res.json() as Promise<Info>
      })
      .then(setInfo)
      .catch((err: unknown) => setError(String(err)))
  }, [])

  return (
    <main style={{ fontFamily: 'sans-serif', padding: '2rem' }}>
      <h1>Kokumi</h1>
      {error && <p style={{ color: 'red' }}>{error}</p>}
      {!info && !error && <p>Loading…</p>}
      {info && (
        <dl>
          <dt>Name</dt>
          <dd>{info.name}</dd>
          <dt>Version</dt>
          <dd>{info.version}</dd>
        </dl>
      )}
    </main>
  )
}

export default App
