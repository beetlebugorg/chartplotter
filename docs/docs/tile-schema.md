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

## A rule that runs through the schema

**Color is always a name, not an RGB value.** Fields like `color_token` and
`halo_color_token` hold S-52 color names. The browser looks them up in
`colortables.json` to get the right Day, Dusk, or Night color. This is what lets
the viewer restyle the chart without re-baking.

## The seven layers

### areas

Filled polygons, such as depth areas and land.

| Field | Type | Meaning |
| --- | --- | --- |
| `color_token` | string | Fill color name. |
| `class` | string | S-57 object class. |
| `draw_prio` | int | Draw priority. |
| `cat` | — | Category. |
| `bnd` | — | Boundary-pass marker. |

### area_patterns

Polygons filled with a repeating pattern instead of a flat color.

| Field | Type | Meaning |
| --- | --- | --- |
| `pattern_name` | string | Name of the fill pattern. |
| `class` | string | S-57 object class. |
| `draw_prio` | int | Draw priority. |
| `cat` | — | Category. |
| `bnd` | — | Boundary-pass marker. |

### lines

Stroked lines, such as depth contours. Sector-light legs and arcs also go here.

| Field | Type | Meaning |
| --- | --- | --- |
| `class` | string | S-57 object class. |
| `color_token` | string | Stroke color name. |
| `width_px` | int | Stroke width in pixels. |
| `dash` | — | Dash pattern. |
| `cat` | — | Category. |
| `bnd` | — | Boundary-pass marker. |

### complex_lines

Lines drawn with a named, repeating line style.

| Field | Type | Meaning |
| --- | --- | --- |
| `class` | string | S-57 object class. |
| `linestyle_name` | string | Name of the line style. |
| `cat` | — | Category. |
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
| `draw_prio` | int | Draw priority. |
| `cat` | — | Category. |
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
| `draw_prio` | int | Draw priority. |
| `cat` | — | Category. |
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
| `draw_prio` | int | Draw priority. |
| `cat` | — | Category. |
| `bnd` | — | Boundary-pass marker. |
