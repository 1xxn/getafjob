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
  const canvas = box.querySelector("#screencast");
  const statusEl = box.querySelector("#screen-status");
  const resumeBtn = box.querySelector("#btn-resume");
  if (!canvas || !box.dataset.app) return () => {
  };
  const appId = box.dataset.app;
  const ctx2d = canvas.getContext("2d");
  const texts = {
    connecting: box.dataset.tConnecting ?? "",
    waiting: box.dataset.tWaiting ?? "",
    view: box.dataset.tViewOnly ?? "",
    interactive: box.dataset.tYourTurn ?? "",
    ended: box.dataset.tEnded ?? "",
    reconnecting: box.dataset.tReconnecting ?? "",
    unavailable: box.dataset.tUnavailable ?? ""
  };
  let ws = null;
  let failures = 0;
  let stopped = false;
  let interactive = false;
  function setMode(mode) {
    box.dataset.mode = mode;
    if (statusEl) statusEl.textContent = texts[mode];
    if (resumeBtn) resumeBtn.hidden = mode !== "interactive";
  }
  function retry(mode) {
    failures++;
    if (failures > MAX_FAILURES) {
      setMode("unavailable");
      return;
    }
    setMode(mode);
    const delay = Math.min(1e3 * 2 ** (failures - 1), 15e3);
    window.setTimeout(connect, delay);
  }
  function connect() {
    if (stopped) return;
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const sock = new WebSocket(`${proto}//${location.host}/applications/${appId}/screen`, "getafjob-screencast");
    sock.binaryType = "blob";
    ws = sock;
    sock.onmessage = async (e) => {
      if (typeof e.data === "string") {
        try {
          const msg = JSON.parse(e.data);
          if (msg.type === "status") {
            interactive = msg.interactive === true;
            setMode(interactive ? "interactive" : "view");
          }
        } catch {
        }
        return;
      }
      if (!(e.data instanceof Blob) || !ctx2d) return;
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
        stopped = true;
        setMode("ended");
        window.setTimeout(() => {
          if (box.isConnected) location.reload();
        }, 1500);
        return;
      }
      retry(ev.code === WAITING ? "waiting" : "reconnecting");
    };
  }
  function send(msg) {
    if (interactive && ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(msg));
    }
  }
  function point(e) {
    const rect = canvas.getBoundingClientRect();
    return {
      x: (e.clientX - rect.left) * canvas.width / rect.width,
      y: (e.clientY - rect.top) * canvas.height / rect.height
    };
  }
  function mouse(action, e) {
    const { x, y } = point(e);
    const button = action === "mouseMoved" ? "none" : ["left", "middle", "right"][e.button] ?? "none";
    send({ type: "mouse", action, x, y, button, clickCount: e.detail || 1 });
  }
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
  function key(action, e) {
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
  }
  canvas.addEventListener("keydown", (e) => key("keyDown", e));
  canvas.addEventListener("keyup", (e) => key("keyUp", e));
  resumeBtn?.addEventListener("click", () => send({ type: "resume" }));
  const stop = () => {
    stopped = true;
    ws?.close();
  };
  window.addEventListener("pagehide", stop);
  setMode("connecting");
  connect();
  return stop;
}
var current = null;
function bind() {
  const push = document.getElementById("btn-push");
  if (push && !push.dataset.bound) {
    push.dataset.bound = "1";
    push.addEventListener("click", () => {
      void enablePush();
    });
  }
  const box = document.getElementById("live-view");
  if (current && current.box !== box) {
    current.stop();
    current = null;
  }
  if (box && !current) {
    current = { box, stop: initLiveView(box) };
  }
}
document.addEventListener("htmx:after:settle", bind);
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", bind);
} else {
  bind();
}
