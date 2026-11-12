// Live changes (#166, #245): one EventSource per page. Application changes and mailbox suggestions are re-dispatched
// as "getafjob:app" on the body, the crawl and new ratings as "getafjob:jobs", so htmx elements refresh with
// hx-trigger="getafjob:app from:body" or "getafjob:jobs from:body".

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
  source.addEventListener('jobs', (e: MessageEvent) => {
    let detail: unknown = null;
    try {
      detail = JSON.parse(e.data as string);
    } catch {
      return;
    }
    document.body.dispatchEvent(new CustomEvent('getafjob:jobs', { detail }));
  });
  window.addEventListener('pagehide', () => source.close());
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initEvents);
} else {
  initEvents();
}
