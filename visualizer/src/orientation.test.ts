import { describe, expect, it } from 'vitest'
import { Quaternion, Vector3 } from 'three'
import { direction, displayRotation, normalized, parseMessage, socketOptions } from './orientation'

const sample = { type: 'orientation', timestamp: '2026-11-01T07:04:47Z', sequence: 42, quaternion: { x: 0, y: 0, z: 0, w: 1 } }

describe('API messages', () => {
  it('accepts the API format and preserves the raw quaternion', () => {
    const event = { ...sample, quaternion: { x: 0.7324, y: -0.4373, z: 0.5217, w: 0.0001 } }
    const parsed = parseMessage(JSON.stringify(event))
    expect(parsed?.type).toBe('orientation')
    if (parsed?.type === 'orientation') {
      expect(parsed.sample.quaternion).toEqual(event.quaternion)
      expect(normalized(parsed.sample.quaternion).length()).toBeCloseTo(1)
    }
  })
  it('rejects malformed, zero, and incomplete samples without throwing', () => {
    for (const value of [null, [], {}, { ...sample, quaternion: { x: 0, y: 0, z: 0, w: 0 } },
      { ...sample, sequence: -1 }, { ...sample, timestamp: 'invalid' },
      { ...sample, quaternion: { x: '1', y: 0, z: 0, w: 1 } }, { ...sample, quaternion: {} }]) {
      expect(parseMessage(JSON.stringify(value))).toBeNull()
    }
    expect(parseMessage('invalid JSON')).toBeNull()
  })
  it('handles status events separately', () => {
    expect(parseMessage('{"type":"status","status":"connected"}')).toEqual({ type: 'status', status: 'connected', message: '' })
  })
})

describe('quaternion → direction', () => {
  it('uses +Z as provisional forward with identity orientation', () => {
    expect(direction(new Quaternion(), '+Z').toArray()).toEqual([0, 0, 1])
  })
  it('rotates +Z toward +X for a 90 degree rotation about +Y', () => {
    const q = new Quaternion().setFromAxisAngle(new Vector3(0, 1, 0), Math.PI / 2)
    const vector = direction(q, '+Z')
    expect(vector.x).toBeCloseTo(1)
    expect(vector.y).toBeCloseTo(0)
    expect(vector.z).toBeCloseTo(0)
    expect(direction(q, '+Z').toArray()).toEqual(vector.toArray())
  })
  it('reverses the negative forward basis', () => {
    expect(direction(new Quaternion(), '-X').toArray()).toEqual([-1, 0, 0])
  })
  it('recenters identical poses without changing raw values or the reference', () => {
    const raw = { x: 0.5, y: 0.5, z: 0.5, w: 0.5 }
    const reference = normalized(raw)
    expect(displayRotation(raw, reference).angleTo(new Quaternion())).toBeCloseTo(0)
    expect(reference.toArray()).toEqual([0.5, 0.5, 0.5, 0.5])
    expect(raw).toEqual({ x: 0.5, y: 0.5, z: 0.5, w: 0.5 })
  })
  it('normalizes large finite components without overflow', () => {
    expect(normalized({ x: 1e200, y: 0, z: 0, w: 1e200 }).length()).toBeCloseTo(1)
  })
})

describe('connection options', () => {
  const token = 'a'.repeat(64)
  it('matches the backend browser authentication subprotocol', () => {
    expect(socketOptions('wss://viewer.example/ws/orientation', token, true).protocols).toEqual(['buds.v1', `buds.auth.${token}`])
  })
  it('allows token-free local development', () => {
    expect(socketOptions('ws://127.0.0.1:8080/ws/orientation', '', false).protocols).toEqual(['buds.v1'])
  })
  it('rejects mixed content, insecure remote tokens, and URL credentials', () => {
    for (const [url, key, secure] of [
      ['ws://localhost:8080', '', true], ['ws://192.168.1.2:8080', token, false],
      ['wss://example.com/?token=secret', '', false], ['wss://user:secret@example.com', '', false],
      ['https://example.com', '', false], ['wss://example.com', 'invalid', false],
    ] as const) expect(() => socketOptions(url, key, secure)).toThrow()
  })
})
