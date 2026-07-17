// calibration.mjs — a "Calibration" settings tab that makes the chart render at TRUE
// physical size on THIS screen. S-52 features are drawn at their real millimetre size
// (icons, line weights, text), which only works if the app knows the screen's actual
// CSS-pixel pitch. We can't read that from the browser, so the user calibrates it the
// ECDIS way: a reference box that should be exactly 5 mm wide (the S-52 CHKSYM check
// box), measured with a ruler. They enter what they actually measure and the whole
// chart rescales.
//
// Registers itself as a settings contribution with a render(host) custom slot, like
// the Advanced/dev-tools tab. Pure UI: the only app coupling is reading the current
// pitch (app._pxPitch) and calling app.setPxPitch(mm), which persists + re-renders.

import { DEFAULT_PX_PITCH_MM, clampPxPitch } from "../lib/util.mjs";

const REF_MM = 5; // the S-52 size-check box (CHKSYM01) is 5 mm × 5 mm

// A feature P mm wide renders at P/pxPitch CSS px (chart-canvas _featureSizeScale), so
// the reference box uses the same mapping — it's a faithful proxy for a 5 mm feature.
const boxPx = (pitch) => Math.max(1, Math.round(REF_MM / pitch));

export function calibrationContribution(app) {
  return {
    id: "calibration",
    tab: { id: "vessel", label: "Vessel", tabOrder: 4 },
    group: "Screen calibration",
    order: 4,
    render: (host) => renderCalibration(host, app),
  };
}

function renderCalibration(host, app) {
  const calibrated = typeof app._pxPitch === "number" && app._pxPitch > 0;
  const pitch = clampPxPitch(calibrated ? app._pxPitch : undefined);
  const px = boxPx(pitch);
  host.innerHTML = `
    <style>
      .cal { max-width: 30rem; }
      .cal__intro { font-size: .9rem; line-height: 1.4; margin: 0 0 1rem; }
      .cal__row { display: flex; gap: 1.25rem; align-items: flex-start; flex-wrap: wrap; }
      .cal__box { background: #1b1b1b; outline: 1px solid #888; flex: 0 0 auto; }
      .cal__form { display: flex; flex-direction: column; gap: .6rem; }
      .cal__label { font-size: .85rem; display: flex; flex-direction: column; gap: .25rem; }
      .cal__in { display: inline-flex; align-items: baseline; gap: .35rem; }
      .cal__in input { width: 5rem; }
      .cal__btns { display: flex; gap: .5rem; }
      .cal__cur { font-size: .78rem; opacity: .7; margin: .25rem 0 0; }
      .cal__hint { font-size: .78rem; opacity: .7; margin: .5rem 0 0; }
    </style>
    <div class="cal">
      <p class="cal__intro">Make the chart match real-world size. Hold a ruler to your screen and measure the box — it should be exactly <b>${REF_MM} mm</b> across. Enter what you actually measure and the whole chart (symbols, line weights, text) rescales to true physical size.</p>
      <div class="cal__row">
        <div class="cal__box" style="width:${px}px;height:${px}px"></div>
        <div class="cal__form">
          <label class="cal__label">Measured width
            <span class="cal__in"><input id="cal-mm" type="number" step="0.1" min="1" value="${REF_MM.toFixed(1)}"> mm</span>
          </label>
          <div class="cal__btns">
            <button id="cal-apply" type="button">Apply</button>
            <button id="cal-reset" type="button">Reset</button>
          </div>
          <p class="cal__cur">Pixel pitch: <b>${pitch.toFixed(4)} mm</b>${calibrated ? "" : " (default — uncalibrated)"}</p>
        </div>
      </div>
      <p class="cal__hint">Tip: a wider measurement zooms the chart down, a narrower one up. Apply, then re-measure to confirm the box reads 5 mm.</p>
    </div>`;

  const mmInput = host.querySelector("#cal-mm");
  host.querySelector("#cal-apply").addEventListener("click", () => {
    const measured = parseFloat(mmInput.value);
    if (!(measured > 0)) return;
    const cur = clampPxPitch(typeof app._pxPitch === "number" && app._pxPitch > 0 ? app._pxPitch : undefined);
    // Physical size ∝ 1/pitch, so to turn the measured size into REF_MM scale the
    // pitch by measured/REF (clampPxPitch in setPxPitch guards absurd values).
    app.setPxPitch(cur * (measured / REF_MM));
    renderCalibration(host, app); // redraw at the new calibration to verify
  });
  host.querySelector("#cal-reset").addEventListener("click", () => {
    app.setPxPitch(undefined); // back to the CSS-reference default
    renderCalibration(host, app);
  });
}

export { DEFAULT_PX_PITCH_MM };
