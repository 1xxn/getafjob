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
function initHandoff() {
  const pushBtn = document.getElementById("btn-push");
  if (pushBtn) {
    pushBtn.addEventListener("click", () => {
      void enablePush();
    });
  }
  const canvas = document.getElementById("screencast");
  if (!canvas) return;
  const appId = canvas.dataset.app;
  if (!appId) return;
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const ws = new WebSocket(`${proto}//${location.host}/applications/${appId}/screen`, "getafjob-screencast");
  ws.binaryType = "blob";
  const statusEl = document.getElementById("screen-status");
  ws.onopen = () => {
    if (statusEl) statusEl.textContent = "Connected to remote browser";
  };
  ws.onclose = () => {
    if (statusEl) statusEl.textContent = "Disconnected";
  };
  ws.onerror = () => {
    if (statusEl) statusEl.textContent = "Connection error";
  };
  ws.onmessage = async (e) => {
    if (e.data instanceof Blob) {
      try {
        const bmp = await createImageBitmap(e.data);
        const ctx = canvas.getContext("2d");
        if (ctx) {
          if (canvas.width !== bmp.width || canvas.height !== bmp.height) {
            canvas.width = bmp.width;
            canvas.height = bmp.height;
          }
          ctx.drawImage(bmp, 0, 0);
        }
      } catch (_) {
      }
    }
  };
  function sendMouse(action, e) {
    if (!canvas || ws.readyState !== WebSocket.OPEN) return;
    const rect = canvas.getBoundingClientRect();
    const scaleX = canvas.width / rect.width;
    const scaleY = canvas.height / rect.height;
    const x = (e.clientX - rect.left) * scaleX;
    const y = (e.clientY - rect.top) * scaleY;
    let button = "none";
    if (e.button === 0) button = "left";
    else if (e.button === 1) button = "middle";
    else if (e.button === 2) button = "right";
    ws.send(JSON.stringify({
      type: "mouse",
      action,
      x,
      y,
      button,
      clickCount: e.detail || 1
    }));
  }
  canvas.addEventListener("mousemove", (e) => sendMouse("mouseMoved", e));
  canvas.addEventListener("mousedown", (e) => {
    canvas.focus();
    sendMouse("mousePressed", e);
  });
  canvas.addEventListener("mouseup", (e) => sendMouse("mouseReleased", e));
  canvas.addEventListener("contextmenu", (e) => e.preventDefault());
  canvas.addEventListener("keydown", (e) => {
    if (ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({
      type: "key",
      action: "keyDown",
      key: e.key,
      code: e.code,
      text: e.key.length === 1 ? e.key : "",
      windowsVirtualKeyCode: e.keyCode
    }));
    if (["Tab", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", " "].includes(e.key)) {
      e.preventDefault();
    }
  });
  canvas.addEventListener("keyup", (e) => {
    if (ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({
      type: "key",
      action: "keyUp",
      key: e.key,
      code: e.code,
      text: e.key.length === 1 ? e.key : "",
      windowsVirtualKeyCode: e.keyCode
    }));
  });
  const resumeBtn = document.getElementById("btn-resume");
  if (resumeBtn) {
    resumeBtn.addEventListener("click", () => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: "resume" }));
      }
    });
  }
}
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", initHandoff);
} else {
  initHandoff();
}
