import React, {useEffect, useRef} from 'react';
import BrowserOnly from '@docusaurus/BrowserOnly';
import useBaseUrl from '@docusaurus/useBaseUrl';

// LiveChart embeds the read-only "widget" build of <chart-plotter> live in the
// page. The chart, MapLibre, and every asset (tiles, sprite, glyphs, basemap,
// catalog) load from the prebaked demo bundle the docs build assembles under
// /<baseUrl>/demo/ — see `make demo` and .github/workflows/docs.yml. Build it
// locally first with: make demo DEMO_OUT=docs/static/demo

// The demo opens on Annapolis harbour at 1:6090 (a detailed harbour view). Widget
// mode is HERMETIC — it ignores localStorage and boots from DEFAULT_MARINER — so the
// non-default display state we want for the demo (Display Other + scale boundaries)
// is forced at ready, and the scale is pinned via the zoom attribute below.
const CENTER = [-76.48167, 38.975]; // Annapolis, MD — 38°58.5′N 076°28.9′W
const SCALE = 6090;               // display scale denominator (1:6090)
// scale → MapLibre zoom (512-tile resolution, default 0.2645 mm CSS pixel — the
// widget's DEFAULT_PX_PITCH_MM), so scaleDenomPhysical reads ~1:6090 in the HUD.
const M_PER_PX_Z0 = 78271.516964020485;
const PX_PITCH_M = 0.0002645;
const ZOOM = Math.log2(
  (M_PER_PX_Z0 * Math.cos((CENTER[1] * Math.PI) / 180)) / (PX_PITCH_M * SCALE),
);

function Chart() {
  // useBaseUrl prefixes the site baseUrl, e.g. "/chartplotter/demo/". The widget
  // resolves ALL of its assets (incl. vendor/maplibre-gl.js and charts-index.json)
  // relative to this, so the whole demo is self-contained in that one directory.
  const base = useBaseUrl('/demo/');
  const ref = useRef(null);
  useEffect(() => {
    const id = 'chartplotter-widget-module';
    if (document.getElementById(id)) return; // define <chart-plotter> once
    const s = document.createElement('script');
    s.type = 'module';
    s.id = id;
    s.src = `${base}src/chartplotter.mjs`;
    document.head.appendChild(s);
  }, [base]);

  // Once the map is ready, force the demo's display state (widget mode is hermetic, so
  // these aren't persisted): Display category Other on, chart scale boundaries on.
  useEffect(() => {
    let tries = 0;
    const iv = setInterval(() => {
      const el = ref.current;
      if (el && el.map) {
        clearInterval(iv);
        if (typeof el.applyMariner === 'function') {
          try { el.applyMariner({displayOther: true, showScaleBoundaries: true}); } catch (e) { /* best-effort */ }
        }
      } else if (++tries > 60) {
        clearInterval(iv);
      }
    }, 200);
    return () => clearInterval(iv);
  }, []);

  return (
    <>
      <div className="liveChart">
        {/* widget = read-only viewer; assets points every fetch at the demo bundle */}
        <chart-plotter
          ref={ref}
          widget=""
          assets={base}
          center={CENTER.join(',')}
          zoom={ZOOM.toFixed(3)}
        />
      </div>
      {/* Plain <a> (not a router Link) → full-page nav to the static bundle. */}
      <p className="liveChart__caption">
        <a href={base}>Open the chart full-screen →</a>
      </p>
    </>
  );
}

export default function LiveChart() {
  return (
    <BrowserOnly
      fallback={<div className="liveChart liveChart--loading">Loading live chart…</div>}
    >
      {() => <Chart />}
    </BrowserOnly>
  );
}
