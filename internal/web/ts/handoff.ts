// Live view island: the run's browser as a canvas over a WebSocket. While the run waits for the user, mouse and
// keyboard go back to the browser. Texts come from the page (message catalog), never from this file.

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

// Close codes sent by the server (internal/web/handoff.go).
const NOT_LIVE = 4000;
const WAITING = 4001;
const MAX_FAILURES = 8;

type Mode = 'connecting' | 'waiting' | 'view' | 'interactive' | 'ended' | 'reconnecting' | 'unavailable';

function initLiveView(): void {
  const pushBtn = document.getElementById('btn-push');
  pushBtn?.addEventListener('click', () => {
    void enablePush();
  });

  const box = document.getElementById('live-view');
  const canvas = document.getElementById('screencast') as HTMLCanvasElement | null;
  const statusEl = document.getElementById('screen-status');
  const resumeBtn = document.getElementById('btn-resume') as HTMLButtonElement | null;
  if (!box || !canvas || !box.dataset.app) return;
  const appId = box.dataset.app;
  const ctx2d = canvas.getContext('2d');

  const texts: Record<Mode, string> = {
    connecting: box.dataset.tConnecting ?? '',
    waiting: box.dataset.tWaiting ?? '',
    view: box.dataset.tViewOnly ?? '',
    interactive: box.dataset.tYourTurn ?? '',
    ended: box.dataset.tEnded ?? '',
    reconnecting: box.dataset.tReconnecting ?? '',
    unavailable: box.dataset.tUnavailable ?? '',
  };

  let ws: WebSocket | null = null;
  let failures = 0;
  let stopped = false;
  let interactive = false;

  function setMode(mode: Mode): void {
    box!.dataset.mode = mode;
    if (statusEl) statusEl.textContent = texts[mode];
    if (resumeBtn) resumeBtn.hidden = mode !== 'interactive';
  }

  function retry(mode: Mode): void {
    failures++;
    if (failures > MAX_FAILURES) {
      setMode('unavailable');
      return;
    }
    setMode(mode);
    const delay = Math.min(1000 * 2 ** (failures - 1), 15000);
    window.setTimeout(connect, delay);
  }

  function connect(): void {
    if (stopped) return;
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const sock = new WebSocket(`${proto}//${location.host}/applications/${appId}/screen`, 'getafjob-screencast');
    sock.binaryType = 'blob';
    ws = sock;

    sock.onmessage = async (e: MessageEvent) => {
      if (typeof e.data === 'string') {
        try {
          const msg = JSON.parse(e.data) as { type?: string; interactive?: boolean };
          if (msg.type === 'status') {
            interactive = msg.interactive === true;
            setMode(interactive ? 'interactive' : 'view');
          }
        } catch {
          // ignore malformed status
        }
        return;
      }
      if (!(e.data instanceof Blob) || !ctx2d) return;
      try {
        const bmp = await createImageBitmap(e.data);
        failures = 0;
        if (canvas!.width !== bmp.width || canvas!.height !== bmp.height) {
          canvas!.width = bmp.width;
          canvas!.height = bmp.height;
        }
        ctx2d.drawImage(bmp, 0, 0);
        bmp.close();
      } catch {
        // drop a corrupt frame
      }
    };

    sock.onclose = (ev: CloseEvent) => {
      ws = null;
      interactive = false;
      if (stopped) return;
      if (ev.code === NOT_LIVE || (ev.code === 1000 && ev.reason === 'session ended')) {
        stopped = true;
        setMode('ended');
        // The run finished or parked: reload once so the page shows the result and evidence.
        window.setTimeout(() => location.reload(), 1500);
        return;
      }
      retry(ev.code === WAITING ? 'waiting' : 'reconnecting');
    };
  }

  function send(msg: object): void {
    if (interactive && ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(msg));
    }
  }

  function point(e: MouseEvent): { x: number; y: number } {
    const rect = canvas!.getBoundingClientRect();
    return {
      x: ((e.clientX - rect.left) * canvas!.width) / rect.width,
      y: ((e.clientY - rect.top) * canvas!.height) / rect.height,
    };
  }

  function mouse(action: string, e: MouseEvent): void {
    const { x, y } = point(e);
    const button = action === 'mouseMoved' ? 'none' : (['left', 'middle', 'right'][e.button] ?? 'none');
    send({ type: 'mouse', action, x, y, button, clickCount: e.detail || 1 });
  }

  let lastMove = 0;
  canvas.addEventListener('mousemove', (e) => {
    const now = performance.now();
    if (now - lastMove < 50) return; // about 20 moves per second are enough for hover menus
    lastMove = now;
    mouse('mouseMoved', e);
  });
  canvas.addEventListener('mousedown', (e) => {
    canvas.focus();
    mouse('mousePressed', e);
  });
  canvas.addEventListener('mouseup', (e) => mouse('mouseReleased', e));
  canvas.addEventListener('contextmenu', (e) => e.preventDefault());

  function key(action: string, e: KeyboardEvent): void {
    if (!interactive) return;
    send({
      type: 'key',
      action,
      key: e.key,
      code: e.code,
      text: action === 'keyDown' && e.key.length === 1 ? e.key : '',
      windowsVirtualKeyCode: e.keyCode,
    });
    e.preventDefault();
  }
  canvas.addEventListener('keydown', (e) => key('keyDown', e));
  canvas.addEventListener('keyup', (e) => key('keyUp', e));

  resumeBtn?.addEventListener('click', () => send({ type: 'resume' }));

  window.addEventListener('pagehide', () => {
    stopped = true;
    ws?.close();
  });

  setMode('connecting');
  connect();
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initLiveView);
} else {
  initLiveView();
}
