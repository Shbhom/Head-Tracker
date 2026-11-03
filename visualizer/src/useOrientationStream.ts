import { useCallback, useEffect, useRef, useState } from 'react'
import { parseMessage, socketOptions, type Sample } from './orientation'

type Phase = 'offline' | 'connecting' | 'connected' | 'retrying'
type View = {
  phase: Phase
  status: string
  message: string
  sample: Sample | null
  age: number | null
  invalid: number
  rate: number
}
const initial: View = { phase: 'offline', status: 'Waiting for connection', message: '', sample: null, age: null, invalid: 0, rate: 0 }

export function useOrientationStream() {
  const latest = useRef<Sample | null>(null)
  const phase = useRef<Phase>('offline')
  const status = useRef(initial.status)
  const message = useRef('')
  const invalid = useRef(0)
  const arrivals = useRef<number[]>([])
  const stop = useRef<() => void>(() => {})
  const [view, setView] = useState<View>(initial)

  const disconnect = useCallback(() => {
    stop.current()
    stop.current = () => {}
    latest.current = null
    arrivals.current = []
    invalid.current = 0
    phase.current = 'offline'
    status.current = 'Disconnected'
    message.current = ''
    setView({ ...initial, status: 'Disconnected' })
  }, [])

  const connect = useCallback((endpoint: string, token: string, autoReconnect: boolean) => {
    disconnect()
    let options: ReturnType<typeof socketOptions>
    try { options = socketOptions(endpoint.trim(), token.trim(), location.protocol === 'https:') }
    catch (error) {
      message.current = error instanceof Error ? error.message : 'Invalid endpoint'
      setView({ ...initial, message: message.current })
      return
    }
    let active = true
    let socket: WebSocket | null = null
    let retry: ReturnType<typeof setTimeout> | undefined
    let timeout: ReturnType<typeof setTimeout> | undefined
    let attempt = 0
    const clearSample = () => { latest.current = null; arrivals.current = [] }
    const open = () => {
      if (!active) return
      phase.current = attempt ? 'retrying' : 'connecting'
      status.current = 'Waiting for server'
      message.current = ''
      try { socket = new WebSocket(options.url, options.protocols) }
      catch { phase.current = 'offline'; message.current = 'Could not create the WebSocket connection.'; return }
      const current = socket
      timeout = setTimeout(() => current.close(), 10_000)
      current.onopen = () => {
        if (!active) return
        clearTimeout(timeout)
        phase.current = 'connected'
        message.current = ''
      }
      current.onmessage = event => {
        if (!active) return
        const parsed = typeof event.data === 'string' ? parseMessage(event.data) : null
        if (!parsed) { invalid.current++; return }
        attempt = 0 // Only reset backoff after receiving a valid API message.
        if (parsed.type === 'status') {
          status.current = parsed.status
          message.current = parsed.message
          if (parsed.status !== 'streaming') clearSample()
        } else {
          latest.current = parsed.sample
          status.current = 'streaming'
          message.current = ''
          arrivals.current.push(parsed.sample.receivedAt)
        }
      }
      current.onerror = () => {
        if (active) message.current = 'Connection failed. Check the backend, allowed origin, token, and WSS certificate trust.'
      }
      current.onclose = () => {
        clearTimeout(timeout)
        clearSample()
        if (!active) return
        status.current = 'Connection lost'
        if (autoReconnect) {
          const delay = Math.min(1000 * 2 ** Math.min(attempt++, 4), 15_000)
          phase.current = 'retrying'
          message.current ||= `Retrying in ${delay / 1000}s…`
          retry = setTimeout(open, delay)
        } else { phase.current = 'offline' }
      }
    }
    stop.current = () => {
      active = false
      clearTimeout(retry)
      clearTimeout(timeout)
      if (socket) { socket.onopen = socket.onmessage = socket.onerror = socket.onclose = null; socket.close() }
    }
    open()
  }, [disconnect])

  useEffect(() => {
    // The scene reads the latest ref at render frequency. The diagnostic text
    // refreshes at 10 Hz rather than rerendering React on every sensor packet.
    const timer = setInterval(() => {
      const now = performance.now()
      arrivals.current = arrivals.current.filter(time => now - time < 1000)
      setView({ phase: phase.current, status: status.current, message: message.current,
        sample: latest.current, age: latest.current ? now - latest.current.receivedAt : null,
        invalid: invalid.current, rate: arrivals.current.length })
    }, 100)
    return () => { clearInterval(timer); stop.current() }
  }, [])

  return { latest, view, connect, disconnect }
}
