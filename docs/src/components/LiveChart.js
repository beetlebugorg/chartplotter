import React, {useEffect} from 'react';
import BrowserOnly from '@docusaurus/BrowserOnly';
import useBaseUrl from '@docusaurus/useBaseUrl';

// LiveChart embeds the read-only "widget" build of <chart-plotter> live in the
// page. The chart, MapLibre, and every asset (tiles, sprite, glyphs, basemap,
// catalog) load from the prebaked demo bundle the docs build assembles under
// /<baseUrl>/demo/ — see `make demo` and .github/workflows/docs.yml. Build it
// locally first with: make demo DEMO_OUT=docs/static/demo
function Chart() {
  // useBaseUrl prefixes the site baseUrl, e.g. "/chartplotter/demo/". The widget
  // resolves ALL of its assets (incl. vendor/maplibre-gl.js and charts-index.json)
  // relative to this, so the whole demo is self-contained in that one directory.
  const base = useBaseUrl('/demo/');
  useEffect(() => {
    const id = 'chartplotter-widget-module';
    if (document.getElementById(id)) return; // define <chart-plotter> once
    const s = document.createElement('script');
    s.type = 'module';
    s.id = id;
    s.src = `${base}src/chartplotter.mjs`;
    document.head.appendChild(s);
  }, [base]);
  return (
    <>
      <div className="liveChart">
        {/* widget = read-only viewer; assets points every fetch at the demo bundle */}
        <chart-plotter widget="" assets={base} center="-76.482,38.978" zoom="13" />
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
