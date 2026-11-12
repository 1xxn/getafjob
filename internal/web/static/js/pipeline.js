// ts/pipeline.ts
var drag = null;
function columnAt(x, y, card) {
  const hit = document.elementsFromPoint(x, y).find((el) => !card.contains(el));
  return hit?.closest("[data-col]") ?? null;
}
function clearTargets() {
  for (const c of document.querySelectorAll(".pl-col-target, .pl-col-over")) {
    c.classList.remove("pl-col-target", "pl-col-over");
  }
}
function end() {
  if (!drag) return;
  drag.card.classList.remove("pl-dragging");
  drag.card.style.removeProperty("translate");
  clearTargets();
  drag = null;
}
document.addEventListener("pointerdown", (e) => {
  const handle = e.target.closest?.("[data-drag-handle]");
  const card = handle?.closest("[data-card]");
  if (!card || e.button !== 0 || !card.dataset.moves) return;
  drag = { card, id: card.dataset.id ?? "", moves: card.dataset.moves.split(" "), x: e.clientX, y: e.clientY, active: false, over: null };
  card.setPointerCapture(e.pointerId);
});
document.addEventListener("pointermove", (e) => {
  if (!drag) return;
  const dx = e.clientX - drag.x;
  const dy = e.clientY - drag.y;
  if (!drag.active) {
    if (Math.hypot(dx, dy) < 6) return;
    drag.active = true;
    drag.card.classList.add("pl-dragging");
    for (const key of drag.moves) document.querySelector(`[data-col="${key}"]`)?.classList.add("pl-col-target");
  }
  e.preventDefault();
  drag.card.style.setProperty("translate", `${dx}px ${dy}px`);
  const col = columnAt(e.clientX, e.clientY, drag.card);
  const ok = col && drag.moves.includes(col.dataset.col ?? "") ? col : null;
  if (ok !== drag.over) {
    drag.over?.classList.remove("pl-col-over");
    ok?.classList.add("pl-col-over");
    drag.over = ok;
  }
});
document.addEventListener("pointerup", () => {
  if (!drag) return;
  const { active, over, id } = drag;
  end();
  const form = document.getElementById("pl-drop");
  if (!active || !over || !form) return;
  form.elements.namedItem("id").value = id;
  form.elements.namedItem("stage").value = over.dataset.col ?? "";
  form.requestSubmit();
});
document.addEventListener("pointercancel", end);
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") end();
});
document.addEventListener("htmx:after:swap", () => {
  for (const m of document.querySelectorAll("#pl-board [popover]")) {
    if (m.matches(":popover-open")) m.hidePopover();
  }
});
