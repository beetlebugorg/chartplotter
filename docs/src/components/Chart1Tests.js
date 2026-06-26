import React, {useEffect, useRef, useState} from 'react';
import BrowserOnly from '@docusaurus/BrowserOnly';
import useBaseUrl from '@docusaurus/useBaseUrl';

// Chart1Tests embeds the S-52 PresLib "ECDIS Chart 1" reference sheet LIVE — one
// read-only <chart-plotter> widget — and turns the docs page into a symbol-compliance
// checker: every panel of the sheet is a row in the list; click one (or arrow up/down)
// and the widget frames that panel. The whole sheet is one contiguous synthetic ENC,
// so navigation is just map.fitBounds(panel). The widget runs in `spec` mode (no
// chrome) with the reference mariner settings forced on, so what you see is an
// apples-to-apples diff against the spec's own reference plots.
// Tiles load from the /chart1/ bundle (`make demo-chart1`); the frontend assets are
// shared with the /demo/ bundle.

// Web-Mercator scale↔zoom (512-tile metres/px at z0, 1/96-inch CSS px) — only used
// for the pre-fit first paint and the no-map fallback; the real framing is fitBounds.
const M_PER_PX_Z0 = 78271.516964020485;
const PX_PITCH_M = 0.00026458;
const zoomForScale = (scale, lat) =>
  Math.log2((M_PER_PX_Z0 * Math.cos((lat * Math.PI) / 180)) / (PX_PITCH_M * scale));

// Spec mode hides all widget chrome, so the fit just needs a small, symmetric margin
// to keep edge symbology off the frame edge — like the thin border around each
// PresLib reference plot. (No asymmetric chrome padding any more.)
const PAD = {top: 18, bottom: 18, left: 18, right: 18};

// Mariner display state pinned to match the IHO PresLib reference plots — kept in
// step with the MARINER block in scripts/preslib-chart1.mjs. The widget is read-only
// here (spec mode), so the viewer can't change anything; we force these at ready so
// the render is an apples-to-apples diff against the spec's own figures. ALL symbology
// shown; data-quality overlay on (CATZOC panels); metres (IHO, not NOAA feet); the
// depth-shading demo's 0/5/10/30 contours labelled; date-dependency + meta boundaries
// shown; 25 mm sectors and symbolized boundaries (the S-52 defaults the plots use).
const MARINER = {
  displayBase: true, displayStandard: true, displayOther: true,
  dataQuality: true,
  depthUnit: 'm',
  showContourLabels: true,
  shallowContour: 5, safetyContour: 10, deepContour: 30,
  highlightDateDependent: true,
  dateDependent: false,
  showMetaBounds: true,
  showFullSectorLines: false,
  boundaryStyle: 'symbolized',
  simplifiedPoints: false,
};

// One row per PresLib reference-plot page (Part I §16, doc pages 238–253). Bounds
// are the cells' data extents [W, S, E, N]; the harbor pages are 1:14 000, the
// overview 1:60 000. Kept in step with the PANELS table in scripts/preslib-chart1.mjs.
const HARBOR = 14000;
const RAW = [
  {page: 238, label: 'Whole sheet (overview)',          b: [-5.135803, 15.00018, -4.997983, 15.133311], scale: 60000},
  {page: 239, label: 'Information about (A, B)',         b: [-5.1307, 15.0993, -5.1002, 15.1288]},
  {page: 240, label: 'Information about (cont.)',        b: [-5.0982, 15.0993, -5.0677, 15.1288]},
  {page: 241, label: 'Natural & man-made (C, D, E)',     b: [-5.0656, 15.0992, -5.0351, 15.1288]},
  {page: 242, label: 'Port features (F)',                b: [-5.0331, 15.0993, -5.0026, 15.1288]},
  {page: 243, label: 'Depths & currents (H, I)',         b: [-5.1307, 15.0677, -5.1002, 15.0973]},
  {page: 244, label: 'Seabed & obstructions (J, K, L)',  b: [-5.0982, 15.0677, -5.0677, 15.0973]},
  {page: 245, label: 'Traffic routes (M)',               b: [-5.0656, 15.0677, -5.0351, 15.0973]},
  {page: 246, label: 'Special areas (N)',                b: [-5.0331, 15.0677, -5.0026, 15.0973]},
  {page: 247, label: 'Lights, buoys & beacons (P–S)',    b: [-5.1307, 15.0362, -5.1002, 15.0657]},
  {page: 248, label: 'Buoys & beacons (Q)',              b: [-5.0982, 15.0362, -5.0676, 15.0657]},
  {page: 250, label: 'Topmarks (Q)',                     b: [-5.0656, 15.0362, -5.0350, 15.0657]},
  {page: 251, label: 'Approved new objects / V-AIS',     b: [-5.1307, 15.0046, -5.1002, 15.0342]},
  {page: 252, label: 'Colour-test diagram (Day)',        b: [-5.0331, 15.0362, -5.0026, 15.0657], scheme: 'day'},
  {page: 253, label: 'Colour-test diagram (Dusk)',       b: [-5.0331, 15.0362, -5.0026, 15.0657], scheme: 'dusk'},
];
const PANELS = RAW.map((p) => {
  const [w, s, e, n] = p.b;
  return {...p, scale: p.scale || HARBOR, lng: (w + e) / 2, lat: (s + n) / 2};
});
const SHEET = PANELS[0]; // page 238 = the whole sheet
const INITIAL_SCALE = 105000; // generous pre-fit paint; fitBounds refines on ready
// These features' SCAMIN is 1:139 000 — zoom out past it and they vanish. Floor the
// map so neither the whole-sheet fit (on a small map) nor a scroll can cross it.
const SCAMIN_MIN_ZOOM = zoomForScale(139000, SHEET.lat);

// Fit the map to a panel's bounds with a symmetric margin. Returns false if the map
// isn't up yet (caller falls back to setView).
function fitPanel(el, p, animate) {
  const m = el && el.map;
  if (!m || typeof m.fitBounds !== 'function') return false;
  const [w, s, e, n] = p.b;
  m.fitBounds([[w, s], [e, n]], {padding: PAD, duration: animate ? 900 : 0});
  return true;
}

function Chart() {
  // /demo/ holds the widget frontend (baked by `make demo`); /chart1/ holds just
  // the Chart 1 tiles + manifest (baked by `make demo-chart1`). The widget reuses
  // the former for assets and points its tile manifest at the latter via catalog=.
  const demo = useBaseUrl('/demo/');
  const manifest = useBaseUrl('/chart1/charts-index.json');
  const overviewImg = useBaseUrl('/img/chart1/page-238-overview.png');
  const ref = useRef(null);
  const listRef = useRef(null);     // the <ol> of panel buttons (for focus moves)
  const activeRef = useRef(SHEET);  // current panel, for handlers in stale closures
  const [active, setActive] = useState(238);
  const [status, setStatus] = useState('checking'); // checking | ready | missing
  const [full, setFull] = useState(false); // fullscreen: panel list + widget fill the viewport

  // Only boot the live widget if the tile bundle is actually published. Locally
  // (no `make demo-chart1`) fall back to the static overview image.
  useEffect(() => {
    let cancelled = false;
    fetch(manifest)
      .then((r) => {
        if (cancelled) return;
        if (!r.ok) { setStatus('missing'); return; }
        setStatus('ready');
        const id = 'chartplotter-widget-module';
        if (!document.getElementById(id)) {
          const sc = document.createElement('script');
          sc.type = 'module';
          sc.id = id;
          sc.src = `${demo}src/chartplotter.mjs`;
          document.head.appendChild(sc);
        }
      })
      .catch(() => { if (!cancelled) setStatus('missing'); });
    return () => { cancelled = true; };
  }, [demo, manifest]);

  // Once the widget's map is ready: pin the reference mariner settings + Day scheme
  // (spec mode is read-only — the viewer can't change them), floor the zoom past the
  // SCAMIN cutoff, and frame the whole sheet.
  useEffect(() => {
    if (status !== 'ready') return undefined;
    let tries = 0;
    const iv = setInterval(() => {
      const el = ref.current;
      const m = el && el.map;
      if (!m) { if (++tries > 60) clearInterval(iv); return; }
      clearInterval(iv);
      try { m.setMinZoom(SCAMIN_MIN_ZOOM); } catch (e) { /* older map */ }
      // Force the reference display state. applyScheme('day') also resets any scheme
      // a previous Day/Dusk click (or the sibling demo) left in localStorage.
      if (typeof el.applyMariner === 'function') {
        try { el.applyMariner(MARINER); } catch (e) { /* widget best-effort */ }
      }
      if (typeof el.applyScheme === 'function') {
        try { el.applyScheme('day'); } catch (e) { /* widget best-effort */ }
      }
      fitPanel(el, SHEET, false);
    }, 200);
    return () => clearInterval(iv);
  }, [status]);

  // Entering/leaving fullscreen resizes the map frame, so let the map relayout and
  // re-frame the active panel. Also lock page scroll + wire Escape / arrow nav.
  useEffect(() => {
    if (typeof document !== 'undefined') {
      document.body.style.overflow = full ? 'hidden' : '';
    }
    const t = setTimeout(() => {
      const m = ref.current && ref.current.map;
      if (!m) return;
      try { m.resize(); } catch (e) { /* older map */ }
      fitPanel(ref.current, activeRef.current, false);
    }, 80);
    if (!full) return () => clearTimeout(t);
    // Fullscreen owns the screen, so Up/Down nav works globally even when focus is on
    // the map. But if focus is on a panel button, the list's own onKeyDown already
    // handled it (and called preventDefault) — bail so we don't step twice.
    const onKey = (e) => {
      if (e.key === 'Escape') { setFull(false); return; }
      if (e.defaultPrevented) return;
      onNavKey(e);
    };
    window.addEventListener('keydown', onKey);
    return () => {
      clearTimeout(t);
      window.removeEventListener('keydown', onKey);
      if (typeof document !== 'undefined') document.body.style.overflow = '';
    };
  }, [full]); // eslint-disable-line react-hooks/exhaustive-deps

  const go = (p) => {
    setActive(p.page);
    activeRef.current = p;
    const el = ref.current;
    if (!el) return;
    // Day/Dusk colour-test panels carry their own scheme; everything else stays Day.
    if (typeof el.applyScheme === 'function') {
      try { el.applyScheme(p.scheme || 'day'); } catch (e) { /* widget-mode best-effort */ }
    }
    if (!fitPanel(el, p, true) && typeof el.setView === 'function') {
      el.setView({lng: p.lng, lat: p.lat, scale: p.scale, animate: true, duration: 900});
    }
  };

  // Keyboard nav: Up/Down step through the panel list. Uses activeRef (always
  // current) so it works from the fullscreen effect's stale closure too.
  const focusBtn = (page) => {
    const root = listRef.current;
    const btn = root && root.querySelector(`button[data-page="${page}"]`);
    if (btn) btn.focus();
  };
  const step = (dir) => {
    const i = PANELS.findIndex((p) => p.page === activeRef.current.page);
    const ni = Math.min(PANELS.length - 1, Math.max(0, i + dir));
    if (ni === i) return;
    go(PANELS[ni]);
    focusBtn(PANELS[ni].page);
  };
  const onNavKey = (e) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); step(1); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); step(-1); }
  };

  if (status === 'missing') {
    return (
      <div className="chart1 chart1--poster">
        <img className="chart1__poster" src={overviewImg} alt="The S-52 ECDIS Chart 1 symbol sheet rendered by chartplotter" />
        <p className="chart1__hint">
          The live, clickable version needs the baked tiles. Build them locally with{' '}
          <code>make demo DEMO_OUT=docs/static/demo</code> and{' '}
          <code>make demo-chart1 DEMO_CHART1_OUT=docs/static/chart1</code>, then{' '}
          <code>make docs</code>.
        </p>
      </div>
    );
  }

  const zoom = zoomForScale(INITIAL_SCALE, SHEET.lat);
  return (
    <div className={'chart1' + (full ? ' chart1--full' : '')}>
      <div className="chart1__panel">
        <div className="chart1__head">
          <div className="chart1__title">
            Reference panels <span className="chart1__sub">PresLib §16, pp. 238–253</span>
          </div>
          <button
            type="button"
            className="chart1__full"
            onClick={() => setFull((v) => !v)}
            aria-pressed={full}
            title={full ? 'Exit fullscreen (Esc)' : 'Fullscreen — compare against the spec plots'}
          >
            {full ? '✕ Exit' : '⤢ Fullscreen'}
          </button>
        </div>
        <ol className="chart1__list" ref={listRef} onKeyDown={onNavKey}>
          {PANELS.map((p) => (
            <li key={p.page}>
              <button
                type="button"
                data-page={p.page}
                className={'chart1__test' + (active === p.page ? ' chart1__test--active' : '')}
                onClick={() => go(p)}
              >
                <span className="chart1__page">p.&nbsp;{p.page}</span>
                <span className="chart1__label">{p.label}</span>
              </button>
            </li>
          ))}
        </ol>
      </div>
      <div className="liveChart chart1__map">
        {/* widget = read-only viewer; assets = demo frontend; catalog = Chart 1 tiles.
            spec = chrome-free clean map (no controls/databox/attr/scalebar) so the
            render matches the PresLib reference plots for side-by-side diffing. */}
        <chart-plotter
          ref={ref}
          widget=""
          spec=""
          assets={demo}
          catalog={manifest}
          basemap="none"
          center={`${SHEET.lng},${SHEET.lat}`}
          zoom={zoom.toFixed(3)}
        />
      </div>
    </div>
  );
}

export default function Chart1Tests() {
  return (
    <BrowserOnly fallback={<div className="liveChart liveChart--loading">Loading the chart…</div>}>
      {() => <Chart />}
    </BrowserOnly>
  );
}
