import React, {useEffect, useRef, useState, useCallback} from 'react';
import BrowserOnly from '@docusaurus/BrowserOnly';
import useBaseUrl from '@docusaurus/useBaseUrl';
import {SCENARIOS, framing, effectiveMariner} from './testharness/scenarios';
import styles from './TestHarness.module.css';

// TestHarness publishes the interactive ENC-symbology review harness as a read-only
// docs page: pick a scenario (Chart 1 panels + S-64 viewer cells) and see our LIVE
// <chart-plotter> render — scaled to the cell's compilation scale — beside the
// official IHO reference plot, with the per-scenario mariner settings shown. It's the
// Docusaurus port of the standalone harness (web/test-harness/*), minus the
// "Send to Claude" note box (that needs the dev Node server).
//
// One long-lived widget in `spec` mode (chrome-free) driven only via
// applyScheme/applyMariner/jumpTo, so the render is an apples-to-apples diff against
// the spec's own figures. Tiles load from the /harness/ bundle (`make docs-harness`);
// the frontend assets are shared with the /demo/ bundle.

const MAX_PX = 4000; // cap the off-screen render box for very large cells

// Plain-text-ish summary of the mariner settings shown in the strip.
function marinerParts(m) {
  const cat = m.displayOther ? 'Base + Standard + Other' : m.displayStandard ? 'Base + Standard' : 'Base only';
  return [
    ['display', cat], ['safety contour', `${m.safetyContour} m`], ['depths', m.depthUnit === 'm' ? 'metres' : 'feet'],
    ['boundaries', m.boundaryStyle], ['quality', m.dataQuality], ['contour labels', m.showContourLabels], ['meta bounds', m.showMetaBounds],
  ];
}

function Harness() {
  // /demo/ holds the widget frontend (built by `make demo`); /harness/ holds the
  // Chart 1 + S-64 tiles + merged manifest (built by `make docs-harness`). The widget
  // reuses the former for assets and points its tile manifest at the latter.
  const demo = useBaseUrl('/demo/');
  const manifest = useBaseUrl('/harness/charts-index.json');
  const refsBase = useBaseUrl('/harness/refs/');

  const plotRef = useRef(null);   // <chart-plotter>
  const stageRef = useRef(null);  // clipping viewport for the scaled render
  const scalerRef = useRef(null); // the CSS-scaled box holding the plotter
  const listRef = useRef(null);
  const frameRef = useRef(null);  // current framing(): {center, zoom, width, height}
  const iRef = useRef(0);         // current scenario index, for stale closures

  const [i, setI] = useState(0);
  const [status, setStatus] = useState('checking'); // checking | ready | missing
  const [full, setFull] = useState(false);
  const [refMissing, setRefMissing] = useState(false);

  const scn = SCENARIOS[i];

  // Only boot the live widget if the tile bundle is actually published. Locally
  // (no `make docs-harness`) show the build-it hint instead.
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

  // Size the off-screen render box to the cell at its compilation scale, then CSS-scale
  // it to fit the stage — so our render shows the WHOLE cell at the reference plot's scale.
  const fitStage = useCallback(() => {
    const f = frameRef.current, stage = stageRef.current, scaler = scalerRef.current;
    if (!f || !stage || !scaler) return;
    const W = Math.min(f.width, MAX_PX), H = Math.min(f.height, MAX_PX);
    const k = Math.min(stage.clientWidth / W, stage.clientHeight / H, 1);
    scaler.style.transform = `translate(-50%, -50%) scale(${k})`;
  }, []);

  const applyScenario = useCallback((s) => {
    const p = plotRef.current;
    if (!p) return;
    try { p.applyScheme(s.scheme); } catch (e) { /* widget best-effort */ }
    try { p.applyMariner(effectiveMariner(s)); } catch (e) { /* widget best-effort */ }
    const f = framing(s);
    frameRef.current = f;
    const W = Math.min(f.width, MAX_PX), H = Math.min(f.height, MAX_PX);
    if (scalerRef.current) { scalerRef.current.style.width = W + 'px'; scalerRef.current.style.height = H + 'px'; }
    p.style.width = W + 'px'; p.style.height = H + 'px';
    fitStage();
    const map = p.map;
    if (map) { try { map.resize(); map.jumpTo({center: f.center, zoom: f.zoom}); } catch (e) { /* older map */ } }
    else { try { p.setView({lng: f.center[0], lat: f.center[1], zoom: f.zoom}); } catch (e) { /* widget */ } }
    setRefMissing(false);
  }, [fitStage]);

  // Apply the active scenario once the widget map is ready, and whenever it changes.
  useEffect(() => {
    if (status !== 'ready') return undefined;
    let tries = 0;
    const run = () => applyScenario(SCENARIOS[iRef.current]);
    const iv = setInterval(() => {
      const p = plotRef.current;
      if (!p || !p.map) { if (++tries > 60) clearInterval(iv); return; }
      clearInterval(iv);
      run();
    }, 150);
    return () => clearInterval(iv);
  }, [status, applyScenario]);

  useEffect(() => { iRef.current = i; if (status === 'ready' && plotRef.current && plotRef.current.map) applyScenario(SCENARIOS[i]); }, [i, status, applyScenario]);

  // Re-fit on window resize and on entering/leaving fullscreen (the stage resizes).
  useEffect(() => {
    const onResize = () => { const m = plotRef.current && plotRef.current.map; if (m) { try { m.resize(); } catch (e) {} } fitStage(); };
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, [fitStage]);

  useEffect(() => {
    if (typeof document !== 'undefined') document.body.style.overflow = full ? 'hidden' : '';
    const t = setTimeout(() => { const m = plotRef.current && plotRef.current.map; if (m) { try { m.resize(); } catch (e) {} } fitStage(); }, 80);
    if (!full) return () => clearTimeout(t);
    const onKey = (e) => {
      if (e.key === 'Escape') { setFull(false); return; }
      if (e.defaultPrevented || e.target.tagName === 'TEXTAREA' || e.target.tagName === 'INPUT') return;
      onNavKey(e);
    };
    window.addEventListener('keydown', onKey);
    return () => { clearTimeout(t); window.removeEventListener('keydown', onKey); if (typeof document !== 'undefined') document.body.style.overflow = ''; };
  }, [full]); // eslint-disable-line react-hooks/exhaustive-deps

  const step = (dir) => {
    const ni = Math.min(SCENARIOS.length - 1, Math.max(0, iRef.current + dir));
    if (ni === iRef.current) return;
    setI(ni);
    const btn = listRef.current && listRef.current.querySelector(`button[data-idx="${ni}"]`);
    if (btn) btn.focus();
  };
  const onNavKey = (e) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowRight') { e.preventDefault(); step(1); }
    else if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') { e.preventDefault(); step(-1); }
  };

  if (status === 'missing') {
    return (
      <div className={styles.missing}>
        <p>
          The live harness needs the generated tiles + reference plots. Build them locally with{' '}
          <code>make docs-harness</code>, then <code>make docs</code> (or <code>cd docs &amp;&amp; npm run start</code>).
        </p>
      </div>
    );
  }

  const eff = effectiveMariner(scn);
  const refPage = scn.refPage;
  const refSrc = `${refsBase}${scn.pdf}/p${refPage}.jpg`;

  return (
    <div className={full ? `${styles.root} ${styles.full}` : styles.root}>
      <aside className={styles.rail}>
        <div className={styles.railHead}>
          <span>Scenarios <small>Chart 1 · S-64</small></span>
          <div className={styles.navbtns}>
            <button type="button" className={styles.fullBtn} onClick={() => step(-1)} title="Previous scenario (↑)">◀</button>
            <button type="button" className={styles.fullBtn} onClick={() => step(1)} title="Next scenario (↓)">▶</button>
            <button type="button" className={styles.fullBtn} onClick={() => setFull((v) => !v)} aria-pressed={full}
              title={full ? 'Exit fullscreen (Esc)' : 'Fullscreen — compare against the spec plots'}>
              {full ? '✕' : '⤢'}
            </button>
          </div>
        </div>
        <ol className={styles.list} ref={listRef} onKeyDown={onNavKey}>
          {SCENARIOS.map((s, idx) => (
            <li key={s.id}>
              <button type="button" data-idx={idx} data-suite={s.suite}
                className={idx === i ? `${styles.item} ${styles.itemActive}` : styles.item}
                onClick={() => setI(idx)}>
                <span className={styles.badge} data-suite={s.suite}>{s.suite === 'chart1' ? 'C1' : 'S64'}</span>
                <span className={styles.ttl}>{s.title.replace(/^(Chart 1|S-64)\s·\s/, '')}</span>
              </button>
            </li>
          ))}
        </ol>
      </aside>

      <section className={styles.pane}>
        <div className={styles.cap}>
          Reference · theirs <span className={styles.grow} />
          <span className={styles.tag}>{scn.pdf} · p{refPage}</span>
        </div>
        <div className={styles.refwrap}>
          {refMissing
            ? <div className={styles.refMissing}>No reference image for this page.<br />Run <code>make docs-harness</code> to extract the PDF crops, then nudge ◀ / ▶ to the exact plot page.</div>
            : <img className={styles.refImg} src={refSrc} alt={`reference plot ${scn.pdf} p${refPage}`} onError={() => setRefMissing(true)} />}
        </div>
      </section>

      <section className={styles.pane}>
        <div className={styles.cap}>
          Ours · live render <span className={styles.scn}>{scn.title}</span><span className={styles.grow} />
          <span className={styles.tag}>1:{scn.cscl.toLocaleString()}</span>
          <span className={styles.tag}>{scn.scheme}</span>
        </div>
        <div className={styles.strip} title="mariner display settings applied for this scenario">
          {marinerParts(eff).map(([k, v]) => (
            <span key={k} className={typeof v === 'boolean' ? `${styles.s} ${v ? styles.on : styles.off}` : styles.s}>
              {k} <b>{typeof v === 'boolean' ? (v ? 'on' : 'off') : v}</b>
            </span>
          ))}
        </div>
        <div className={styles.stage} ref={stageRef}>
          <div className={styles.scaler} ref={scalerRef}>
            {/* widget = read-only viewer; spec = chrome-free clean map; assets = demo
                frontend; catalog = Chart 1 + S-64 tiles. */}
            <chart-plotter ref={plotRef} widget="" spec="" assets={demo} catalog={manifest} basemap="none" />
          </div>
        </div>
      </section>
    </div>
  );
}

export default function TestHarness() {
  return (
    <BrowserOnly fallback={<div className={styles.loading}>Loading the harness…</div>}>
      {() => <Harness />}
    </BrowserOnly>
  );
}
