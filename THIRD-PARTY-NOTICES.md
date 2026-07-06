# Third-party notices

chartplotter is licensed under the [MIT License](LICENSE), © 2026 Jeremy Collins.

The program bundles, embeds, links, or builds on the third-party software and
data listed below. Each remains under its own license. This file is
informational; the upstream license text governs.

## libtile57 (the native chart engine)

The binary statically links **libtile57**, the native S-57/S-101 chart engine
built from the sibling [tile57](https://github.com/beetlebugorg/tile57)
repository. tile57 is **MIT**-licensed, © 2026 Jeremy Collins.

tile57 carries its own third-party inventory in its
[`THIRD_PARTY_LICENSES.md`](https://github.com/beetlebugorg/tile57/blob/main/THIRD_PARTY_LICENSES.md),
which covers the components compiled into libtile57 — and therefore into every
chartplotter binary — including vendored **Lua** (MIT), **nanosvg** (zlib),
**stb_image_write** (public domain / MIT), an embedded **Noto Sans** face (SIL
OFL 1.1), ported algorithms, and the **IHO S-101 catalogues** (see the IHO
section below).

## Go dependencies

All are permissive (MIT or BSD-3-Clause). None are copyleft.

| Module | License |
| --- | --- |
| github.com/BertoldVdb/go-ais | MIT |
| github.com/adrianmo/go-nmea | MIT |
| github.com/alecthomas/kong | MIT |
| github.com/dustin/go-humanize | MIT |
| github.com/mattn/go-isatty | MIT |
| github.com/ncruces/go-strftime | MIT |
| github.com/google/uuid | BSD-3-Clause |
| github.com/remyoudompheng/bigfft | BSD-3-Clause |
| golang.org/x/image | BSD-3-Clause |
| golang.org/x/sync | BSD-3-Clause |
| golang.org/x/sys | BSD-3-Clause |
| modernc.org/libc | BSD-3-Clause |
| modernc.org/mathutil | BSD-3-Clause |
| modernc.org/memory | BSD-3-Clause |
| modernc.org/sqlite | BSD-3-Clause |

Regenerate this list from a built binary with `go version -m bin/chartplotter`
(the module graph in `go.mod` includes a few indirect and test-only modules that
are not linked into the binary).

## Bundled web assets

| Asset | Where | License |
| --- | --- | --- |
| MapLibre GL JS v5.24.0 | `web/vendor/maplibre-gl.js` | BSD-3-Clause |
| Noto Sans glyphs | `web/glyphs/` | SIL Open Font License 1.1 (full text: [`web/fonts/OFL.txt`](web/fonts/OFL.txt)) |
| OpenBridge icons | `web/src/lib/openbridge-icons.mjs` | Artwork CC BY 4.0; code Apache-2.0 |

OpenBridge attribution, as required by CC BY 4.0: *"Icons from the OpenBridge Icon
Pack by the Ocean Industries Concept Lab, CC BY 4.0."*

## Bundled data

### GSHHG coastline basemap

`web/basemap/coastline.geojson` is derived from the **GSHHG** shoreline data set
(A Global Self-consistent, Hierarchical, High-resolution Geography Database) by
Paul Wessel and Walter H. F. Smith, distributed under the **GNU LGPL**. Only the
GeoJSON is tracked in this repository; the viewer can optionally load a tiled
`basemap/coastline.pmtiles` derived from the same data if you provide one. The
offline basemap underlay is optional.

### NOAA ENC data and catalog

chartplotter reads NOAA S-57 ENC cells and ships a distilled product catalog
(`web/catalog.json`, derived from NOAA `ENCProdCat.xml`). NOAA charts are works of
the U.S. Government and are in the **public domain**. They carry NOAA's standard
disclaimer: the data is **not to be used for navigation**.

Two NOAA cells **are tracked in this repository** as test fixtures:
`testdata/US4MD81M.000` (with its `.001`–`.003` update files) and
`testdata/US5MD1MC.000`.

### S-57 attribute table (from GDAL)

`internal/s57/parser/s57attributes.csv` is the S-57 attribute catalogue from the
**GDAL** project (<https://gdal.org>), licensed **MIT/X11**. The acronyms and codes
it contains originate from the IHO S-57 Object and Attribute Catalogue.

## IHO S-101 Portrayal Catalogue and Feature Catalogue

> **License status: undeclared.** The IHO publishes these catalogues with no
> license statement, so redistribution rights are not formally cleared — treat
> this as a known open item. The project nonetheless **distributes binaries that
> embed the catalogue** as an accepted position; if the IHO clarifies or objects
> to its terms, this section and that decision must be revisited.

chartplotter portrays charts using the **IHO S-101 Portrayal Catalogue** and
**Feature Catalogue** (the symbols, color profiles, drawing rules, and feature
definitions). These materials are **© the International Hydrographic
Organization (IHO)**.

Source repositories (public, but **no license declared** — `license: null`,
i.e. all rights reserved):

- Portrayal Catalogue — <https://github.com/iho-ohi/S-101_Portrayal-Catalogue>
- Feature Catalogue — <https://github.com/iho-ohi/S-101-Documentation-and-FC>

How the project handles them — **fetch from the IHO at build, embed, and
distribute the built binaries**:

- **Not in either repository.** Neither this repo nor
  [tile57](https://github.com/beetlebugorg/tile57) commits any IHO catalogue
  content. The tile57 repo references the two IHO repositories above as **git
  submodules** (`vendor/S-101_Portrayal-Catalogue`,
  `vendor/S-101-Documentation-and-FC`), so
  `git submodule update --init --recursive` fetches the material **directly
  from the IHO's own repositories** onto the builder's machine.
- **Embedded in every binary.** Building libtile57 compiles the fetched
  catalogue into the library, and every `chartplotter` binary links libtile57 —
  so **any built binary embeds IHO material**, whether built locally or on CI.
- **Distributed in published binaries.** Tagged releases publish per-platform
  `chartplotter` binaries built on CI, where the runner obtains the catalogue
  from the IHO's own repositories at build time. Those released binaries
  therefore embed IHO material, and the project distributes them as an accepted
  position despite the undeclared license.

The IHO copyright and reproduction policy is at <https://iho.int>. If the IHO
clarifies (or objects to) the catalogues' redistribution terms, this section —
and the decision to distribute binaries — must be revisited.
