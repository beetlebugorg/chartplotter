# Tile pipeline (backend baker)

Files: `internal/engine/bake/bake.go`, `internal/engine/tile/tile.go`,
`internal/engine/mvt/{mvt.go,pbf.go}`, `internal/engine/portrayal/primitive.go`.

## 1. Architecture — how a cell becomes tiles

**Add → route → enumerate → emit → encode.**

1. **`AddCell`** (`bake.go:208`) walks a parsed cell's features. Per feature it
   computes the scale band (`BandForScale`, `bake.go:72`) from CSCL, the display
   z-min from SCAMIN (`specZMin`, `bake.go:124`), then runs the S-52 portrayal
   pass(es) (`BuildFeaturePasses`) → `portrayal.Primitive` IR.
2. **`route` / `routeSymbol` / `routeSoundingGroup`** (`bake.go:322/403/269`)
   type-switch each primitive into a `routed` record (`bake.go:157`): target MVT
   layer, geometry kind, geometry **pre-projected once** to normalized `[0,1]`
   Web-Mercator (`normPt/normPts/normRings`), a world-space AABB, the display zoom
   span `[zMin,zMax]`, the native band span `[natMin,natMax]`, and the pre-built
   MVT attribute set. Sounding digit-glyphs sharing an anchor are coalesced into
   one `soundings` feature (`bake.go:247`). Sector lights are deferred as
   `sectorPrim` (`bake.go:392`) because their geometry is screen-pixel sized and
   must be re-tessellated per zoom.
3. **`TileCoords`** (`bake.go:427`) enumerates the unique `(z,x,y)` set the prims
   (plus sector spill) touch, deduped via a `map[uint64]` packing `z<<40|x<<20|y`.
4. **`EmitTileInto`** (`bake.go:520`) per tile: spatial-reject prims by world AABB
   vs the buffered tile window → best-available suppression → affine-project each
   surviving prim (`ProjectNorm`, no trig) → clip (Sutherland–Hodgman polygons /
   Liang–Barsky lines) → quantize to integer vertices → append to a
   `mvt.TileBuilder`. Sector lights are tessellated/clipped last.
5. **`tb.Encode()`** emits MVT bytes; empty tiles return nil and are dropped.

Geometry projection is correctly **hoisted**: lat/lon → normalized world is done
once at add time; the per-tile step is a trig-free affine map (`tile.go:72`).

### Per-band model + best-available suppression

Each cell's prims carry their band's native zoom span. Where cells of different
scales overlap, suppression decides which one shows at a given tile zoom `bandZ`:

- **Down-fill** (`bandZ < natMin`, `bake.go:563`): a finer cell shown *below* its
  native band is suppressed **only where a strictly-coarser eligible prim's AABB
  overlaps it** (`anyCoarserOverlaps`, `bake.go:742`). On zoom-out only the
  coarsest blanket survives where cells stack. Geometry-aware ✔.
- **Up** (`bandZ > natMax`, `bake.go:566`): a coarse prim shown *above* its band
  is suppressed if **any** finer cell is eligible on the tile
  (`r.natMax < finestNat`) — **no overlap test**. Tile-global ✘ (see §3).

### Sector-light tessellation

`expandSector` (`bake.go:668`) lays the figure out in a 256-px-per-tile world
(`worldPx = 256·2^z`) so radii are a fixed fraction of a tile at all zooms. A ring
(sweep ≈ 0/360) → one 26 mm OUTLW-backed coloured circle; a true sector → two
dashed CHBLK legs (25 mm) + a 20 mm coloured arc, bearings reversed +180 (seaward
convention). Points are un-projected to lat/lon then re-projected per tile and
clipped with `ClipLine`. Spill into neighbour tiles is enumerated in `TileCoords`
(`bake.go:443`) and rejected in emit with a `margin` (`bake.go:610`), both keyed to
`sectorRadiusNorm` so enumeration and emit agree.

## 2. MVT 2.1 conformance

**Correct** (verified):

- Command/parameter encoding: `commandInteger` packs `(id&0x7)|(count<<3)`
  (`mvt.go:72`); MoveTo=1 / LineTo=2 / ClosePath=7 with correct counts; points use
  one MoveTo with count = #points (multipoint form, `mvt.go:111`).
- Zigzag: `(n<<1)^(n>>31)` arithmetic shift on int32 (`pbf.go:17`); deltas are
  cursor-relative and the cursor advances (`mvt.go:113`).
- Extent (4096) field 5, version 2 field 15, layers field 3 (`mvt.go:308/314/362`).
- Closing-duplicate vertex stripped before emit since ClosePath closes the ring
  (`dropClosingDuplicate`, `mvt.go:156`); rings < 3 dropped.
- Key/value pools deduped (keys + string values interned; numerics appended —
  dedup is optional in spec) (`mvt.go:188`). Value type tags correct
  (string=1, float=fixed32 2, int(varint)=4, bool=7, `mvt.go:270`).

**Needs validation — likely benign** (`mvt.go:86,145`): ring winding. The code
forces ring 0 to standard-shoelace `signedArea >= 0` and holes negative. MVT's
"exterior = positive area" is defined with the *surveyor* formula in Y-down tile
space, which is the **negation** of this code's `signedArea` — so strictly per spec
the exterior/interior windings may be inverted. **Relative** orientation
(exterior vs holes) is kept consistent and MapLibre fills by even-odd/non-zero, so
rendering is correct in practice. Validate with a strict consumer
(Tippecanoe/`vt2geojson`) before relying on the absolute winding; fix is to flip
the orientation test if confirmed.

**Minor deviation** (`bake.go:597`): point in-bounds reject is `p.X>=e || p.Y>=e`,
dropping symbols whose anchor sits in the render-buffer zone just off the tile,
unlike polygons/lines which are buffer-clipped. Edge symbols can pop at tile
seams. Fix: reject points against the buffered rect like the other geom kinds.

## 3. Best-available suppression — correctness gap

The **up-direction** path (`bake.go:566`) is **tile-global, not geometry-aware**:

```go
if bandZ > r.natMax && r.natMax < finestNat { continue }   // finestNat = max natMax over ALL eligible prims on the tile
```

It asks only "does any finer-band prim appear *anywhere on this tile*", with no
overlap test between `r` and the finer prim — unlike the down path, which gates on
`anyCoarserOverlaps`. So a coarse feature is dropped whenever a finer cell merely
*touches the tile*, even if the finer cell doesn't cover the feature's location (a
hole, a different footprint, or simply doesn't carry that object class).

**This is the root of the "disappearing light" class of bug.** It was *confirmed by
reproduction*: baking the coastal cell `US3EC08M` (carries Thomas Point Shoal
Light) together with the finer Annapolis cell `US5MD1MC`, the LIGHTS point is
present in the z11 coastal tile but **suppressed** in the z12/z13 tiles (which the
finer band serves), even though the finer cell doesn't carry that light:

```
[coastal-only]  z11: LIGHTS_point=true   z12/z13: (coastal tops at z11, overzooms)
[coastal+finer] z11: LIGHTS_point=true   z12/z13: LIGHTS_point=FALSE (suppressed)
```

The **frontend fix already shipped** (commit `59f34b2`, draw symbols above all
bands' fills, plus floating `natMax`→z18 so the coastal source overzooms its z11
tile that *does* carry the light) resolves the visible symptom: the coastal light
overzooms and now draws above the finer chart's fills. But the **baker logic is
still latent** — it will drop coarse points again for any corpus where bands
genuinely differ in `natMax`. **Recommended backend fix:** mirror the down path —
add `anyFinerOverlaps(eligible, r)` and gate the up path on actual AABB overlap
instead of the tile-global `finestNat`.

(Also note `finestNat` guards the `MaxUint32` sentinel but `minNatMin` doesn't,
`bake.go:545/548` — dead asymmetry, the sentinel never occurs.)

## 4. Performance

Hot path is `EmitTileInto`, run once per tile.

- **`EmitTileInto` scans *all* `b.prims` per tile** (`bake.go:536`) → whole-bake is
  **O(#tiles × #prims)**. No spatial index over prims. `TileCoords` already
  computes each prim's tile range; building an inverted `tile→[]primIdx` index in
  the same pass would make emit O(prims-on-tile). **Biggest scaling win.**
- **Down-fill suppression is O(eligible²)** worst case on heavily-stacked
  multi-band tiles (`anyCoarserOverlaps` scans all eligible per down-filled prim,
  `bake.go:742`). Bounded by the `natMin > minNatMin` short-circuit
  (`bake.go:563`, which makes single-band bakes pay zero) and AABB pre-reject.
- **Per-feature/per-ring allocations each tile**: `quantizeRing` `make` per
  ring/run (`bake.go:814`); `outRings`/`paths` slice-of-slices (`bake.go:571/586`);
  `addFeature` rebuilds the tag-index array per feature per tile even though
  `r.attrs` is identical across every tile a prim spans (`mvt.go:218`). Reuse a
  scratch in `TileScratch`; cache the interned tag array per prim.
- **Un-presized geometry buffers**: `encodePolygon`/`encodeLines` grow
  `var out []uint32` (`mvt.go:78/93`); presize to `~1+3·verts`.
- **`TileBuilder.Layer` linear scan** by name per Add call (`mvt.go:329`) —
  negligible (~8 layers) but technically O(features × layers).

**Already good:** `TileScratch` reuses clipper ping-pong buffers, projection
scratch, and the eligible index slice across tiles (`bake.go:506`); `ProjectNorm`
hoists trig out of the per-tile loop (`tile.go:72`); clip rect/buffer math and the
all-inside fast path (`tile.go:137/203`); zxy key packing (`bake.go:471`).
