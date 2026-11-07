/* Shared helpers for the HERTZ admin UI. No external dependencies: the admin
 * page must work offline on a laptop at a venue. */
window.HZ = (() => {
  async function api(path, opts = {}) {
    const headers = Object.assign({}, opts.headers || {});
    headers["X-HERTZ-Admin"] = "1";
    if (opts.body) headers["Content-Type"] = "application/json";
    let res;
    try {
      res = await fetch(path, Object.assign({ cache: "no-store" }, opts, { headers }));
    } catch (err) {
      throw new Error("Cannot reach the admin page: " + err.message);
    }
    const text = await res.text();
    let body = null;
    try { body = text ? JSON.parse(text) : null; } catch { body = text; }
    if (!res.ok) {
      const msg = body && body.error ? body.error : (text || res.statusText);
      throw new Error(msg || ("HTTP " + res.status));
    }
    return body;
  }

  async function copy(text) {
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text);
        return true;
      }
    } catch { /* fall through to the legacy path */ }
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.left = "-9999px";
    document.body.appendChild(ta);
    ta.select();
    let ok = false;
    try { ok = document.execCommand("copy"); } catch { ok = false; }
    ta.remove();
    if (!ok) throw new Error("Your browser blocked clipboard access. Select the text and press Ctrl+C.");
    return true;
  }

  let toastTimer = null;
  function toast(message, kind = "") {
    let el = document.getElementById("hz-toast");
    if (!el) {
      el = document.createElement("div");
      el.id = "hz-toast";
      el.className = "toast";
      document.body.appendChild(el);
    }
    el.textContent = message;
    el.className = "toast show " + kind;
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { el.className = "toast " + kind; }, 4200);
  }

  function fmtBps(bps) {
    const n = Number(bps || 0);
    if (!isFinite(n) || n <= 0) return "0 kbps";
    if (n >= 1e6) return (n / 1e6).toFixed(2) + " Mbps";
    return (n / 1e3).toFixed(1) + " kbps";
  }

  function fmtNum(n) {
    const v = Number(n || 0);
    if (!isFinite(v)) return "0";
    return new Intl.NumberFormat("en-US", { maximumFractionDigits: 0 }).format(v);
  }

  function fmtAge(seconds) {
    const s = Number(seconds || 0);
    if (!isFinite(s) || s < 0) return "never";
    if (s < 1) return "just now";
    if (s < 60) return Math.round(s) + "s ago";
    if (s < 3600) return Math.round(s / 60) + "m ago";
    return Math.round(s / 3600) + "h ago";
  }

  function fmtDuration(seconds) {
    const s = Math.max(0, Number(seconds || 0));
    const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60);
    if (h > 0) return h + "h " + m + "m";
    if (m > 0) return m + "m";
    return Math.round(s % 60) + "s";
  }

  function $(sel) { return document.querySelector(sel); }
  function $all(sel) { return Array.from(document.querySelectorAll(sel)); }

  return { api, copy, toast, fmtBps, fmtNum, fmtAge, fmtDuration, $, $all };
})();
