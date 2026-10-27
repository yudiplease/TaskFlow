/* Радар дедлайнов — view-модуль (vanilla JS, без сборки).
   Полноценный вид наряду с канбаном и календарём: показывается в основной
   рабочей области через switchView('radar'), вне своего вида полностью
   скрыт (никаких виджетов в углу и мини-радаров в других видах).
   Логика — в radar-core.js (RadarCore), звуки — window.chibiSounds.
   Данные берутся из state.tasks (app.js); клик по сигналу — openModal(). */
(() => {
  'use strict';
  if (!window.RadarCore) return; // ядро не загрузилось — вид просто недоступен

  const C = window.RadarCore;

  /* ---------- Хранилище: localStorage может быть недоступен ---------- */
  const mem = {};
  const store = {
    get(k) {
      try { return localStorage.getItem(k); } catch (e) { return mem[k] != null ? mem[k] : null; }
    },
    set(k, v) {
      mem[k] = v;
      try { localStorage.setItem(k, v); } catch (e) { /* приватный режим — держим в памяти */ }
    },
  };
  const loadJSON = (k, fallback) => {
    try {
      const raw = store.get(k);
      if (!raw) return JSON.parse(JSON.stringify(fallback));
      const v = JSON.parse(raw);
      return v && typeof v === 'object' ? v : JSON.parse(JSON.stringify(fallback));
    } catch (e) { return JSON.parse(JSON.stringify(fallback)); }
  };

  /* ---------- Состояние вида ---------- */
  const LS_FILTERS = 'radar.filters';
  const LS_ANGLES = 'radar.customAngles';
  const LS_SETTINGS = 'radar.settings';
  const SOUND_COOLDOWN_MS = 10 * 60 * 1000; // 10 минут на задачу и тип звука

  let filters = Object.assign({}, C.DEFAULT_FILTERS, loadJSON(LS_FILTERS, {}));
  // Ссылка с URL важнее localStorage: ?view=radar&priorities=... — шаринг состояния.
  filters = Object.assign(filters, C.parseQueryFilters(location.search));

  let customAngles = loadJSON(LS_ANGLES, {});                  // {taskId: градусы}
  let settings = Object.assign(
    { radarSounds: true, horizonDays: 90 },
    loadJSON(LS_SETTINGS, {}),
  );
  if ([30, 90].indexOf(settings.horizonDays) < 0) settings.horizonDays = 90;

  let active = false;            // вид сейчас на экране
  let sessionSquee = false;      // squee — один раз за сессию
  const soundCooldowns = new Map(); // taskId -> {hum: ts, nya: ts}
  const prevMinutes = new Map();    // для детекции пересечения колец
  let memo = null;                  // мемоизация селектора+коллизий
  let sectorOptionsKey = '';        // для пересборки списка исполнителей
  let lastWrittenFilters = '';      // чтобы не дёргать URL на каждом тике

  const root = document.getElementById('radar-view');
  if (!root) return;

  root.innerHTML = `
    <div class="radar-toolbar">
      <button class="radar-filters-toggle" id="radar-filters-toggle" title="Свернуть фильтры" aria-label="Свернуть панель фильтров">◧</button>
      <span class="radar-toolbar-hint">Центр — сейчас. Ближе к центру — ближе дедлайн; просроченные — за центром.</span>
      <span class="spacer"></span>
      <button class="btn-ghost" id="radar-export-png" title="Скачать PNG">PNG</button>
      <button class="btn-ghost" id="radar-export-svg" title="Скачать SVG">SVG</button>
      <button class="btn-ghost" id="radar-copy-link" title="Скопировать ссылку на этот вид">🔗 Ссылка</button>
      <button class="btn-ghost" id="radar-reset-angles" title="Убрать все ручные углы">Сбросить углы</button>
      <button class="icon-btn" id="radar-settings-btn" aria-label="Настройки радара" aria-expanded="false">⚙️</button>
    </div>
    <div class="radar-layout" id="radar-layout">
      <aside class="radar-filters" id="radar-filters-panel" aria-label="Фильтры радара">
        <div class="radar-filters-body" id="radar-filters-body"></div>
      </aside>
      <div class="radar-stage" id="radar-svg-wrap">
        <svg id="radar-svg" viewBox="-100 -100 200 200" role="img" aria-label="Радар дедлайнов"></svg>
        <div class="radar-popover hidden" id="radar-popover">
          <div class="settings-title">Настройки радара</div>
          <label class="radar-check"><input type="checkbox" id="radar-sounds-toggle">
            <span>Звуки радара</span></label>
          <label class="radar-filter-label" for="radar-horizon">Горизонт (кольца масштаба)</label>
          <select id="radar-horizon" class="input">
            <option value="30">30 дней</option>
            <option value="90">90 дней</option>
          </select>
          <p class="radar-toolbar-hint">Звук учитывает общий тумблер «Звуки персонажа» в настройках.</p>
        </div>
      </div>
      <aside class="radar-list-panel" aria-label="Список сигналов">
        <div class="radar-list-head">Сигналы по срочности <span id="radar-list-count"></span></div>
        <div class="radar-list" id="radar-list" role="list"></div>
      </aside>
    </div>
    <div class="radar-mobile-tabs" id="radar-mobile-tabs">
      <button data-pane="filters">Фильтры</button>
      <button data-pane="radar" class="active">Радар</button>
      <button data-pane="list">Список</button>
    </div>
  `;

  const svg = document.getElementById('radar-svg');
  const listEl = document.getElementById('radar-list');
  const layout = document.getElementById('radar-layout');

  // Тултип — портал в body: живёт вне SVG и вне .radar-view.
  const tooltip = document.createElement('div');
  tooltip.className = 'radar-tooltip hidden';
  tooltip.setAttribute('role', 'tooltip');
  document.body.appendChild(tooltip);
  let tooltipTimer = null;

  /* ---------- Сектора/цвета (адаптация модели TaskFlow) ---------- */
  const sectorKey = (t) => (t.assignee && t.assignee.trim()) || '—';
  const colorOf = (t) => t.color || null; // нет projectColor — фолбэк в ядре (#9E9E9E)
  const currentUser = ACTOR; // «мои задачи» = исполнитель совпадает с ACTOR

  /* ---------- Статичная часть SVG: кольца, оси, центр ---------- */
  function renderStatic() {
    const rings = C.ringRadii(settings.horizonDays);
    let s = '';
    s += '<g id="radar-static">';
    for (var i = 0; i < 4; i++) {
      // Оси-кроссы: тоньше колец, чистая ориентация.
      s += `<line class="radar-axis" x1="-100" y1="0" x2="100" y2="0"/>`;
      s += `<line class="radar-axis" x1="0" y1="-100" x2="0" y2="100"/>`;
    }
    for (i = 0; i < rings.length; i++) {
      const rg = rings[i];
      const isHorizon = rg.days === settings.horizonDays;
      s += `<circle class="radar-ring${isHorizon ? ' horizon' : ''}" r="${rg.radius.toFixed(2)}"/>`;
      // Подпись по внешнему краю кольца сверху; pointer-events:none в CSS.
      s += `<text class="radar-ring-label" x="0" y="${-(rg.radius + 1.6).toFixed(2)}" text-anchor="middle">${rg.days}д</text>`;
    }
    s += '<circle class="radar-center" r="1.4"/></g>';
    s += '<g id="radar-aggregates"></g><g id="radar-signals"></g>';
    svg.innerHTML = s;
    signalEls.clear(); // старые <g> утрачены вместе со слоем — карта тоже
  }

  /* ---------- Пересчёт (мемоизация: селектор + коллизии) ---------- */
  function recompute(force) {
    if (!active) return;
    const nowMs = Date.now();
    const nowBucket = Math.floor(nowMs / 60000); // тик — раз в минуту
    const fKey = JSON.stringify(filters);
    const aKey = JSON.stringify(customAngles);
    if (!force && memo && memo.tasks === state.tasks && memo.nowBucket === nowBucket
      && memo.fKey === fKey && memo.aKey === aKey && memo.horizon === settings.horizonDays) {
      return;
    }
    const sel = C.selectRadarSignals(state.tasks, nowMs, filters, {
      horizonDays: settings.horizonDays,
      currentUser: currentUser,
      sectorKey: sectorKey,
      colorOf: colorOf,
      customAngles: customAngles,
    });
    const col = C.resolveCollisions(sel.signals);
    memo = { key: 1, tasks: state.tasks, nowBucket, fKey, aKey, horizon: settings.horizonDays, sel, col };
    renderRadar();
    renderList();
    checkSoundCrossings(sel.signals);
  }

  /* ---------- Рисование сигналов ----------
     DOM сигналов персистентный: при тике обновляется только transform
     и атрибуты кружка — дерево не пересоздаётся (требование
     производительности; в React-стеке это был бы ре-рендер по transform). */
  const signalEls = new Map();

  function renderRadar() {
    if (!memo) return;
    const layer = svg.querySelector('#radar-signals');
    const aggLayer = svg.querySelector('#radar-aggregates');
    const alive = new Set();
    const byUrgency = memo.col.placed.slice().sort(
      (a, b) => a.minutesToDeadline - b.minutesToDeadline,
    );

    byUrgency.forEach((s) => {
      alive.add(s.taskId);
      let g = signalEls.get(s.taskId);
      if (!g) {
        g = document.createElementNS('http://www.w3.org/2000/svg', 'g');
        g.setAttribute('class', 'radar-signal');
        g.setAttribute('role', 'button');
        g.setAttribute('tabindex', '0');
        // Tab обходит сигналы в порядке приближения дедлайна: порядок DOM.
        g.innerHTML = '<circle class="radar-pulse" r="0"></circle>'
          + '<circle class="radar-signal-dot" r="0"></circle>'
          + '<circle class="radar-hit" fill="transparent" r="0"></circle>';
        g.addEventListener('pointerdown', onSignalPointerDown);
        g.addEventListener('mouseenter', () => onSignalEnter(s.taskId, g));
        g.addEventListener('mouseleave', hideTooltip);
        g.addEventListener('focus', () => onSignalEnter(s.taskId, g));
        g.addEventListener('blur', hideTooltip);
        g.addEventListener('keydown', (e) => {
          if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); openTask(s.taskId); }
        });
        layer.appendChild(g);
        signalEls.set(s.taskId, g);
      }
      updateSignalEl(g, s);
    });
    // Обводка «+1» у агрегата-якоря: рисуется отдельным элементом ниже.
    signalEls.forEach((g, id) => {
      if (!alive.has(id)) { g.remove(); signalEls.delete(id); }
    });

    // Агрегаты «+N» (ключ — якорь, счётчик может совпадать у двух групп).
    let aggHtml = '';
    memo.col.aggregates.forEach((a) => {
      aggHtml += `<g class="radar-aggregate" transform="translate(${a.x.toFixed(2)},${a.y.toFixed(2)})" data-anchor="${esc(a.anchor.taskId)}" tabindex="0" role="button" aria-label="Группа из ${a.count} задач с близкими дедлайнами">`
        + `<circle r="${a.size.toFixed(2)}" fill="#4b5573" stroke="rgba(255,255,255,0.5)" stroke-width="0.5"/>`
        + `<text y="1.4" text-anchor="middle" font-size="3.6" fill="#fff">+${a.count - 1}</text></g>`;
    });
    aggLayer.innerHTML = aggHtml;
    aggLayer.querySelectorAll('.radar-aggregate').forEach((el) => {
      el.addEventListener('mouseenter', () => showAggregateTooltip(el));
      el.addEventListener('mouseleave', hideTooltip);
      el.addEventListener('focus', () => showAggregateTooltip(el));
      el.addEventListener('blur', hideTooltip);
      el.addEventListener('click', () => flashRows(aggItems(el)));
      el.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault(); flashRows(aggItems(el));
        }
      });
    });
  }

  function updateSignalEl(g, s) {
    g.setAttribute('transform', `translate(${s.x.toFixed(2)},${s.y.toFixed(2)})`);
    g.setAttribute('aria-label', ariaLabel(s));
    const dot = g.querySelector('.radar-signal-dot');
    const hit = g.querySelector('.radar-hit');
    const pulse = g.querySelector('.radar-pulse');
    const r = s.size.toFixed(2);
    if (dot.getAttribute('r') !== r) {
      dot.setAttribute('r', r);
      hit.setAttribute('r', String(parseFloat(r) + 3)); // зона клика больше кружка
      pulse.setAttribute('r', r);
    }
    if (dot.getAttribute('fill') !== s.color) {
      dot.setAttribute('fill', s.color);
      dot.setAttribute('stroke', 'rgba(255,255,255,0.55)');
      dot.setAttribute('stroke-width', '0.35');
      pulse.style.color = s.color;
    }
    const cls = ['radar-signal'];
    if (s.isOverdue) cls.push('overdue');
    if (s.hasCustomAngle) cls.push('custom');
    if (memo.col.overlappingIds[s.taskId]) cls.push('overlap');
    if (g.getAttribute('class') !== cls.join(' ')) g.setAttribute('class', cls.join(' '));
    // Пинчик у сигнала с ручным углом.
    let pin = g.querySelector('.radar-pin');
    if (s.hasCustomAngle && !pin) {
      pin = document.createElementNS('http://www.w3.org/2000/svg', 'text');
      pin.setAttribute('class', 'radar-pin');
      pin.setAttribute('y', -(s.size + 1.6));
      pin.setAttribute('text-anchor', 'middle');
      pin.setAttribute('font-size', '3.4');
      pin.textContent = '📌';
      g.appendChild(pin);
    } else if (!s.hasCustomAngle && pin) {
      pin.remove();
    }
  }

  function ariaLabel(s) {
    const due = dueLabelOf(s.taskId) || '';
    const when = s.isOverdue
      ? 'просрочено на ' + humanizeSpan(-s.minutesToDeadline)
      : 'осталось ' + humanizeSpan(s.minutesToDeadline);
    return `${s.title}. ${s.sectorKey === '—' ? 'без исполнителя' : 'исполнитель ' + s.sectorKey}. `
      + `Срок ${due}. ${when}. Приоритет: ${(PRIORITY_LABELS[s.priority] || s.priority)}.`;
  }

  /* ---------- Список справа ---------- */
  function renderList() {
    if (!memo) return;
    const sel = memo.sel.signals.filter((s) => s.taskId !== '__overflow__');
    let html = '';
    sel.forEach((s) => {
      const due = dueLabelOf(s.taskId) || '—';
      const cls = s.isOverdue ? 'overdue' : (!s.isOverdue && s.minutesToDeadline < 1440 ? 'soon' : '');
      html += `<button type="button" class="radar-row" role="listitem" data-id="${esc(s.taskId)}">`
        + `<span class="radar-row-dot" style="background:${esc(s.color)}"></span>`
        + `<span class="radar-row-main"><span class="radar-row-title${s.status === 'done' ? ' done' : ''}">${esc(s.title)}</span>`
        + `<span class="radar-row-meta ${cls}">${esc(s.sectorKey === '—' ? 'без исполнителя' : s.sectorKey)} · ${esc(due)}`
        + `${s.hasCustomAngle ? ' · 📌' : ''}</span></span>`
        + `<span class="radar-row-prio">${esc(shortPriority(s.priority))}</span></button>`;
    });
    if (memo.sel.overflowCount > 0) {
      html += `<div class="radar-empty">…и ещё ${memo.sel.overflowCount} за пределами топ-${C.MAX_SIGNALS}</div>`;
    }
    if (!sel.length) html = '<div class="radar-empty">Сигналов нет: у задач в рамках фильтров нет сроков.</div>';
    listEl.innerHTML = html;
    document.getElementById('radar-list-count').textContent = `(${sel.length})`;
    listEl.querySelectorAll('.radar-row').forEach((row) => {
      row.addEventListener('mouseenter', () => hoverSignal(row.dataset.id, true));
      row.addEventListener('mouseleave', () => hoverSignal(row.dataset.id, false));
      row.addEventListener('click', () => openTask(row.dataset.id));
    });
  }

  const shortPriority = (p) => ({ low: 'низкй', medium: 'среднй', high: 'высокй', urgent: 'срочно' }[p] || p);

  function dueLabelOf(taskId) {
    if (taskId === '__overflow__') return '';
    const t = state.tasks.find((x) => x.id === taskId);
    if (!t) return '';
    const f = formatDue(t);
    return f ? f.label.replace(/^📅\s*|^⏰\s*/, '') : '';
  }

  /* Человеческий спан: «2 д 5 ч», «3 ч 20 мин», «45 мин». */
  function humanizeSpan(minutes) {
    const m = Math.max(0, Math.round(minutes));
    if (m >= 1440) {
      let d = Math.floor(m / 1440), h = Math.round((m % 1440) / 60);
      if (h >= 24) { d += 1; h = 0; } // 5 д 24 ч → 6 д
      return h ? `${d} д ${h} ч` : `${d} д`;
    }
    if (m >= 60) {
      const h = Math.floor(m / 60), mm = m % 60;
      return mm ? `${h} ч ${mm} мин` : `${h} ч`;
    }
    return `${m} мин`;
  }

  /* ---------- Двусторонняя подсветка ---------- */
  function hoverSignal(taskId, on) {
    const g = signalEls.get(taskId);
    if (g) g.classList.toggle('hovered', on);
    const row = listEl.querySelector(`.radar-row[data-id="${CSS.escape(taskId)}"]`);
    if (row) {
      row.classList.toggle('highlighted', on);
      if (on) row.scrollIntoView({ block: 'nearest' });
    }
  }

  function flashRows(ids) {
    ids.forEach((id) => {
      const row = listEl.querySelector(`.radar-row[data-id="${CSS.escape(id)}"]`);
      if (row) { row.classList.remove('flash'); void row.offsetWidth; row.classList.add('flash'); row.scrollIntoView({ block: 'nearest' }); }
    });
  }

  function aggItems(el) {
    const anchor = el.getAttribute('data-anchor');
    const a = memo && memo.col.aggregates.find((x) => x.anchor.taskId === anchor);
    return a ? a.items.map((s) => s.taskId) : [];
  }

  /* ---------- Тултип ---------- */
  function onSignalEnter(taskId, g) {
    clearTimeout(tooltipTimer);
    tooltipTimer = setTimeout(() => showTaskTooltip(taskId, g), 200); // задержка из ТЗ
    hoverSignal(taskId, true);
  }

  function showTaskTooltip(taskId, g) {
    const s = memo && memo.sel.signals.find((x) => x.taskId === taskId);
    if (!s) return;
    const t = state.tasks.find((x) => x.id === taskId);
    const due = dueLabelOf(taskId);
    const when = s.isOverdue
      ? `Просрочено на ${humanizeSpan(-s.minutesToDeadline)}`
      : `Осталось ${humanizeSpan(s.minutesToDeadline)}`;
    let html = `<div class="radar-tooltip-title">${esc(s.title)}</div>`
      + `<div class="radar-tooltip-row"><span class="radar-dot" style="background:${esc(s.color)}"></span>`
      + `${esc(s.sectorKey === '—' ? 'без исполнителя' : s.sectorKey)}</div>`
      + `<div class="radar-tooltip-row">${esc(due)}${s.dueAllDay ? ' (весь день)' : ''}</div>`
      + `<div class="radar-tooltip-row">${esc(when)}</div>`
      + `<span class="radar-badge">${esc(PRIORITY_LABELS[s.priority] || s.priority)}</span>`;
    if (s.hasCustomAngle) {
      html += '<br><button type="button" class="radar-tooltip-reset" data-reset="' + esc(taskId) + '">Сбросить мой угол</button>';
    }
    showTooltipAt(html, g.getBoundingClientRect());
    tooltip.querySelectorAll('[data-reset]').forEach((btn) => {
      btn.addEventListener('click', () => {
        delete customAngles[btn.dataset.reset];
        saveAngles();
        hideTooltip();
        recompute(true);
      });
    });
  }

  function showAggregateTooltip(el) {
    const anchor = el.getAttribute('data-anchor');
    const a = memo && memo.col.aggregates.find((x) => x.anchor.taskId === anchor);
    if (!a) return;
    const items = a.items.slice(0, 10).map((s) =>
      `<div class="radar-tooltip-list-item"><span class="radar-dot" style="background:${esc(s.color)}"></span>`
      + `<span>${esc(s.title)}</span></div>`).join('');
    const more = a.items.length > 10 ? `<div class="radar-toolbar-hint">…и ещё ${a.items.length - 10}</div>` : '';
    showTooltipAt(
      `<div class="radar-tooltip-title">${a.count} задач рядом</div>`
      + `<div class="radar-tooltip-list">${items}</div>${more}`,
      el.getBoundingClientRect(),
    );
  }

  function showTooltipAt(html, rect) {
    tooltip.innerHTML = html;
    tooltip.classList.remove('hidden');
    const tw = tooltip.offsetWidth, th = tooltip.offsetHeight;
    let x = rect.right + 8, y = rect.top + rect.height / 2 - th / 2;
    if (x + tw > window.innerWidth - 8) x = rect.left - tw - 8;
    y = Math.max(8, Math.min(y, window.innerHeight - th - 8));
    tooltip.style.left = x + 'px';
    tooltip.style.top = y + 'px';
  }

  function hideTooltip() {
    clearTimeout(tooltipTimer);
    tooltip.classList.add('hidden');
    listEl.querySelectorAll('.radar-row.highlighted').forEach((r) => r.classList.remove('highlighted'));
    signalEls.forEach((g) => g.classList.remove('hovered'));
  }

  /* ---------- Drag сигнала: угол вручную, радиус от времени ---------- */
  const DRAG_THRESHOLD = 5;   // > 5 px — drag, меньше — клик
  const LONGPRESS_MS = 500;   // тач: long-press начинает drag

  function onSignalPointerDown(e) {
    if (e.button !== 0 && e.pointerType === 'mouse') return;
    const g = e.currentTarget;
    const taskId = findSignalId(g);
    const s = memo && memo.sel.signals.find((x) => x.taskId === taskId);
    if (!s || s.taskId === '__overflow__') return;

    const startX = e.clientX, startY = e.clientY;
    let dragging = false;
    let longPress = null;
    if (e.pointerType === 'touch') {
      longPress = setTimeout(() => { dragging = true; hideTooltip(); }, LONGPRESS_MS);
    }
    try { g.setPointerCapture(e.pointerId); } catch (err) { /* ignore */ }

    const onMove = (ev) => {
      const moved = Math.hypot(ev.clientX - startX, ev.clientY - startY);
      if (!dragging && (e.pointerType !== 'touch' && moved > DRAG_THRESHOLD)) dragging = true;
      if (!dragging) return;
      // Радиус фиксирован — сигнал следует по дуге.
      const rect = svg.getBoundingClientRect();
      const cx = rect.left + rect.width / 2, cy = rect.top + rect.height / 2;
      const theta = Math.atan2(-(ev.clientY - cy), ev.clientX - cx);
      const xy = C.polarToCartesian(s.radius, theta);
      g.setAttribute('transform', `translate(${xy.x.toFixed(2)},${xy.y.toFixed(2)})`);
      g._dragTheta = theta;
    };
    const onUp = (ev) => {
      clearTimeout(longPress);
      g.removeEventListener('pointermove', onMove);
      g.removeEventListener('pointerup', onUp);
      g.removeEventListener('pointercancel', onUp);
      const moved = Math.hypot(ev.clientX - startX, ev.clientY - startY);
      const theta = g._dragTheta;
      delete g._dragTheta;
      if (dragging && theta !== undefined && moved > DRAG_THRESHOLD) {
        customAngles[taskId] = C.normDeg(theta * 180 / Math.PI);
        saveAngles();
        recompute(true);
      } else {
        openTask(taskId); // drag < 5px — обычный клик
      }
    };
    g.addEventListener('pointermove', onMove);
    g.addEventListener('pointerup', onUp);
    g.addEventListener('pointercancel', onUp);
  }

  function findSignalId(g) {
    for (const [id, el] of signalEls) if (el === g) return id;
    return null;
  }

  /* ---------- Клик → задача ---------- */
  function openTask(taskId) {
    if (taskId === '__overflow__') return;
    if (window.chibiSounds) window.chibiSounds.playEvent('click'); // «хи-хи»
    if (typeof openModal === 'function') openModal(taskId);        // существующий «роутинг»
  }

  /* ---------- Звуки пересечений (чиби) ---------- */
  function playRadarSound(category, taskId) {
    const chibi = window.chibiSounds;
    if (!chibi || !settings.radarSounds) return;
    const cs = chibi.getSettings();
    if (!cs.enabled) return;
    if (cs.clickOnly && category !== 'giggle') return; // «Только по клику» глушит фон
    const now = Date.now();
    const rec = soundCooldowns.get(taskId) || {};
    if (rec[category] && now - rec[category] < SOUND_COOLDOWN_MS) return;
    rec[category] = now;
    soundCooldowns.set(taskId, rec);
    chibi.play(category);
  }

  /* Пересечение кольца «< 24 ч» → hum; пересечение центра (просрочка) → nya. */
  function checkSoundCrossings(signals) {
    if (!active || document.visibilityState !== 'visible') return;
    for (const s of signals) {
      if (s.taskId === '__overflow__') continue;
      const prev = prevMinutes.get(s.taskId);
      const cur = s.minutesToDeadline;
      if (prev !== undefined && prev !== cur) {
        if (prev > 1440 && cur <= 1440 && cur > 0) playRadarSound('hum', s.taskId);
        else if (prev > 0 && cur <= 0) playRadarSound('nya', s.taskId);
      }
      prevMinutes.set(s.taskId, cur);
    }
  }

  /* ---------- Фильтры: панель ---------- */
  function renderFilters() {
    const body = document.getElementById('radar-filters-body');
    const assignees = {};
    state.tasks.forEach((t) => {
      const k = sectorKey(t);
      assignees[k] = (assignees[k] || 0) + 1;
    });
    const keys = Object.keys(assignees).sort((a, b) => (a === '—' ? 1 : a.localeCompare(b === '—' ? '' : b)));

    let html = '<div class="radar-filter-group"><div class="radar-filters-title">Фильтры радара</div>';
    html += '<div class="radar-filter-label">Исполнители</div>';
    keys.forEach((k) => {
      const checked = filters.sectors.includes(k) ? ' checked' : '';
      const hue = k === '—' ? 0 : hueFromString(k);
      const dot = k === '—' ? '#5c6478' : `hsl(${hue}, 55%, 60%)`;
      html += `<label class="radar-check"><input type="checkbox" data-sector="${esc(k)}"${checked}>`
        + `<span class="radar-dot" style="background:${dot}"></span><span>${esc(k === '—' ? '(без исполнителя)' : k)}</span>`
        + `<span class="count">${assignees[k]}</span></label>`;
    });
    html += '</div><div class="radar-filter-group"><div class="radar-filter-label">Приоритеты</div>';
    C.PRIORITIES.forEach((p) => {
      const checked = filters.priorities.includes(p) ? ' checked' : '';
      html += `<label class="radar-check"><input type="checkbox" data-priority="${p}"${checked}>`
        + `<span>${esc(PRIORITY_LABELS[p] || p)}</span></label>`;
    });
    html += '</div><div class="radar-filter-group">'
      + `<label class="radar-check"><input type="checkbox" id="rf-include-done"${filters.includeDone ? ' checked' : ''}><span>Показывать выполненные</span></label>`
      + `<label class="radar-check"><input type="checkbox" id="rf-only-overdue"${filters.onlyOverdue ? ' checked' : ''}><span>Только просроченные</span></label>`
      + `<label class="radar-check"><input type="checkbox" id="rf-only-mine"${filters.onlyMine ? ' checked' : ''}><span>Только мои задачи</span></label>`
      + '</div><div class="radar-filter-group radar-slider-row">'
      + '<label class="radar-filter-label" for="rf-max-days">Ближе чем <span id="rf-max-days-val"></span></label>'
      + `<input type="range" id="rf-max-days" min="0" max="90" step="5" value="${filters.maxDays}">`
      + '</div>'
      + '<button type="button" class="btn-ghost" id="rf-reset">Сбросить фильтры</button>';

    body.innerHTML = html;

    body.querySelectorAll('[data-sector]').forEach((cb) => cb.addEventListener('change', () => {
      const k = cb.dataset.sector;
      filters.sectors = cb.checked
        ? filters.sectors.concat([k])
        : filters.sectors.filter((x) => x !== k);
      onFiltersChanged();
    }));
    body.querySelectorAll('[data-priority]').forEach((cb) => cb.addEventListener('change', () => {
      const p = cb.dataset.priority;
      filters.priorities = cb.checked
        ? filters.priorities.concat([p])
        : filters.priorities.filter((x) => x !== p);
      onFiltersChanged();
    }));
    body.querySelector('#rf-include-done').addEventListener('change', (e) => { filters.includeDone = e.target.checked; onFiltersChanged(); });
    body.querySelector('#rf-only-overdue').addEventListener('change', (e) => { filters.onlyOverdue = e.target.checked; onFiltersChanged(); });
    body.querySelector('#rf-only-mine').addEventListener('change', (e) => { filters.onlyMine = e.target.checked; onFiltersChanged(); });
    const slider = body.querySelector('#rf-max-days');
    const sliderVal = () => {
      const v = parseInt(slider.value, 10);
      document.getElementById('rf-max-days-val').textContent = v >= 90 ? 'любой давности' : `${v} дн`;
    };
    slider.addEventListener('input', sliderVal);
    slider.addEventListener('change', () => {
      filters.maxDays = parseInt(slider.value, 10);
      sliderVal();
      onFiltersChanged();
    });
    sliderVal();
    body.querySelector('#rf-reset').addEventListener('click', () => {
      filters = JSON.parse(JSON.stringify(C.DEFAULT_FILTERS));
      onFiltersChanged();
      renderFilters();
    });
    sectorOptionsKey = keys.join('|');
  }

  function onFiltersChanged() {
    store.set(LS_FILTERS, JSON.stringify(filters));
    memo = null;
    recompute(true);
    writeURL();
  }

  /* ---------- URL: фильтры радара (+ view ставит switchView) ---------- */
  function writeURL() {
    const q = C.filtersToQuery(filters);
    const serialized = 'view=radar' + (q ? '&' + q : '');
    if (serialized === lastWrittenFilters) return;
    lastWrittenFilters = serialized;
    const u = new URL(location.href);
    u.search = serialized;
    history.replaceState(null, '', u.pathname + (serialized ? '?' + serialized : ''));
  }

  /* ---------- Настройки радара (поповер) ---------- */
  function initSettingsPopover() {
    const btn = document.getElementById('radar-settings-btn');
    const pop = document.getElementById('radar-popover');
    const sounds = document.getElementById('radar-sounds-toggle');
    const horizon = document.getElementById('radar-horizon');
    sounds.checked = settings.radarSounds;
    horizon.value = String(settings.horizonDays);
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const open = pop.classList.toggle('hidden');
      btn.setAttribute('aria-expanded', String(!open));
    });
    document.addEventListener('click', (e) => {
      if (!pop.classList.contains('hidden') && !pop.contains(e.target)) {
        pop.classList.add('hidden');
        btn.setAttribute('aria-expanded', 'false');
      }
    });
    sounds.addEventListener('change', () => {
      settings.radarSounds = sounds.checked;
      saveSettings();
    });
    horizon.addEventListener('change', () => {
      settings.horizonDays = parseInt(horizon.value, 10);
      saveSettings();
      renderStatic();
      memo = null;
      recompute(true);
    });
  }

  const saveAngles = () => store.set(LS_ANGLES, JSON.stringify(customAngles));
  const saveSettings = () => store.set(LS_SETTINGS, JSON.stringify(settings));

  /* ---------- Экспорт ---------- */
  function exportSvgMarkup() {
    const clone = svg.cloneNode(true);
    clone.querySelectorAll('[tabindex]').forEach((el) => el.removeAttribute('tabindex'));
    clone.querySelectorAll('[role]').forEach((el) => el.removeAttribute('role'));
    clone.querySelectorAll('[data-count]').forEach((el) => el.removeAttribute('data-count'));
    return new XMLSerializer().serializeToString(clone);
  }

  function downloadBlob(name, blob) {
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = name;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 5000);
  }

  function exportRadar(requested) {
    try {
      const markup = C.buildExportSvgMarkup(exportSvgMarkup(), {
        width: 600, height: 600, bg: '#0b0f1e',
      });
      const base = 'taskflow-radar-' + new Date().toISOString().slice(0, 10);
      // PNG при отсутствии canvas.toBlob → фолбэк на SVG (краевой случай ТЗ).
      const fmt = C.chooseExportFormat(requested, typeof HTMLCanvasElement !== 'undefined' && !!HTMLCanvasElement.prototype.toBlob);
      if (fmt === 'svg') {
        downloadBlob(base + '.svg', new Blob([markup], { type: 'image/svg+xml;charset=utf-8' }));
        return;
      }
      const url = URL.createObjectURL(new Blob([markup], { type: 'image/svg+xml;charset=utf-8' }));
      const img = new Image();
      img.onload = () => {
        try {
          const canvas = document.createElement('canvas');
          canvas.width = 1200; canvas.height = 1200;
          canvas.getContext('2d').drawImage(img, 0, 0, 1200, 1200);
          canvas.toBlob((blob) => {
            if (blob) downloadBlob(base + '.png', blob);
            else downloadBlob(base + '.svg', new Blob([markup], { type: 'image/svg+xml;charset=utf-8' }));
          }, 'image/png');
        } finally { URL.revokeObjectURL(url); }
      };
      img.onerror = () => {
        URL.revokeObjectURL(url);
        downloadBlob(base + '.svg', new Blob([markup], { type: 'image/svg+xml;charset=utf-8' }));
      };
      img.src = url;
    } catch (e) {
      console.error('radar export', e); // логируем только ошибки
    }
  }

  function copyLink() {
    const url = location.href;
    const done = () => toast('Ссылка на радар скопирована');
    const fail = () => toast('Не удалось скопировать ссылку', 'error');
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(url).then(done, () => fallbackCopy(url) ? done() : fail());
    } else {
      fallbackCopy(url) ? done() : fail();
    }
  }
  function fallbackCopy(text) {
    try {
      const ta = document.createElement('textarea');
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      const ok = document.execCommand('copy');
      ta.remove();
      return ok;
    } catch (e) { return false; }
  }

  /* ---------- Переключение панелей на мобильных ---------- */
  document.getElementById('radar-mobile-tabs').addEventListener('click', (e) => {
    const btn = e.target.closest('button[data-pane]');
    if (!btn) return;
    document.querySelectorAll('#radar-mobile-tabs button').forEach((b) => b.classList.toggle('active', b === btn));
    layout.classList.toggle('show-filters', btn.dataset.pane === 'filters');
    layout.classList.toggle('show-list', btn.dataset.pane === 'list');
  });

  /* ---------- Тулбар ---------- */
  document.getElementById('radar-filters-toggle').addEventListener('click', () => {
    layout.classList.toggle('filters-collapsed');
  });
  document.getElementById('radar-export-png').addEventListener('click', () => exportRadar('png'));
  document.getElementById('radar-export-svg').addEventListener('click', () => exportRadar('svg'));
  document.getElementById('radar-copy-link').addEventListener('click', copyLink);
  document.getElementById('radar-reset-angles').addEventListener('click', () => {
    customAngles = {};
    saveAngles();
    memo = null;
    recompute(true);
  });
  initSettingsPopover();

  /* ---------- Синхронизация между вкладками (storage event) ---------- */
  window.addEventListener('storage', (e) => {
    if (e.key === LS_ANGLES) {
      customAngles = loadJSON(LS_ANGLES, {});
      memo = null;
      recompute(true);
    } else if (e.key === LS_FILTERS) {
      filters = Object.assign({}, C.DEFAULT_FILTERS, loadJSON(LS_FILTERS, {}));
      renderFilters();
      memo = null;
      recompute(true);
    } else if (e.key === 'ui.lastView' && e.newValue && typeof switchView === 'function') {
      switchView(e.newValue); // второй таб тоже переключает вид
    }
  });

  /* ---------- Жизненный цикл вида ---------- */
  function show() {
    active = true;
    root.classList.remove('hidden');
    renderStatic();
    // Пересборка списка исполнителей, только если состав изменился.
    const keys = Array.from(new Set(state.tasks.map(sectorKey))).sort().join('|');
    if (keys !== sectorOptionsKey) renderFilters();
    recompute(true);
    // Переключение в режим «Радар» → squee один раз за сессию (если звуки вообще включены).
    if (!sessionSquee && window.chibiSounds && settings.radarSounds) {
      const cs = window.chibiSounds.getSettings();
      if (cs.enabled && !cs.clickOnly) {
        sessionSquee = true;
        window.chibiSounds.play('squee');
      }
    }
  }

  function hide() {
    active = false;
    root.classList.add('hidden');
    hideTooltip();
  }

  function onTasksChanged() {
    if (!active) return;
    const keys = Array.from(new Set(state.tasks.map(sectorKey))).sort().join('|');
    if (keys !== sectorOptionsKey) renderFilters();
    memo = null;
    recompute(true);
  }

  // Тик раз в минуту: в скрытой вкладке пересчёт пропускается, при возврате
  // visibilitychange делает один пересчёт (в т.ч. после >30 мин в фоне).
  setInterval(() => {
    if (active && document.visibilityState === 'visible') recompute();
  }, 60000);
  document.addEventListener('visibilitychange', () => {
    if (active && document.visibilityState === 'visible') recompute(true);
  });

  /* ---------- Внешний интерфейс (app.js) ---------- */
  window.radarView = { show, hide, onTasksChanged, refresh: () => recompute(true) };
})();
