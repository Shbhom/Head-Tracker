import { Quaternion, Vector3 } from 'three'

export type RawQuaternion = { x: number; y: number; z: number; w: number }
export type Sample = {
  quaternion: RawQuaternion
  timestamp: string
  sequence: number
  receivedAt: number
}
export type Axis = '+X' | '-X' | '+Y' | '-Y' | '+Z' | '-Z'
export const axes: Axis[] = ['+X', '-X', '+Y', '-Y', '+Z', '-Z']

export function parseMessage(text: string):
  | { type: 'orientation'; sample: Sample }
  | { type: 'status'; status: string; message: string }
  | null {
  try {
    const value = JSON.parse(text)
    if (!value || typeof value !== 'object') return null
    if (value.type === 'status' && typeof value.status === 'string') {
      return { type: 'status', status: value.status, message: typeof value.message === 'string' ? value.message : '' }
    }
    if (value.type !== 'orientation' || typeof value.timestamp !== 'string' ||
      !Number.isFinite(Date.parse(value.timestamp)) || !Number.isSafeInteger(value.sequence) || value.sequence < 0) return null
    const q = value.quaternion
    if (!q || ![q.x, q.y, q.z, q.w].every(v => typeof v === 'number' && Number.isFinite(v))) return null
    const length = Math.hypot(q.x, q.y, q.z, q.w)
    if (length < 1e-6 || !Number.isFinite(length)) return null
    return { type: 'orientation', sample: { quaternion: { x: q.x, y: q.y, z: q.z, w: q.w }, timestamp: value.timestamp, sequence: value.sequence, receivedAt: performance.now() } }
  } catch { return null }
}

export function normalized(q: RawQuaternion): Quaternion {
  const length = Math.hypot(q.x, q.y, q.z, q.w)
  return new Quaternion(q.x / length, q.y / length, q.z / length, q.w / length)
}

// World-frame relative rotation, matching the backend plan. Keep the original
// sensor sample untouched; reference and display normalization are local only.
export function displayRotation(q: RawQuaternion, reference: Quaternion | null): Quaternion {
  const current = normalized(q)
  return reference ? current.multiply(reference.clone().invert()).normalize() : current
}

export function axisVector(axis: Axis): Vector3 {
  const vector = new Vector3(axis.endsWith('X') ? 1 : 0, axis.endsWith('Y') ? 1 : 0, axis.endsWith('Z') ? 1 : 0)
  return vector.multiplyScalar(axis.startsWith('-') ? -1 : 1)
}

export function direction(q: Quaternion, forward: Axis): Vector3 {
  // Always start with the same basis vector; do not accumulate rotations.
  return axisVector(forward).applyQuaternion(q).normalize()
}

export function socketOptions(endpoint: string, token: string, securePage: boolean): { url: string; protocols: string[] } {
  const url = new URL(endpoint)
  if (!['ws:', 'wss:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) {
    throw new Error('Use a ws:// or wss:// URL without credentials, query parameters, or fragments.')
  }
  if (securePage && url.protocol !== 'wss:') throw new Error('An HTTPS page requires a secure wss:// endpoint.')
  const local = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
  if (token && url.protocol !== 'wss:' && !local) throw new Error('Use wss:// before sending a token outside localhost.')
  if (token && !/^[A-Za-z0-9_-]{32,256}$/.test(token)) throw new Error('Token must be 32–256 URL-safe characters.')
  return { url: url.href, protocols: token ? ['buds.v1', `buds.auth.${token}`] : ['buds.v1'] }
}
