# Galaxy Buds2 Pro head tracking

Experimental Go/macOS backend for Galaxy Buds2 Pro **orientation**, not position
or 6DoF tracking. Live quaternion acquisition has been verified in `session.jsonl`.
WebSocket streaming supports local WS and TLS-protected WSS. Optional UDP export
connects the same sensor stream to MacOpentrack and X-Plane 12.

**Status:** end-to-end cockpit tracking has worked on this Mac, but physical
axis/sign calibration is still experimental. Pitch reversal after recentering
has been reported and is not yet resolved. The mapping examples below are
starting points, not a verified convention for every session/device.

## How the components work

```text
Galaxy Buds2 Pro
  │ Bluetooth SPP / RFCOMM
  ▼
buds-spatial (cmd/spatial)
  ├─ stdout: decoded JSON events
  ├─ WebSocket/API: raw quaternions → browser 3D viewer
  └─ optional UDP exporter: rotation angles → MacOpentrack
                                               │ opentrackclient 1.0
                                               ▼
                                      X-Plane plugin → cockpit POV
```

| Component | Responsibility |
| --- | --- |
| `pkg/bluetooth/osx` | Discover Buds, resolve their SPP service, and manage RFCOMM through native macOS IOBluetooth. |
| `pkg/protocol` | Parse frames and decode signed-int16 quaternion components, scaled by 10000 in `x,y,z,w` order. |
| `pkg/spatial` | Enable/attach the sensor, send keep-alives every two seconds, deliver events, and clean up on exit. |
| `cmd/spatial` | Run the session and distribute events to JSON, API, and optional UDP outputs. |
| `pkg/api` | Broadcast raw orientation over WS/WSS and expose HTTP status/latest-orientation endpoints. |
| `pkg/opentrack` | Normalize quaternions, optionally subtract a neutral orientation, convert to Euler angles, map/invert axes, and send UDP. |
| `visualizer/` | Render the raw WebSocket stream; its display centering/smoothing is local to the browser. |
| MacOpentrack | Receive UDP, apply its own centering, filtering and response curves, then send game output. |
| X-Plane plugin | Receive MacOpentrack output and drive the pilot's cockpit view. |

Only **one exporter/backend process** should open the Buds. The browser and
MacOpentrack can consume its outputs simultaneously; neither needs another
Bluetooth connection. MacOpentrack reads UDP, **not** the WebSocket endpoint.

## Requirements

- macOS with Apple's Command Line Tools (`xcode-select --install`).
- Go matching `go.mod` (currently 1.27.3), with cgo enabled.
- Galaxy Buds2 Pro paired and connected in macOS Bluetooth settings.
- For the browser viewer: Node 20.19+ (or current Node LTS) and npm.
- For flight simulation: MacOpentrack and its matching X-Plane plugin. Local
  installation/build details are in [docs/OPENTRACK.md](docs/OPENTRACK.md).

## Run

Wear both earbuds, connect them in macOS Bluetooth settings, then start head
tracking. Discovery and connection happen automatically; there is no separate
Bluetooth-test command:

```sh
CGO_ENABLED=1 go run ./cmd/spatial
```

The command discovers connected Buds, resolves their proprietary SPP service to
an RFCOMM channel, enables spatial mode, attaches the sensor, and sends a
keep-alive every two seconds. It writes newline-delimited JSON quaternion events
to stdout and diagnostics/control events to stderr. Ctrl-C sends detach/disable
and closes RFCOMM. Raw signed values are scaled by 10000, ordered `x,y,z,w`;
the JSON and WebSocket outputs are not normalized, recentered, smoothed, or
mapped to application axes. Those transformations are separate in the UDP path.
The command also serves `ws://127.0.0.1:8080/ws/orientation` by default. Use
`-listen ""` to disable the API. For WSS/LAN access, see [docs/API.md](docs/API.md).

Useful options:

```sh
CGO_ENABLED=1 go run ./cmd/spatial -duration 30s
CGO_ENABLED=1 go run ./cmd/spatial -address AA:BB:CC:DD:EE:FF -idle-timeout 30s
```

The CLI has no raw-traffic capture option. To save the existing decoded JSON
output instead, redirect stdout to a file of your choice (shell `>` overwrites
an existing file). Logs remain on stderr.
Without `-address`, discovery requires an already connected, recognizably named
device. Explicit addresses bypass name discovery but must refer to Buds2 Pro.

Build and test:

```sh
CGO_ENABLED=1 go build -o buds-spatial ./cmd/spatial
CGO_ENABLED=1 go test -race ./...
go vet ./...
```

## MacOpentrack / X-Plane 12

### 1. Build the exporter

```sh
CGO_ENABLED=1 go build -o buds-spatial ./cmd/spatial
```

Rebuild after Go source changes, then restart the running exporter to use the
new binary. Replacing the executable does not update an existing process.

### 2. Configure MacOpentrack

Open `/Applications/MacOpentrack.app` on this Mac and create a Buds profile:

| Setting | Value |
| --- | --- |
| Input | **UDP over network** |
| Input port | **4242** |
| Output | **opentrackclient 1.0** for the installed Matatata build/plugin |
| Filter | **Accela** |
| Positional X/Y/Z | Disabled; the exporter always sends zero translation |

To find the port, click the **small hammer button immediately beside the Input
dropdown**, not the main Options button or the Output hammer. The **UDP tracker
settings** window has **Port** at the top. Stop tracking first if necessary.

Keep MacOpentrack's source-axis selectors/inversions at defaults initially when
mapping in the exporter. Otherwise a swap or inversion may be applied twice.
Click **Start** to receive data. Save/retain your profile settings for later use.

### 3. Start UDP export

Wear both Buds and fully quit competing Buds-management applications. For the
current manually tested yaw/roll swap, with pitch inversion:

```sh
./buds-spatial -opentrack 127.0.0.1:4242 \
  -opentrack-center -opentrack-map roll,-pitch,yaw > /dev/null
```

`127.0.0.1` keeps packets on this Mac. Port `4242` must match MacOpentrack's
input port. `> /dev/null` hides JSON output only; diagnostics remain visible and
the terminal still accepts Enter. Do **not** use `< /dev/null` or background the
process if you want interactive keyboard recentering.

For unmapped, absolute orientation instead, omit both calibration/mapping flags:

```sh
./buds-spatial -opentrack 127.0.0.1:4242
```

The UDP stream consists of 48-byte packets containing six little-endian float64
values: `x, y, z, yaw, pitch, roll`. Translation is zero (cm); rotation is degrees.
The original conversion uses ZYX Euler angles: sensor Z → raw yaw, Y → raw pitch,
X → raw roll. The optional map selects these raw Euler sources for output axes.

UDP is **best-effort**. MacOpentrack need not be running when the exporter starts.
Stopping/restarting it does not stop Bluetooth, JSON or WebSocket streaming.
Packets sent without a listener are discarded, not buffered; delivery is not
acknowledged. Runtime UDP errors are logged at most once every five seconds and
do not stop tracking. Invalid destination/socket setup still fails at startup.
Bluetooth errors, sensor idle timeout, Ctrl-C, and other fatal backend errors can
still stop the process; automatic Bluetooth reconnection is not implemented.

### 4. Calibrate the center

There are **three independent references**:

| Control | What it changes |
| --- | --- |
| Exporter `-opentrack-center` / terminal Enter | Neutral quaternion for UDP output only. |
| MacOpentrack Center / startup centering | Its transformation from raw tracker input to Game data. |
| Browser **Set current pose as forward** | Browser rendering only; never UDP or the game. |

Recommended order:

1. Start streaming with `-opentrack-center`. Its first valid sample becomes an
   initial neutral pose, but it may arrive before you are comfortably settled.
2. Sit upright, face the screen normally, and **press Enter in the exporter's
   terminal**. The next valid quaternion becomes the new neutral pose.
3. Hold still. MacOpentrack's **Raw tracker data** yaw/pitch/roll should be near
   zero. Small sensor noise/drift is expected; translation remains zero.
4. Clear MacOpentrack's previous offset with its **Center** action, or click
   **Stop → Start** while still facing forward if **Center at startup** is enabled
   (the default in our local setup).
5. Wait briefly for the filter to settle. **Game data** rotations should now also
   be near zero. These values, not raw tracker data, are sent to X-Plane.

Repeat steps 2–5 after repositioning the earbuds or when the neutral view drifts.
Assign MacOpentrack's Center shortcut in Options if desired; verify it works with
X-Plane focused. Do not keep resetting only the exporter while leaving an old
MacOpentrack center offset active.

**What calibration does not do:** Enter captures a pose; it does not automatically
detect physical axes, fix their signs, calibrate gyro bias, or remove drift. The
exporter computes `inverse(neutral) * current` before Euler conversion. Its map
and signs stay unchanged when recentering. The current terminal message confirms
only that a recenter was **requested**, not that a fresh sample was received or
that MacOpentrack/X-Plane applied it. Verify the displayed values.

### 5. Enable X-Plane tracking

The plugin on this Mac is installed at:

```text
~/X-Plane 12/Resources/plugins/opentrack/mac_x64/opentrack.xpl
```

1. Keep the exporter and MacOpentrack tracking running.
2. Restart X-Plane if the plugin was installed while it was open.
3. Load an aircraft and enter the **3D cockpit** (default shortcut **Shift+9**).
4. Enable **Plugins → Opentrack → Head tracking**.
5. Leave **Input translation** off. If using X-Camera, enable the appropriate
   head-tracking support for the active camera too.

No additional UDP port is needed between MacOpentrack and this plugin: they use
local shared memory through opentrackclient. Use a matching app/plugin version,
not an older Wine/X-Plane shared-memory plugin. If the menu is missing, inspect
`~/X-Plane 12/Log.txt` for a plugin-loading error.

### Axis mapping and direction checks

`-opentrack-map` lists sources for **output yaw, pitch, roll**, in that order.
Use each of `yaw`, `pitch`, `roll` exactly once; prefix a source with `-` to invert
it. Restart the exporter to change this option.

| Example | Effect |
| --- | --- |
| `yaw,pitch,roll` | Default: no swap/inversion. |
| `roll,pitch,yaw` | Swap yaw and roll, for left/right movement appearing as sideways tilt. |
| `roll,-pitch,yaw` | Same swap, with up/down inverted. |
| `-roll,-pitch,yaw` | Also invert left/right after the swap. |

If the value begins with a minus sign, use an equals sign, for example:
`-opentrack-map=-roll,-pitch,yaw`.

After centering, test small movements independently:

| Physical movement | Expected dominant output |
| --- | --- |
| Turn left/right ("no") | Yaw: look left/right, not tilt. |
| Look up/down ("yes") | Pitch: look up/down. |
| Tilt ear toward shoulder | Roll: tilt the horizon/view sideways. |

Check raw input, then Game data, then the cockpit. Fix axes/directions before
tuning gain or Accela smoothing. Neutral calibration and axis mapping are
different operations; changing one does not establish the correctness of the
other. Euler pitch has a singularity at ±90°, so begin with modest head angles.

**Known issue: pitch reverses after recalibration.** This has been reported even
with `roll,-pitch,yaw`; the cause is not confirmed. Do not treat another sign flip
as a proven fix. Recenter in the exporter, test a small upward movement **before**
MacOpentrack centering, then center MacOpentrack and repeat. Record both Raw Pitch
and Game Pitch and specify which control was used (terminal Enter, Center, or
Stop/Start). Raw-direction changes point toward sensor/conversion/calibration;
Game-only changes point toward MacOpentrack transformations. If Game data is
correct but the cockpit is not, investigate the plugin, X-Camera, and other
camera/head-tracking integrations.

## Browser 3D visualizer

The separate [React Three Fiber + Drei viewer](visualizer/README.md) shows a unit
sphere, direction arrow, roll marker, and live quaternion diagnostics.

To use it alongside UDP export, add its allowed origin to the **same** backend:

```sh
./buds-spatial -origins http://127.0.0.1:3000 \
  -opentrack 127.0.0.1:4242 \
  -opentrack-center -opentrack-map roll,-pitch,yaw > /dev/null
```

In a second terminal:

```sh
cd visualizer
npm ci
npm run dev
```

Open **http://127.0.0.1:3000**, then select **Connect stream** with endpoint
`ws://127.0.0.1:8080/ws/orientation`. Do not launch a second Bluetooth backend.
For browser-only use, omit the `-opentrack*` flags.

The browser receives **raw quaternions**, not the UDP-mapped Euler angles. Its
forward reference uses `current * inverse(reference)` locally, whereas the UDP
exporter's neutral transform uses `inverse(reference) * current`. These are
different frames, so a centered browser scene does **not** prove that the game
is centered or that its axes are correct. The default browser forward axis is
also provisional. See [docs/API.md](docs/API.md) for authentication, HTTP
endpoints, WSS, and LAN access.

## Troubleshooting

- **No MacOpentrack raw motion:** select UDP **Input**, match port 4242, click
  Start, and check the exporter's diagnostics. **NO VIDEO** is normal for UDP
  input; a camera preview is not expected.
- **Raw input moves but Game data does not:** check MacOpentrack's source axes,
  disabled axes, centering, and response curves.
- **Raw data centers but Game data does not:** clear the older MacOpentrack
  center using the sequence above. Browser centering cannot fix game output.
- **Game data moves but X-Plane does not:** verify opentrackclient output, plugin
  loading, Head tracking enabled, 3D cockpit view, and any X-Camera settings.
- **Enter appears ineffective:** use `-opentrack-center`, keep stdin open, focus
  the exporter terminal, and verify fresh sensor data is arriving. A request log
  alone does not prove completion. Browser/API quaternions deliberately remain raw.
- **Only rotation works:** expected. Positional leaning needs another sensor or
  tracking source; the Buds do not supply measured X/Y/Z position.

- **Not connected:** connect the Buds in macOS first. Discovery checks both
  IOBluetooth and `system_profiler`; names must contain `Buds2 Pro` or `SM-R510`,
  otherwise supply `-address`.
- **Permission/service failure:** check System Settings → Privacy & Security →
  Bluetooth for the launching terminal/IDE. Discovery via `system_profiler`
  does **not** grant direct Bluetooth access. Permission behavior for an unsigned
  command-line binary still needs validation; app packaging/signing may be needed.
  The spatial executable embeds Bluetooth usage descriptions in its Info.plist.
- **RFCOMM busy or no quaternions:** close GalaxyBudsClient and disconnect
  Galaxy Wearable/other competing clients, wear both buds, and retry. Exact
  conflict behavior and supported firmware versions remain unverified.
  Fully quit the app (closing its window may leave it running). If channel 27
  still fails, disconnect/reconnect the Buds in macOS to clear stale RFCOMM state.
  Our first failure coincided with GalaxyBudsClient already holding that channel.
- **No sensor data:** the command fails after 15 seconds without a quaternion
  rather than pretending tracking works. Review terminal diagnostics and, if
  needed, retain decoded JSON output. Raw-traffic capture is no longer available.
- **Embedding the transport:** lock the initial OS thread in `main`'s `init`
  and call `osx.Run`. The worker must close connections before returning;
  IOBluetooth operations depend on the main event loop remaining active.

Protocol sources and limitations: [docs/PROTOCOL.md](docs/PROTOCOL.md).

The native implementation is `pkg/bluetooth/osx/native/buds_bluetooth_darwin.cc`,
compiled as Objective-C++ with `-x objective-c++`. Keeping it as a directly
recognized Go build input avoids stale cached objects after native source edits.
