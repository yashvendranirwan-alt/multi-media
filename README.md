# Multi-Window Media Sequencer with Sync Playback

Four display windows each loop their own media list continuously inside a
5-hour cycle. Playlists can be edited while everything is running, and a sync
action puts one chosen item on every window **at the same instant** — after
which each window carries on with its own sequence, exactly where it would
have been.

- **Frontend:** React 18 + Vite
- **Backend:** Go 1.22, **no third-party dependencies**
- **Storage:** JSON document with atomic writes
- **Live updates:** Server-Sent Events

---

## Quick start

**Prerequisites:** Go 1.22+ and Node 22.12+ (or just Docker for option 3).
The Go module has no third-party dependencies, so there is nothing to download
for the backend. `npm audit` reports zero vulnerabilities.

### Option 1 — one process serves everything (closest to production)

```bash
make run          # builds the React app, then serves it from the Go binary
# open http://localhost:8080
```

### Option 2 — two processes with hot reload (for development)

```bash
make dev-backend   # terminal 1 -> Go API on :8080
make dev-frontend  # terminal 2 -> Vite on :5173
# open http://localhost:5173
```

Vite proxies `/api` and `/media` to `:8080`, so the browser sees one origin and
CORS never enters the picture during development.

### Option 3 — Docker

```bash
docker compose up --build
# open http://localhost:8080
```

No database to install, no API keys, no `.env` required. Every setting has a
working default and the seed data is created on first boot.

### See the 5-hour cycle restart without waiting 5 hours

```bash
cd backend && CYCLE_DURATION=20s go run .
```

The cycle length is configuration, not a constant, so the same arithmetic that
governs the 5-hour cycle can be observed in 20 seconds. Everything else is
unchanged.

---

## How it works

### The core idea: the schedule is a pure function of time

The obvious design — the backend tells each window what to play next — drifts
apart, breaks whenever a browser reloads, and turns sync into a race between
four separate messages.

This implementation stores, per window, a **playlist** and an **anchor
timestamp**. What is on screen at any moment is derived:

```
what_is_playing(window, now) = f(window.items, window.anchorMs, window.cycleMs, now)
```

No item is ever pushed. Nothing is "in progress" on the server. A window that
reloads, connects an hour late, or opens in a fifth browser tab computes the
same answer as everybody else, because they are all evaluating one function
over one shared clock.

The assignment allows exactly this: *"The backend does not need to expose an
API that continuously serves the next media item."*

The resolver lives in `backend/internal/schedule` and is mirrored in
`frontend/src/lib/schedule.js` so the browser can render every frame locally
without a network round trip per item. The Go version is authoritative and is
exposed at `GET /api/frames` for verification; both are unit-tested against the
same cases.

### The 5-hour cycle

Each window's total play size is 5 hours. Its list repeats back to back inside
that window of time, and the cycle restarts at the boundary.

```
cycleElapsed = (now - anchorMs) mod cycleMs      // 0 .. 5h
loopLength   = sum(item durations)
position     = cycleElapsed mod loopLength       // where we are in the list
```

Worked example — a 18s list inside a (compressed) 20s cycle:

```
 t=0s   ├─ M1 ─┤├─ M2 ─┤├─ M3 ─┤          loop 0
 t=18s  ├─ M1 …                            loop 1 begins, list repeats
 t=20s  ╳ cut short by the cycle boundary
 t=20s  ├─ M1 ─┤├─ M2 ─┤ …                 cycle restarts from item 0
```

Two decisions worth stating:

- **The list repeats; the remainder is not padded with blank.** The assignment
  is explicit that blank is only a playlist item when it has been added
  deliberately, so the cycle is filled by replaying the list.
- **An item straddling the 5-hour boundary is truncated, not skipped.** The
  boundary wins so that the restart is exact and every window's cycle stays
  aligned to its anchor.

### Sync behaviour

This is the part the assignment cares about most, so here is the full sequence.

When an operator triggers sync for M2:

1. The backend computes `startAtMs = now + 750ms` (the **lead time**) and
   stores `{mediaId, startAtMs, durationMs}`.
2. The new state is pushed to every connected window over SSE.
3. Each window has already measured its offset from the server clock, so it
   converts `startAtMs` into its own local time and waits.
4. At `startAtMs`, every window switches to M2 — together.
5. At `startAtMs + durationMs`, the sync interval simply stops matching, and
   each window resolves its own playlist again.

**The lead time is what makes it simultaneous.** A message that says "show M2
now" arrives at four browsers at four slightly different moments. A message
that says "show M2 at 14:32:07.250" is acted on at one moment by all of them.
750ms is comfortably more than a typical delivery time and short enough to feel
immediate; tune it with `SYNC_LEAD`, or pass `"leadMs": 0` to start instantly.

**Nothing is lost afterwards.** Sync is an *overlay*, not a pause. The playlist
keeps advancing on wall-clock time underneath it, so when the overlay ends each
window shows whatever it would have been showing had the sync never happened.
Playlists are never rewritten by a sync — verified by a test that compares
every window's items before and after.

```
window timeline ──── M1 ──── M2 ──── M3 ──── M1 ──── M2 ────▶  (keeps running)
sync overlay                 ╠════ M2 on every window ════╣
what you see    ──── M1 ────╠════════ M2 ════════╣─ M1 ── M2 ─▶
```

### Keeping clocks honest

Every timestamp in the system is on the server's clock. A laptop whose clock is
40 seconds fast would otherwise display the wrong item and miss the sync
moment entirely.

On load, and every 60 seconds after, the frontend probes `GET /api/time` five
times, keeps the sample with the **fastest round trip** (least uncertainty) and
derives an offset the NTP way:

```
offset = serverTime - (sentAt + rtt/2)
```

It also re-measures when the tab becomes visible again, because a sleeping
laptop drifts. The measured offset and round-trip time are shown in the UI
status rail, so a reviewer can confirm the correction is active.

### Videos land in the right place too

Showing a video from the start when a window joins halfway through would break
the illusion. Each window seeks the element to its position within the slot,
re-checks periodically and corrects drift beyond tolerance. Videos are muted
and `playsInline` to satisfy browser autoplay rules; if autoplay is still
blocked, the window says so and one click resumes it.

---

## Seed data

Created automatically on first boot. Four windows, deliberately different
lengths so they visibly drift out of phase with each other — which makes the
sync moment obvious.

| Window | ID | Sequence | Loop |
|---|---|---|---|
| Lobby wall | `wn_lobby` | M1 (6s) → M2 (6s) → M3 (6s) | 18s |
| Corridor A | `wn_corridor` | M2 (5s) → M4 (5s) → V1 (12s) → Blank (4s) | 26s |
| Cafeteria | `wn_cafeteria` | M5 (7s) → M6 (7s) → M1 (7s) | 21s |
| Reception | `wn_reception` | M3 (8s) → V2 (12s) → M6 (8s) | 28s |

Media library: six images (`M1`–`M6`), two videos (`V1`, `V2`) and one `Blank`
item. Corridor A includes `Blank` to show it working as a *configured* item
rather than as default dead air.

The images are SVGs **embedded in the Go binary**, so a fresh deployment shows
real media without depending on any external image host. The two videos are
Google's public sample clips.

---

## API

Base path `/api`. All bodies are JSON. Errors return
`{"error": "..."}` with `400` (invalid), `404` (unknown id) or `500`.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/health` | Liveness, server time, connected SSE clients |
| `GET` | `/api/time` | `{serverTimeMs}` — the clock-offset probe |
| `GET` | `/api/state` | Windows, media, active sync, server time |
| `GET` | `/api/frames` | What each window is showing **now**, resolved server-side |
| `GET` | `/api/events` | SSE stream of state changes |
| `GET` | `/api/media` | List media |
| `POST` | `/api/media` | Add media to the library |
| `GET` | `/api/windows` | List windows |
| `POST` | `/api/windows` | Create a window |
| `DELETE` | `/api/windows/{windowId}` | Delete a window |
| `POST` | `/api/windows/{windowId}/items` | **Append media to a window's list** |
| `DELETE` | `/api/windows/{windowId}/items/{itemId}` | Remove an item |
| `POST` | `/api/windows/{windowId}/restart` | Move the anchor to now |
| `POST` | `/api/sync` | **Trigger sync across all windows** |
| `DELETE` | `/api/sync` | End the sync early |
| `POST` | `/api/reset` | Restore the seed data |

### Add media to a window

```http
POST /api/windows/wn_lobby/items
{ "mediaId": "md_m5", "durationMs": 9000 }     -> 201 {"window": {...}}
```

`durationMs` is optional: omit it (or send `0`) to use the media's own
duration. A negative value is rejected. The window's anchor is **not** moved,
so the addition takes effect on the next pass instead of restarting whatever is
currently on screen.

### Trigger sync

```http
POST /api/sync
{ "mediaId": "md_m2", "durationMs": 6000 }     -> 201
{
  "sync": { "id": "sy_…", "mediaId": "md_m2",
            "startAtMs": 1789672527407, "durationMs": 6000 },
  "serverTimeMs": 1789672526657
}
```

`durationMs` omitted uses `SYNC_DURATION`. `leadMs` overrides the scheduling
lead for this call; `0` starts immediately.

Note `startAtMs` is 750ms **after** `serverTimeMs` — that gap is the lead time
described above.

### Live updates

```
GET /api/events        # text/event-stream
event: state
data: {"windows":[...],"media":[...],"sync":{...},"serverTimeMs":...}
```

A `state` event is sent on connect and on every change, with heartbeats to keep
the connection open. The frontend also polls as a fallback, so a proxy that
buffers SSE degrades to slightly slower updates rather than a dead UI.

---

## Deployment

The backend compiles to a **single static binary with no CGO and no runtime
dependencies**, which makes hosting straightforward.

### Recommended: one service (Render, Fly.io, Railway, any Docker host)

`render.yaml` is included as a blueprint. The image builds the React app,
builds the Go binary, and serves both from one process:

1. Push to GitHub and create a **Docker** web service from the repo.
2. Health check path: `/api/health`.
3. Attach a **persistent disk mounted at `/data`** (see the storage note below).
4. Deploy. The root URL serves the UI; `/api/*` serves the API.

```bash
docker build -t media-sequencer .
docker run -p 8080:8080 -v sequencer-data:/data media-sequencer
```

### Alternative: split deployment

Backend on Render/Fly, frontend on Vercel/Netlify:

- Frontend build command `npm run build`, output `dist`.
- Set `VITE_API_BASE_URL=https://your-api-host` at build time (it is inlined by
  Vite, so it must be set *before* building).
- Set `CORS_ORIGIN=https://your-frontend-host` on the backend — not `*`.

### Configuration

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | Listen port; accepts `8080` or `:8080` |
| `DATA_FILE` | `./data/state.json` | Where state is persisted |
| `CYCLE_DURATION` | `5h` | Total play size per window |
| `SYNC_DURATION` | `15s` | Default sync length |
| `SYNC_LEAD` | `750ms` | How far ahead a sync is scheduled |
| `CORS_ORIGIN` | `*` | Allowed origin for a split deployment |
| `STATIC_DIR` | *(empty)* | Path to the built React app; empty = API only |

See `.env.example`. Invalid values fail fast at startup with a clear message
rather than misbehaving later.

---

## Testing

```bash
make test          # everything
make test-backend  # go vet + go test -race
make test-frontend # node --test
```

Current state: **backend passes under `-race`**, `go vet` and `gofmt` clean;
**21/21 frontend tests pass**.

The schedule resolver is the component worth testing hardest, and it is covered
in both languages: playback inside the first pass, the list repeating with no
blank filler, truncation at the cycle boundary, restart from item 0, a clock
behind the anchor, empty playlists, zero-duration items, sync overlay
intervals, the playlist continuing underneath a sync, expired syncs, and a
gap-free sweep across an entire cycle.

Beyond unit tests, the running system was verified end to end over HTTP:

- Triggering sync moved all four windows to M2 with **identical start and end
  instants**, then each returned to its own sequence with playlists unchanged.
- With `CYCLE_DURATION=20s`, sampling once a second showed the list repeating
  inside the cycle, the boundary item truncated, and `cycleElapsed` wrapping to
  0 with playback restarting from item 0.
- Adding media, creating a window and adding custom media all survived a full
  process restart.

---

## Assumptions

1. **The example media lists were not in the brief.** The PDF references `M2`
   but contains no window/media table, so representative seed data was created:
   four windows with differing loop lengths, covering image, video and blank.
2. **Sync is global**, applying to every window at once, which is what "every
   window should display M2 at the same time" describes.
3. **Sync overlays rather than pauses.** "Continue its own normal sequence
   without losing its playlist configuration" is read as the playlist remaining
   intact and correctly positioned — so the schedule runs underneath the sync.
   The alternative (shifting every window's timeline by the sync duration)
   would make windows diverge permanently after each sync and would make
   "playing within a 5-hour cycle" ill-defined.
4. **New items append and the anchor does not move**, so editing a playlist
   never interrupts what is currently on screen.
5. **One operator.** There is no authentication; last write wins. Suitable for
   an internal signage console, not for the public internet.
6. **Durations are per playlist item**, defaulting to the media's own duration.
   A video longer than its slot is cut off at the slot; a shorter one loops.

## Tradeoffs

**Storage is a JSON document with atomic writes** (write temp file → `fsync` →
`rename`), guarded by a mutex, with an in-memory copy for reads. This was
chosen because the Go module proxy was unreachable in the build environment, so
a pure-standard-library store was the only option that could be guaranteed to
compile and run anywhere — and it removes the entire class of "works locally,
missing driver in production" failures.

The honest limitation: **on an ephemeral filesystem this resets on redeploy.**
Attach a persistent disk and point `DATA_FILE` at it, as `render.yaml` does.

The data layer sits behind a narrow interface, so moving to Postgres means
implementing the same handful of methods and changing one line in `main.go`;
no handler or scheduling code would change. At this data size (a few windows,
a few dozen items) the JSON store is not a performance compromise — the
compromise is purely operational.

**Other tradeoffs:**

- The schedule resolver exists in Go and JavaScript. Duplication is a real
  cost, accepted so the browser can render locally without a request per item.
  Both are tested against the same scenarios, and `/api/frames` exposes the Go
  result to catch divergence.
- SSE rather than WebSockets: updates are one-directional, and SSE reconnects
  on its own and survives proxies more predictably. Polling is kept as a
  fallback.
- No authentication, as above.

## What I would add next

Reordering items by drag, per-window sync targeting, an operator audit log,
and `Cache-Control` tuning for uploaded media.

---

## Project structure

```
backend/
  main.go                    entrypoint, config, graceful shutdown
  assets/media/              SVG media embedded into the binary
  internal/
    models/                  domain types
    schedule/                the resolver — pure, no I/O, heavily tested
    store/                   JSON persistence, seed data, validation
    api/                     routing, handlers, middleware, SSE hub
frontend/
  src/
    lib/schedule.js          the resolver, mirrored for local rendering
    lib/api.js               single place for base URL and error handling
    hooks/useServerClock.js  NTP-style offset measurement
    hooks/useSequencerState.js  SSE subscription + polling fallback
    components/              wall, tiles, media surface, control rack
Dockerfile, docker-compose.yml, render.yaml, Makefile
```
