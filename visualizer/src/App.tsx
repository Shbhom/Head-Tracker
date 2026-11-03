import { useEffect, useState, type FormEvent } from 'react'
import { Leva, useControls } from 'leva'
import { Quaternion } from 'three'
import { axes, normalized, type Axis } from './orientation'
import { Scene } from './Scene'
import { useOrientationStream } from './useOrientationStream'

export function App() {
  const stream = useOrientationStream()
  const [endpoint, setEndpoint] = useState('ws://127.0.0.1:8080/ws/orientation')
  const [token, setToken] = useState('')
  const [reference, setReference] = useState<Quaternion | null>(null)
  const [cameraKey, setCameraKey] = useState(0)
  const controls = useControls({
    'Forward axis': { value: '+Z', options: axes },
    'Visual smoothing': false,
    'Show grid': true,
    'Auto reconnect': true,
  })
  const { view } = stream
  useEffect(() => {
    if (view.phase === 'offline' || view.phase === 'retrying') setReference(null)
  }, [view.phase])
  const live = view.phase === 'connected' && view.age !== null && view.age < 2500
  const stale = view.sample !== null && !live
  const active = view.phase !== 'offline'
  const label = live ? 'Live orientation' : stale ? 'Data stale' : view.phase === 'connected' ? 'Waiting for sensor' : view.phase === 'retrying' ? 'Reconnecting' : view.phase === 'connecting' ? 'Connecting' : 'Not connected'
  const connect = (event: FormEvent) => {
    event.preventDefault()
    setReference(null)
    stream.connect(endpoint, token, controls['Auto reconnect'])
  }

  return <main>
    <header>
      <div className="brand"><span className="brand-icon">◉</span><div><h1>Buds <span>/ spatial viewer</span></h1><p>ORIENTATION LAB · GALAXY BUDS2 PRO</p></div></div>
      <span className="tag">DIAGNOSTIC TOOL</span>
    </header>
    <div className="workspace">
      <section className="viewport" aria-label="3D orientation sphere">
        <div className="viewport-top"><span className={`badge ${live ? 'live' : ''}`} role="status"><i />{label}</span><span className="unit">UNIT SPHERE / 3 DOF</span></div>
        <Scene latest={stream.latest} reference={reference} forward={controls['Forward axis'] as Axis} smoothing={controls['Visual smoothing']} grid={controls['Show grid']} cameraKey={cameraKey} />
        {!live && <div className="empty-scene"><span>{stale ? 'Waiting for fresh data' : active ? 'Listening for orientation' : 'Your orientation, in space.'}</span><p>{active ? 'The pointer appears when a valid quaternion arrives.' : 'Connect to the WebSocket stream to reveal the direction pointer.'}</p></div>}
        <div className="viewport-bottom"><span><i className="cyan" />Forward direction <i className="amber" />Roll / up marker</span><span>DRAG TO ORBIT · SCROLL TO ZOOM</span></div>
      </section>
      <aside>
        <section className="panel">
          <div className="section-heading"><span>01</span><h2>Connect a stream</h2></div>
          <form onSubmit={connect}>
            <label htmlFor="endpoint">WebSocket endpoint</label>
            <input id="endpoint" type="url" value={endpoint} onChange={e => setEndpoint(e.target.value)} disabled={active} required spellCheck={false} />
            <label htmlFor="token">API token <span>optional on localhost</span></label>
            <input id="token" type="password" autoComplete="off" value={token} onChange={e => setToken(e.target.value)} disabled={active} placeholder="Kept in memory only" />
            {active ? <button type="button" className="secondary" onClick={event => { event.preventDefault(); stream.disconnect() }}>Disconnect</button> : <button type="submit">Connect stream <span>↗</span></button>}
          </form>
          <p className="connection-note">{view.message || (active ? `Backend: ${view.status}` : 'Start the Go backend first. This viewer never opens Bluetooth.')}</p>
        </section>
        <section className="panel">
          <div className="section-heading"><span>02</span><h2>Sensor readout</h2></div>
          <div className="metrics"><div><span>ARRIVAL RATE</span><strong>{view.rate}<small> msg/s</small></strong></div><div><span>LAST RECEIVED</span><strong>{view.age === null ? '—' : (view.age / 1000).toFixed(1)}<small>{view.age === null ? '' : ' s ago'}</small></strong></div></div>
          <div className="quaternion" aria-label="Raw quaternion">{(['x', 'y', 'z', 'w'] as const).map(axis => <div key={axis}><span>{axis}</span><code>{view.sample ? view.sample.quaternion[axis].toFixed(4) : '—'}</code></div>)}</div>
          <dl><div><dt>Sequence</dt><dd>{view.sample?.sequence ?? '—'}</dd></div><div><dt>Host timestamp</dt><dd>{view.sample ? new Date(view.sample.timestamp).toISOString().slice(11, 23) + ' UTC' : '—'}</dd></div><div><dt>Rejected messages</dt><dd>{view.invalid}</dd></div></dl>
          <p className="hint">Raw x, y, z, w. Display rotation is normalized locally. Arrival rate is not the sensor’s hardware sample rate.</p>
        </section>
        <section className="panel">
          <div className="section-heading"><span>03</span><h2>Inspect the orientation</h2></div>
          <Leva fill flat titleBar={false} hideCopyButton />
          <div className="actions"><button className="secondary" disabled={!live} onClick={() => {
            if (stream.latest.current) setReference(normalized(stream.latest.current.quaternion))
          }}>Set current pose as forward</button><button className="text-button" disabled={!reference} onClick={() => setReference(null)}>Clear reference</button><button className="text-button" onClick={() => setCameraKey(key => key + 1)}>Reset camera</button></div>
          <p className="hint">{reference ? 'Relative to your saved pose.' : 'Showing raw sensor orientation.'} Forward axis is provisional; validate it with real head movements. Smoothing and reference changes affect only this viewer.</p>
        </section>
      </aside>
    </div>
    <footer><span>Quaternion → direction · No position or 6DoF inference</span><span>SPHERE = POSSIBLE DIRECTIONS, NOT A ROOM MAP</span></footer>
  </main>
}
