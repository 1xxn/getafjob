// Job list island (also the bulk bars of applications and approvals): the bulk bar counts the selected rows, and the list answers single keys while focus is inside
// it (j/k move, a apply, s save, x skip), so the keys never fire elsewhere (WCAG 2.1.4). Texts come from the page.

function rows(list: Element): HTMLElement[] {
  return [...list.querySelectorAll<HTMLElement>('[data-job-row]')];
}

function updateCount(): void {
  const counter = document.querySelector<HTMLElement>('[data-bulk-count]');
  const bulk = document.querySelector<HTMLElement>('[data-bulk]');
  if (!counter || !bulk) return;
  const n = document.querySelectorAll('input.job-check:checked').length;
  counter.textContent = n === 0 ? (counter.dataset.tNone ?? '') : (counter.dataset.tSome ?? '').replace('{n}', String(n));
  bulk.classList.toggle('jobs-bulk-active', n > 0);
}

document.addEventListener('change', (e) => {
  const target = e.target as HTMLInputElement;
  // "Select all" ticks every row checkbox that belongs to the same bulk form.
  if (target.matches?.('input[data-select-all]')) {
    for (const box of document.querySelectorAll<HTMLInputElement>(`input.job-check[form="${target.getAttribute('form') ?? ''}"]`)) {
      box.checked = target.checked;
    }
  }
  if (target.matches?.('input.job-check, input[data-select-all]')) updateCount();
});

function act(row: HTMLElement, action: string): void {
  const form = document.getElementById('row-action') as HTMLFormElement | null;
  if (!form) return;
  (form.elements.namedItem('ids') as HTMLInputElement).value = row.dataset.id ?? '';
  (form.elements.namedItem('action') as HTMLInputElement).value = action;
  form.requestSubmit();
}

document.addEventListener('keydown', (e) => {
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  const target = e.target as HTMLElement;
  const list = target.closest?.('[data-joblist]');
  if (!list || target.matches('input, select, textarea')) return;
  const row = target.closest<HTMLElement>('[data-job-row]');
  const all = rows(list);
  const i = row ? all.indexOf(row) : -1;
  const focusRow = (r: HTMLElement | undefined) => r?.querySelector<HTMLElement>('.job-row-link')?.focus();
  switch (e.key) {
    case 'j':
      focusRow(all[Math.min(i + 1, all.length - 1)]);
      break;
    case 'k':
      focusRow(all[Math.max(i - 1, 0)]);
      break;
    case 'a':
      if (row?.dataset.id) location.assign(`/jobs/${row.dataset.id}/apply`);
      break;
    case 's':
      if (row) act(row, 'save');
      break;
    case 'x':
      if (row) act(row, 'skip');
      break;
    default:
      return;
  }
  e.preventDefault();
});

// The open row follows the panel: a click marks it at once, before the panel arrives.
document.addEventListener('click', (e) => {
  const link = (e.target as Element).closest?.('.job-row-link');
  if (!link) return;
  for (const r of document.querySelectorAll('.job-row-current')) r.classList.remove('job-row-current');
  for (const a of document.querySelectorAll('.job-row-link[aria-current]')) a.removeAttribute('aria-current');
  link.closest('[data-job-row]')?.classList.add('job-row-current');
  link.setAttribute('aria-current', 'true');
});

document.addEventListener('htmx:after:settle', updateCount);
updateCount();
