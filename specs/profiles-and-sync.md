# Profiles & server-side shared state (DRAFT proposal)

Status: **proposal for review** — not yet implemented. Captures the direction
from the "profiles + everything server-side + two clients in sync" discussion.

## Vision

**Deployment model:** local-first, single-device. The native Go server runs on
the boat's Raspberry Pi (or a laptop), bakes + serves tiles, and holds all app
state. Client screens (phones/tablets at the helm + nav station) connect over the
boat's LAN. A future iPad app is the same thing packaged self-contained. There is
always a local server — this is not a cloud/CDN service.

Treat the app as a **single shared instrument**: all meaningful state lives on
the server, and any number of clients render from it and stay in sync. The helm
and nav-station screens (or two people aboard) see the same profiles, the same
active selection, and each other's changes live.

Built **single-tenant** now (one boat = one shared state), but every API is
shaped so a later `userId`/`boatId` layer can scope it for multi-user without
reshaping the endpoints.

**Rendering:** one client rendering model (per-band overzooming sources), fed by
either the local server (`/tiles/<set-band>`) or bundled PMTiles (`pmtiles://`,
for the offline/iPad case). The PMTiles path is a tile *source*, not a second
rendering path — see specs note + the coverage-hole fix below.

## Profile

A named bundle that captures "how I sail here":

- **charts** — which packs/sets belong to the profile, and enabled/disabled
  within it (e.g. *Chesapeake* = NOAA D5 + a couple of user imports).
- **config** — the S-52 mariner/display settings (depths, units, detail level,
  the per-feature toggles), basemap, and default colour scheme.
- **view** — home centre/zoom to jump to when the profile is activated.
- **meta** — name, kind ("coastal cruising", "offshore racing", …), timestamps.

Examples: *Chesapeake cruising* (harbor detail, shallow contour 2 m, OSM
basemap) vs *Offshore delivery* (coarser bands, deep contours, night scheme).

## Server state model (extends `prefs.go`)

Today `prefs.json` holds only `{disabled: {set:bool}}`. Generalise to a single
persisted store + an in-memory broadcast hub:

```
state.json {
  activeProfile: "<id>",
  profiles: {
    "<id>": { name, kind, config{…s52…}, view{center,zoom}, basemap, scheme,
              sets: { "<set>": enabled } }
  }
}
```

The **download queue** is server-side runtime state (persisted enough to resume):
`queue: [{key, profile, kind, …}]` + the one active job (the existing
`importJobs`).

## API (REST for mutations, SSE for sync)

- `GET  /api/state` → full snapshot `{activeProfile, profiles[], queue[], packs[]}`.
- `GET  /api/events` (SSE) → server pushes on **any** state change (profile
  switched, config edited, queue/job progress, pack installed/removed). This is
  the sync backbone — every client subscribes; one client's change reaches the
  rest. (We already use SSE for bake job progress — same machinery.)
- Profiles: `POST /api/profiles`, `PATCH /api/profiles/{id}`,
  `DELETE /api/profiles/{id}`, `POST /api/profiles/{id}/activate`.
- Config: `PATCH /api/profiles/{id}/config` (a mariner/display/basemap/scheme
  patch) — broadcast so other clients restyle live.
- Queue: `POST /api/queue` (enqueue a pack for the active profile),
  `DELETE /api/queue/{key}` (cancel/dequeue). Queue + progress arrive via
  `/api/state` + `/api/events`.

## Server-side download queue

Move `_dlQueue`/`_activeDownloadKey` out of the browser into the server:

- Client `POST /api/queue` with a pack ref. Server appends, runs one at a time
  (reusing `importJobs`), and broadcasts queue + progress on `/api/events`.
- Survives client reload; both clients see the same Download / Downloading… /
  Queued states. The notification pill + per-pack buttons render from the
  streamed queue state instead of local fields.
- A finished download adds the pack to the **active profile's** `sets`.

## Client

- On boot: `GET /api/state`, then subscribe `/api/events`. Render everything —
  active profile, its charts, its config, queue/button states — from server
  state. Drop `localStorage` for anything shared (keep only truly client-local
  view bits like "which dev panel is open").
- Switch profile → `POST …/activate`; the map swaps to that profile's sets,
  applies its config + view. Every client follows.
- Edit a setting → `PATCH …/config`; broadcast; other clients restyle live.

## Multi-user (future, not now)

Add a tenant key (`userId`/`boatId` from auth) over `profiles` + `activeProfile`
and scope `/api/events` per tenant. The single-tenant store becomes a map keyed
by tenant; the REST/SSE shapes are unchanged.

## Phasing

1. **State + SSE backbone** — generalise `prefs` into the state store; add
   `GET /api/state` + `GET /api/events` broadcast; migrate pack enable/disable
   and the currently-`localStorage` config (mariner/basemap/scheme/view) into
   it; client reads/writes via API and subscribes. *Foundational.*
2. **Server-side download queue** — move the queue server-side; client renders
   queue/button state from `/api/state` + `/api/events`. *(The feature asked for.)*
3. **Profiles** — CRUD + activate; each profile owns sets + config + view; UI to
   create/switch profiles.
4. **Multi-user** — auth + per-tenant scoping. *(Future.)*

## Open / parked (track separately from this work)

- **Missing-holes rendering regression** — could not reproduce in the Annapolis
  harbor view (z15.5 renders clean); need a region/zoom where it shows.
- **Picker circle not showing** — `_pickReportAt` uses `queryRenderedFeatures`,
  so it depends on chart features actually rendering; likely the same root cause
  as the holes regression.
- **Zoom remap** ("z3.5 → z0, stretch to z18") — needs a decision on mechanism
  (clamp min-zoom vs client zoom-offset vs re-bake the tile pyramid).
