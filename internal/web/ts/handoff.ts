// Handoff island (#31): CDP screencast on canvas, mouse and key dispatch back over WebSocket, and Web Push registration.

function urlBase64ToUint8Array(base64String: string): Uint8Array {
  const padding = '='.repeat((4 - (base64String.length % 4)) % 4);
  const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
  const raw = atob(base64);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) {
    out[i] = raw.charCodeAt(i);
  }
  return out;
}

async function enablePush(): Promise<void> {
  if (!('serviceWorker' in navigator) || !('PushManager' in window)) return;
  const reg = await navigator.serviceWorker.register('/sw.js', { scope: '/' });
  const keyRes = await fetch('/push/key');
  if (!keyRes.ok) return;
  const { publicKey } = (await keyRes.json()) as { publicKey?: string };
  if (!publicKey) return;
  const sub = await reg.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: urlBase64ToUint8Array(publicKey),
  });
  await fetch('/push/subscribe', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(sub.toJSON()),
  });
}

function initHandoff(): void {
  const pushBtn = document.getElementById('btn-push');
  if (pushBtn) {
    pushBtn.addEventListener('click', () => {
      void enablePush();
    });
  }

  const canvas = document.getElementById('screencast') as HTMLCanvasElement | null;
  if (!canvas) return;

  const appId = canvas.dataset.app;
  if (!appId) return;

  const fallbackUrl = canvas.dataset.fallback;
  let receivedFrame = false;

  // Render fallback snapshot if present while waiting for live frames
  if (fallbackUrl) {
    const img = new Image();
    img.onload = () => {
      if (!receivedFrame) {
        const ctx = canvas.getContext('2d');
        if (ctx) {
          canvas.width = img.naturalWidth || 1280;
          canvas.height = img.naturalHeight || 800;
          ctx.drawImage(img, 0, 0);
        }
      }
    };
    img.src = fallbackUrl;
  }

  const statusEl = document.getElementById('screen-status');
  function updateStatus(text: string): void {
    if (statusEl) statusEl.textContent = text;
  }

  let ws: WebSocket | null = null;
  let reconnectTimer: number | null = null;
  let retryDelay = 1000;
  const maxRetryDelay = 5000;

  function connect(): void {
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }

    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(`${proto}//${location.host}/applications/${appId}/screen`, 'getafjob-screencast');
    ws.binaryType = 'blob';

    updateStatus('Connecting to remote browser...');

    ws.onopen = () => {
      retryDelay = 1000;
      updateStatus('Connected to remote browser');
    };

    ws.onmessage = async (e: MessageEvent) => {
      if (e.data instanceof Blob) {
        try {
          const bmp = await createImageBitmap(e.data);
          const ctx = canvas.getContext('2d');
          if (ctx) {
            receivedFrame = true;
            if (canvas.width !== bmp.width || canvas.height !== bmp.height) {
              canvas.width = bmp.width;
              canvas.height = bmp.height;
            }
            ctx.drawImage(bmp, 0, 0);
            updateStatus('Live streaming active');
          }
        } catch (_) {
          // drop corrupt frames
        }
      }
    };

    ws.onclose = (ev) => {
      ws = null;
      if (ev.code === 1000 && ev.reason === 'session ended') {
        updateStatus('Remote browser session ended');
        return;
      }
      const delaySec = Math.round(retryDelay / 1000);
      updateStatus(`Disconnected. Reconnecting in ${delaySec}s...`);
      reconnectTimer = window.setTimeout(() => {
        retryDelay = Math.min(retryDelay * 1.5, maxRetryDelay);
        connect();
      }, retryDelay);
    };

    ws.onerror = () => {
      updateStatus('Connection error. Reconnecting...');
    };
  }

  connect();

  function sendMouse(action: string, e: MouseEvent): void {
    if (!canvas || !ws || ws.readyState !== WebSocket.OPEN) return;
    const rect = canvas.getBoundingClientRect();
    const scaleX = canvas.width / rect.width;
    const scaleY = canvas.height / rect.height;
    const x = (e.clientX - rect.left) * scaleX;
    const y = (e.clientY - rect.top) * scaleY;
    let button = 'none';
    if (e.button === 0) button = 'left';
    else if (e.button === 1) button = 'middle';
    else if (e.button === 2) button = 'right';

    ws.send(JSON.stringify({
      type: 'mouse',
      action,
      x,
      y,
      button,
      clickCount: e.detail || 1,
    }));
  }

  canvas.addEventListener('mousemove', (e) => sendMouse('mouseMoved', e));
  canvas.addEventListener('mousedown', (e) => {
    canvas.focus();
    sendMouse('mousePressed', e);
  });
  canvas.addEventListener('mouseup', (e) => sendMouse('mouseReleased', e));
  canvas.addEventListener('contextmenu', (e) => e.preventDefault());

  canvas.addEventListener('keydown', (e) => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({
      type: 'key',
      action: 'keyDown',
      key: e.key,
      code: e.code,
      text: e.key.length === 1 ? e.key : '',
      windowsVirtualKeyCode: e.keyCode,
    }));
    if (['Tab', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', ' '].includes(e.key)) {
      e.preventDefault();
    }
  });

  canvas.addEventListener('keyup', (e) => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({
      type: 'key',
      action: 'keyUp',
      key: e.key,
      code: e.code,
      text: e.key.length === 1 ? e.key : '',
      windowsVirtualKeyCode: e.keyCode,
    }));
  });

  const resumeBtn = document.getElementById('btn-resume');
  if (resumeBtn) {
    resumeBtn.addEventListener('click', () => {
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resume' }));
      }
    });
  }

  window.addEventListener('beforeunload', () => {
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer);
    }
    if (ws) {
      ws.close();
    }
  });
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initHandoff);
} else {
  initHandoff();
}
