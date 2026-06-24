# Third-party notices

chartplotter is licensed under the [MIT License](LICENSE), © 2026 Jeremy Collins.

The program bundles, embeds, or builds on the third-party software and data
listed below. Each remains under its own license. This file is informational; the
upstream license text governs.

## Go dependencies (linked into the binary)

All are permissive (MIT or BSD-3-Clause). None are copyleft.

| Module | License |
| --- | --- |
| github.com/BertoldVdb/go-ais | MIT |
| github.com/adrianmo/go-nmea | MIT |
| github.com/alecthomas/kong | MIT |
| github.com/dustin/go-humanize | MIT |
| github.com/yuin/gopher-lua | MIT |
| github.com/dhconnelly/rtreego | BSD-3-Clause |
| github.com/srwiley/oksvg | BSD-3-Clause |
| github.com/srwiley/rasterx | BSD-3-Clause |
| github.com/google/uuid | BSD-3-Clause |
| github.com/remyoudompheng/bigfft | BSD-3-Clause |
| golang.org/x/image | BSD-3-Clause |
| golang.org/x/net | BSD-3-Clause |
| golang.org/x/sys | BSD-3-Clause |
| golang.org/x/text | BSD-3-Clause |
| modernc.org/libc | BSD-3-Clause |
| modernc.org/mathutil | BSD-3-Clause |
| modernc.org/memory | BSD-3-Clause |
| modernc.org/sqlite | BSD-3-Clause |

Regenerate this list from a built binary with `go version -m bin/chartplotter`.

## Bundled web assets

| Asset | Where | License |
| --- | --- | --- |
| MapLibre GL JS v5.24.0 | `web/vendor/maplibre-gl.js` | BSD-3-Clause |
| Noto Sans glyphs | `web/glyphs/` | SIL Open Font License 1.1 |
| OpenBridge icons | `web/src/lib/openbridge-icons.mjs` | Artwork CC BY 4.0; code Apache-2.0 |

OpenBridge attribution, as required by CC BY 4.0: *"Icons from the OpenBridge Icon
Pack by the Ocean Industries Concept Lab, CC BY 4.0."*

## Bundled data

### GSHHG coastline basemap

`web/basemap/coastline.geojson` and `coastline.pmtiles` are derived from the
**GSHHG** shoreline data set (A Global Self-consistent, Hierarchical,
High-resolution Geography Database) by Paul Wessel and Walter H. F. Smith,
distributed under the **GNU LGPL**. The offline basemap underlay is optional.

### NOAA ENC data and catalog

chartplotter reads NOAA S-57 ENC cells and ships a distilled product catalog
(`web/catalog.json`, derived from NOAA `ENCProdCat.xml`). NOAA charts are works of
the U.S. Government and are in the **public domain**. They carry NOAA's standard
disclaimer: the data is **not to be used for navigation**.

### S-57 attribute table (from GDAL)

`internal/s57/parser/s57attributes.csv` is the S-57 attribute catalogue from the
**GDAL** project (<https://gdal.org>), licensed **MIT/X11**. The acronyms and codes
it contains originate from the IHO S-57 Object and Attribute Catalogue.

## IHO S-101 Portrayal Catalogue and Feature Catalogue

> **License status: to be confirmed.** Treat this section as a known open item,
> not a cleared right to redistribute.

chartplotter portrays charts using the **IHO S-101 Portrayal Catalogue** and
**Feature Catalogue** (the symbols, color profiles, drawing rules, and feature
definitions). The embedded copy is a **draft** — `S-101 2.1.0-DRAFT`, built on
S-100 Edition 5.2 — sourced from the IHO working-group repositories. These
materials are **© the International Hydrographic Organization (IHO)**.

Source repositories (public, but **no license declared** — `license: null`, i.e.
all rights reserved):

- Portrayal Catalogue — <https://github.com/iho-ohi/S-101_Portrayal-Catalogue>
- Feature Catalogue — <https://github.com/iho-ohi/S-101-Documentation-and-FC>

How chartplotter handles them:

- **Not in this source repository.** The catalogue is `.gitignore`d
  (`internal/engine/s101catalog/catalog/`) and synced from an external copy at
  build time (`make sync-s101`), so the repository itself does not redistribute
  IHO material.
- **Embedded in release binaries.** The release workflow clones the two IHO repos
  above and builds with `-tags embed_s101`, so the published binaries are
  self-contained and **do** redistribute the catalogue. A plain `go build`
  (without the tag) omits it and requires `--s101 <dir>` at runtime.

Because the IHO repositories declare no license, the right to redistribute this
material in release binaries is **unconfirmed**. The IHO copyright and
reproduction policy is at <https://iho.int>.
