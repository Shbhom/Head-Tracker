# Buds spatial viewer

A separate diagnostic viewer built with React, TypeScript, Vite, React Three
Fiber, Drei, and Leva. It consumes the existing WebSocket API; it does not open
Bluetooth, change earbud settings, or modify backend orientation data.

## Run locally

Requires Node 20.19+ (or a current Node LTS) and npm.

From the repository root, start the backend:

```sh
CGO_ENABLED=1 go run ./cmd/spatial -origins http://127.0.0.1:3000
```

In a second terminal:

```sh
cd visualizer
npm ci
npm run dev
```

Open **http://127.0.0.1:3000**, then select **Connect stream**. The default
endpoint is `ws://127.0.0.1:8080/ws/orientation`. Enter the token if the backend
uses `BUDS_API_TOKEN`. Connection is manual; there is no simulated sensor data.

If port 3000 is busy, leave that process alone and use another port:

```sh
npm run dev -- --port 3001
```

Allow the corresponding frontend origin on the backend with
`-origins http://127.0.0.1:3001`. Using `localhost` instead requires allowing
`http://localhost:3001`; browser origins include the host and port.

## Reading the scene

- The sphere is a **unit sphere of possible pointing directions**, not a room
  map or measured position.
- The cyan arrow runs from the origin to `rotate(forwardBasis, quaternion)`.
- The amber perpendicular marker reveals roll, which a single arrow cannot.
- The sphere, world axes, and grid stay fixed. Drag/zoom changes only the camera.
- The default forward basis is **+Z, provisional**. Select another axis in the
  Leva panel to investigate the earbuds' physical convention; no axis mapping
  has yet been verified against real yaw/pitch/roll movements.
- Quaternions are normalized **only for rendering**. The readout retains the raw
  values from the API in `x,y,z,w` order.
- **Set current pose as forward** applies `current * inverse(reference)` locally.
  Clear the reference to return to raw orientation. Reconnecting clears it too.
- Visual smoothing uses frame-rate-independent quaternion SLERP. It is off by
  default so the viewer does not hide acquisition behavior.
- No arrow is displayed until valid data arrives. After 2.5 seconds without a
  fresh message, the arrow is hidden and marked stale; the last raw readout is
  retained for inspection. Socket loss clears the sample immediately.
- Staleness is based on local message arrival, not synchronized device clocks.
  An initial server snapshot may already be old; inspect its host timestamp.
- Arrival rate is a rolling count of quaternion messages received over one
  second, not an estimate of the hardware sensor sample rate. Text refreshes
  at 10 Hz; rendering uses the latest sample on each animation frame.
- Automatic reconnect uses 1–15 second backoff. Change its setting before
  connecting; manual disconnect always cancels retries. Invalid messages are
  counted and ignored instead of crashing the scene.

## WSS / another machine

Enter `wss://<backend-host>:8443/ws/orientation` and the token. Allow the viewer's
actual origin on the backend. The browser must trust the backend certificate,
and its SAN must match the hostname/IP. The error event cannot distinguish
certificate, origin, and authentication failures; check backend/browser logs.

An HTTPS-served viewer must use WSS (browsers block mixed-content WS). The viewer
also refuses to send a token over plaintext WS outside localhost. Tokens are
supplied through the API's authentication subprotocol and kept only in memory:
never in URLs, localStorage, build-time environment variables, or logs. Disconnect
and clear the token input when you are finished on a shared machine.

For a built deployment, serve `dist/` through your HTTPS server. The development
server binds only loopback by default; changing its host/port also changes which
backend origin must be allowed. See [../docs/API.md](../docs/API.md) for TLS setup.

## Verify

```sh
npm test
npm run build
npx playwright install chromium
npm run test:e2e
```

Browser tests run a temporary Vite server on port 3173 and mock the WebSocket,
so they do not need Bluetooth or disturb the live backend. They cover scene
startup, quaternion readouts, recenter controls, stale data, reconnection,
manual disconnect, and a narrow-screen layout. Math tests cover identity,
90-degree rotation, basis selection, normalization, reference rotation, API
validation, and secure connection options.

The production bundle includes Three.js/WebGL and is intentionally heavier than
a plain status page. This is a local diagnostic tool, without routing, accounts,
recording, or product-dashboard features. Real head-axis calibration remains a
manual hardware check; a successful browser test is not proof of correct axes.
