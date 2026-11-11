// Live application changes (#166): one EventSource per page; every change is re-dispatched as a DOM event
// "getafjob:app" on the body, so htmx elements refresh with hx-trigger="getafjob:app from:body".

interface AppEvent {
  id: number;
  app: string;
  event: string;
  from: string;
  to: string;
}

const LIVE = new Set(['queued', 'running', 'needs_input', 'submitting']);

function initEvents(): void {
  if (!('EventSource' in window) || !document.body.dataset.events) return;
  const source = new EventSource('/events');
  source.addEventListener('app', (e: MessageEvent) => {
    let ev: AppEvent;
    try {
      ev = JSON.parse(e.data as string) as AppEvent;
    } catch {
      return;
    }
    document.body.dispatchEvent(new CustomEvent('getafjob:app', { detail: ev }));

    // A page about this application reloads when the run starts or ends; the live view handles the rest.
    const watched = document.querySelector<HTMLElement>('[data-watch-app]');
    if (watched && watched.dataset.watchApp === ev.app) {
      const shown = watched.dataset.watchState ?? '';
      if (LIVE.has(shown) !== LIVE.has(ev.to)) {
        location.reload();
      }
    }
  });
  window.addEventListener('pagehide', () => source.close());
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initEvents);
} else {
  initEvents();
}
