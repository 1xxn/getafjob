// ts/jobs.ts
function rows(list) {
  return [...list.querySelectorAll("[data-job-row]")];
}
function updateCount() {
  const counter = document.querySelector("[data-bulk-count]");
  const bulk = document.querySelector("[data-bulk]");
  if (!counter || !bulk) return;
  const n = document.querySelectorAll("input.job-check:checked").length;
  counter.textContent = n === 0 ? counter.dataset.tNone ?? "" : (counter.dataset.tSome ?? "").replace("{n}", String(n));
  bulk.classList.toggle("jobs-bulk-active", n > 0);
}
document.addEventListener("change", (e) => {
  const target = e.target;
  if (target.matches?.("input[data-select-all]")) {
    for (const box of document.querySelectorAll(`input.job-check[form="${target.getAttribute("form") ?? ""}"]`)) {
      box.checked = target.checked;
    }
  }
  if (target.matches?.("input.job-check, input[data-select-all]")) updateCount();
});
function act(row, action) {
  const form = document.getElementById("row-action");
  if (!form) return;
  form.elements.namedItem("ids").value = row.dataset.id ?? "";
  form.elements.namedItem("action").value = action;
  form.requestSubmit();
}
document.addEventListener("keydown", (e) => {
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  const target = e.target;
  const list = target.closest?.("[data-joblist]");
  if (!list || target.matches("input, select, textarea")) return;
  const row = target.closest("[data-job-row]");
  const all = rows(list);
  const i = row ? all.indexOf(row) : -1;
  const focusRow = (r) => r?.querySelector(".job-row-link")?.focus();
  switch (e.key) {
    case "j":
      focusRow(all[Math.min(i + 1, all.length - 1)]);
      break;
    case "k":
      focusRow(all[Math.max(i - 1, 0)]);
      break;
    case "a":
      if (row?.dataset.id) location.assign(`/jobs/${row.dataset.id}/apply`);
      break;
    case "s":
      if (row) act(row, "save");
      break;
    case "x":
      if (row) act(row, "skip");
      break;
    default:
      return;
  }
  e.preventDefault();
});
document.addEventListener("click", (e) => {
  const link = e.target.closest?.(".job-row-link");
  if (!link) return;
  for (const r of document.querySelectorAll(".job-row-current")) r.classList.remove("job-row-current");
  for (const a of document.querySelectorAll(".job-row-link[aria-current]")) a.removeAttribute("aria-current");
  link.closest("[data-job-row]")?.classList.add("job-row-current");
  link.setAttribute("aria-current", "true");
});
document.addEventListener("htmx:after:settle", updateCount);
updateCount();
