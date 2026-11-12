// ts/sortable.ts
var sort = null;
var order = (list) => [...list.querySelectorAll(":scope > [data-sort-item]")].map((el) => el.dataset.id ?? "");
function end() {
  if (!sort) return;
  sort.item.classList.remove("st-dragging");
  sort = null;
}
document.addEventListener("pointerdown", (e) => {
  const handle = e.target.closest?.("[data-drag-handle]");
  const item = handle?.closest("[data-sort-item]");
  const group = item?.closest("[data-sortable]");
  const list = item?.parentElement;
  if (!item || !group || !list || e.button !== 0) return;
  e.preventDefault();
  sort = { item, list, group, before: order(list).join(" "), y: e.clientY, active: false };
  item.setPointerCapture(e.pointerId);
});
document.addEventListener("pointermove", (e) => {
  if (!sort) return;
  if (!sort.active) {
    if (Math.abs(e.clientY - sort.y) < 6) return;
    sort.active = true;
    sort.item.classList.add("st-dragging");
  }
  e.preventDefault();
  const siblings = [...sort.list.querySelectorAll(":scope > [data-sort-item]")].filter((el) => el !== sort?.item);
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
document.addEventListener("pointerup", () => {
  if (!sort) return;
  const { active, list, group, before } = sort;
  end();
  const ids = order(list);
  const form = group.querySelector("[data-sort-form]");
  if (!active || !form || ids.join(" ") === before) return;
  form.replaceChildren(
    ...ids.map((id) => {
      const input = document.createElement("input");
      input.type = "hidden";
      input.name = "id";
      input.value = id;
      return input;
    })
  );
  form.requestSubmit();
});
document.addEventListener("pointercancel", end);
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") end();
});
