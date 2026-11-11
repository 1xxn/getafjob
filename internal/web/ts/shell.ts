// App shell island (ADR 0025): navigation progress, focus and announcement after boosted page swaps, live
// navigation counters, the command palette (Ctrl+K, /), confirm dialogs for forms with data-confirm, closable
// toasts and the sidebar width. Texts come from the page (message catalog), never from this file.

type Counts = Record<string, number>;

const html = document.documentElement;
let lastMain: Element | null = null;

// --- navigation progress, focus and announcement ---

function isPageSwap(e: Event): boolean {
  const ctx = (e as CustomEvent<{ ctx?: { target?: Element } }>).detail?.ctx;
  return ctx?.target === document.body;
}

document.addEventListener('htmx:before:request', (e) => {
  if (isPageSwap(e)) html.classList.add('is-navigating');
});
document.addEventListener('htmx:finally:request', () => html.classList.remove('is-navigating'));

function afterPage(): void {
  const main = document.getElementById('main');
  if (!main || main === lastMain) return;
  const first = lastMain === null;
  lastMain = main;
  if (first) return; // the initial load keeps the browser's own focus handling
  const heading = main.querySelector<HTMLElement>('h1');
  const target = heading ?? main;
  if (heading && !heading.hasAttribute('tabindex')) heading.tabIndex = -1;
  target.focus({ preventScroll: true });
  window.scrollTo({ top: 0 });
  const announcer = document.getElementById('route-announcer');
  if (announcer) {
    announcer.textContent = '';
    window.setTimeout(() => {
      announcer.textContent = heading?.textContent?.trim() || document.title;
    }, 50);
  }
}

document.addEventListener('htmx:after:settle', afterPage);

// --- counters ---

let countTimer = 0;

async function refreshCounts(): Promise<void> {
  const res = await fetch('/shell/counts', { headers: { Accept: 'application/json' } });
  if (!res.ok) return;
  const counts = (await res.json()) as Counts;
  for (const el of document.querySelectorAll<HTMLElement>('[data-count]')) {
    const n = counts[el.dataset.count ?? ''] ?? 0;
    el.textContent = String(n);
    el.hidden = n === 0;
  }
}

document.body.addEventListener('getafjob:app', () => {
  window.clearTimeout(countTimer);
  countTimer = window.setTimeout(() => void refreshCounts(), 400);
});

// --- command palette ---

function paletteParts() {
  const dialog = document.getElementById('palette') as HTMLDialogElement | null;
  const input = document.getElementById('palette-q') as HTMLInputElement | null;
  const list = document.getElementById('palette-list');
  return { dialog, input, list };
}

function visibleOptions(list: HTMLElement): HTMLElement[] {
  return [...list.querySelectorAll<HTMLElement>('[role="option"]')].filter((o) => !o.hidden);
}

function select(input: HTMLInputElement, list: HTMLElement, option: HTMLElement | undefined): void {
  for (const o of list.querySelectorAll<HTMLElement>('[aria-selected="true"]')) o.setAttribute('aria-selected', 'false');
  if (!option) {
    input.removeAttribute('aria-activedescendant');
    return;
  }
  option.setAttribute('aria-selected', 'true');
  input.setAttribute('aria-activedescendant', option.id);
  option.scrollIntoView({ block: 'nearest' });
}

function filterPalette(): void {
  const { input, list } = paletteParts();
  if (!input || !list) return;
  const q = input.value.trim().toLowerCase();
  for (const o of list.querySelectorAll<HTMLElement>('[role="option"]')) {
    if (o.id === 'pal-jobs') {
      o.hidden = q === '';
      const a = o.querySelector('a');
      const label = o.querySelector('span');
      if (a && label) {
        a.href = '/jobs?q=' + encodeURIComponent(input.value.trim());
        label.textContent = (o.dataset.template ?? '').replace('{q}', input.value.trim());
      }
      continue;
    }
    o.hidden = q !== '' && !(o.textContent ?? '').toLowerCase().includes(q);
  }
  select(input, list, visibleOptions(list)[0]);
}

function openPalette(): void {
  const { dialog, input } = paletteParts();
  if (!dialog || !input || dialog.open) return;
  dialog.showModal();
  input.value = '';
  filterPalette();
  input.focus();
}

document.addEventListener('input', (e) => {
  if ((e.target as Element).id === 'palette-q') filterPalette();
});

document.addEventListener('keydown', (e) => {
  const { dialog, input, list } = paletteParts();
  if (dialog?.open && input && list && e.target === input) {
    const opts = visibleOptions(list);
    const cur = opts.findIndex((o) => o.getAttribute('aria-selected') === 'true');
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const next = e.key === 'ArrowDown' ? Math.min(cur + 1, opts.length - 1) : Math.max(cur - 1, 0);
      select(input, list, opts[next]);
    } else if (e.key === 'Enter' && cur >= 0) {
      e.preventDefault();
      opts[cur].querySelector('a')?.click();
      dialog.close();
    }
    return;
  }
  const typing = (e.target as Element).closest?.('input, textarea, select, [contenteditable="true"], canvas');
  if ((e.key === 'k' || e.key === 'K') && (e.ctrlKey || e.metaKey) && !e.altKey) {
    e.preventDefault();
    openPalette();
  } else if (e.key === '/' && !typing && !e.ctrlKey && !e.metaKey && !e.altKey) {
    e.preventDefault();
    openPalette();
  }
});

// The palette opens without script through its button (invoker command); reset its state then too.
document.addEventListener(
  'toggle',
  (e) => {
    const t = e.target as HTMLElement;
    if (t.id === 'palette' && (e as ToggleEvent).newState === 'open') {
      const { input } = paletteParts();
      if (input) {
        input.value = '';
        filterPalette();
        input.focus();
      }
    }
  },
  true,
);

// --- confirm dialogs ---

let confirmed: HTMLFormElement | null = null;

document.addEventListener(
  'submit',
  (e) => {
    const form = e.target as HTMLFormElement;
    const text = form.dataset.confirm;
    if (!text || confirmed === form) {
      confirmed = null;
      return;
    }
    const dialog = document.getElementById('confirm-dialog') as HTMLDialogElement | null;
    const body = document.getElementById('confirm-text');
    const ok = dialog?.querySelector<HTMLButtonElement>('[data-confirm-ok]');
    if (!dialog || !body || !ok) return; // no dialog: the native submit goes ahead
    e.preventDefault();
    e.stopImmediatePropagation();
    const submitter = (e as SubmitEvent).submitter;
    body.textContent = text;
    ok.onclick = () => {
      dialog.close();
      confirmed = form;
      form.requestSubmit(submitter instanceof HTMLButtonElement ? submitter : undefined);
    };
    dialog.showModal();
  },
  true,
);

// --- toasts and sidebar ---

document.addEventListener('click', (e) => {
  const t = e.target as Element;
  const dismiss = t.closest('[data-dismiss]');
  if (dismiss) {
    dismiss.closest('.ui-toast')?.remove();
    return;
  }
  const toggle = t.closest<HTMLButtonElement>('[data-nav-toggle]');
  if (toggle) {
    const collapsed = document.body.classList.toggle('nav-collapsed');
    toggle.setAttribute('aria-pressed', String(collapsed));
    document.cookie = `nav=${collapsed ? 'collapsed' : 'wide'}; Path=/; Max-Age=31536000; SameSite=Lax; Secure`;
  }
});

lastMain = document.getElementById('main');
