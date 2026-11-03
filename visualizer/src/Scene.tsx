import { Component, Suspense, useEffect, useMemo, useRef, type ComponentRef, type ReactNode, type RefObject } from 'react'
import { Canvas, useFrame, useThree } from '@react-three/fiber'
import { GizmoHelper, GizmoViewport, Grid, Line, OrbitControls, Sphere } from '@react-three/drei'
import { ArrowHelper, Group, Quaternion, Vector3 } from 'three'
import { direction, displayRotation, axisVector, type Axis, type Sample } from './orientation'

type Props = {
  latest: RefObject<Sample | null>
  reference: Quaternion | null
  forward: Axis
  smoothing: boolean
  grid: boolean
  cameraKey: number
}

function Indicator({ latest, reference, forward, smoothing }: Props) {
  const root = useRef<Group>(null)
  const shown = useRef(new Quaternion())
  const previous = useRef<Sample | null>(null)
  const previousReference = useRef(reference)
  const arrow = useMemo(() => new ArrowHelper(new Vector3(0, 1, 0), new Vector3(), 1, 0x6ee7ef, 0.16, 0.075), [])
  useEffect(() => () => arrow.dispose(), [arrow])
  const tip = useRef<Group>(null)
  const up = useMemo(() => {
    // Keep the roll marker perpendicular to the selected forward basis.
    const candidate: Axis = forward.endsWith('Y') ? '+Z' : '+Y'
    return axisVector(candidate).multiplyScalar(0.24)
  }, [forward])
  useFrame((_, delta) => {
    const sample = latest.current
    const live = !!sample && performance.now() - sample.receivedAt < 2500
    if (root.current) root.current.visible = live
    if (!live || !sample) { previous.current = null; return }
    const target = displayRotation(sample.quaternion, reference)
    if (!smoothing || !previous.current || previousReference.current !== reference) shown.current.copy(target)
    else shown.current.slerp(target, 1 - Math.exp(-12 * delta))
    previous.current = sample
    previousReference.current = reference
    const vector = direction(shown.current, forward)
    arrow.setDirection(vector)
    if (tip.current) {
      tip.current.position.copy(vector)
      tip.current.quaternion.copy(shown.current)
    }
  })
  return <group ref={root} visible={false}>
    <primitive object={arrow} />
    <group ref={tip}>
      <Sphere args={[0.035, 16, 16]}><meshBasicMaterial color="#c6fbff" /></Sphere>
      <Line points={[[0, 0, 0], up.toArray()]} color="#ffcf80" lineWidth={4} />
      <Sphere position={up} args={[0.022, 12, 12]}><meshBasicMaterial color="#ffcf80" /></Sphere>
    </group>
  </group>
}

function CameraController({ reset }: { reset: number }) {
  const camera = useThree(state => state.camera)
  const controls = useRef<ComponentRef<typeof OrbitControls>>(null)
  useEffect(() => {
    camera.position.set(2.6, 1.8, 2.6)
    controls.current?.target.set(0, 0, 0)
    controls.current?.update()
  }, [camera, reset])
  return <OrbitControls ref={controls} makeDefault enablePan={false} minDistance={1.8} maxDistance={7} />
}

class SceneBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() { return { failed: true } }
  render() {
    return this.state.failed ? <div className="canvas-error">3D rendering is unavailable. Enable WebGL/hardware acceleration and reload. The connection panel still works.</div> : this.props.children
  }
}

export function Scene(props: Props) {
  return <SceneBoundary><Canvas camera={{ position: [2.6, 1.8, 2.6], fov: 43 }} dpr={[1, 2]} fallback={<div className="canvas-error">This browser does not support WebGL.</div>}>
    <color attach="background" args={['#0c111b']} />
    <ambientLight intensity={0.8} />
    <directionalLight position={[4, 6, 2]} intensity={2} />
    <Suspense fallback={null}>
      <Sphere args={[1, 32, 20]}><meshBasicMaterial color="#57758b" wireframe transparent opacity={0.17} depthWrite={false} /></Sphere>
      <Sphere args={[0.035, 16, 16]}><meshBasicMaterial color="#7e93a9" /></Sphere>
      <axesHelper args={[1.35]} />
      <Indicator {...props} />
      {props.grid && <Grid position={[0, -1.12, 0]} args={[8, 8]} cellSize={0.5} cellColor="#1f3246" sectionColor="#334e65" sectionSize={2} fadeDistance={9} />}
      <CameraController reset={props.cameraKey} />
      <GizmoHelper alignment="bottom-right" margin={[70, 70]}><GizmoViewport axisColors={['#fa879b', '#9cddb5', '#93acff']} labelColor="#0c111b" /></GizmoHelper>
    </Suspense>
  </Canvas></SceneBoundary>
}
