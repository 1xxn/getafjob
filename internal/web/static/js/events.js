// ts/events.ts
var LIVE = /* @__PURE__ */ new Set(["queued", "running", "needs_input", "submitting"]);
function initEvents() {
  if (!("EventSource" in window) || !document.body.dataset.events) return;
  const source = new EventSource("/events");
  source.addEventListener("app", (e) => {
    let ev;
    try {
      ev = JSON.parse(e.data);
    } catch {
      return;
    }
    document.body.dispatchEvent(new CustomEvent("getafjob:app", { detail: ev }));
    const watched = document.querySelector("[data-watch-app]");
    if (watched && watched.dataset.watchApp === ev.app) {
      const shown = watched.dataset.watchState ?? "";
      if (LIVE.has(shown) !== LIVE.has(ev.to)) {
        location.reload();
      }
    }
  });
  source.addEventListener("jobs", (e) => {
    let detail = null;
    try {
      detail = JSON.parse(e.data);
    } catch {
      return;
    }
    document.body.dispatchEvent(new CustomEvent("getafjob:jobs", { detail }));
  });
  window.addEventListener("pagehide", () => source.close());
}
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", initEvents);
} else {
  initEvents();
}
