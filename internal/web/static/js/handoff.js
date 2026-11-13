// ts/handoff.ts
function urlBase64ToUint8Array(base64String) {
  const padding = "=".repeat((4 - base64String.length % 4) % 4);
  const base64 = (base64String + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(base64);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) {
    out[i] = raw.charCodeAt(i);
  }
  return out;
}
async function enablePush() {
  if (!("serviceWorker" in navigator) || !("PushManager" in window)) return;
  const reg = await navigator.serviceWorker.register("/sw.js", { scope: "/" });
  const keyRes = await fetch("/push/key");
  if (!keyRes.ok) return;
  const { publicKey } = await keyRes.json();
  if (!publicKey) return;
  const sub = await reg.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: urlBase64ToUint8Array(publicKey)
  });
  await fetch("/push/subscribe", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(sub.toJSON())
  });
}
var NOT_LIVE = 4e3;
var WAITING = 4001;
var MAX_FAILURES = 8;
function initLiveView(box) {
  const canvas = box.querySelector("[data-screen]");
  const statusEl = box.querySelector("[data-screen-status]");
  const resumeBtn = box.querySelector("[data-resume]");
  const answersLink = box.querySelector("[data-answers]");
  const appId = box.dataset.app;
  if (!canvas || !appId) return () => {
  };
  const ctx2d = canvas.getContext("2d");
  const inputOn = box.dataset.input !== "off";
  const minGap = box.dataset.fps ? 1e3 / Number(box.dataset.fps) : 0;
  const texts = {
    connecting: box.dataset.tConnecting ?? "",
    waiting: box.dataset.tWaiting ?? "",
    view: box.dataset.tViewOnly ?? "",
    interactive: box.dataset.tYourTurn ?? "",
    answers: box.dataset.tAnswers ?? "",
    ended: box.dataset.tEnded ?? "",
    reconnecting: box.dataset.tReconnecting ?? "",
    unavailable: box.dataset.tUnavailable ?? ""
  };
  let ws = null;
  let failures = 0;
  let stopped = false;
  let interactive = false;
  let lastDraw = 0;
  let timer = 0;
  function setMode(mode) {
    box.dataset.mode = mode;
    if (statusEl) statusEl.textContent = texts[mode];
    if (resumeBtn) resumeBtn.hidden = mode !== "interactive";
    if (answersLink) {
      answersLink.hidden = mode !== "answers";
      answersLink.href = `/needs-input?app=${encodeURIComponent(appId ?? "")}`;
    }
    if (mode === "interactive" || mode === "answers" || mode === "view") {
      const want = mode === "view" ? "running" : "waiting";
      box.closest(".live-focus")?.querySelectorAll("[data-live-state]").forEach((el) => {
        el.hidden = el.dataset.liveState !== want;
      });
    }
  }
  function retry(mode) {
    failures++;
    if (failures > MAX_FAILURES) {
      setMode("unavailable");
      return;
    }
    setMode(mode);
    const delay = Math.min(1e3 * 2 ** (failures - 1), 15e3);
    timer = window.setTimeout(connect, delay);
  }
  function connect() {
    if (stopped) return;
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const fps = box.dataset.fps ? `?fps=${encodeURIComponent(box.dataset.fps)}` : "";
    const sock = new WebSocket(`${proto}//${location.host}/applications/${appId}/screen${fps}`, "getafjob-screencast");
    sock.binaryType = "blob";
    ws = sock;
    sock.onmessage = async (e) => {
      if (typeof e.data === "string") {
        try {
          const msg = JSON.parse(e.data);
          if (msg.type === "status") {
            interactive = msg.interactive === true;
            setMode(interactive ? msg.answers === true ? "answers" : "interactive" : "view");
          }
        } catch {
        }
        return;
      }
      if (!(e.data instanceof Blob) || !ctx2d) return;
      const now = performance.now();
      if (minGap && now - lastDraw < minGap) return;
      lastDraw = now;
      try {
        const bmp = await createImageBitmap(e.data);
        failures = 0;
        if (canvas.width !== bmp.width || canvas.height !== bmp.height) {
          canvas.width = bmp.width;
          canvas.height = bmp.height;
        }
        ctx2d.drawImage(bmp, 0, 0);
        bmp.close();
      } catch {
      }
    };
    sock.onclose = (ev) => {
      ws = null;
      interactive = false;
      if (stopped) return;
      if (ev.code === NOT_LIVE || ev.code === 1e3 && ev.reason === "session ended") {
        setMode("ended");
        if (box.dataset.onEnd === "reload") {
          stopped = true;
          window.setTimeout(() => {
            if (box.isConnected) location.reload();
          }, 1500);
          return;
        }
        if (ev.code === NOT_LIVE) {
          stopped = true;
          return;
        }
        failures = Math.max(failures, 3);
        retry("ended");
        return;
      }
      retry(ev.code === WAITING ? "waiting" : "reconnecting");
    };
  }
  function send(msg) {
    if (inputOn && interactive && ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(msg));
    }
  }
  if (inputOn) {
    const point = (e) => {
      const rect = canvas.getBoundingClientRect();
      return {
        x: (e.clientX - rect.left) * canvas.width / rect.width,
        y: (e.clientY - rect.top) * canvas.height / rect.height
      };
    };
    const mouse = (action, e) => {
      const { x, y } = point(e);
      const button = action === "mouseMoved" ? "none" : ["left", "middle", "right"][e.button] ?? "none";
      send({ type: "mouse", action, x, y, button, clickCount: e.detail || 1 });
    };
    let lastMove = 0;
    canvas.addEventListener("mousemove", (e) => {
      const now = performance.now();
      if (now - lastMove < 50) return;
      lastMove = now;
      mouse("mouseMoved", e);
    });
    canvas.addEventListener("mousedown", (e) => {
      canvas.focus();
      mouse("mousePressed", e);
    });
    canvas.addEventListener("mouseup", (e) => mouse("mouseReleased", e));
    canvas.addEventListener("contextmenu", (e) => e.preventDefault());
    const key = (action, e) => {
      if (!interactive) return;
      send({
        type: "key",
        action,
        key: e.key,
        code: e.code,
        text: action === "keyDown" && e.key.length === 1 ? e.key : "",
        windowsVirtualKeyCode: e.keyCode
      });
      e.preventDefault();
    };
    canvas.addEventListener("keydown", (e) => key("keyDown", e));
    canvas.addEventListener("keyup", (e) => key("keyUp", e));
    resumeBtn?.addEventListener("click", () => send({ type: "resume" }));
  }
  const stop = () => {
    stopped = true;
    window.clearTimeout(timer);
    ws?.close();
  };
  window.addEventListener("pagehide", stop);
  setMode("connecting");
  connect();
  return stop;
}
var views = /* @__PURE__ */ new Map();
function bindViews() {
  for (const [box, stop] of views) {
    if (!box.isConnected) {
      stop();
      views.delete(box);
    }
  }
  for (const box of document.querySelectorAll("[data-live-view]")) {
    if (!views.has(box) && box.dataset.app) views.set(box, initLiveView(box));
  }
  const push = document.getElementById("btn-push");
  if (push && !push.dataset.bound) {
    push.dataset.bound = "1";
    push.addEventListener("click", () => {
      enablePush().catch(() => void 0);
    });
  }
}
var DOCK_RUN = "getafjob.dock.run";
var DOCK_OPEN = "getafjob.dock.open";
function store(key, value) {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
  }
}
function load(key) {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}
function dockParts() {
  const dock = document.querySelector("[data-dock]");
  return {
    dock,
    body: document.getElementById("dock-body"),
    toggle: dock?.querySelector("[data-dock-toggle]") ?? null,
    screen: dock?.querySelector("[data-dock-screen]") ?? null,
    runs: dock?.querySelector("[data-dock-runs]") ?? null,
    tpl: dock?.querySelector("template[data-dock-template]") ?? null,
    countText: dock?.querySelector("[data-t-count]")?.dataset.tCount ?? "{n}"
  };
}
function showRun(appId) {
  const { screen, tpl, runs } = dockParts();
  if (!screen || !tpl) return;
  const current = screen.querySelector("[data-live-view]");
  if (current?.dataset.app === appId) return;
  screen.replaceChildren();
  if (appId) {
    const view = tpl.content.firstElementChild.cloneNode(true);
    view.dataset.app = appId;
    screen.append(view);
  }
  for (const b of runs?.querySelectorAll("[data-dock-run]") ?? []) {
    b.setAttribute("aria-pressed", String(b.dataset.dockRun === appId));
  }
  bindViews();
}
function setOpen(open) {
  const { body, toggle, dock } = dockParts();
  if (!body || !toggle || !dock) return;
  body.hidden = !open;
  toggle.setAttribute("aria-expanded", String(open));
  dock.classList.toggle("dock-open", open);
  store(DOCK_OPEN, open ? "1" : null);
  syncDock();
}
function syncDock() {
  const { dock, runs, countText } = dockParts();
  if (!dock || !runs) return;
  const ids = [...runs.querySelectorAll("[data-dock-run]")].map((b) => b.dataset.dockRun ?? "");
  dock.hidden = ids.length === 0;
  const count = dock.querySelector("[data-dock-count]");
  if (count) count.textContent = countText.replace("{n}", String(ids.length));
  if (ids.length === 0 || dock.querySelector("#dock-body")?.hasAttribute("hidden")) {
    showRun(null);
    return;
  }
  const saved = load(DOCK_RUN);
  showRun(saved && ids.includes(saved) ? saved : ids[0]);
}
document.addEventListener("click", (e) => {
  const t = e.target;
  const toggle = t.closest("[data-dock-toggle]");
  if (toggle) {
    setOpen(toggle.getAttribute("aria-expanded") !== "true");
    return;
  }
  const run = t.closest("[data-dock-run]");
  if (run?.dataset.dockRun) {
    store(DOCK_RUN, run.dataset.dockRun);
    showRun(run.dataset.dockRun);
    return;
  }
  const size = t.closest("[data-dock-size-set]");
  if (size?.dataset.dockSizeSet) {
    setLayout(void 0, size.dataset.dockSizeSet);
    return;
  }
  if (t.closest("[data-dock-side-toggle]")) {
    setLayout(dockSlot()?.dataset.dockSide === "left" ? "right" : "left", void 0);
    return;
  }
  if (t.closest("[data-dock-detach]")) {
    const current = dockParts().screen?.querySelector("[data-live-view]")?.dataset.app;
    window.open(
      current ? `/live?run=${encodeURIComponent(current)}` : "/live",
      "getafjob-live",
      "popup,width=960,height=720"
    );
    setOpen(false);
  }
});
function dockSlot() {
  return document.getElementById("live-dock");
}
function syncLayout() {
  const slot = dockSlot();
  for (const b of slot?.querySelectorAll("[data-dock-size-set]") ?? []) {
    b.setAttribute("aria-pressed", String(b.dataset.dockSizeSet === slot?.dataset.dockSize));
  }
}
function setLayout(side, size) {
  const slot = dockSlot();
  if (!slot) return;
  if (side) slot.dataset.dockSide = side;
  if (size) slot.dataset.dockSize = size;
  syncLayout();
  const body = new URLSearchParams({ side: slot.dataset.dockSide ?? "right", size: slot.dataset.dockSize ?? "md" });
  void fetch("/live/dock/layout", { method: "POST", body, credentials: "same-origin" }).then((r) => r.text()).catch(() => {
  });
}
document.addEventListener("htmx:after:settle", () => {
  bindViews();
  syncDock();
});
function start() {
  syncLayout();
  const { dock } = dockParts();
  if (dock) {
    dock.hidden = true;
    if (load(DOCK_OPEN)) setOpen(true);
  }
  bindViews();
}
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", start);
} else {
  start();
}
