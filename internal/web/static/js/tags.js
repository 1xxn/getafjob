// ts/tags.ts
var split = (s) => s.split(/[\n,;]/).map((x) => x.replace(/\s+/g, " ").trim()).filter((x) => x !== "");
function enhance(area) {
  area.dataset.tagsReady = "";
  const say = (tpl, item) => (tpl ?? "").replace("{item}", item);
  let items = [];
  for (const x of split(area.value)) {
    if (!items.some((y) => y.toLowerCase() === x.toLowerCase())) items.push(x);
  }
  const box = document.createElement("div");
  box.className = "ui-tags";
  box.dataset.tagsBox = area.name;
  const list = document.createElement("ul");
  list.className = "ui-tags-list";
  list.setAttribute("role", "list");
  const input = document.createElement("input");
  input.type = "text";
  input.className = "ui-tags-input";
  input.id = area.id + "-tag";
  input.autocomplete = "off";
  input.dataset.tagsInput = area.name;
  const described = area.getAttribute("aria-describedby");
  if (described) input.setAttribute("aria-describedby", described);
  if (area.getAttribute("aria-invalid")) input.setAttribute("aria-invalid", "true");
  const live = document.createElement("span");
  live.className = "ui-sr";
  live.setAttribute("aria-live", "polite");
  box.append(list, input, live);
  area.after(box);
  area.hidden = true;
  const label = document.querySelector(`label[for="${CSS.escape(area.id)}"]`);
  if (label) label.htmlFor = input.id;
  const sync = () => {
    area.value = items.join("\n");
    area.dispatchEvent(new Event("input", { bubbles: true }));
  };
  const render = () => {
    list.replaceChildren(
      ...items.map((item, i) => {
        const li = document.createElement("li");
        li.className = "ui-tag";
        li.dataset.tag = item;
        const text = document.createElement("span");
        text.textContent = item;
        const remove = document.createElement("button");
        remove.type = "button";
        remove.className = "ui-tag-remove";
        remove.setAttribute("aria-label", say(area.dataset.tagsRemove, item));
        remove.textContent = "×";
        remove.addEventListener("click", () => {
          items.splice(i, 1);
          live.textContent = say(area.dataset.tagsRemoved, item);
          render();
          sync();
          input.focus();
        });
        li.append(text, remove);
        return li;
      })
    );
  };
  const add = (text) => {
    let added = false;
    for (const x of split(text)) {
      if (items.some((y) => y.toLowerCase() === x.toLowerCase())) continue;
      items.push(x);
      live.textContent = say(area.dataset.tagsAdded, x);
      added = true;
    }
    if (added) {
      render();
      sync();
    }
    return added;
  };
  input.addEventListener("keydown", (e) => {
    if ((e.key === "Enter" || e.key === ",") && input.value.trim() !== "") {
      e.preventDefault();
      add(input.value);
      input.value = "";
    } else if (e.key === "Enter") {
      e.preventDefault();
    } else if (e.key === "Backspace" && input.value === "" && items.length > 0) {
      const last = items.pop() ?? "";
      live.textContent = say(area.dataset.tagsRemoved, last);
      render();
      sync();
    }
  });
  input.addEventListener("input", () => {
    if (!/[,;\n]/.test(input.value)) return;
    const parts = input.value.split(/[,;\n]/);
    const rest = parts.pop() ?? "";
    add(parts.join("\n"));
    input.value = rest.trimStart();
  });
  input.addEventListener("paste", (e) => {
    const text = e.clipboardData?.getData("text") ?? "";
    if (/[\n,;]/.test(text)) {
      e.preventDefault();
      add(text);
    }
  });
  input.addEventListener("blur", () => {
    if (input.value.trim() !== "" && add(input.value)) input.value = "";
  });
  box.addEventListener("click", (e) => {
    if (e.target === box || e.target === list) input.focus();
  });
  render();
}
function bind() {
  for (const area of document.querySelectorAll("textarea[data-tags]:not([data-tags-ready])")) {
    enhance(area);
  }
}
bind();
document.addEventListener("htmx:after:settle", bind);
