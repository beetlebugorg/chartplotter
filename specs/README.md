# chartplotter-go — implementation specs & review

This directory documents how the **tile pipeline** and **web frontend** work, and
records a performance / spec-correctness review of both. It is both a reference
("how does baking work?") and a punch-list ("what should we fix, in what order?").

Reviewed against: IHO **S-52** Presentation Library Ed 4.0, IHO **S-57**, the
**Mapbox Vector Tile** spec 2.1, and Web Mercator tiling. Review date: 2026-06-17
(commit `59f34b2`).

## Documents

| File | What it covers |
|------|----------------|
| [tile-pipeline.md](tile-pipeline.md) | Backend baker: S-57 → primitives → MVT tiles. `internal/engine/{bake,tile,mvt,portrayal/primitive}`. Architecture, MVT conformance, best-available suppression, performance. |
| [portrayal.md](portrayal.md) | S-52 portrayal engine: LUPT lookup, conditional symbology (LIGHTS06, SOUNDG03, …), colour tables, the build.go walker. `internal/engine/portrayal` + `pkg/s52`. |
| [frontend.md](frontend.md) | MapLibre GL frontend: per-band sources, layer expansion, day/dusk/night, client-side symbology, the inspector. `web/*.mjs`. |
| [review-findings.md](review-findings.md) | Consolidated, prioritized findings across all three subsystems — the actionable list. |

## How the pieces fit

```
S-57 cell (.000)
  │  pkg/s57.Parse
  ▼
Chart (features: class + geometry + attributes)
  │  internal/engine/portrayal  ── pkg/s52 (LUPT lookup + conditional symbology)
  ▼
Primitive IR (points/lines/areas/text, lat-lon, viewport-independent)
  │  internal/engine/bake  (project → suppress → clip → quantize)
  ▼
MVT tiles  (internal/engine/mvt encode)  →  PMTiles archive (per band / merged)
  │  HTTP range / blob
  ▼
web/pmtiles-source.mjs  →  MapLibre GL  (web/chartplotter.mjs style, per-band sources)
  ▼
Canvas  (day/dusk/night, client-side mariner symbology applied as GL expressions)
```

A key architectural choice: **colour and most mariner-selectable symbology are NOT
baked.** Tiles carry semantic attributes (depth, class, tokens, `rotation_deg`,
`scale`, …) and the GL style turns them into pixels, so changing scheme / safety
contour / units is an instant restyle with no re-bake.
