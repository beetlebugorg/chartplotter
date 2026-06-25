// VesselStateStore is the client's single source of truth for live NMEA0183
// vessel state. It subscribes to the server's /api/vessel/stream (coalesced
// deltas over SSE), holds the latest snapshot, and notifies listeners. Render
// plugins (own-ship marker, AIS overlay, instrument HUD) read from it; it owns
// no DOM and renders nothing — mirroring the server-side "a source, not a
// renderer" split.
//
// The feed requires the Go server, so in widget (server-less, prebaked) mode it
// stays idle. Where EventSource is unavailable it falls back to polling.

export class VesselStateStore {
  constructor({ assets = "/", widget = false } = {}) {
    this._assets = assets;
    this._widget = widget;
    this._state = {};
    this._listeners = new Set();
    this._es = null;
    this._polling = false;
  }

  /** Latest vessel-state snapshot (the JSON from /api/vessel). */
  get state() {
    return this._state;
  }

  /** Subscribe to state changes; returns an unsubscribe function. */
  onChange(fn) {
    this._listeners.add(fn);
    return () => this._listeners.delete(fn);
  }

  _emit() {
    for (const fn of this._listeners) {
      try {
        fn(this._state);
      } catch (e) {
        console.warn("[vessel] listener", e);
      }
    }
  }

  /** Begin streaming. No-op in widget (no server feed) or if already started. */
  start() {
    if (this._widget || this._es || this._polling) return;
    if (!window.EventSource) {
      this._poll();
      return;
    }
    const es = new EventSource(this._assets + "api/vessel/stream");
    es.onmessage = (ev) => {
      let s;
      try {
        s = JSON.parse(ev.data);
      } catch {
        return;
      }
      this._state = s;
      this._emit();
    };
    es.onerror = () => {
      // EventSource reconnects on its own; nothing to do.
    };
    this._es = es;
  }

  /** Stop streaming and release the connection. */
  stop() {
    if (this._es) {
      this._es.close();
      this._es = null;
    }
    this._polling = false;
  }

  async _poll() {
    this._polling = true;
    while (this._polling) {
      try {
        const r = await fetch(this._assets + "api/vessel", { cache: "no-store" });
        if (r.ok) {
          this._state = await r.json();
          this._emit();
        }
      } catch {
        // ignore; retry on the next tick
      }
      await new Promise((res) => setTimeout(res, 1000));
    }
  }
}
