// OrientationControl — a compass button for chart orientation, mounted as a round
// `.rbtn` in the shell's top-right control group (alongside charts/scheme/settings)
// so it matches the app's design language.
//
// S-52 / IMO display orientation (Ed 6.1.1 §3.1.6): the chart may be shown
// north-up or in other orientations. Tapping the button CYCLES through the enabled
// modes (each tap = the next mode) and reorients the map if that mode implies a
// bearing; the compass needle always tracks the live bearing so true north is
// visible at a glance.
//
// Modes are driven through the renderer's sealed camera API (chart-canvas
// `setCameraMode`), never by poking the map directly, so when an <own-ship> plugin
// lands the cycle just grows: insert "course-up"/"head-up" into `_modes` and the
// renderer rotates to the fix's course/heading. Today there is no GPS/own-ship
// feed, so the cycle is the two modes that need no vessel data: north-up (snap
// bearing to 0) and free (user rotation).
//
// Built like the other host-mounted controllers (see plugins/hud.mjs): the shell
// constructs it on `ready` with { host, map, plotter } and calls destroy() to tear
// it down.
export class OrientationControl {
  constructor({ host, map, plotter } = {}) {
    this._map = map;
    this._plotter = plotter; // <chart-canvas> — setCameraMode (sealed API)
    // Ordered tap cycle. Extend with "course-up"/"head-up" once a heading/course
    // source exists; the renderer already understands "course-up".
    this._modes = ["north-up", "free"];
    this._i = 0; // map boots at bearing 0 → north-up
    this._mount(host);
  }

  _mount(host) {
    if (!host || !this._map) return;
    const btn = document.createElement("button");
    btn.className = "rbtn";
    btn.type = "button";
    btn.setAttribute("aria-label", "Map orientation");
    // Static compass ring + a needle that rotates to point at true north on screen
    // (north half red per convention). Matches the other 24×24 stroke-currentColor
    // chrome icons; the needle group is rotated by -bearing in _syncNeedle.
    btn.innerHTML = `<svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <circle cx="12" cy="12" r="9" stroke="currentColor" stroke-width="1.3" opacity="0.4"/>
      <g class="orient-needle">
        <path d="M12 4.2 L14.6 12.4 L12 10.6 L9.4 12.4 Z" fill="#e23b3b"/>
        <path d="M12 19.8 L9.4 11.6 L12 13.4 L14.6 11.6 Z" fill="currentColor" opacity="0.5"/>
      </g>
    </svg>`;
    host.appendChild(btn);
    this._btn = btn;
    this._needle = btn.querySelector(".orient-needle");
    this._needle.style.transformOrigin = "12px 12px";
    this._needle.style.transition = "transform .12s linear";

    this._onClick = () => this._cycle();
    btn.addEventListener("click", this._onClick);
    // Keep the needle aligned with true north as the user rotates the map.
    this._onRotate = () => this._syncNeedle();
    this._map.on("rotate", this._onRotate);

    this._syncNeedle();
    this._reflectMode();
  }

  destroy() {
    if (this._btn) {
      this._btn.removeEventListener("click", this._onClick);
      this._btn.remove();
    }
    if (this._map) this._map.off("rotate", this._onRotate);
    this._btn = this._needle = this._map = null;
  }

  _cycle() {
    this._i = (this._i + 1) % this._modes.length;
    const mode = this._modes[this._i];
    // Drive the renderer's camera state. setCameraMode("north-up") eases bearing→0;
    // "free" releases to user rotation; future modes follow the own-ship fix.
    if (this._plotter && this._plotter.setCameraMode) this._plotter.setCameraMode(mode);
    else if (mode === "north-up" && this._map) this._map.easeTo({ bearing: 0, duration: 300 });
    this._reflectMode();
  }

  _reflectMode() {
    if (!this._btn) return;
    const mode = this._modes[this._i];
    this._btn.title = "Orientation: " + mode + " — tap to cycle";
  }

  _syncNeedle() {
    if (this._needle && this._map) this._needle.style.transform = `rotate(${-this._map.getBearing()}deg)`;
  }
}
