// Tag input for list fields (#597): a textarea[data-tags] with one entry per line becomes a one-line input with
// removable chips. The textarea stays in the form, hidden, and always holds the entries one per line, so the server,
// validation and autosave see the same value as without JavaScript. Enter or a comma adds an entry, pasted text is
// split at line breaks and commas, Backspace in the empty input removes the last chip.

const split = (s: string): string[] =>
  s.split(/[\n,;]/).map((x) => x.replace(/\s+/g, ' ').trim()).filter((x) => x !== '');

function enhance(area: HTMLTextAreaElement): void {
  area.dataset.tagsReady = '';
  const say = (tpl: string | undefined, item: string) => (tpl ?? '').replace('{item}', item);
  let items: string[] = [];
  for (const x of split(area.value)) {
    if (!items.some((y) => y.toLowerCase() === x.toLowerCase())) items.push(x);
  }

  const box = document.createElement('div');
  box.className = 'ui-tags';
  box.dataset.tagsBox = area.name;
  const list = document.createElement('ul');
  list.className = 'ui-tags-list';
  list.setAttribute('role', 'list');
  const input = document.createElement('input');
  input.type = 'text';
  input.className = 'ui-tags-input';
  input.id = area.id + '-tag';
  input.autocomplete = 'off';
  input.dataset.tagsInput = area.name;
  const described = area.getAttribute('aria-describedby');
  if (described) input.setAttribute('aria-describedby', described);
  if (area.getAttribute('aria-invalid')) input.setAttribute('aria-invalid', 'true');
  const live = document.createElement('span');
  live.className = 'ui-sr';
  live.setAttribute('aria-live', 'polite');
  box.append(list, input, live);
  area.after(box);
  area.hidden = true;
  const label = document.querySelector<HTMLLabelElement>(`label[for="${CSS.escape(area.id)}"]`);
  if (label) label.htmlFor = input.id;

  // Only "input": autosave debounces it, while "change" saves at once, and several chips added in a row would send
  // saves with the same version that conflict.
  const sync = () => {
    area.value = items.join('\n');
    area.dispatchEvent(new Event('input', { bubbles: true }));
  };
  const render = () => {
    list.replaceChildren(
      ...items.map((item, i) => {
        const li = document.createElement('li');
        li.className = 'ui-tag';
        li.dataset.tag = item;
        const text = document.createElement('span');
        text.textContent = item;
        const remove = document.createElement('button');
        remove.type = 'button';
        remove.className = 'ui-tag-remove';
        remove.setAttribute('aria-label', say(area.dataset.tagsRemove, item));
        remove.textContent = '×';
        remove.addEventListener('click', () => {
          items.splice(i, 1);
          live.textContent = say(area.dataset.tagsRemoved, item);
          render();
          sync();
          input.focus();
        });
        li.append(text, remove);
        return li;
      }),
    );
  };
  const add = (text: string): boolean => {
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

  input.addEventListener('keydown', (e) => {
    if ((e.key === 'Enter' || e.key === ',') && input.value.trim() !== '') {
      e.preventDefault();
      add(input.value);
      input.value = '';
    } else if (e.key === 'Enter') {
      e.preventDefault(); // an empty Enter never submits the whole form by accident
    } else if (e.key === 'Backspace' && input.value === '' && items.length > 0) {
      const last = items.pop() ?? '';
      live.textContent = say(area.dataset.tagsRemoved, last);
      render();
      sync();
    }
  });
  // Phone keyboards and text insertion often send no keydown for the comma: split on input as well and keep only
  // the unfinished last part in the field.
  input.addEventListener('input', () => {
    if (!/[,;\n]/.test(input.value)) return;
    const parts = input.value.split(/[,;\n]/);
    const rest = parts.pop() ?? '';
    add(parts.join('\n'));
    input.value = rest.trimStart();
  });
  input.addEventListener('paste', (e) => {
    const text = e.clipboardData?.getData('text') ?? '';
    if (/[\n,;]/.test(text)) {
      e.preventDefault();
      add(text);
    }
  });
  input.addEventListener('blur', () => {
    if (input.value.trim() !== '' && add(input.value)) input.value = '';
  });
  box.addEventListener('click', (e) => {
    if (e.target === box || e.target === list) input.focus();
  });
  render();
}

function bind(): void {
  for (const area of document.querySelectorAll<HTMLTextAreaElement>('textarea[data-tags]:not([data-tags-ready])')) {
    enhance(area);
  }
}

bind();
document.addEventListener('htmx:after:settle', bind);
