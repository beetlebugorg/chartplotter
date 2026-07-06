// fps-meter.mjs — a tiny live FPS + frame-time overlay for performance work.
//
// A requestAnimationFrame loop measures the delta between frames; the DOM
// readout refreshes only ~3x/second (never per frame), so the meter itself
// can't cause the jank it's meant to reveal. Shows current FPS, the average
// frame time over the window, and the WORST frame time (the number that
// actually reads as a stutter). Turns amber when it drops below 50 fps.
//
// Mounted by the shell when the `?fps` query param is present (or via
// setEnabled), so it's an opt-in measurement tool, not default chrome.
export class FpsMeter {
  constructor({ host } = {}) {
    if (!host) return;
    const el = document.createElement("div");
    el.className = "fps-meter";
    el.setAttribute("aria-hidden", "true");
    el.textContent = "— fps";
    host.appendChild(el);
    this._el = el;
    this._frames = 0;
    this._acc = 0;      // accumulated ms in the current window
    this._worst = 0;    // worst single-frame ms in the current window
    this._last = 0;
    this._running = true;
    this._raf = requestAnimationFrame((t) => this._tick(t));
  }

  _tick(t) {
    if (!this._running) return;
    if (this._last) {
      const dt = t - this._last;
      this._frames++;
      this._acc += dt;
      if (dt > this._worst) this._worst = dt;
      if (this._acc >= 400) {
        const fps = Math.round((this._frames * 1000) / this._acc);
        const avg = (this._acc / this._frames).toFixed(1);
        this._el.textContent = `${fps} fps · ${avg}ms avg · ${this._worst.toFixed(0)}ms max`;
        this._el.dataset.low = fps < 50 ? "1" : "";
        this._frames = 0;
        this._acc = 0;
        this._worst = 0;
      }
    }
    this._last = t;
    this._raf = requestAnimationFrame((t2) => this._tick(t2));
  }

  destroy() {
    this._running = false;
    if (this._raf) cancelAnimationFrame(this._raf);
    if (this._el) this._el.remove();
    this._el = null;
  }
}
