// ts/shell.ts
var html = document.documentElement;
var lastMain = null;
var skippedTransition = /* @__PURE__ */ new Set(["AbortError", "InvalidStateError"]);
var startViewTransition = document.startViewTransition?.bind(document);
if (startViewTransition) {
  document.startViewTransition = ((arg) => {
    const vt = startViewTransition(arg);
    const skipped = (err) => {
      if (!(err instanceof DOMException && skippedTransition.has(err.name))) throw err;
    };
    vt.ready.catch(skipped);
    vt.finished.catch(skipped);
    vt.updateCallbackDone.catch(skipped);
    return vt;
  });
}
function isPageSwap(e) {
  const ctx = e.detail?.ctx;
  return ctx?.target === document.body;
}
document.addEventListener("htmx:before:request", (e) => {
  if (isPageSwap(e)) html.classList.add("is-navigating");
});
document.addEventListener("htmx:finally:request", () => html.classList.remove("is-navigating"));
function afterPage() {
  const main = document.getElementById("main");
  if (!main || main === lastMain) return;
  const first = lastMain === null;
  lastMain = main;
  if (first) return;
  const heading = main.querySelector("h1");
  const target = heading ?? main;
  if (heading && !heading.hasAttribute("tabindex")) heading.tabIndex = -1;
  target.focus({ preventScroll: true });
  window.scrollTo({ top: 0 });
  const announcer = document.getElementById("route-announcer");
  if (announcer) {
    announcer.textContent = "";
    window.setTimeout(() => {
      announcer.textContent = heading?.textContent?.trim() || document.title;
    }, 50);
  }
}
document.addEventListener("htmx:after:settle", afterPage);
var countTimer = 0;
async function refreshCounts() {
  const res = await fetch("/shell/counts", { headers: { Accept: "application/json" } });
  if (!res.ok) return;
  const counts = await res.json();
  for (const el of document.querySelectorAll("[data-count]")) {
    const n = counts[el.dataset.count ?? ""] ?? 0;
    el.textContent = String(n);
    el.hidden = n === 0;
  }
}
document.body.addEventListener("getafjob:app", () => {
  window.clearTimeout(countTimer);
  countTimer = window.setTimeout(() => refreshCounts().catch(() => void 0), 400);
});
function paletteParts() {
  const dialog = document.getElementById("palette");
  const input = document.getElementById("palette-q");
  const list = document.getElementById("palette-list");
  return { dialog, input, list };
}
function visibleOptions(list) {
  return [...list.querySelectorAll('[role="option"]')].filter((o) => !o.hidden);
}
function select(input, list, option) {
  for (const o of list.querySelectorAll('[aria-selected="true"]')) o.setAttribute("aria-selected", "false");
  if (!option) {
    input.removeAttribute("aria-activedescendant");
    return;
  }
  option.setAttribute("aria-selected", "true");
  input.setAttribute("aria-activedescendant", option.id);
  option.scrollIntoView({ block: "nearest" });
}
function filterPalette() {
  const { input, list } = paletteParts();
  if (!input || !list) return;
  const q = input.value.trim().toLowerCase();
  for (const o of list.querySelectorAll('[role="option"]')) {
    if (o.id === "pal-jobs") {
      o.hidden = q === "";
      const a = o.querySelector("a");
      const label = o.querySelector("span");
      if (a && label) {
        a.href = "/jobs?q=" + encodeURIComponent(input.value.trim());
        label.textContent = (o.dataset.template ?? "").replace("{q}", input.value.trim());
      }
      continue;
    }
    o.hidden = q !== "" && !(o.textContent ?? "").toLowerCase().includes(q);
  }
  select(input, list, visibleOptions(list)[0]);
}
function openPalette() {
  const { dialog, input } = paletteParts();
  if (!dialog || !input || dialog.open) return;
  dialog.showModal();
  input.value = "";
  filterPalette();
  input.focus();
}
document.addEventListener("input", (e) => {
  if (e.target.id === "palette-q") filterPalette();
});
document.addEventListener("keydown", (e) => {
  const { dialog, input, list } = paletteParts();
  if (dialog?.open && input && list && e.target === input) {
    const opts = visibleOptions(list);
    const cur = opts.findIndex((o) => o.getAttribute("aria-selected") === "true");
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const next = e.key === "ArrowDown" ? Math.min(cur + 1, opts.length - 1) : Math.max(cur - 1, 0);
      select(input, list, opts[next]);
    } else if (e.key === "Enter" && cur >= 0) {
      e.preventDefault();
      opts[cur].querySelector("a")?.click();
      dialog.close();
    }
    return;
  }
  const typing = e.target.closest?.('input, textarea, select, [contenteditable="true"], canvas');
  if ((e.key === "k" || e.key === "K") && (e.ctrlKey || e.metaKey) && !e.altKey) {
    e.preventDefault();
    openPalette();
  } else if (e.key === "/" && !typing && !e.ctrlKey && !e.metaKey && !e.altKey) {
    e.preventDefault();
    openPalette();
  }
});
document.addEventListener(
  "toggle",
  (e) => {
    const t = e.target;
    if (t.id === "palette" && e.newState === "open") {
      const { input } = paletteParts();
      if (input) {
        input.value = "";
        filterPalette();
        input.focus();
      }
    }
  },
  true
);
var confirmed = null;
document.addEventListener(
  "submit",
  (e) => {
    const form = e.target;
    const text = e.submitter?.dataset.confirm || form.dataset.confirm;
    if (!text || confirmed === form) {
      confirmed = null;
      return;
    }
    const dialog = document.getElementById("confirm-dialog");
    const body = document.getElementById("confirm-text");
    const ok = dialog?.querySelector("[data-confirm-ok]");
    if (!dialog || !body || !ok) return;
    e.preventDefault();
    e.stopImmediatePropagation();
    const submitter = e.submitter;
    body.textContent = text;
    ok.onclick = () => {
      dialog.close();
      confirmed = form;
      form.requestSubmit(submitter instanceof HTMLButtonElement ? submitter : void 0);
    };
    dialog.showModal();
  },
  true
);
function dirty() {
  return !!document.querySelector("form[data-dirty-guard][data-dirty]");
}
document.addEventListener("input", (e) => {
  const form = e.target.closest?.("form[data-dirty-guard]");
  if (form) form.dataset.dirty = "1";
});
document.addEventListener("submit", (e) => {
  delete e.target.dataset.dirty;
});
window.addEventListener("beforeunload", (e) => {
  if (dirty()) e.preventDefault();
});
var running = /* @__PURE__ */ new Set();
var leaving = false;
window.addEventListener("beforeunload", () => {
  if (!dirty()) leaving = true;
});
window.addEventListener("pageshow", () => {
  leaving = false;
});
document.addEventListener("htmx:before:request", (e) => {
  if (leaving) {
    e.preventDefault();
    return;
  }
  const ctx = e.detail?.ctx;
  if (ctx) running.add(ctx);
});
document.addEventListener("htmx:finally:request", (e) => {
  const ctx = e.detail?.ctx;
  if (ctx) running.delete(ctx);
});
var abortRunning = () => {
  for (const ctx of running) ctx.request?.abort?.();
  running.clear();
};
window.addEventListener("beforeunload", abortRunning);
window.addEventListener("pagehide", abortRunning);
document.addEventListener(
  "click",
  (e) => {
    const link = e.target.closest?.("a[href]");
    if (!link || !dirty() || link.getAttribute("href")?.startsWith("#")) return;
    const dialog = document.getElementById("confirm-dialog");
    const body = document.getElementById("confirm-text");
    const ok = dialog?.querySelector("[data-confirm-ok]");
    if (!dialog || !body || !ok) return;
    e.preventDefault();
    e.stopImmediatePropagation();
    body.textContent = document.querySelector("[data-t-unsaved]")?.dataset.tUnsaved ?? "";
    ok.onclick = () => {
      dialog.close();
      for (const f of document.querySelectorAll("form[data-dirty]")) delete f.dataset.dirty;
      link.click();
    };
    dialog.showModal();
  },
  true
);
document.addEventListener("click", (e) => {
  const t = e.target;
  const dismiss = t.closest("[data-dismiss]");
  if (dismiss) {
    dismiss.closest(".ui-toast")?.remove();
    return;
  }
  const toggle = t.closest("[data-nav-toggle]");
  if (toggle) {
    const collapsed = document.body.classList.toggle("nav-collapsed");
    toggle.setAttribute("aria-pressed", String(collapsed));
    document.cookie = `nav=${collapsed ? "collapsed" : "wide"}; Path=/; Max-Age=31536000; SameSite=Lax; Secure`;
  }
});
lastMain = document.getElementById("main");
