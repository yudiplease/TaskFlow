/* TaskFlow — клиент канбан-доски */
'use strict';

const STATUSES = [
  { id: 'backlog',     title: 'Бэклог',    icon: '📦' },
  { id: 'todo',        title: 'К работе',  icon: '🗓️' },
  { id: 'in_progress', title: 'В работе',  icon: '⚙️' },
  { id: 'review',      title: 'Ревью',     icon: '👀' },
  { id: 'done',        title: 'Готово',    icon: '✅' },
];

const PRIORITY_LABELS = { urgent: '🔥 Срочно', high: 'Высокий', medium: 'Средний', low: 'Низкий' };
const ACTOR = 'Вы';

/* ---------- Sticky / smart defaults ----------
   Какие поля «залипают» между задачами. Поля, привязанные к конкретной
   задаче (title, description, id), запоминать нельзя, как и АБСОЛЮТНУЮ
   дату срока (due_at) — иначе новая задача получит просроченный срок.
   Вместо даты залипает только время суток (dueTimeOfDay) и напоминание. */
const STICKY_FIELDS = ['status', 'priority', 'color', 'assignee', 'labels', 'dueTimeOfDay', 'reminderOffsetMinutes'];
const STICKY_FIELD_LABELS = {
  status: 'Колонка', priority: 'Приоритет', color: 'Цвет', assignee: 'Исполнитель',
  labels: 'Метки', dueTimeOfDay: 'Время срока', reminderOffsetMinutes: 'Напоминание',
};
// База, если ни preselect, ни sticky-значения не заданы.
const BASE_TASK_DEFAULTS = {
  status: 'backlog', priority: 'medium', color: '', assignee: '', labels: [],
  dueTimeOfDay: '', reminderOffsetMinutes: null,
};

// Зона пользователя: храним и отдаём её с каждой задачей (due_timezone),
// чтобы граница «дня» у all-day задач не зависела от того, кто смотрит.
const VIEWER_TZ = (Intl.DateTimeFormat().resolvedOptions().timeZone) || 'UTC';

/**
 * Срок задачи, разложенный на части (JSDoc-тип вместо TypeScript-типа
 * из ТЗ — проект на чистом JS):
 * @typedef {Object} DueDateTime
 * @property {string} date            «ГГГГ-ММ-ДД» в зоне timezone
 * @property {string|null} time       «ЧЧ:ММ» или null для «весь день»
 * @property {string} timezone        IANA-зона, в которой задан срок
 * @property {boolean} allDay         весь день — время игнорируем
 */

const state = {
  tasks: [],
  stats: null,
  colors: [], // справочник цветовых меток (GET /api/colors) — единый источник
  mode: 'server', // 'desktop' приходит из /readyz — влияет на демо-данные
  view: 'board',
  search: '',
  filters: { assignee: '', priority: '', color: '', overdue: false },
  editingId: null,
  draggingId: null,
  calMonth: new Date(),
  notifications: [],
  unread: 0,
  lastSeenNotifId: Number(localStorage.getItem('lastNotifId') || 0),
  sticky: {
    enabled: localStorage.getItem('tf_sticky_enabled') !== 'off',
    values: {},       // последние запомненные значения sticky-полей
    updatedAt: null,  // updated_at записи на сервере (для разрешения гонок)
    applied: [],      // какие поля текущей формы заполнены из sticky
  },
};

/* ---------- API ---------- */
async function api(path, options = {}) {
  const res = await fetch('/api' + path, {
    // Кириллицу в заголовках кодируем: fetch допускает только ISO-8859-1.
    headers: { 'Content-Type': 'application/json', 'X-Actor': encodeURIComponent(ACTOR) },
    ...options,
  });
  if (res.status === 204) return null;
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
  return body;
}

/* ---------- Утилиты ---------- */
const $ = (sel) => document.querySelector(sel);

function esc(s) {
  const div = document.createElement('div');
  div.textContent = s ?? '';
  return div.innerHTML;
}

function initials(name) {
  return name.trim().split(/\s+/).slice(0, 2).map((w) => w[0].toUpperCase()).join('');
}

function hueFromString(s) {
  let h = 0;
  for (const c of s) h = (h * 31 + c.charCodeAt(0)) % 360;
  return h;
}

function timeAgo(iso) {
  const sec = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return 'только что';
  if (sec < 3600) return `${Math.floor(sec / 60)} мин назад`;
  if (sec < 86400) return `${Math.floor(sec / 3600)} ч назад`;
  return new Date(iso).toLocaleDateString('ru-RU', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
}

/* ---------- Тосты ---------- */
function toast(message, type = 'success') {
  // Ася (assistant.js) слушает это событие и реагирует на происходящее.
  window.dispatchEvent(new CustomEvent('tf-toast', { detail: { message, type } }));
  const el = document.createElement('div');
  el.className = `toast ${type}`;
  el.innerHTML = `${type === 'success' ? '✓' : '⚠'} ${esc(message)}`;
  $('#toasts').appendChild(el);
  setTimeout(() => {
    el.classList.add('hide');
    setTimeout(() => el.remove(), 300);
  }, 3200);
}

/* ---------- Цветовые метки ---------- */
async function loadColors() {
  try {
    const data = await api('/colors');
    state.colors = data.colors || [];
  } catch {
    state.colors = []; // фильтр/палитра/легенда просто останутся пустыми
  }
  $('#filter-color').innerHTML = '<option value="">Цвет: любой</option>' +
    state.colors.map((c) => `<option value="${esc(c.hex)}">${esc(c.emoji)} ${esc(c.title)}</option>`).join('');
  $('#f-color-palette').innerHTML =
    `<button type="button" class="palette-dot none" data-color="" title="Без цвета">✕</button>` +
    state.colors.map((c) =>
      `<button type="button" class="palette-dot" data-color="${esc(c.hex)}" style="background:${esc(c.hex)}" title="${esc(`${c.emoji} ${c.title} — ${c.description}`)}"></button>`
    ).join('');
  $('#color-legend-list').innerHTML = state.colors.map((c) => `
    <div class="legend-row">
      <span class="legend-dot" style="background:${esc(c.hex)}"></span>
      <div>
        <div class="legend-title">${esc(c.emoji)} ${esc(c.title)}</div>
        <div class="legend-desc">${esc(c.description)}</div>
      </div>
    </div>`).join('');
}

function setColorPalette(hex) {
  const pal = $('#f-color-palette');
  pal.dataset.value = hex || '';
  pal.querySelectorAll('.palette-dot').forEach((d) => {
    d.classList.toggle('selected', (d.dataset.color || '') === (hex || ''));
  });
}

/* ---------- Срок (due_at): разбор, сборка, форматирование ----------
   Храним UTC-инстант (RFC3339) + флаг due_all_day + зону постановки
   due_timezone. Отображение — через Intl.DateTimeFormat в зоне задачи. */
const fmtCache = {};
function intlFmt(tz, opts, key) {
  const id = key + '|' + tz;
  if (!fmtCache[id]) fmtCache[id] = new Intl.DateTimeFormat('ru-RU', { timeZone: tz, ...opts });
  return fmtCache[id];
}

/** Дата «ГГГГ-ММ-ДД» момента d в зоне tz. Собирается из formatToParts:
 *  у многих локалей (включая ru-RU) формат «ДД.ММ.ГГГГ» — а ключи дней
 *  календаря и значение input[type=date] требуют именно ISO-порядок. */
function isoDateInTZ(d, tz) {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: tz, year: 'numeric', month: '2-digit', day: '2-digit',
  }).formatToParts(d);
  const get = (t) => (parts.find((p) => p.type === t) || {}).value || '';
  return `${get('year')}-${get('month')}-${get('day')}`;
}

/** Разбирает дату задачи в DueDateTime (см. typedef выше). null — срока нет. */
function parseDueTask(task) {
  if (!task || !task.due_at) return null;
  const d = new Date(task.due_at);
  if (Number.isNaN(d.getTime())) return null;
  const allDay = !!task.due_all_day;
  const tz = task.due_timezone || VIEWER_TZ;
  // Ключ дня — всегда ISO: им группируется календарь и заполняется форма.
  const date = isoDateInTZ(d, tz);
  let time = null;
  if (!allDay) {
    time = intlFmt(tz, { hour: '2-digit', minute: '2-digit', hour12: false }, 'time').format(d);
  }
  return { date, time, timezone: tz, allDay };
}

/** Собирает RFC3339-инстант из «ГГГГ-ММ-ДД» + «ЧЧ:ММ» (зона пользователя). */
function dueAtISO(date, time, allDay) {
  if (!date) return '';
  if (!time || allDay) {
    // «Весь день» = конец дня (23:59) локального времени — бизнес-правило,
    // унаследованное от дата-онли эпохи; см. миграцию 005_due_at.sql.
    return new Date(date + 'T23:59:00').toISOString();
  }
  // Валидация на стороне клиента: невозможные значения не собираем.
  const m = /^(\d{1,2}):(\d{1,2})$/.exec(time);
  if (!m) return '';
  const h = +m[1], min = +m[2];
  if (h > 23 || min > 59) return '';
  return new Date(date + 'T' + String(h).padStart(2, '0') + ':' + String(min).padStart(2, '0') + ':00').toISOString();
}

/** Формат карточки: «05.11.2026, 18:30» или дата для all-day. */
function formatDue(task) {
  const due = parseDueTask(task);
  if (!due) return null;
  const d = new Date(task.due_at);
  const now = new Date();
  const diffMs = d - now;
  let cls = '';
  if (task.status !== 'done') {
    if (diffMs < 0) cls = 'overdue';
    else if (diffMs < 3600000) cls = 'imminent'; // < 1 часа — акцент + пульсация
    else if (diffMs < 86400000) cls = 'soon';    // < 24 часов — оранжевый
  }
  const dateStr = intlFmt(due.timezone, { day: '2-digit', month: '2-digit', year: 'numeric' }, 'dmy').format(d);
  let label;
  if (due.allDay) {
    label = '📅 ' + dateStr;
  } else {
    const timeStr = intlFmt(due.timezone, { hour: '2-digit', minute: '2-digit' }, 'hm').format(d);
    label = (diffMs < 0 ? '⏰ ' : '📅 ') + `${dateStr}, ${timeStr}`;
  }
  return { label, cls };
}

/** Склонённый остаток для пресетов/подсказок: «1 ч 5 мин». */
function humanizeMinutes(total) {
  if (total < 60) return `${total} мин`;
  const h = Math.floor(total / 60), m = total % 60;
  return m ? `${h} ч ${m} мин` : `${h} ч`;
}

/* ---------- Sticky defaults: синхронизация ----------
   Сервер — источник истины: он сам обновляет значения при сохранении
   задач (POST/PATCH). localStorage — мгновенный кэш; при загрузке
   побеждает та сторона, у которой свежее updated_at.
   Сервер отдаёт/принимает snake_case (due_time_of_day), внутри фронта
   значения живут в camelCase (dueTimeOfDay) — конвертируем на границе. */
const STICKY_SERVER_KEYS = { due_time_of_day: 'dueTimeOfDay', reminder_offset_minutes: 'reminderOffsetMinutes' };

function defaultsFromServer(raw) {
  const out = {};
  for (const [k, v] of Object.entries(raw || {})) out[STICKY_SERVER_KEYS[k] || k] = v;
  return out;
}

function defaultsToServer(values) {
  const camelToSnake = {};
  for (const [snake, camel] of Object.entries(STICKY_SERVER_KEYS)) camelToSnake[camel] = snake;
  const out = {};
  for (const [k, v] of Object.entries(values || {})) out[camelToSnake[k] || k] = v;
  return out;
}

function saveStickyLocal() {
  localStorage.setItem('tf_task_defaults_v1', JSON.stringify({
    defaults: state.sticky.values,
    updated_at: state.sticky.updatedAt,
  }));
}

function normalizeDefaultsPayload(data) {
  const ts = data && data.updated_at ? String(data.updated_at) : '';
  return {
    values: data && data.defaults && Object.keys(data.defaults).length ? defaultsFromServer(data.defaults) : {},
    updatedAt: ts && !ts.startsWith('0001-') ? ts : null,
  };
}

async function pushDefaults(values) {
  const data = await api('/defaults', { method: 'PUT', body: JSON.stringify(defaultsToServer(values)) });
  const norm = normalizeDefaultsPayload(data);
  state.sticky.values = norm.values;
  state.sticky.updatedAt = norm.updatedAt;
  saveStickyLocal();
}

async function loadDefaults() {
  // 1) Мгновенный старт из localStorage.
  try {
    const cached = JSON.parse(localStorage.getItem('tf_task_defaults_v1') || 'null');
    if (cached && cached.defaults) {
      state.sticky.values = cached.defaults;
      state.sticky.updatedAt = cached.updated_at || null;
    }
  } catch { /* повреждённый кэш игнорируем */ }

  // 2) Сверка с сервером по updated_at.
  try {
    const { values, updatedAt } = normalizeDefaultsPayload(await api('/defaults'));
    if (!updatedAt) {
      // На сервере ещё нет записи — переносим туда локальные значения.
      if (Object.keys(state.sticky.values).length) await pushDefaults(state.sticky.values);
    } else if (!state.sticky.updatedAt || new Date(updatedAt) >= new Date(state.sticky.updatedAt)) {
      // Сервер свежее или равен — он источник истины.
      state.sticky.values = values;
      state.sticky.updatedAt = updatedAt;
      saveStickyLocal();
    } else {
      // Локаль свежее (например, сохранение при недоступном сервере) — выгружаем.
      await pushDefaults(state.sticky.values);
    }
  } catch { /* офлайн — работаем с кэшем */ }
}

// После успешного сохранения задачи из формы: сервер уже запомнил значения
// (POST/PATCH делает это сам) — синхронизируем локальный кэш и updated_at.
function rememberStickyFromForm(payload) {
  if (!state.sticky.enabled) return;
  // Форма шлёт все sticky-поля — запоминаем их как есть. Абсолютную дату
  // срока НЕ запоминаем (просрочка у новой задачи), только время суток;
  // all-day или отсутствие срока сбрасывают запомненное время.
  state.sticky.values = {
    status: payload.status,
    priority: payload.priority,
    color: payload.color || '',
    assignee: payload.assignee || '',
    labels: payload.labels || [],
    dueTimeOfDay: payload.due_at && !payload.due_all_day
      ? intlFmt(VIEWER_TZ, { hour: '2-digit', minute: '2-digit', hour12: false }, 'time').format(new Date(payload.due_at))
      : '',
    reminderOffsetMinutes: payload.reminder_offset_minutes || 0,
  };
  saveStickyLocal();
  api('/defaults').then((data) => {
    const norm = normalizeDefaultsPayload(data);
    state.sticky.values = norm.values;
    state.sticky.updatedAt = norm.updatedAt;
    saveStickyLocal();
  }).catch(() => { /* локальные значения уже применены */ });
}

/* Sticky-время срока: подставляем запомненное «ЧЧ:ММ», но НЕ дату.
   ПРАВИЛО (зафиксировано): дата = сегодня; если «сегодня + это время»
   уже прошло — берём завтра, чтобы не подставлять заведомо просроченный
   срок. Возвращает «ГГГГ-ММ-ДД» в зоне пользователя. */
function stickyDueDate(timeOfDay) {
  const [h, m] = timeOfDay.split(':').map(Number);
  const now = new Date();
  const cand = new Date(now.getFullYear(), now.getMonth(), now.getDate(), h || 0, m || 0, 0, 0);
  if (cand <= now) cand.setDate(cand.getDate() + 1);
  return localDateKey(cand);
}

/* ---------- Sticky defaults: заполнение формы ----------
   Приоритет источников: preselected (фильтр/канбан) > sticky-значения
   пользователя > базовые дефолты. */
function initialTaskValues(preselect = {}) {
  const sticky = state.sticky.enabled ? state.sticky.values : {};
  const values = {};
  const applied = [];
  for (const field of STICKY_FIELDS) {
    const base = BASE_TASK_DEFAULTS[field];
    const pre = preselect[field];
    let value = base;
    let fromSticky = false;
    if (Array.isArray(pre) ? pre.length > 0 : (pre !== undefined && pre !== null && pre !== '')) {
      value = pre; // preselected — наивысший приоритет
    } else if (Array.isArray(base)
      ? Array.isArray(sticky[field]) && sticky[field].length > 0
      : (sticky[field] !== undefined && sticky[field] !== null && sticky[field] !== '' && sticky[field] !== 0)) {
      value = sticky[field];
      fromSticky = true;
    }
    values[field] = value;
    // «Предзаполнено» показываем только если значение реально отличается
    // от базового: запомненный status=backlog ничем не отличается от базы.
    if (fromSticky && (Array.isArray(value) ? value.length > 0 : value !== base)) {
      applied.push(field);
    }
  }
  return { values, applied };
}

// preselected из активных фильтров: новая задача наследует контекст доски.
function newTaskPreselect() {
  return {
    priority: state.filters.priority || undefined,
    color: state.filters.color || undefined,
  };
}

/* ---------- Рендер доски ---------- */
function visibleTasks() {
  let tasks = state.tasks;
  const { assignee, priority, color, overdue } = state.filters;
  if (assignee) tasks = tasks.filter((t) => t.assignee === assignee);
  if (priority) tasks = tasks.filter((t) => t.priority === priority);
  if (color) tasks = tasks.filter((t) => t.color === color);
  if (overdue) {
    tasks = tasks.filter((t) => t.due_at && t.status !== 'done' && new Date(t.due_at) < new Date());
  }
  if (state.search) {
    const q = state.search.toLowerCase();
    tasks = tasks.filter(
      (t) => t.title.toLowerCase().includes(q) || (t.description || '').toLowerCase().includes(q)
    );
  }
  return tasks;
}

function colorTitle(hex) {
  const c = state.colors.find((x) => x.hex === hex);
  return c ? `${c.emoji} ${c.title}` : hex;
}

function cardHTML(task) {
  const due = formatDue(task);
  const labels = (task.labels || [])
    .map((l) => `<span class="label-chip">${esc(l)}</span>`)
    .join('');
  const colorDot = task.color
    ? `<span class="card-color" style="background:${esc(task.color)}" title="${esc(colorTitle(task.color))}"></span>`
    : '';
  return `
    <div class="card p-${esc(task.priority)}" draggable="true" data-id="${task.id}">
      <div class="card-top">
        ${colorDot}
        <span class="prio-pill ${esc(task.priority)}">${PRIORITY_LABELS[task.priority] || task.priority}</span>
        ${due ? `<span class="card-due ${due.cls}">${due.label}</span>` : ''}
      </div>
      <div class="card-title">${esc(task.title)}</div>
      ${task.description ? `<div class="card-desc">${esc(task.description)}</div>` : ''}
      ${labels ? `<div class="card-labels">${labels}</div>` : ''}
      <div class="card-bottom">
        ${task.assignee
          ? `<div class="avatar" style="background:hsl(${hueFromString(task.assignee)} 65% 45%)">${esc(initials(task.assignee))}</div>
             <span class="card-assignee">${esc(task.assignee)}</span>`
          : `<div class="avatar empty">?</div><span class="card-assignee">не назначен</span>`}
      </div>
    </div>
  `;
}

function renderBoard() {
  const board = $('#board');
  const tasks = visibleTasks();
  board.innerHTML = STATUSES.map((col) => {
    const items = tasks.filter((t) => t.status === col.id);
    return `
      <div class="column col-${col.id}" data-status="${col.id}">
        <div class="column-head">
          <span class="column-dot"></span>
          <span>${col.icon} ${col.title}</span>
          <span class="column-count">${items.length}</span>
        </div>
        <div class="column-body" data-status="${col.id}">
          ${items.length === 0
            ? `<div class="column-empty">Перетащите задачу<br>или создайте новую</div>`
            : items.map(cardHTML).join('')}
        </div>
      </div>
    `;
  }).join('');
  wireCards();
  wireColumns();
}

function renderStats() {
  if (!state.stats) return;
  const s = state.stats;
  $('#stat-total').textContent = s.total;
  $('#stat-done-today').textContent = s.done_today;
  $('#stat-overdue').textContent = s.overdue;
  $('#stat-unassigned').textContent = s.unassigned;
  const done = (s.by_status && s.by_status.done) || 0;
  const pct = s.total ? Math.round((done / s.total) * 100) : 0;
  $('#board-progress').style.width = pct + '%';
  $('#board-progress-note').textContent = `${pct}% завершено (${done} из ${s.total})`;
}

/* ---------- Drag & Drop ---------- */
function wireCards() {
  document.querySelectorAll('.card').forEach((card) => {
    card.addEventListener('dragstart', (e) => {
      state.draggingId = card.dataset.id;
      card.classList.add('dragging');
      e.dataTransfer.effectAllowed = 'move';
      e.dataTransfer.setData('text/plain', card.dataset.id);
    });
    card.addEventListener('dragend', () => {
      card.classList.remove('dragging');
      state.draggingId = null;
    });
    card.addEventListener('click', () => openModal(card.dataset.id));
  });
}

function wireColumns() {
  document.querySelectorAll('.column-body').forEach((body) => {
    body.addEventListener('dragover', (e) => {
      e.preventDefault();
      e.dataTransfer.dropEffect = 'move';
      body.closest('.column').classList.add('drag-over');
    });
    body.addEventListener('dragleave', (e) => {
      if (!body.contains(e.relatedTarget)) {
        body.closest('.column').classList.remove('drag-over');
      }
    });
    body.addEventListener('drop', (e) => {
      e.preventDefault();
      const column = body.closest('.column');
      column.classList.remove('drag-over');
      const id = e.dataTransfer.getData('text/plain') || state.draggingId;
      if (!id) return;
      const status = body.dataset.status;
      const task = state.tasks.find((t) => t.id === id);
      if (!task || task.status === status) return;

      // Позиция = середина между соседями в месте вставки.
      const cards = [...body.querySelectorAll('.card:not(.dragging)')];
      const after = cards.find((c) => e.clientY < c.getBoundingClientRect().top + c.getBoundingClientRect().height / 2);
      const list = state.tasks.filter((t) => t.status === status).sort((a, b) => a.position - b.position);
      let pos;
      if (!after) {
        pos = list.length ? list[list.length - 1].position + 1000 : 1000;
      } else {
        const afterTask = state.tasks.find((t) => t.id === after.dataset.id);
        const idx = list.findIndex((t) => t.id === afterTask.id);
        const prev = idx > 0 ? list[idx - 1].position : 0;
        pos = (prev + afterTask.position) / 2 || afterTask.position / 2;
      }
      moveTask(id, status, pos);
    });
  });
}

async function moveTask(id, status, position) {
  // Оптимистично двигаем карточку локально.
  const task = state.tasks.find((t) => t.id === id);
  const from = task.status;
  task.status = status;
  task.position = position;
  renderBoard();
  try {
    await api(`/tasks/${id}/move`, {
      method: 'POST',
      body: JSON.stringify({ status, position }),
    });
    toast(`«${task.title}»: ${statusTitle(from)} → ${statusTitle(status)}`);
    await refresh({ silent: true });
  } catch (err) {
    toast(err.message, 'error');
    await refresh({ silent: true });
  }
}

function statusTitle(id) {
  return (STATUSES.find((s) => s.id === id) || {}).title || id;
}

/* ---------- Модальное окно ---------- */
function openModal(taskId = null, presetDate = null, preselect = null) {
  state.editingId = taskId;
  const task = taskId ? state.tasks.find((t) => t.id === taskId) : null;

  $('#modal-title-text').textContent = task ? 'Редактирование' : 'Новая задача';
  $('#btn-save').textContent = task ? 'Сохранить' : 'Создать';
  $('#btn-delete').classList.toggle('hidden', !task);

  if (task) {
    $('#f-title').value = task.title;
    $('#f-description').value = task.description;
    $('#f-status').value = task.status;
    $('#f-priority').value = task.priority;
    $('#f-assignee').value = task.assignee;
    $('#f-labels').value = task.labels ? task.labels.join(', ') : '';
    const due = parseDueTask(task);
    $('#f-due').value = due ? due.date : '';
    $('#f-due-time').value = due && !due.allDay ? due.time : '';
    $('#f-due-allday').checked = due ? due.allDay : true;
    $('#f-reminder').value = task.reminder_offset_minutes ? String(task.reminder_offset_minutes) : '';
    setColorPalette(task.color);
    state.sticky.applied = [];
    $('#modal-sticky-note').classList.add('hidden');
  } else {
    const { values, applied } = initialTaskValues(preselect || newTaskPreselect());
    $('#f-title').value = '';
    $('#f-description').value = '';
    $('#f-status').value = values.status;
    $('#f-priority').value = values.priority;
    $('#f-assignee').value = values.assignee;
    $('#f-labels').value = values.labels.join(', ');
    setColorPalette(values.color);
    // Срок: preselect из календаря (конкретный день) > sticky-время суток
    // (сегодня/завтра) > пусто. «Весь день» включён по умолчанию —
    // совместимо со старым поведением «только дата».
    let dueDate = presetDate || '';
    let dueTime = '';
    if (!presetDate && values.dueTimeOfDay) {
      dueDate = stickyDueDate(values.dueTimeOfDay);
      dueTime = values.dueTimeOfDay;
    }
    $('#f-due').value = dueDate;
    $('#f-due-time').value = dueTime;
    $('#f-due-allday').checked = !dueTime;
    $('#f-reminder').value = values.reminderOffsetMinutes ? String(values.reminderOffsetMinutes) : '';
    state.sticky.applied = applied;
    $('#modal-sticky-note').classList.toggle('hidden', !applied.length);
  }
  syncDueTimeInputs();

  const wrap = $('#modal-timeline-wrap');
  if (task) {
    loadTaskTimeline(task.id);
    wrap.classList.remove('hidden');
  } else {
    wrap.classList.add('hidden');
  }

  $('#modal-backdrop').classList.remove('hidden');
  setTimeout(() => $('#f-title').focus(), 50);
}

/* DateTimePicker: «Весь день» прячет поле времени; live-подсказка,
   если время сегодня уже прошло (предупреждение, но не блок). */
function syncDueTimeInputs() {
  const allDay = $('#f-due-allday').checked;
  $('#f-due-time').classList.toggle('hidden', allDay);
  $('#f-due-time').disabled = allDay;
  updateDueWarning();
}

function updateDueWarning() {
  const date = $('#f-due').value;
  const time = $('#f-due-time').value;
  const warn = $('#f-due-warn');
  if (!date || !time || $('#f-due-allday').checked) {
    warn.classList.add('hidden');
    return;
  }
  const when = new Date(date + 'T' + time + ':00');
  warn.classList.toggle('hidden', !(when <= new Date() && !state.editingId));
}

function wireDuePicker() {
  $('#f-due-allday').addEventListener('change', syncDueTimeInputs);
  $('#f-due').addEventListener('input', updateDueWarning);
  $('#f-due-time').addEventListener('input', updateDueWarning);

  // Быстрые пресеты. «Пн 09:00» = ближайший ПОНЕДЕЛЬНИК (не сегодня).
  $('#f-due-presets').addEventListener('click', (e) => {
    const btn = e.target.closest('.chip-btn');
    if (!btn) return;
    const now = new Date();
    switch (btn.dataset.preset) {
      case 'today1800':
        $('#f-due').value = localDateKey(now);
        $('#f-due-time').value = '18:00';
        break;
      case 'tomorrow1000': {
        const t = new Date(now); t.setDate(t.getDate() + 1);
        $('#f-due').value = localDateKey(t);
        $('#f-due-time').value = '10:00';
        break;
      }
      case 'nextMon0900': {
        const mon = new Date(now);
        mon.setDate(mon.getDate() + ((8 - mon.getDay()) % 7 || 7)); // след. понедельник
        $('#f-due').value = localDateKey(mon);
        $('#f-due-time').value = '09:00';
        break;
      }
      case 'plus1h': {
        const in1h = new Date(now.getTime() + 3600000);
        in1h.setMinutes(in1h.getMinutes() >= 55 ? 59 : Math.round(in1h.getMinutes() / 5) * 5);
        $('#f-due').value = localDateKey(in1h);
        $('#f-due-time').value = `${String(in1h.getHours()).padStart(2, '0')}:${String(in1h.getMinutes()).padStart(2, '0')}`;
        break;
      }
    }
    $('#f-due-allday').checked = false;
    syncDueTimeInputs();
  });
}

function closeModal() {
  $('#modal-backdrop').classList.add('hidden');
  state.editingId = null;
}

async function loadTaskTimeline(taskId) {
  try {
    const data = await api(`/tasks/${taskId}/activity?limit=30`);
    const items = data.activity.map(
      (a) => `
        <div class="timeline-item">
          <div>
            <div class="timeline-text">${activityText(a)}</div>
            <div class="timeline-time">${esc(a.actor)} · ${timeAgo(a.created_at)}</div>
          </div>
        </div>
      `
    );
    $('#modal-timeline').innerHTML = items.length
      ? items.join('')
      : '<div class="timeline-item"><div class="timeline-text" style="color:var(--text-faint)">Событий пока нет</div></div>';
  } catch {
    $('#modal-timeline').innerHTML = '<div class="timeline-item"><div class="timeline-text" style="color:var(--text-faint)">Не удалось загрузить историю</div></div>';
  }
}

function activityText(a) {
  const p = a.payload || {};
  switch (a.type) {
    case 'task.created':
      return `создал(а) задачу <b>«${esc(p.title || '')}»</b>`;
    case 'task.status_changed':
      return `перенёс(ла) <b>«${esc(p.title || '')}»</b>: ${esc(statusTitle(p.from))} → <b>${esc(statusTitle(p.to))}</b>`;
    case 'task.updated':
      return `обновил(а) <b>«${esc(p.title || '')}»</b>`;
    case 'task.deleted':
      return `удалил(а) <b>«${esc(p.title || '')}»</b>`;
    case 'task.due_soon':
      return `напоминает: у <b>«${esc(p.title || '')}»</b> срок ${esc(dueLabel(p.due_at))}`;
    case 'task.overdue':
      return `⚠ просрочена <b>«${esc(p.title || '')}»</b> (срок ${esc(dueLabel(p.due_at))})`;
    case 'task.reminder':
      return `🔔 персональное напоминание: <b>«${esc(p.title || '')}»</b> — срок близко`;
    default:
      return esc(a.type);
  }
}

function dueLabel(iso) {
  if (!iso) return '';
  return new Date(iso).toLocaleDateString('ru-RU', { day: 'numeric', month: 'long' });
}

function activityIcon(type) {
  switch (type) {
    case 'task.created': return '✨';
    case 'task.status_changed': return '🔀';
    case 'task.updated': return '✏️';
    case 'task.deleted': return '🗑️';
    case 'task.due_soon': return '🗓️';
    case 'task.overdue': return '⏰';
    case 'task.reminder': return '🔔';
    default: return '⚡';
  }
}

/* ---------- Лента активности ---------- */
async function loadActivity() {
  try {
    const data = await api('/activity?limit=40');
    const feed = $('#activity-feed');
    feed.innerHTML = data.activity.length
      ? data.activity
          .map(
            (a) => `
        <div class="feed-item">
          <div class="feed-icon">${activityIcon(a.type)}</div>
          <div class="feed-body">
            <div class="feed-line"><b>${esc(a.actor)}</b> ${activityText(a)}</div>
            <div class="feed-meta">
              <span>${timeAgo(a.created_at)}</span>
              <span class="feed-kafka">Kafka ✓</span>
            </div>
          </div>
        </div>`
          )
          .join('')
      : '<div class="column-empty" style="margin-top:20px">Событий пока нет.<br>Создайте или переместите задачу — событие пройдёт через Kafka и попадёт сюда.</div>';
  } catch {
    $('#activity-feed').innerHTML = '<div class="column-empty" style="margin-top:20px">Лента недоступна</div>';
  }
}

/* ---------- Календарь ---------- */
const MONTHS = ['Январь','Февраль','Март','Апрель','Май','Июнь','Июль','Август','Сентябрь','Октябрь','Ноябрь','Декабрь'];
const DOW = ['Пн','Вт','Ср','Чт','Пт','Сб','Вс'];

function dateKey(iso) {
  return localDateKey(new Date(iso));
}

// Ключ даты в локальном часовом поясе (YYYY-MM-DD).
function localDateKey(d) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

function tasksByDate() {
  const map = {};
  for (const t of state.tasks) {
    const due = parseDueTask(t);
    if (!due) continue;
    // Ключ дня — в зоне постановки срока (граница «дня» у all-day задач).
    (map[due.date] = map[due.date] || []).push(t);
  }
  // Внутри дня — по времени срока; all-day (23:59) естественно уходит в конец.
  for (const k of Object.keys(map)) {
    map[k].sort((a, b) => (dueSortKey(a) - dueSortKey(b)) || (a.position - b.position));
  }
  return map;
}

// Ключ сортировки по сроку: all-day ставим в конец дня (23:59).
function dueSortKey(t) {
  if (!t.due_at) return Infinity;
  const d = new Date(t.due_at);
  if (t.due_all_day) {
    return new Date(d.getFullYear(), d.getMonth(), d.getDate(), 23, 59).getTime();
  }
  return d.getTime();
}

function renderCalendar() {
  const grid = $('#cal-grid');
  const y = state.calMonth.getFullYear();
  const m = state.calMonth.getMonth();
  $('#cal-title').textContent = `${MONTHS[m]} ${y}`;

  const first = new Date(y, m, 1);
  // Пн = 0: сдвигаем воскресенье (0) в конец недели
  const offset = (first.getDay() + 6) % 7;
  const start = new Date(y, m, 1 - offset);

  const byDate = tasksByDate();
  const todayKey = dateKey(new Date());

  let html = DOW.map((d) => `<div class="cal-dow">${d}</div>`).join('');
  for (let i = 0; i < 42; i++) {
    const day = new Date(start.getFullYear(), start.getMonth(), start.getDate() + i);
    const key = dateKey(day);
    const other = day.getMonth() !== m;
    const tasks = byDate[key] || [];
    const shown = tasks.slice(0, 3);
    html += `
      <div class="cal-cell ${other ? 'other-month' : ''} ${key === todayKey ? 'today' : ''}" data-date="${key}">
        <div class="cal-day-num">
          ${day.getDate()}
          <button class="cal-add" data-date="${key}" title="Создать задачу на этот день">＋</button>
        </div>
        ${shown.map((t) => `
          <div class="cal-task p-${esc(t.priority)} ${t.status === 'done' ? 'done' : ''}" draggable="true" data-id="${t.id}" title="${esc(t.title)}"${t.color ? ` style="border-left:3px solid ${esc(t.color)}"` : ''}>
            <span class="cal-task-title">${esc(t.title)}</span>
          </div>`).join('')}
        ${tasks.length > 3 ? `<div class="cal-more">＋ ещё ${tasks.length - 3}</div>` : ''}
      </div>
    `;
  }
  grid.innerHTML = html;

  // Несроковые задачи — можно перетащить в день
  const unscheduled = state.tasks.filter((t) => !t.due_at);
  $('#cal-unscheduled').innerHTML = unscheduled.length
    ? `<div class="cal-unscheduled-title">Без срока — перетащите в день календаря</div>
       <div class="cal-unscheduled-list">
        ${unscheduled.map((t) => `
          <div class="cal-chip" draggable="true" data-id="${t.id}">
            <span class="dot" style="background:${prioColor(t.priority)}"></span>${esc(t.title)}
          </div>`).join('')}
       </div>`
    : '';

  wireCalendar();
}

function prioColor(p) {
  return { urgent: '#f87171', high: '#fbbf24', medium: '#60a5fa', low: '#64748b' }[p] || '#6d7dff';
}

function wireCalendar() {
  document.querySelectorAll('.cal-task, .cal-chip').forEach((el) => {
    el.addEventListener('dragstart', (e) => {
      state.draggingId = el.dataset.id;
      e.dataTransfer.effectAllowed = 'move';
      e.dataTransfer.setData('text/plain', el.dataset.id);
    });
    el.addEventListener('click', (e) => {
      e.stopPropagation();
      openModal(el.dataset.id);
    });
  });

  document.querySelectorAll('.cal-cell').forEach((cell) => {
    cell.addEventListener('dragover', (e) => {
      e.preventDefault();
      cell.classList.add('drag-over');
    });
    cell.addEventListener('dragleave', () => cell.classList.remove('drag-over'));
    cell.addEventListener('drop', (e) => {
      e.preventDefault();
      cell.classList.remove('drag-over');
      const id = e.dataTransfer.getData('text/plain') || state.draggingId;
      if (!id) return;
      rescheduleTask(id, cell.dataset.date);
    });
    cell.addEventListener('dblclick', () => openModal(null, cell.dataset.date));
  });

  document.querySelectorAll('.cal-add').forEach((btn) => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      openModal(null, btn.dataset.date);
    });
  });
}

async function rescheduleTask(id, dateStr) {
  const task = state.tasks.find((t) => t.id === id);
  if (!task) return;
  // Перенос дня сохраняет время срока: 18:30 из вторника → 18:30 в среду;
  // у all-day (или бессрочного по времени) остаётся «весь день».
  const due = parseDueTask(task);
  const time = due && !due.allDay ? due.time : '';
  try {
    await api(`/tasks/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({
        due_at: dueAtISO(dateStr, time, !time),
        due_all_day: !time,
        due_timezone: VIEWER_TZ,
      }),
    });
    toast(`«${task.title}» → ${new Date(dateStr + 'T12:00').toLocaleDateString('ru-RU', { day: 'numeric', month: 'long' })}`);
    await refresh({ silent: true });
  } catch (err) {
    toast(err.message, 'error');
  }
}

/* ---------- Уведомления ---------- */
function renderBell() {
  const badge = $('#bell-badge');
  badge.textContent = state.unread > 99 ? '99+' : state.unread;
  badge.classList.toggle('hidden', state.unread === 0);

  const list = $('#notif-list');
  if (!state.notifications.length) {
    list.innerHTML = `<div class="notif-empty">Пока пусто.<br>Планировщик напомнит о задачах со сроком «сегодня», «завтра» и просроченных.</div>`;
    return;
  }
  list.innerHTML = state.notifications
    .map((n) => `
      <div class="notif-item ${n.read ? '' : 'unread'}" data-id="${n.id}" data-task="${n.task_id}">
        <div class="notif-icon">${n.type === 'overdue' ? '⏰' : n.type === 'reminder' ? '🔔' : '🗓️'}</div>
        <div class="notif-body">
          <div class="notif-title">${esc(n.title)}</div>
          <div class="notif-msg">${esc(n.message)}</div>
          <div class="notif-time">${timeAgo(n.created_at)}</div>
        </div>
      </div>`)
    .join('');
}

async function pollNotifications({ announce = false } = {}) {
  try {
    const res = await api('/notifications?limit=30');
    state.notifications = res.notifications;
    state.unread = res.unread;
    renderBell();

    const maxId = res.notifications.reduce((m, n) => Math.max(m, n.id), 0);
    if (announce && maxId > state.lastSeenNotifId) {
      const fresh = res.notifications.filter((n) => n.id > state.lastSeenNotifId && !n.read);
      for (const n of fresh.slice(0, 3)) {
        toast(`${n.type === 'overdue' ? '⏰' : '🗓️'} ${n.title}: ${n.message}`);
        showBrowserNotification(n);
      }
    }
    if (maxId > state.lastSeenNotifId) {
      state.lastSeenNotifId = maxId;
      localStorage.setItem('lastNotifId', String(maxId));
    }
  } catch {
    /* панель обновится при следующем опросе */
  }
}

function showBrowserNotification(n) {
  if (!('Notification' in window) || Notification.permission !== 'granted') return;
  try {
    new Notification(`TaskFlow: ${n.title}`, { body: n.message, tag: `task-${n.task_id}` });
  } catch {
    /* браузер может требовать service worker — тост уже показан */
  }
}

function wireNotifications() {
  $('#btn-bell').addEventListener('click', (e) => {
    e.stopPropagation();
    const panel = $('#notif-panel');
    panel.classList.toggle('hidden');
    if (!panel.classList.contains('hidden')) {
      pollNotifications();
      if ('Notification' in window && Notification.permission === 'default') {
        // Спрашиваем разрешение при первом открытии панели
        Notification.requestPermission().then(() => updatePushButton());
      }
      updatePushButton();
    }
  });
  document.addEventListener('click', (e) => {
    if (!e.target.closest('.bell-wrap')) $('#notif-panel').classList.add('hidden');
    if (!e.target.closest('.legend-wrap')) $('#color-legend').classList.add('hidden');
  });

  $('#btn-enable-push').addEventListener('click', async () => {
    if (!('Notification' in window)) return;
    await Notification.requestPermission();
    updatePushButton();
    toast(Notification.permission === 'granted' ? 'Браузерные уведомления включены' : 'Уведомления не разрешены', Notification.permission === 'granted' ? 'success' : 'error');
  });

  $('#btn-read-all').addEventListener('click', async () => {
    try {
      await api('/notifications/read-all', { method: 'POST' });
      await pollNotifications();
    } catch (err) {
      toast(err.message, 'error');
    }
  });

  $('#notif-list').addEventListener('click', async (e) => {
    const item = e.target.closest('.notif-item');
    if (!item) return;
    try {
      await api(`/notifications/${item.dataset.id}/read`, { method: 'POST' });
    } catch { /* уже прочитано */ }
    $('#notif-panel').classList.add('hidden');
    const task = state.tasks.find((t) => t.id === item.dataset.task);
    if (task) openModal(task.id);
    pollNotifications();
  });
}

function updatePushButton() {
  const can = 'Notification' in window && Notification.permission === 'granted';
  $('#btn-enable-push').classList.toggle('hidden', can);
}

/* ---------- Настройки профиля ---------- */
function openSettings() {
  $('#settings-sticky-enabled').checked = state.sticky.enabled;
  renderSettingsDefaults();
  syncChibiSettingsUI();
  // Секция обновлений видна только когда сервер отвечает /api/updates/state;
  // на случай, пока автопрос fresh, перерисовываем из последнего снимка.
  if (updatesUI.status) renderUpdatesState(updatesUI.status);
  $('#settings-backdrop').classList.remove('hidden');
}

function closeSettings() {
  $('#settings-backdrop').classList.add('hidden');
}

/* ---------- Обновления (MVP GitHub Releases) ---------- */
// Баннер и секция настроек живут от GET /api/updates/state (см. также
// chibi-секцию выше). «×» — только в памяти сессии: до следующего запуска,
// версию это НЕ помечает пропущенной.
const updatesUI = {
  dismissed: false, // «×» нажат в этой сессии
  url: '',          // html_url последнего известного релиза
  status: null,     // последний ответ сервера — для перерисовки при открытии настроек
};

// «Последняя проверка» — в TZ пользователя, через Intl.
const updatesDtFormat = new Intl.DateTimeFormat('ru-RU', {
  day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit',
});

async function fetchUpdatesState() {
  try {
    const st = await api('/updates/state');
    renderUpdatesState(st);
    $('#updates-settings-section').classList.remove('hidden');
  } catch {
    // 503 (режим без проверки обновлений) или сеть — секцию и баннер молча скрываем.
    $('#updates-settings-section').classList.add('hidden');
    $('#update-banner').classList.add('hidden');
  }
}

function renderUpdatesState(st) {
  updatesUI.status = st;
  if (st.latest && st.latest.url) updatesUI.url = st.latest.url;
  renderUpdateLastCheck(st.last_checked_at);
  $('#settings-updates-enabled').checked = st.enabled;
  $('#settings-updates-interval').value = String(st.interval_hours || 24);
  const banner = $('#update-banner');
  if (st.update_available && !updatesUI.dismissed) {
    $('#update-banner-text').textContent = `Доступна версия ${st.latest.tag}`;
    banner.classList.remove('hidden');
  } else {
    banner.classList.add('hidden');
  }
}

function renderUpdateLastCheck(iso) {
  $('#update-last-check').textContent = iso
    ? `Последняя проверка: ${updatesDtFormat.format(new Date(iso))}`
    : 'Последняя проверка: ещё не было';
}

// Внешние ссылки: в десктопе — мост tfOpenExternal (Go открывает системный
// браузер), в обычном браузере — window.open.
function openExternal(url) {
  if (typeof window.tfOpenExternal === 'function') window.tfOpenExternal(url);
  else window.open(url, '_blank', 'noopener');
}

function bindUpdatesUI() {
  $('#update-whatsnew').addEventListener('click', () => {
    if (updatesUI.url) openExternal(updatesUI.url);
  });
  $('#update-close').addEventListener('click', () => {
    updatesUI.dismissed = true;
    $('#update-banner').classList.add('hidden');
  });
  $('#settings-updates-enabled').addEventListener('change', saveUpdatesSettings);
  $('#settings-updates-interval').addEventListener('change', saveUpdatesSettings);
  $('#btn-check-updates').addEventListener('click', checkUpdatesNow);
}

async function saveUpdatesSettings() {
  try {
    const st = await api('/updates/settings', {
      method: 'PUT',
      body: JSON.stringify({
        enabled: $('#settings-updates-enabled').checked,
        interval_hours: Number($('#settings-updates-interval').value),
      }),
    });
    renderUpdatesState(st);
  } catch (err) {
    toast('Не удалось сохранить настройки обновлений: ' + err.message, 'error');
  }
}

// Кнопка «Проверить сейчас»: блокируем на время запроса. Выбор зафиксирован:
// если новой версии нет — тост «У вас актуальная версия» (явный клик требует
// явного ответа, тишина выглядела бы как подвисание); ошибка — единственное
// место, где пользователь видит сбой проверки.
async function checkUpdatesNow() {
  const btn = $('#btn-check-updates');
  btn.disabled = true;
  try {
    const st = await api('/updates/check', { method: 'POST' });
    renderUpdatesState(st);
    if (!st.update_available) toast('У вас актуальная версия ✨');
  } catch {
    toast('Не удалось проверить обновления', 'error');
  } finally {
    btn.disabled = false;
  }
}

/* ---------- Настройки звуков персонажа (chibi-sounds.js) ---------- */
function syncChibiSettingsUI() {
  const svc = window.chibiSounds;
  if (!svc) return;
  const s = svc.getSettings();
  $('#settings-chibi-enabled').checked = s.enabled;
  $('#settings-chibi-volume').value = Math.round(s.volume * 100);
  $('#chibi-volume-value').textContent = Math.round(s.volume * 100) + '%';
  $('#settings-chibi-click-only').checked = s.clickOnly;
}

function bindChibiSettings() {
  const svc = window.chibiSounds;
  if (!svc) return; // скрипт не загрузился — секция настроек просто не действует

  $('#settings-chibi-enabled').addEventListener('change', (e) => {
    svc.setEnabled(e.target.checked);
  });

  // Ползунок: подпись обновляется на каждый пиксель, семпл громкости
  // играем один раз — при отпускании (change), чтобы не «пулемётить».
  $('#settings-chibi-volume').addEventListener('input', (e) => {
    $('#chibi-volume-value').textContent = e.target.value + '%';
  });
  $('#settings-chibi-volume').addEventListener('change', (e) => {
    svc.setVolume(Number(e.target.value) / 100);
    svc.play('hum');
  });

  $('#settings-chibi-click-only').addEventListener('change', (e) => {
    svc.setClickOnly(e.target.checked);
  });
}

function stickyFieldValueHTML(field, value) {
  const dash = '<span class="sticky-empty">—</span>';
  switch (field) {
    case 'status': return value ? esc(statusTitle(value)) : dash;
    case 'priority': return value ? esc(PRIORITY_LABELS[value] || value) : dash;
    case 'color': return value
      ? `<span class="sticky-color" style="background:${esc(value)}"></span>${esc(colorTitle(value))}`
      : dash;
    case 'assignee': return value ? esc(value) : dash;
    case 'labels': return value && value.length
      ? value.map((l) => `<span class="label-chip">${esc(l)}</span>`).join(' ')
      : dash;
    case 'dueTimeOfDay': return value ? esc(value) : dash;
    case 'reminderOffsetMinutes': return value ? `за ${esc(humanizeMinutes(value))}` : dash;
    default: return value ? esc(String(value)) : dash;
  }
}

function renderSettingsDefaults() {
  const list = $('#sticky-values-list');
  if (!list) return;
  list.innerHTML = STICKY_FIELDS.map((field) => `
    <div class="sticky-row">
      <span class="sticky-row-label">${STICKY_FIELD_LABELS[field]}</span>
      <span class="sticky-row-value">${stickyFieldValueHTML(field, state.sticky.values[field])}</span>
      <button type="button" class="sticky-row-reset" data-field="${field}" title="Сбросить это поле">✕</button>
    </div>`).join('');
}

/* ---------- Данные ---------- */
async function refresh({ silent = false } = {}) {
  try {
    const [listData, stats] = await Promise.all([api('/tasks?limit=500'), api('/stats')]);
    state.tasks = listData.tasks || [];
    state.stats = stats;
    renderBoard();
    renderCalendar();
    renderStats();
  } catch (err) {
    if (!silent) toast('Не удалось загрузить данные: ' + err.message, 'error');
  }
}

async function checkHealth() {
  const el = $('#health');
  try {
    const res = await fetch('/readyz');
    const body = await res.json();
    if (!res.ok) throw new Error(body.error || 'хранилище недоступно');
    state.mode = body.mode || 'server';
    if (state.mode === 'desktop') {
      $('.brand-sub').textContent = 'Desktop · SQLite · локально';
    }
    if (body.kafka === 'ok') {
      el.className = 'health ok';
      $('#health-text').textContent = 'все системы в норме';
    } else {
      el.className = 'health degraded';
      $('#health-text').textContent = 'работа: Kafka недоступна';
    }
  } catch {
    el.className = 'health down';
    $('#health-text').textContent = 'сервер недоступен';
  }
}

/* ---------- События ---------- */
function bindEvents() {
  wireDuePicker();
  $('#btn-new').addEventListener('click', () => openModal(null, null, newTaskPreselect()));
  $('#modal-close').addEventListener('click', closeModal);
  $('#btn-cancel').addEventListener('click', closeModal);
  $('#modal-backdrop').addEventListener('click', (e) => {
    if (e.target === $('#modal-backdrop')) closeModal();
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      closeModal();
      closeSettings();
    }
    if (e.key === '/' && !e.target.closest('input, textarea, select')) {
      e.preventDefault();
      $('#search').focus();
    }
  });

  // Индикатор «Предзаполнено из прошлой задачи»: вернуть sticky-поля
  // к базовым значениям (само запомненное не стирается).
  $('#btn-sticky-reset').addEventListener('click', () => {
    for (const field of state.sticky.applied) {
      if (field === 'status') $('#f-status').value = BASE_TASK_DEFAULTS.status;
      else if (field === 'priority') $('#f-priority').value = BASE_TASK_DEFAULTS.priority;
      else if (field === 'color') setColorPalette(BASE_TASK_DEFAULTS.color);
      else if (field === 'assignee') $('#f-assignee').value = BASE_TASK_DEFAULTS.assignee;
      else if (field === 'labels') $('#f-labels').value = BASE_TASK_DEFAULTS.labels.join(', ');
      else if (field === 'dueTimeOfDay') {
        // Время срока не залипает датой: сброс = пустой срок.
        $('#f-due').value = '';
        $('#f-due-time').value = '';
        $('#f-due-allday').checked = true;
        syncDueTimeInputs();
      } else if (field === 'reminderOffsetMinutes') {
        $('#f-reminder').value = '';
      }
    }
    state.sticky.applied = [];
    $('#modal-sticky-note').classList.add('hidden');
  });

  $('#btn-delete').addEventListener('click', async () => {
    if (!state.editingId) return;
    const task = state.tasks.find((t) => t.id === state.editingId);
    if (!confirm(`Удалить задачу «${task ? task.title : ''}»?`)) return;
    try {
      await api(`/tasks/${state.editingId}`, { method: 'DELETE' });
      toast('Задача удалена');
      closeModal();
      await refresh({ silent: true });
    } catch (err) {
      toast(err.message, 'error');
    }
  });

  $('#task-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const allDay = $('#f-due-allday').checked;
    const dueAt = dueAtISO($('#f-due').value, $('#f-due-time').value, allDay);
    const payload = {
      title: $('#f-title').value.trim(),
      description: $('#f-description').value.trim(),
      status: $('#f-status').value,
      priority: $('#f-priority').value,
      assignee: $('#f-assignee').value.trim(),
      labels: $('#f-labels').value.split(',').map((s) => s.trim()).filter(Boolean),
      color: $('#f-color-palette').dataset.value || '',
      // Срок: пустая строка = убрать срок; all-day = 23:59 локального дня.
      due_at: dueAt,
      due_all_day: allDay && !!dueAt,
      due_timezone: VIEWER_TZ,
      reminder_offset_minutes: $('#f-reminder').value ? Number($('#f-reminder').value) : null,
      // Опция «Запоминать параметры» выключена — просим сервер не запоминать.
      skip_sticky_update: !state.sticky.enabled,
    };
    try {
      if (state.editingId) {
        await api(`/tasks/${state.editingId}`, { method: 'PATCH', body: JSON.stringify(payload) });
        toast('Задача обновлена');
      } else {
        await api('/tasks', { method: 'POST', body: JSON.stringify(payload) });
        toast('Задача создана');
      }
      rememberStickyFromForm(payload);
      closeModal();
      await refresh({ silent: true });
    } catch (err) {
      toast(err.message, 'error');
    }
  });

  let searchTimer;
  $('#search').addEventListener('input', (e) => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      state.search = e.target.value.trim();
      renderBoard();
    }, 150);
  });

  $('#filter-assignee').addEventListener('input', (e) => {
    state.filters.assignee = e.target.value.trim();
    renderBoard();
  });
  $('#filter-priority').addEventListener('change', (e) => {
    state.filters.priority = e.target.value;
    renderBoard();
  });
  $('#filter-color').addEventListener('change', (e) => {
    state.filters.color = e.target.value;
    renderBoard();
  });
  $('#f-color-palette').addEventListener('click', (e) => {
    const dot = e.target.closest('.palette-dot');
    if (!dot) return;
    setColorPalette(dot.dataset.color);
  });
  $('#btn-legend').addEventListener('click', (e) => {
    e.stopPropagation();
    $('#color-legend').classList.toggle('hidden');
  });
  $('#filter-overdue').addEventListener('change', (e) => {
    state.filters.overdue = e.target.checked;
    renderBoard();
  });

  $('#nav-board').addEventListener('click', () => switchView('board'));
  $('#nav-calendar').addEventListener('click', () => switchView('calendar'));
  $('#nav-activity').addEventListener('click', () => switchView('activity'));
  $('#nav-settings').addEventListener('click', openSettings);

  $('#settings-close').addEventListener('click', closeSettings);
  $('#settings-backdrop').addEventListener('click', (e) => {
    if (e.target === $('#settings-backdrop')) closeSettings();
  });

  $('#settings-sticky-enabled').addEventListener('change', (e) => {
    state.sticky.enabled = e.target.checked;
    localStorage.setItem('tf_sticky_enabled', e.target.checked ? 'on' : 'off');
  });

  bindChibiSettings();
  bindUpdatesUI();

  // Сброс одного sticky-поля: PUT без него (пустое значение = «не запомнено»).
  $('#sticky-values-list').addEventListener('click', async (e) => {
    const btn = e.target.closest('.sticky-row-reset');
    if (!btn) return;
    try {
      const next = { ...state.sticky.values };
      delete next[btn.dataset.field];
      await pushDefaults(next);
      renderSettingsDefaults();
      toast(`«${STICKY_FIELD_LABELS[btn.dataset.field]}» сброшено`);
    } catch (err) {
      toast(err.message, 'error');
    }
  });

  $('#btn-sticky-reset-all').addEventListener('click', async () => {
    try {
      await pushDefaults({});
      renderSettingsDefaults();
      toast('Значения по умолчанию сброшены');
    } catch (err) {
      toast(err.message, 'error');
    }
  });

  $('#cal-prev').addEventListener('click', () => shiftMonth(-1));
  $('#cal-next').addEventListener('click', () => shiftMonth(1));
  $('#cal-today').addEventListener('click', () => {
    state.calMonth = new Date();
    renderCalendar();
  });

  wireNotifications();
}

function shiftMonth(delta) {
  const d = new Date(state.calMonth);
  d.setMonth(d.getMonth() + delta);
  state.calMonth = d;
  renderCalendar();
}

function switchView(view) {
  state.view = view;
  $('#nav-board').classList.toggle('active', view === 'board');
  $('#nav-calendar').classList.toggle('active', view === 'calendar');
  $('#nav-activity').classList.toggle('active', view === 'activity');
  $('#board').classList.toggle('hidden', view !== 'board');
  $('#calendar-view').classList.toggle('hidden', view !== 'calendar');
  $('#activity-view').classList.toggle('hidden', view !== 'activity');
  $('#page-title').textContent =
    view === 'board' ? 'Канбан-доска' : view === 'calendar' ? 'Календарь' : 'Лента активности';
  if (view === 'calendar') renderCalendar();
  if (view === 'activity') loadActivity();
}

/* ---------- Старт ---------- */
async function seedDemoData() {
  // Демо-задачи — только для серверного режима (docker compose).
  // Десктоп стартует с чистой доской.
  if (state.mode !== 'server') return;
  try {
    const { total } = await api('/stats');
    if (total > 0) return;
    const demo = [
      { title: 'Спроектировать схему БД', description: 'Таблицы tasks и activity, индексы, миграции.', status: 'done', priority: 'high', assignee: 'Анна', labels: ['backend', 'бд'] },
      { title: 'Настроить Kafka-топик событий', description: '3 партиции, ключ = ID задачи для упорядоченности.', status: 'done', priority: 'high', assignee: 'Михаил', labels: ['инфраструктура'] },
      { title: 'REST API для задач', description: 'CRUD + перенос между колонками + журнал активности.', status: 'review', priority: 'urgent', assignee: 'Анна', labels: ['backend', 'api'] },
      { title: 'Дашборд Grafana', description: 'RPS, латентность p95, операции над задачами, Kafka-события.', status: 'in_progress', priority: 'high', assignee: 'Дмитрий', labels: ['мониторинг'] },
      { title: 'Метрики Prometheus', description: 'Гистограммы запросов, счётчики операций, gauge по статусам.', status: 'in_progress', priority: 'medium', assignee: 'Дмитрий', labels: ['мониторинг'] },
      { title: 'Drag & drop на доске', description: 'Перетаскивание карточек с сохранением позиции.', status: 'todo', priority: 'high', assignee: 'Мария', labels: ['frontend'] },
      { title: 'Тёмная тема интерфейса', description: 'Градиенты, стеклянные панели, микроанимации.', status: 'todo', priority: 'low', assignee: 'Мария', labels: ['frontend', 'дизайн'] },
      { title: 'Покрыть сервис тестами', description: 'Unit-тесты бизнес-логики и обработчиков HTTP.', status: 'todo', priority: 'medium', assignee: '', labels: ['тесты'] },
      { title: 'Экспорт задач в CSV', description: 'Выгрузка текущего состояния доски.', status: 'backlog', priority: 'low', assignee: '', labels: ['frontend'] },
      { title: 'Уведомления о дедлайнах', description: 'Планировщик, проверяющий просроченные задачи.', status: 'backlog', priority: 'medium', assignee: '', labels: ['backend', 'идея'] },
    ];
    const day = 86400000;
    for (const [i, t] of demo.entries()) {
      if (t.priority === 'urgent' || i % 3 === 0) {
        // Демо-данные сознательно шлют СТАРЫЙ формат (due_date): на них
        // проверяется совместимость — сервер трактует их как «весь день».
        t.due_date = new Date(Date.now() + day * ((i % 7) - 2)).toISOString();
      }
      // Демо-задачи созданы без явного действия пользователя —
      // их параметры не должны «залипать».
      await api('/tasks', { method: 'POST', body: JSON.stringify({ ...t, skip_sticky_update: true }) });
    }
  } catch {
    /* демо-данные не критичны */
  }
}

async function main() {
  bindEvents();
  // Режим узнаём до решения о демо-данных; справочник цветов — для палитры/легенды/фильтра;
  // sticky-значения подтягиваем с сервера (localStorage — только кэш).
  await Promise.all([refresh(), checkHealth(), loadColors(), loadDefaults()]);
  if (state.mode === 'server') {
    await seedDemoData();
    await refresh({ silent: true });
  }
  pollNotifications(); // первый опрос — без всплывающих уведомлений
  fetchUpdatesState();
  // Локальный запрос к своей БД, дёшево: закрывает гонку «воркер ещё чекает
  // при старте» и показывает обновление, найденное в длинной сессии.
  setInterval(fetchUpdatesState, 60000);
  setInterval(checkHealth, 15000);
  setInterval(() => pollNotifications({ announce: true }), 30000);
  setInterval(() => {
    if (state.view === 'activity') loadActivity();
  }, 5000);
}

main();
