// VgRail — the compact viewing-group quick-toggle rail pinned to the left edge
// of the map (mid-left, clear of the top-left search button and the bottom-left
// scalebar/attribution). One tiny pill per S-52 §14.5 viewing group from
// viewing-groups.mjs: green + "✓" when the group is shown, red + "✕" and a
// struck-through label when hidden (the glyph/strike carry the state for
// colour-blind users, not just the colour). Collapsed by default to a single
// grip button so it takes no room; the open/closed state persists per screen.
//
// Toggling a pill goes through the SAME path as the Settings "Viewing groups"
// tab — core-settings.mjs vgGroupOn/vgSetGroupOn → applyMariner
// ({ viewingGroupsOff }) — so it restyles instantly and persists to
// localStorage + POST /api/settings (shared across screens). The shell calls
// refresh() whenever viewingGroupsOff changes (any writer), keeping the rail,
// the Settings tab and remote screens in step.
//
// Built like the other host-mounted controllers (plugins/hud.mjs,
// orientation-control.mjs): the shell constructs it on `ready` with
// { host, isOn, setOn } and calls destroy() to tear it down. Shell chrome —
// NOT mounted in the hermetic widget viewer (gated on !this._widget in
// chartplotter.mjs), so its localStorage use never runs in an embed.
import { VIEWING_GROUP_SECTIONS } from "../core/viewing-groups.mjs";

const LS_OPEN = "chartplotter:vg-rail-open"; // "1" = expanded (shell mode only; never touched in widget mode)

// OpenBridge-style stacked-layers glyph for the grip (inline, matches the
// stroke style of the shell's corner buttons).
const LAYERS_ICON = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="m12 3 9 5-9 5-9-5 9-5Z"/><path d="m3.6 12.5 8.4 4.7 8.4-4.7"/><path d="m3.6 16.5 8.4 4.7 8.4-4.7"/></svg>`;

export class VgRail {
  constructor({ host, isOn, setOn } = {}) {
    this._isOn = isOn || (() => true);
    this._setOn = setOn || (() => {});
    this._open = localStorage.getItem(LS_OPEN) === "1";
    this._mount(host);
  }

  _mount(host) {
    if (!host) return;
    this._host = host;

    // Grip: the collapse affordance — the rail folds to this one button.
    const grip = document.createElement("button");
    grip.className = "vg-grip";
    grip.type = "button";
    grip.setAttribute("aria-label", "Viewing-group quick toggles");
    grip.setAttribute("aria-expanded", "false");
    grip.innerHTML = LAYERS_ICON;
    this._onGrip = () => this._setOpen(!this._open);
    grip.addEventListener("click", this._onGrip);
    host.appendChild(grip);
    this._grip = grip;

    // Pills: one per viewing group, in taxonomy order, a compact 2-across grid.
    const pills = document.createElement("div");
    pills.className = "vg-pills";
    pills.hidden = true;
    for (const s of VIEWING_GROUP_SECTIONS) {
      for (const g of s.groups) {
        const b = document.createElement("button");
        b.className = "vg-pill";
        b.type = "button";
        b.dataset.g = g.id;
        b.dataset.label = g.label;
        b.dataset.desc = g.desc;
        b.innerHTML = `<span class="vg-glyph"></span><span class="vg-abbr">${g.short || g.label}</span>`;
        pills.appendChild(b);
      }
    }
    this._onPill = (e) => {
      const b = e.target.closest(".vg-pill");
      if (!b) return;
      this._setOn(b.dataset.g, !this._isOn(b.dataset.g)); // → applyMariner → shell calls refresh()
    };
    pills.addEventListener("click", this._onPill);
    host.appendChild(pills);
    this._pills = pills;

    this._setOpen(this._open, true);
    this.refresh();
  }

  destroy() {
    if (this._grip) this._grip.removeEventListener("click", this._onGrip);
    if (this._pills) this._pills.removeEventListener("click", this._onPill);
    if (this._grip) this._grip.remove();
    if (this._pills) this._pills.remove();
    this._grip = this._pills = this._host = null;
  }

  _setOpen(open, boot = false) {
    this._open = !!open;
    if (this._pills) this._pills.hidden = !this._open;
    if (this._grip) {
      this._grip.classList.toggle("on", this._open);
      this._grip.setAttribute("aria-expanded", String(this._open));
      this._grip.title = this._open ? "Hide viewing-group toggles" : "Viewing-group quick toggles";
    }
    if (!boot) localStorage.setItem(LS_OPEN, this._open ? "1" : "0");
  }

  // Re-sync every pill from the mariner state (called by the shell whenever
  // viewingGroupsOff changes — a pill tap, the Settings tab, or a server load).
  refresh() {
    if (!this._pills) return;
    let anyOff = false;
    for (const b of this._pills.querySelectorAll(".vg-pill")) {
      const on = this._isOn(b.dataset.g);
      if (!on) anyOff = true;
      b.classList.toggle("on", on);
      b.classList.toggle("off", !on);
      b.querySelector(".vg-glyph").textContent = on ? "✓" : "✕";
      b.setAttribute("aria-pressed", String(on));
      b.title = `${b.dataset.label} — ${b.dataset.desc} (${on ? "shown — tap to hide" : "hidden — tap to show"})`;
    }
    // Collapsed cue: tint the grip when any group is hidden, so a folded rail
    // still says "the chart is filtered".
    if (this._grip) this._grip.classList.toggle("filtered", anyOff);
  }
}
