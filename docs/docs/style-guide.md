---
id: style-guide
title: UI style guide
---

# UI style guide

The rules every panel, dialog, and plugin UI in chartplotter follows. This is a
working document: when a PR and this guide disagree, fix one of them — never
ship a third convention.

The design north star is [OpenBridge](https://www.openbridge.no/) — the open
maritime UI standard: glanceable at arm's length, dark-first (a bridge at night),
finger-operable on a wet touchscreen, and calm. Chrome should recede; the chart
is the product.

## Principles

1. **The chart is the hero.** UI floats over it, takes the minimum footprint,
   and gets out of the way. Prefer collapsing/folding chrome (the weather card's
   slim scrubber) over permanently large panels.
2. **Stable geometry.** Nothing the user is about to tap may move. Dialogs and
   panels have *fixed* sizes; content changes scroll, they do not resize.
3. **One scroll container.** Per view, exactly one element scrolls. Nested
   scrollbars are a bug. (Documented exception: capped log viewers such as the
   NMEA sniffer `<pre>`, max-height 160px.)
4. **Spacing separates; borders delineate.** Prefer whitespace over boxes. Never
   nest a bordered container directly inside another bordered container with no
   job for the inner border ("double frames").
5. **Three themes, one variable set.** Day / dusk / night are all served by the
   same `--ui-*` tokens. Components never hardcode colours (status hues aside)
   and never assume light or dark.

## Design tokens

Tokens are defined once in `chartplotter.view.mjs` and inherited everywhere
(including plugin UI and shadow roots). Always write `var(--ui-x)`; a fallback
literal (`var(--ui-x, #161b22)`) is allowed only in plugin UI that may render
outside the shell.

| Token | Role |
| --- | --- |
| `--ui-bg` | App/drawer background — the deepest layer |
| `--ui-surface` | Cards, rows, inputs — one step up |
| `--ui-surface-2` | Buttons, rails — two steps up |
| `--ui-hover` | Hover fill for buttons/rows |
| `--ui-text` / `--ui-text-dim` / `--ui-text-faint` | Primary / secondary / tertiary text |
| `--ui-border` / `--ui-border-2` / `--ui-border-strong` | Standard / hairline / control borders |
| `--ui-accent` / `--ui-accent-hover` / `--ui-accent-text` | Selection, primary buttons, active states |
| `--ui-shadow` | Elevation shadows |
| `--tap-min` | Minimum touch target (44px) |

Status colours (the only sanctioned hardcoded hues, shared app-wide):
`#3fb950` live/ok · `#d29922` stale/warn · `#f85149` error/danger ·
`#58a6ff` connecting/info · `#6e7681` off/unknown.

## Scales

**Spacing** — 4px grid. Use these steps, nothing in between:
`4, 8, 12, 16, 20, 24`. Row padding 12px; card padding 12–16px; gaps 8–12px.

**Radius** — three sizes only:

| Radius | Use |
| --- | --- |
| 7–8px | Controls: buttons, inputs, selects, segmented strips, rail tabs |
| 10px | Cards: list rows, forms, editors, empty states |
| 14px | Surfaces: popovers, drawers, floating HUD cards |

**Type** — system-ui stack, sizes:

| Size | Use |
| --- | --- |
| 11px, 600–700, uppercase, letter-spacing .03–.06em | Section headers, badges, tiny labels |
| 12px | Secondary text: descriptions, meta lines, hints |
| 13px | Body/default: buttons, fields, rail tabs |
| 13.5px, 600 | Row titles |
| 14px, 600+ | Panel/dialog titles |

Numeric readouts use `font-variant-numeric: tabular-nums`. Inputs are ≥16px on
touch (iOS zooms otherwise) — this is the one sanctioned deviation from the type
scale.

## Layout rules

- **Fixed dialog geometry.** The settings shell is `height:min(62dvh,620px)`
  on desktop, `min(72dvh,620px)` stacked on mobile — identical on every tab.
  If a new pane needs more room, it scrolls; it never grows the dialog.
- **One scroll container** per view (`overflow-y:auto` +
  `overscroll-behavior:contain`). The settings *pane* scrolls; the drawer body
  around it is `overflow:hidden` while settings is open. If you add an inner
  region that must scroll (log viewer), cap its height and document it.
- **Sticky section headers** inside a scrolling pane: full-bleed, opaque, in
  the *pane's actual background* (`--ui-bg` in the drawer) so content slides
  under without seams, with a hairline `--ui-border-2` bottom edge.
- **No double frames.** The drawer/popover provides the outer frame. Inner
  structure is expressed with a single divider (`border-right` between rail and
  pane) or spacing, not another full border+radius box.
- **Touch targets** ≥ `--tap-min` (44px) under `@media (pointer:coarse)`;
  visual size may stay smaller (switch tracks use an invisible padded overlay).

## Components

**Buttons** — `--ui-surface-2` fill, `--ui-border`, radius 8, padding 6×12,
13px. `.primary` = accent fill, transparent border. `.danger` = `#f85149` text,
red-tinted hover. Icon buttons: padding 6×8, min-width 34px.

**List rows** (connections, plugins) — `--ui-surface` card, radius 10, padding
12×14, 12px gap, 10px between rows: status dot (10px circle) · info block
(13.5px/600 name + uppercase badge, 12px dim meta with ellipsis) · right-aligned
actions. Live updates *patch* badge/meta in place — never re-render a list the
user may be interacting with.

**Settings rows** — label left, control right, full-width 12px dim description
underneath (max-width 56ch), separated by `--ui-border-2` hairlines, 12px
vertical padding. No card boxes inside the pane.

**Forms/editors** — `--ui-surface` card, radius 10, padding 14×16, uppercase
12px heading; fields as label (92–110px, dim) + flexed input; actions row
right-aligned (Cancel, then primary). Inline errors in `#f85149` 12px, never
alerts.

**Badges** — 11px/500 uppercase dim text next to the name; the coloured dot
carries the state, the badge names it.

**Drill-down panes** (plugin → connections) — replace the pane content, top bar
with `← Back` on the left and a dim context label on the right. Keep the child
panel element persistent across re-renders so its state survives.

**Empty states** — dashed `--ui-border` box, radius 10, centered dim text,
padding 26px. Say what to do next, not just "nothing here".

**Floating HUD cards** (weather scrubber, pill controls) — radius 14, surface
background + border + shadow, minimum footprint, fold-out details. Anything
drawn *on the chart* (arrows, streamlines) must respect the map's rotation.

## Writing

Sentence case everywhere ("Add connection", not "Add Connection"). Descriptions
say what the thing does in the user's terms, ≤ one sentence, no jargon the
pick-report wouldn't use. Errors state what failed and what to try.

## Review checklist

- [ ] Same size before/after every interaction (no layout shift)?
- [ ] Exactly one scrollbar in the view?
- [ ] All spacing on the 4px grid; radii from {8, 10, 14}?
- [ ] Only `--ui-*` tokens (plus sanctioned status hues)?
- [ ] Legible in all three themes (day/dusk/night)?
- [ ] Touch targets ≥44px on coarse pointers; inputs ≥16px?
- [ ] Live data patches in place instead of re-rendering under the user?
