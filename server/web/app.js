// Tabula page: renders tasks and categories from the server's JSON API.

const $ = (sel, root = document) => root.querySelector(sel);

const state = {
  tasks: [],
  categories: [],
  filter: 'open',
  icons: null, // all Material icon names, loaded when the picker first opens
  newCategoryIcon: '',
  editing: null, // task being edited
};

const MAX_ICONS_SHOWN = 200;

// Shown in the icon picker before anything is typed.
const COMMON_ICONS = [
  'home', 'work', 'shopping_cart', 'local_grocery_store', 'shopping_bag', 'restaurant', 'local_cafe', 'cake',
  'fitness_center', 'directions_run', 'sports_soccer', 'local_hospital', 'medication', 'healing', 'spa',
  'pets', 'child_care', 'family_restroom', 'people', 'school', 'menu_book', 'local_library',
  'directions_car', 'local_gas_station', 'directions_bike', 'train', 'flight', 'luggage', 'beach_access',
  'build', 'handyman', 'cleaning_services', 'local_laundry_service', 'yard', 'local_florist', 'park',
  'attach_money', 'payments', 'account_balance', 'receipt_long', 'savings', 'card_giftcard', 'celebration',
  'event', 'alarm', 'call', 'email', 'chat', 'favorite', 'star', 'flag', 'push_pin', 'lightbulb',
  'computer', 'code', 'phone_android', 'tv', 'music_note', 'movie', 'sports_esports', 'camera_alt', 'palette',
  'local_shipping', 'inventory_2', 'description', 'print',
];

// el builds an element. Text children are added as text, never parsed as HTML.
function el(tag, props = {}, ...children) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === 'class') e.className = v;
    else if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
    else if (v != null && v !== false) e.setAttribute(k, v === true ? '' : v);
  }
  for (const c of children.flat()) {
    if (c != null && c !== false) e.append(c);
  }
  return e;
}

const icon = (name, cls) => el('span', { class: cls ? `mi ${cls}` : 'mi', 'aria-hidden': 'true' }, name);

const iconButton = (name, label, onclick) =>
  el('button', { type: 'button', class: 'icon-btn', title: label, 'aria-label': label, onclick }, icon(name));

const iconLabel = (name) => name.replaceAll('_', ' ');

async function api(method, path, body) {
  const res = await fetch('/api' + path, {
    method,
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 204) return null;
  let data = null;
  try {
    data = await res.json();
  } catch {
    // not JSON; handled below
  }
  if (!res.ok) throw new Error(data?.error || `${res.status} ${res.statusText}`);
  return data;
}

// run calls fn and shows any error in errorBox (the page's error bar by default).
async function run(fn, errorBox = $('#error')) {
  showError(errorBox, '');
  try {
    await fn();
  } catch (e) {
    showError(errorBox, e.message || String(e));
  }
}

function showError(box, msg) {
  const text = box.querySelector('.error-text') || box;
  text.textContent = msg;
  box.hidden = !msg;
}

// Dates

const dateFormat = new Intl.DateTimeFormat(undefined, { weekday: 'short', month: 'short', day: 'numeric' });

function formatDue(s) {
  const [y, m, d] = s.split('-').map(Number);
  return dateFormat.format(new Date(y, m - 1, d));
}

// Loading

async function loadTasks() {
  state.tasks = await api('GET', `/tasks?status=${state.filter}`);
  renderTasks();
}

async function loadCategories() {
  state.categories = await api('GET', '/categories');
  renderCategorySelects();
  renderCategories();
}

// Tasks

function renderTasks() {
  const cats = new Map(state.categories.map((c) => [c.id, c]));
  $('#tasks').replaceChildren(...state.tasks.map((t) => {
    const cat = t.category_id == null ? null : cats.get(t.category_id);
    const meta = [];
    if (t.due) meta.push(`Due ${formatDue(t.due)}`);
    if (cat) meta.push(cat.name);
    if (t.closed_at) meta.push(`Closed ${dateFormat.format(new Date(t.closed_at))}`);
    const closed = t.status === 'closed';
    return el('li', { class: `item p-${t.priority}${closed ? ' closed' : ''}` },
      cat?.icon ? icon(cat.icon, 'cat') : el('span', { class: 'cat' }),
      el('div', { class: 'body' },
        el('div', { class: 'text' }, t.text),
        meta.length ? el('div', { class: 'meta' }, meta.join(' · ')) : null),
      el('div', { class: 'actions' },
        closed ? null : iconButton('check', 'Mark done', () => run(() => closeTask(t))),
        iconButton('edit', 'Edit', () => openTaskDialog(t)),
        iconButton('delete', 'Delete', () => run(() => deleteTask(t)))));
  }));
  const empty = $('#tasks-empty');
  empty.textContent = { open: 'No open tasks', closed: 'No closed tasks', all: 'No tasks' }[state.filter];
  empty.hidden = state.tasks.length > 0;
}

function taskFromForm(form) {
  const f = form.elements;
  return {
    text: f.text.value,
    priority: form.querySelector('input[name=priority]:checked').value,
    due: f.due.value || null,
    category_id: f.category_id.value ? Number(f.category_id.value) : null,
  };
}

async function closeTask(t) {
  await api('POST', `/tasks/${t.id}/close`);
  await loadTasks();
}

async function deleteTask(t) {
  if (!(await confirmDelete(`Delete “${t.text}”? This can't be undone.`))) return;
  await api('DELETE', `/tasks/${t.id}`);
  await loadTasks();
}

function openTaskDialog(t) {
  state.editing = t;
  const form = $('#task-form');
  form.elements.text.value = t.text;
  form.querySelector(`input[name=priority][value=${t.priority}]`).checked = true;
  form.elements.due.value = t.due || '';
  form.elements.category_id.value = t.category_id ?? '';
  showError($('.form-error', form), '');
  $('#task-dialog').showModal();
}

// Categories

function renderCategorySelects() {
  for (const select of document.querySelectorAll('select[name=category_id]')) {
    const value = select.value;
    select.replaceChildren(
      el('option', { value: '' }, 'No category'),
      ...state.categories.map((c) => el('option', { value: c.id }, c.name)));
    select.value = state.categories.some((c) => String(c.id) === value) ? value : '';
  }
}

function renderCategories() {
  $('#categories').replaceChildren(...state.categories.map((c) =>
    el('li', { class: 'item' },
      c.icon ? icon(c.icon, 'cat') : el('span', { class: 'cat' }),
      el('div', { class: 'body' },
        el('div', { class: 'text' }, c.name),
        el('div', { class: 'meta' }, c.icon ? iconLabel(c.icon) : 'No icon')),
      el('div', { class: 'actions' },
        iconButton('delete', 'Delete', () => run(() => deleteCategory(c)))))));
  $('#categories-empty').hidden = state.categories.length > 0;
}

function renderNewCategoryIcon() {
  const name = state.newCategoryIcon;
  $('#new-cat-icon').replaceChildren(
    icon(name || 'block', name ? '' : 'muted'),
    el('span', {}, name ? iconLabel(name) : 'No icon'));
}

async function deleteCategory(c) {
  if (!(await confirmDelete(`Delete category “${c.name}”? Its tasks are kept, without a category.`))) return;
  await api('DELETE', `/categories/${c.id}`);
  await loadCategories();
  await loadTasks();
}

// Icon picker. Resolves to the chosen icon name, '' for "No icon", or null if closed without choosing.

async function pickIcon() {
  if (!state.icons) state.icons = await api('GET', '/icons');
  const dialog = $('#icon-dialog');
  const search = $('#icon-search');
  search.value = '';
  renderIconGrid('');
  dialog.returnValue = '';
  dialog.showModal();
  search.focus();
  return new Promise((resolve) => {
    dialog.addEventListener('close', () => {
      const v = dialog.returnValue;
      resolve(v.startsWith('icon:') ? v.slice(5) : null);
    }, { once: true });
  });
}

function renderIconGrid(query) {
  const q = query.trim().toLowerCase().replace(/[\s-]+/g, '_');
  const matches = q ? state.icons.filter((n) => n.includes(q)) : COMMON_ICONS.filter((n) => state.icons.includes(n));
  const shown = matches.slice(0, MAX_ICONS_SHOWN);
  const choice = (value, glyph, label) =>
    el('button', { type: 'button', class: 'icon-choice', title: label, onclick: () => $('#icon-dialog').close(`icon:${value}`) },
      icon(glyph, value ? '' : 'muted'), el('span', {}, label));
  $('#icon-grid').replaceChildren(
    choice('', 'block', 'No icon'),
    ...shown.map((n) => choice(n, n, iconLabel(n))));
  $('#icon-count').textContent =
    !q ? `Common icons. Type to search all ${state.icons.length.toLocaleString()}.` :
    matches.length === 0 ? 'No icons match' :
    matches.length > shown.length ? `Showing ${shown.length} of ${matches.length.toLocaleString()}, type more to narrow down` :
    `${matches.length} ${matches.length === 1 ? 'icon' : 'icons'}`;
}

// Confirmation dialog. Resolves to true if the user confirmed.

function confirmDelete(message) {
  const dialog = $('#confirm-dialog');
  $('#confirm-text').textContent = message;
  dialog.returnValue = '';
  dialog.showModal();
  return new Promise((resolve) => {
    dialog.addEventListener('close', () => resolve(dialog.returnValue === 'ok'), { once: true });
  });
}

// Tabs and theme

const TABS = ['tasks', 'categories', 'preview'];

function showTab(name) {
  if (!TABS.includes(name)) name = 'tasks';
  for (const b of document.querySelectorAll('[data-tab]')) {
    b.setAttribute('aria-selected', String(b.dataset.tab === name));
  }
  for (const t of TABS) $(`#tab-${t}`).hidden = t !== name;
  if (name === 'preview') $('#preview').src = `/api/image.png?t=${Date.now()}`;
  if (location.hash !== `#${name}`) history.replaceState(null, '', `#${name}`);
}

function setTheme(theme) {
  document.documentElement.dataset.theme = theme;
  const next = theme === 'dark' ? 'light' : 'dark';
  const button = $('#theme');
  $('.mi', button).textContent = `${next}_mode`;
  button.title = `${next[0].toUpperCase()}${next.slice(1)} theme`;
  button.setAttribute('aria-label', button.title);
  try {
    localStorage.setItem('theme', theme);
  } catch {
    // storage unavailable; the theme just won't be remembered
  }
}

// Wiring

function init() {
  setTheme(document.documentElement.dataset.theme === 'light' ? 'light' : 'dark');
  $('#theme').addEventListener('click', () =>
    setTheme(document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark'));

  for (const b of document.querySelectorAll('[data-tab]')) {
    b.addEventListener('click', () => showTab(b.dataset.tab));
  }
  window.addEventListener('hashchange', () => showTab(location.hash.slice(1)));

  $('#error button').addEventListener('click', () => showError($('#error'), ''));

  for (const b of document.querySelectorAll('.clear-date')) {
    b.addEventListener('click', () => { b.previousElementSibling.value = ''; });
  }
  for (const b of document.querySelectorAll('dialog [data-close]')) {
    b.addEventListener('click', () => b.closest('dialog').close());
  }

  // The add form starts collapsed so more of the list fits on screen.
  const setAddFormOpen = (open) => {
    $('#add-task').hidden = !open;
    $('#show-add').hidden = open;
    if (open) $('#add-task').elements.text.focus();
  };
  $('#show-add').addEventListener('click', () => setAddFormOpen(true));
  $('#hide-add').addEventListener('click', () => setAddFormOpen(false));
  $('#add-task').addEventListener('keydown', (e) => {
    if (e.key === 'Escape') setAddFormOpen(false);
  });

  $('#add-task').addEventListener('submit', (e) => {
    e.preventDefault();
    const form = e.target;
    run(async () => {
      await api('POST', '/tasks', taskFromForm(form));
      form.elements.text.value = '';
      form.elements.due.value = '';
      await loadTasks();
      form.elements.text.focus();
    });
  });

  for (const r of document.querySelectorAll('input[name=filter]')) {
    r.addEventListener('change', () => {
      state.filter = r.value;
      run(loadTasks);
    });
  }

  $('#task-form').addEventListener('submit', (e) => {
    e.preventDefault();
    const form = e.target;
    run(async () => {
      await api('PUT', `/tasks/${state.editing.id}`, taskFromForm(form));
      $('#task-dialog').close();
      await loadTasks();
    }, $('.form-error', form));
  });

  $('#new-cat-icon').addEventListener('click', () => run(async () => {
    const name = await pickIcon();
    if (name !== null) {
      state.newCategoryIcon = name;
      renderNewCategoryIcon();
    }
  }));
  $('#icon-search').addEventListener('input', (e) => renderIconGrid(e.target.value));

  $('#add-category').addEventListener('submit', (e) => {
    e.preventDefault();
    const form = e.target;
    run(async () => {
      await api('POST', '/categories', { name: form.elements.name.value, icon: state.newCategoryIcon });
      form.elements.name.value = '';
      state.newCategoryIcon = '';
      renderNewCategoryIcon();
      await loadCategories();
      await loadTasks();
    });
  });

  renderNewCategoryIcon();
  showTab(location.hash.slice(1));
  run(async () => {
    await loadCategories();
    await loadTasks();
  });
}

init();
