// Sortable lists (profile stations): an entry moves by dragging its handle with mouse or touch (pointer events);
// the keyboard keeps the up and down buttons. On drop the whole order goes to the server in one form
// ([data-sort-form] of the list, one "id" per entry, top first), which answers with the page.

interface Sort {
  item: HTMLElement;
  list: HTMLElement;
  group: HTMLElement;
  before: string;
  y: number;
  active: boolean;
}

let sort: Sort | null = null;

const order = (list: HTMLElement): string[] =>
  [...list.querySelectorAll<HTMLElement>(':scope > [data-sort-item]')].map((el) => el.dataset.id ?? '');

function end(): void {
  if (!sort) return;
  sort.item.classList.remove('st-dragging');
  sort = null;
}

document.addEventListener('pointerdown', (e) => {
  const handle = (e.target as Element).closest?.('[data-drag-handle]');
  const item = handle?.closest<HTMLElement>('[data-sort-item]');
  const group = item?.closest<HTMLElement>('[data-sortable]');
  const list = item?.parentElement;
  if (!item || !group || !list || e.button !== 0) return;
  e.preventDefault(); // no text selection while dragging
  sort = { item, list, group, before: order(list).join(' '), y: e.clientY, active: false };
  item.setPointerCapture(e.pointerId);
});

document.addEventListener('pointermove', (e) => {
  if (!sort) return;
  if (!sort.active) {
    if (Math.abs(e.clientY - sort.y) < 6) return;
    sort.active = true;
    sort.item.classList.add('st-dragging');
  }
  e.preventDefault();
  // Move the entry before the first sibling whose middle lies below the pointer.
  const siblings = [...sort.list.querySelectorAll<HTMLElement>(':scope > [data-sort-item]')].filter((el) => el !== sort?.item);
  const next = siblings.find((el) => {
    const r = el.getBoundingClientRect();
    return e.clientY < r.top + r.height / 2;
  });
  if (next) {
    if (next.previousElementSibling !== sort.item) sort.list.insertBefore(sort.item, next);
  } else if (sort.list.lastElementChild !== sort.item) {
    sort.list.appendChild(sort.item);
  }
});

document.addEventListener('pointerup', () => {
  if (!sort) return;
  const { active, list, group, before } = sort;
  end();
  const ids = order(list);
  const form = group.querySelector<HTMLFormElement>('[data-sort-form]');
  if (!active || !form || ids.join(' ') === before) return;
  form.replaceChildren(
    ...ids.map((id) => {
      const input = document.createElement('input');
      input.type = 'hidden';
      input.name = 'id';
      input.value = id;
      return input;
    }),
  );
  form.requestSubmit();
});

document.addEventListener('pointercancel', end);
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') end();
});
