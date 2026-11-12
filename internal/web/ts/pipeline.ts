// Pipeline island: cards move between columns by dragging with mouse or touch (pointer events). The keyboard uses
// each card's move menu instead. A drop only goes to a column the card may move to (data-moves); the server
// decides again and answers with the new board.

interface Drag {
  card: HTMLElement;
  id: string;
  moves: string[];
  x: number;
  y: number;
  active: boolean;
  over: HTMLElement | null;
}

let drag: Drag | null = null;

// columnAt finds the column under the pointer, looking past the dragged card itself.
function columnAt(x: number, y: number, card: HTMLElement): HTMLElement | null {
  const hit = document.elementsFromPoint(x, y).find((el) => !card.contains(el));
  return hit?.closest<HTMLElement>('[data-col]') ?? null;
}

function clearTargets(): void {
  for (const c of document.querySelectorAll('.pl-col-target, .pl-col-over')) {
    c.classList.remove('pl-col-target', 'pl-col-over');
  }
}

function end(): void {
  if (!drag) return;
  drag.card.classList.remove('pl-dragging');
  drag.card.style.removeProperty('translate');
  clearTargets();
  drag = null;
}

document.addEventListener('pointerdown', (e) => {
  const handle = (e.target as Element).closest?.('[data-drag-handle]');
  const card = handle?.closest<HTMLElement>('[data-card]');
  if (!card || e.button !== 0 || !card.dataset.moves) return;
  drag = { card, id: card.dataset.id ?? '', moves: card.dataset.moves.split(' '), x: e.clientX, y: e.clientY, active: false, over: null };
  card.setPointerCapture(e.pointerId);
});

document.addEventListener('pointermove', (e) => {
  if (!drag) return;
  const dx = e.clientX - drag.x;
  const dy = e.clientY - drag.y;
  if (!drag.active) {
    if (Math.hypot(dx, dy) < 6) return;
    drag.active = true;
    drag.card.classList.add('pl-dragging');
    for (const key of drag.moves) document.querySelector(`[data-col="${key}"]`)?.classList.add('pl-col-target');
  }
  e.preventDefault();
  drag.card.style.setProperty('translate', `${dx}px ${dy}px`);
  const col = columnAt(e.clientX, e.clientY, drag.card);
  const ok = col && drag.moves.includes(col.dataset.col ?? '') ? col : null;
  if (ok !== drag.over) {
    drag.over?.classList.remove('pl-col-over');
    ok?.classList.add('pl-col-over');
    drag.over = ok;
  }
});

document.addEventListener('pointerup', () => {
  if (!drag) return;
  const { active, over, id } = drag;
  end();
  const form = document.getElementById('pl-drop') as HTMLFormElement | null;
  if (!active || !over || !form) return;
  (form.elements.namedItem('id') as HTMLInputElement).value = id;
  (form.elements.namedItem('stage') as HTMLInputElement).value = over.dataset.col ?? '';
  form.requestSubmit();
});

document.addEventListener('pointercancel', end);
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') end();
});

// A move replaces the board: a menu that was open would lose the card it belongs to, so close it.
document.addEventListener('htmx:after:swap', () => {
  for (const m of document.querySelectorAll<HTMLElement>('#pl-board [popover]')) {
    if (m.matches(':popover-open')) m.hidePopover();
  }
});
