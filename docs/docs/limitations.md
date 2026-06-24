---
id: limitations
title: Known Limitations
sidebar_position: 7
---

# Known Limitations

chartplotter runs the official IHO S-101 Portrayal Catalogue, so most everyday
ENC features draw correctly: depth areas and contours, soundings, coastline and
land, buoys and beacons, lights (including sectored and directional lights),
restricted/anchorage areas, and text labels.

But the portrayal is **not complete**, and this project is an AI-built experiment
and learning tool — **do not use it for navigation**. This page is an honest list
of what does *not* render fully today, taken from the engine code rather than from
a wishlist. When a feature can't be portrayed, the result is one of three things:
it falls back to the magenta *unknown object* mark, it is **silently dropped**, or
a *part* of it (an arc, a pattern) is missing.

## Features dropped on a rule error

Some S-101 line and area rules need parts of the S-57 spatial topology that the
portrayal host does not model yet. When such a rule errors, the feature is
**suppressed** — drawn as nothing rather than as a placeholder — so it simply does
not appear. This is the most significant gap: an affected feature is missing, not
just mis-styled.

## Geometry-construction instructions

The catalogue builds some figures from geometry-construction instructions
(`AugmentedRay`, `AugmentedPath`, `ArcByRadius`, `CoverageFill`). The engine does
**not** lower these into tile geometry, with one exception:

- **Handled:** sectored and directional **light** figures — their legs and arcs
  are drawn (tessellated per zoom into a screen-space layer).
- **Not handled:** any *other* feature that relies on these constructions loses
  that constructed geometry (the rest of the feature — its symbol, fill, or label
  — still draws).

## Time-dependent portrayal is ignored

Date and time modifiers (`Date`, `Time`, `DateTime`, `TimeValid`) are no-ops, so
**seasonal or time-varying features are always shown**, regardless of the current
date. There is no notion of "show this buoy only in summer."

## Alerts and indications are not surfaced

The S-100 alert mechanism (`AlertReference`, `Warning`, `Error`) is not wired to
anything. chartplotter does not raise guard-zone alarms or any other indication —
consistent with it being a viewer, not a certified ECDIS.

## Text placement

Labels are emitted with simple offsets. There is **no S-100 text-placement or
decluttering** logic; label collisions are left to the map renderer's defaults, so
dense areas can over- or under-label compared to a reference chart.

## Unknown objects and unrecognized instructions

- An object class with no S-101 rule is drawn as the magenta **unknown object**
  mark (S-52 §10.1.1 parity) — a visible placeholder, not the real symbology.
- Any drawing instruction the lowering step does not recognize is skipped.

## S-102 bathymetry

S-102 gridded bathymetric surfaces are a work in progress and are not yet
portrayed.
