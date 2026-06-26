// notification-center.mjs — the app-level notification + task-progress bus.
//
// The shell owns ONE instance and renders into its existing chrome (the
// databox/db-prog progress row + transient banners); feature components and
// plugins (chart-library now; own-ship/AIS later) post to it instead of
// touching the shell's DOM. That keeps progress reporting a shared app service
// rather than something every plugin re-implements.
//
//   const notify = new NotificationCenter({
//     onProgress: (p) => shell._setNotification(p),   // p = {label, sub, detail, frac, error} | null
//     onMessage:  (m) => shell._toast(m),             // m = {level, msg}
//   });
//   const t = notify.task("download:noaa-d5", { label: "NOAA · Mid-Atlantic" });
//   t.progress(0.4, "Generating coastal…", "12 / 30 tiles");  // frac, action, count
//   t.done();                                          // or t.fail(err)
//   notify.warn("No charts are enabled");
//
// Concurrent tasks collapse to a single progress surface: the most-recently
// updated active task wins (one job bar, not a stack). A failed task lingers on
// the surface (so the error is seen) until the next task starts or it's cleared.

export class NotificationCenter {
  constructor({ onProgress, onMessage } = {}) {
    this._onProgress = onProgress || (() => {});
    this._onMessage = onMessage || (() => {});
    this._tasks = new Map(); // id → {label, frac, sub, error, done}
    this._lastFailed = false; // keep an error on screen until something replaces it
  }

  // Begin (or re-open) a task; returns a handle. The handle's methods are the
  // only way to drive the progress surface — callers never format the payload.
  task(id, { label = "" } = {}) {
    const t = { id, label, frac: null, sub: "", detail: "", error: null, done: false };
    this._tasks.set(id, t);
    this._lastFailed = false;
    this._render(t);
    const update = (patch) => {
      if (!this._tasks.has(id)) return; // ignore updates after done/fail
      Object.assign(t, patch);
      this._render(t);
    };
    return {
      // frac: 0..1 or null (indeterminate); sub: the live action line; detail: the
      // count-with-unit beside it. Pass null to leave either unchanged.
      progress: (frac, sub, detail) => update({ frac, sub: sub == null ? t.sub : sub, detail: detail == null ? t.detail : detail }),
      label: (l) => update({ label: l }),
      done: () => { t.frac = 1; t.done = true; this._render(t); this._end(id, false); },
      fail: (err) => { t.error = err ? String((err && err.message) || err) : "Failed"; t.done = true; this._render(t); this._end(id, true); },
    };
  }

  _render(t) { this._onProgress({ label: t.label, sub: t.sub, detail: t.detail, frac: t.frac, error: t.error }); }

  _end(id, failed) {
    this._tasks.delete(id);
    const next = [...this._tasks.values()].pop();
    if (next) { this._lastFailed = false; this._render(next); return; }
    if (failed) { this._lastFailed = true; return; } // leave the error showing
    this._onProgress(null); // nothing active, last one succeeded → clear the bar
  }

  // Clear any lingering surface (e.g. a finished/failed job) on demand.
  clear() { if (!this._tasks.size) { this._lastFailed = false; this._onProgress(null); } }

  // Transient banners / toasts. Levels drive styling in the shell's renderer.
  info(msg) { this._onMessage({ level: "info", msg }); }
  warn(msg) { this._onMessage({ level: "warn", msg }); }
  error(msg) { this._onMessage({ level: "error", msg }); }
}
