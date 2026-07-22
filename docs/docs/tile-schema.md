---
id: tile-schema
title: Tile Schema
sidebar_position: 6
---

# Tile Schema

The baked vector tiles use a fixed set of layers and fields. The web viewer
depends on this schema, so the names are a contract. Do not rename a layer or a
field without updating the frontend to match.

Every tile uses an extent of 4096 and a buffer of 64. The PMTiles metadata lists
all seven layer ids under `vector_layers`.

## Zoom levels and navigational bands

A nautical chart is not one map at one scale. NOAA compiles each ENC cell for a
**navigational purpose** — from a wide overview to a close-in berthing plan — and
the right cell to show depends on how far you are zoomed in. chartplotter mirrors
that scheme instead of baking every cell at every zoom.

### Each cell gets a band

Every cell carries a compilation scale (its `CSCL`, a `1:N` denominator). The
baker maps that scale to one of six bands:

| Band | Compilation scale (1:N) |
| --- | --- |
| Overview | coarser than 1:2,300,000 |
| General | 1:500,000 – 1:2,300,000 |
| Coastal | 1:130,000 – 1:500,000 |
| Approach | 1:32,000 – 1:130,000 |
| Harbor | 1:8,000 – 1:32,000 |
| Berthing | 1:8,000 and finer |

### Each band bakes over its own zoom range

A band bakes only the Web-Mercator zoom levels that match its scale. Coarse bands
fill the low zooms; fine bands fill the high zooms. Adjacent bands overlap by one
zoom so there is no gap at the handoff.

| Band | Baked zoom range |
| --- | --- |
| Overview | 0 – 8 |
| General | 8 – 10 |
| Coastal | 10 – 12 |
| Approach | 12 – 14 |
| Harbor | 14 – 16 |
| Berthing | 16 – 18 |

This keeps the bake small. A large-scale harbor cell covers a tiny area, so
baking it down to zoom 0 would do nothing useful; a coarse overview cell baked up
to zoom 18 would emit millions of redundant tiles. Each cell only produces tiles
where its scale is the right one to read.

### The client overzooms above a band's max

Vector tiles scale crisply, so the viewer **overzooms** the top baked level rather
than baking more. Zoom past a band's max and MapLibre stretches its highest tiles;
the chart stays sharp because the geometry is vector, not pixels. The baker may
add a level or two past a band's native max to keep the boundary against the next
finer band clean, but the rest is free on the client.

### Best-available display across bands

When you bake one archive per band (`bake --bands`), the viewer loads each band as
its own source and shows the **finest band that actually covers a point**. Where a
fine band has no data — open water off the edge of a harbor cell — the next coarser
band shows through the gap. Bands never bleed into each other.

### Within a band: SCAMIN

A single band still holds more detail than fits at its low zooms. Each feature
carries an S-52 **SCAMIN** (the scale below which it should disappear), and the
viewer honors it exactly with a per-SCAMIN layer minzoom. So as you zoom within a
band, minor features drop out at their own thresholds and the chart never clutters.

## A rule that runs through the schema

**Color is always a name, not an RGB value.** Fields like `color_token` and
`halo_color_token` hold S-101 color names. The browser looks them up in
`colortables.json` to get the right Day, Dusk, or Night color. This is what lets
the viewer restyle the chart without re-baking.

## The seven layers

### areas

Filled polygons, such as depth areas and land.

| Field | Type | Meaning |
| --- | --- | --- |
| `color_token` | string | Fill color name. |
| `class` | string | S-57 object class. |
| `display_priority` | int | S-52 display priority (S-101 DrawingPriority, 0..30). |
| `display_plane` | int | S-101 DisplayPlane (1 = OverRadar); omitted when 0. Outranks `display_priority` in paint order. |
| `display_category` | — | Display category (0 base, 1 standard, 2 other). |
| `bnd` | — | Boundary-pass marker. |

### area_patterns

Polygons filled with a repeating pattern instead of a flat color.

| Field | Type | Meaning |
| --- | --- | --- |
| `pattern_name` | string | Name of the fill pattern. |
| `class` | string | S-57 object class. |
| `display_priority` | int | S-52 display priority (S-101 DrawingPriority, 0..30). |
| `display_plane` | int | S-101 DisplayPlane (1 = OverRadar); omitted when 0. Outranks `display_priority` in paint order. |
| `display_category` | — | Display category (0 base, 1 standard, 2 other). |
| `bnd` | — | Boundary-pass marker. |

### lines

Stroked lines, such as depth contours. Sector-light legs and arcs also go here.

| Field | Type | Meaning |
| --- | --- | --- |
| `class` | string | S-57 object class. |
| `color_token` | string | Stroke color name. |
| `width_px` | int | Stroke width in pixels. |
| `dash` | — | Dash pattern. |
| `display_category` | — | Display category (0 base, 1 standard, 2 other). |
| `bnd` | — | Boundary-pass marker. |

### complex_lines

Lines drawn with a named, repeating line style.

| Field | Type | Meaning |
| --- | --- | --- |
| `class` | string | S-57 object class. |
| `linestyle_name` | string | Name of the line style. |
| `display_category` | — | Display category (0 base, 1 standard, 2 other). |
| `bnd` | — | Boundary-pass marker. |

### point_symbols

Single symbols placed at a point, such as buoys and beacons.

| Field | Type | Meaning |
| --- | --- | --- |
| `class` | string | S-57 object class. |
| `symbol_name` | string | Name of the symbol. |
| `rotation_deg` | number | Rotation in degrees. |
| `scale` | number | Scale factor. |
| `offset_x`, `offset_y` | number | Pixel offset from the point. |
| `halo_color_token` | string | Halo color name. |
| `halo_width` | number | Halo width. |
| `display_priority` | int | S-52 display priority (S-101 DrawingPriority, 0..30). |
| `display_plane` | int | S-101 DisplayPlane (1 = OverRadar); omitted when 0. Outranks `display_priority` in paint order. |
| `display_category` | — | Display category (0 base, 1 standard, 2 other). |
| `bnd` | — | Boundary-pass marker. |

When the symbol carries a depth, two more fields appear: `danger_depth` and
`sym_deep`.

### soundings

Depth soundings, drawn as digit symbols.

| Field | Type | Meaning |
| --- | --- | --- |
| `class` | string | S-57 object class. |
| `symbol_names` | string | The digit symbols that make up the sounding. |
| `scale` | number | Scale factor. |
| `display_priority` | int | S-52 display priority (S-101 DrawingPriority, 0..30). |
| `display_plane` | int | S-101 DisplayPlane (1 = OverRadar); omitted when 0. Outranks `display_priority` in paint order. |
| `display_category` | — | Display category (0 base, 1 standard, 2 other). |
| `bnd` | — | Boundary-pass marker. |

When the depth is known, three more fields appear: `depth`, `sym_s`, and
`sym_g`.

### text

Text labels.

| Field | Type | Meaning |
| --- | --- | --- |
| `class` | string | S-57 object class. |
| `text` | string | The label text. |
| `font_size_px` | number | Font size in pixels. |
| `color_token` | string | Text color name. |
| `halign`, `valign` | — | Horizontal and vertical alignment. |
| `offset_x`, `offset_y` | number | Pixel offset from the anchor. |
| `halo_color_token` | string | Halo color name. |
| `halo_width` | number | Halo width. |
| `display_priority` | int | S-52 display priority (S-101 DrawingPriority, 0..30). |
| `display_plane` | int | S-101 DisplayPlane (1 = OverRadar); omitted when 0. Outranks `display_priority` in paint order. |
| `display_category` | — | Display category (0 base, 1 standard, 2 other). |
| `bnd` | — | Boundary-pass marker. |
