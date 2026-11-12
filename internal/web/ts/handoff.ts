// Live view island: a run's browser as a canvas over a WebSocket. Every [data-live-view] on the page connects on
// its own (grid thumbnails, focus view, dock, application page) and closes when it leaves the page. While the run
// waits for the user, mouse and keyboard of a view with controls go back to the browser. The dock in the preserved
// #live-dock slot keeps its picture across boosted navigation. Texts come from the page, never from this file.

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

// initLiveView connects the live view in box and returns a function that closes it.
function initLiveView(box: HTMLElement): () => void {
  const canvas = box.querySelector<HTMLCanvasElement>('[data-screen]');
  const statusEl = box.querySelector<HTMLElement>('[data-screen-status]');
  const resumeBtn = box.querySelector<HTMLButtonElement>('[data-resume]');
  const appId = box.dataset.app;
  if (!canvas || !appId) return () => {};
  const ctx2d = canvas.getContext('2d');
  const inputOn = box.dataset.input !== 'off';
  const minGap = box.dataset.fps ? 1000 / Number(box.dataset.fps) : 0;

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
  let lastDraw = 0;
  let timer = 0;

  function setMode(mode: Mode): void {
    box.dataset.mode = mode;
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
    timer = window.setTimeout(connect, delay);
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
      const now = performance.now();
      if (minGap && now - lastDraw < minGap) return; // thumbnails draw a few frames per second
      lastDraw = now;
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
        setMode('ended');
        if (box.dataset.onEnd === 'reload') {
          // The run finished or parked: reload once so the page shows the result and evidence.
          stopped = true;
          window.setTimeout(() => {
            if (box.isConnected) location.reload();
          }, 1500);
          return;
        }
        // The browser session ended, the run may go on with a new one (after an answer or an approval): try again
        // now and then. A run that is over is refused at once and leaves the grid and the dock on the next event.
        if (ev.code === NOT_LIVE) {
          stopped = true;
          return;
        }
        failures = Math.max(failures, 3);
        retry('ended');
        return;
      }
      retry(ev.code === WAITING ? 'waiting' : 'reconnecting');
    };
  }

  function send(msg: object): void {
    if (inputOn && interactive && ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(msg));
    }
  }

  if (inputOn) {
    const point = (e: MouseEvent): { x: number; y: number } => {
      const rect = canvas.getBoundingClientRect();
      return {
        x: ((e.clientX - rect.left) * canvas.width) / rect.width,
        y: ((e.clientY - rect.top) * canvas.height) / rect.height,
      };
    };
    const mouse = (action: string, e: MouseEvent): void => {
      const { x, y } = point(e);
      const button = action === 'mouseMoved' ? 'none' : (['left', 'middle', 'right'][e.button] ?? 'none');
      send({ type: 'mouse', action, x, y, button, clickCount: e.detail || 1 });
    };
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
    const key = (action: string, e: KeyboardEvent): void => {
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
    };
    canvas.addEventListener('keydown', (e) => key('keyDown', e));
    canvas.addEventListener('keyup', (e) => key('keyUp', e));
    resumeBtn?.addEventListener('click', () => send({ type: 'resume' }));
  }

  const stop = (): void => {
    stopped = true;
    window.clearTimeout(timer);
    ws?.close();
  };
  window.addEventListener('pagehide', stop);

  setMode('connecting');
  connect();
  return stop;
}

// --- binding after every swap ---

const views = new Map<HTMLElement, () => void>();

function bindViews(): void {
  for (const [box, stop] of views) {
    if (!box.isConnected) {
      stop();
      views.delete(box);
    }
  }
  for (const box of document.querySelectorAll<HTMLElement>('[data-live-view]')) {
    if (!views.has(box) && box.dataset.app) views.set(box, initLiveView(box));
  }
  const push = document.getElementById('btn-push');
  if (push && !push.dataset.bound) {
    push.dataset.bound = '1';
    push.addEventListener('click', () => {
      enablePush().catch(() => undefined); // denied or cut off: the button stays, the user can try again
    });
  }
}

// --- dock ---

const DOCK_RUN = 'getafjob.dock.run';
const DOCK_OPEN = 'getafjob.dock.open';

function store(key: string, value: string | null): void {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    // storage blocked: the dock simply forgets
  }
}

function load(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function dockParts() {
  const dock = document.querySelector<HTMLElement>('[data-dock]');
  return {
    dock,
    body: document.getElementById('dock-body'),
    toggle: dock?.querySelector<HTMLButtonElement>('[data-dock-toggle]') ?? null,
    screen: dock?.querySelector<HTMLElement>('[data-dock-screen]') ?? null,
    runs: dock?.querySelector<HTMLElement>('[data-dock-runs]') ?? null,
    tpl: dock?.querySelector<HTMLTemplateElement>('template[data-dock-template]') ?? null,
    countText: dock?.querySelector<HTMLElement>('[data-t-count]')?.dataset.tCount ?? '{n}',
  };
}

// showRun puts the run's live view into the dock (or clears it when the run is gone).
function showRun(appId: string | null): void {
  const { screen, tpl, runs } = dockParts();
  if (!screen || !tpl) return;
  const current = screen.querySelector<HTMLElement>('[data-live-view]');
  if (current?.dataset.app === appId) return;
  screen.replaceChildren();
  if (appId) {
    const view = (tpl.content.firstElementChild as HTMLElement).cloneNode(true) as HTMLElement;
    view.dataset.app = appId;
    screen.append(view);
  }
  for (const b of runs?.querySelectorAll<HTMLElement>('[data-dock-run]') ?? []) {
    b.setAttribute('aria-pressed', String(b.dataset.dockRun === appId));
  }
  bindViews();
}

function setOpen(open: boolean): void {
  const { body, toggle, dock } = dockParts();
  if (!body || !toggle || !dock) return;
  body.hidden = !open;
  toggle.setAttribute('aria-expanded', String(open));
  dock.classList.toggle('dock-open', open);
  store(DOCK_OPEN, open ? '1' : null);
  syncDock(); // a closed dock holds no connection
}

// syncDock runs after the run list refreshed: count, empty state, selected run (waiting runs come first).
function syncDock(): void {
  const { dock, runs, countText } = dockParts();
  if (!dock || !runs) return;
  const ids = [...runs.querySelectorAll<HTMLElement>('[data-dock-run]')].map((b) => b.dataset.dockRun ?? '');
  dock.hidden = ids.length === 0;
  const count = dock.querySelector('[data-dock-count]');
  if (count) count.textContent = countText.replace('{n}', String(ids.length));
  if (ids.length === 0 || dock.querySelector('#dock-body')?.hasAttribute('hidden')) {
    showRun(null);
    return;
  }
  const saved = load(DOCK_RUN);
  showRun(saved && ids.includes(saved) ? saved : ids[0]);
}

document.addEventListener('click', (e) => {
  const t = e.target as Element;
  const toggle = t.closest('[data-dock-toggle]');
  if (toggle) {
    setOpen(toggle.getAttribute('aria-expanded') !== 'true');
    return;
  }
  const run = t.closest<HTMLElement>('[data-dock-run]');
  if (run?.dataset.dockRun) {
    store(DOCK_RUN, run.dataset.dockRun);
    showRun(run.dataset.dockRun);
  }
});

document.addEventListener('htmx:after:settle', () => {
  bindViews();
  syncDock();
});

function start(): void {
  const { dock } = dockParts();
  if (dock) {
    dock.hidden = true;
    if (load(DOCK_OPEN)) setOpen(true);
  }
  bindViews();
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', start);
} else {
  start();
}
