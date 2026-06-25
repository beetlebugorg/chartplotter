// SpriteBuilder — sprite/glyph IMAGE SYNTHESIS, extracted from <chart-canvas>.
//
// Pure image CONSTRUCTION: given the decoded sprite + pattern atlases (metadata
// + images), it builds the ImageData for a requested image id (a centred point
// symbol, a composited sounding glyph, or a raw pattern cell). It performs NO
// MapLibre side effects — the element keeps all map.addImage/hasImage and the
// styleimagemissing wiring and delegates only the pixel synthesis here.
import { M_TO_FT } from "./s52-style.mjs";

export class SpriteBuilder {
  constructor({ sprite, spriteImg, patterns, patternsImg, atlasPpu }) {
    this.sprite = sprite;
    this.spriteImg = spriteImg;
    this.patterns = patterns;
    this.patternsImg = patternsImg;
    this.atlasPpu = atlasPpu;
  }

  // Build the ImageData for an image id (the dispatch lifted from registerImage):
  // a synthesized sounding (`snd:…`), a composited glyph list (comma-joined), or
  // a single centred point symbol. Returns ImageData or null.
  imageFor(id) {
    return id.startsWith("ctr:") ? this.centredGlyph(id.slice(4))
      : id.startsWith("snd:") ? this.synthSounding(id)
      : id.indexOf(",") >= 0 ? this.compositeSounding(id)
      : this.centredSymbol(id);
  }

  // centredGlyph centres the GLYPH's bounding box on the point, ignoring the
  // catalogue pivot — used for a lone centred-area symbol (pivot_center) whose
  // corner pivot would otherwise throw the glyph far off its area. The rendered
  // cell is the glyph cropped to its content, so drawing it into a w×h canvas and
  // letting MapLibre centre that canvas puts the glyph centre on the point.
  centredGlyph(name) {
    const c = this.sprite[name];
    if (!c) return null;
    return this.rawCell(this.spriteImg, c);
  }

  // Build a sounding number in non-metric units from a synthesized name
  // `snd:<unit>:<palette>:<deci-metres>` (see soundingsIconImage). Converts the
  // baked metres depth, formats it as S-52 SNDFRM04 column glyphs, and reuses
  // the metres compositor. Quality/drying markers (QUASOU) aren't carried in the
  // numeric depth, so imperial soundings are the plain number (+ drying marker).
  synthSounding(id) {
    const [, unit, pal, dm] = id.split(":");           // ["snd","ft","S","123"]
    const meters = (parseInt(dm, 10) || 0) / 10;
    const value = unit === "ft" ? Math.abs(meters) * M_TO_FT : Math.abs(meters);
    let names = this.soundingGlyphs(Math.round(value), pal === "G" ? "G" : "S");
    if (meters < 0) names = "SOUNDSA1," + names;        // drying-height marker (always bold)
    return this.compositeSounding(names);
  }

  // S-52 SNDFRM04 whole-number column classes → a comma-joined glyph list. Each
  // glyph `SOUND<pal><class><digit>` self-positions into its column (the art
  // carries the shift), mirroring soundg03.zig's emitDigits without the metric
  // decimal subscript (imperial soundings are whole units).
  soundingGlyphs(n, pal) {
    const g = (cls, d) => `SOUND${pal}${cls}${d}`;
    n = Math.max(0, n);
    if (n < 10) return g(1, n);
    if (n < 100) return [g(1, (n / 10) | 0), g(0, n % 10)].join(",");
    if (n < 1000) return [g(2, (n / 100) | 0), g(1, ((n / 10) | 0) % 10), g(0, n % 10)].join(",");
    if (n < 10000) return [g(2, (n / 1000) | 0), g(1, ((n / 100) | 0) % 10), g(0, ((n / 10) | 0) % 10), g(4, n % 10)].join(",");
    return [g(3, (n / 10000) | 0), g(2, ((n / 1000) | 0) % 10), g(1, ((n / 100) | 0) % 10), g(0, ((n / 10) | 0) % 10), g(4, n % 10)].join(",");
  }

  rawCell(img, cell) {
    const cv = document.createElement("canvas");
    cv.width = cell.w; cv.height = cell.h;
    const ctx = cv.getContext("2d");
    ctx.drawImage(img, cell.x, cell.y, cell.w, cell.h, 0, 0, cell.w, cell.h);
    return ctx.getImageData(0, 0, cell.w, cell.h);
  }

  centredSymbol(name) {
    const c = this.sprite[name];
    if (!c) return null;
    const halfW = Math.max(c.pivot_x, c.w - c.pivot_x);
    const halfH = Math.max(c.pivot_y, c.h - c.pivot_y);
    const w = Math.max(1, Math.ceil(2 * halfW));
    const h = Math.max(1, Math.ceil(2 * halfH));
    const cv = document.createElement("canvas");
    cv.width = w; cv.height = h;
    const ctx = cv.getContext("2d");
    ctx.drawImage(this.spriteImg, c.x, c.y, c.w, c.h, w / 2 - c.pivot_x, h / 2 - c.pivot_y, c.w, c.h);
    return ctx.getImageData(0, 0, w, h);
  }

  compositeSounding(namesStr) {
    const cells = [];
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    for (const name of namesStr.split(",")) {
      const c = this.sprite[name];
      if (!c) continue;
      const left = -c.pivot_x, top = -c.pivot_y;
      cells.push({ c, left, top });
      minX = Math.min(minX, left); minY = Math.min(minY, top);
      maxX = Math.max(maxX, left + c.w); maxY = Math.max(maxY, top + c.h);
    }
    if (!cells.length) return null;
    const halfW = Math.max(-minX, maxX), halfH = Math.max(-minY, maxY);
    const w = Math.max(1, Math.ceil(2 * halfW)), h = Math.max(1, Math.ceil(2 * halfH));
    const cv = document.createElement("canvas");
    cv.width = w; cv.height = h;
    const ctx = cv.getContext("2d");
    for (const { c, left, top } of cells) {
      ctx.drawImage(this.spriteImg, c.x, c.y, c.w, c.h, w / 2 + left, h / 2 + top, c.w, c.h);
    }
    return ctx.getImageData(0, 0, w, h);
  }
}
