/* RadarCore — чистая логика «Радара дедлайнов» (без DOM).
   Подключается и в браузере (window.RadarCore), и в node-тестах
   (module.exports) — UMD-обёртка, внешних зависимостей нет.

   Адаптация модели TaskFlow: в проекте нет проектов/ProjectId — роль
   сектора играет исполнитель (assignee), цвет — task.color ?? #9E9E9E.
   Приоритеты — существующее перечисление low/medium/high/urgent. */
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.RadarCore = factory();
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  /* ---------- Константы (все — из ТЗ) ---------- */

  // Спред дрожания угла внутри сектора: ±15° вокруг угла сектора.
  var SPREAD_DEG = 30;

  // Минимальная дистанция между сигналами в единицах viewBox (диаметр 200).
  var MIN_DIST = 4;

  // Просрочка старше стольких дней не показывается (прошла центр насквозь
  // и «ушла за край радара»).
  var OVERDUE_CUTOFF_DAYS = 30;

  // Максимум сигналов на радаре; остальное — топ по близости, хвост агрегируется.
  var MAX_SIGNALS = 300;

  // Число итераций разведения коллизий.
  var COLLISION_ITERATIONS = 3;

  // Базовый размер кружка по приоритету (viewBox-единицы, радиус).
  var PRIORITY_SIZE = { low: 1.5, medium: 2.5, high: 4, urgent: 6 };

  var PRIORITIES = ['low', 'medium', 'high', 'urgent'];

  // Статусы, которые радар не показывает никогда («архив» в TaskFlow нет,
  // но если появится — отсечём здесь же).
  var HIDDEN_STATUSES_ALWAYS = ['archived'];
  var HIDDEN_STATUSES_DEFAULT = ['done'].concat(HIDDEN_STATUSES_ALWAYS);

  var DEFAULT_FILTERS = {
    sectors: [],      // исполнители (роль «проектов» в TaskFlow)
    priorities: [],   // пусто = все
    includeDone: false,
    maxDays: 90,      // слайдер «Ближе чем N дней», 0..90
    onlyOverdue: false,
    onlyMine: false,
  };

  /* ---------- Хеш (детерминированный угол) ---------- */

  // FNV-1a 32 бита: детерминирован между рендерами и между сессиями,
  // в отличие от Math.random.
  function hashString(s) {
    var h = 0x811c9dc5;
    s = String(s);
    for (var i = 0; i < s.length; i++) {
      h ^= s.charCodeAt(i);
      h = (h * 0x01000193) >>> 0; // uint32
    }
    return h >>> 0;
  }

  /* ---------- Радиус: логарифмическая шкала времени ----------
     r(days) = 100 * ln(days+1) / ln(horizon+1), clamp [0..100].
     Логарифм — иначе при горизонте 90 дней внутренние кольца (3д, 7д)
     слипаются у центра: линейно 3 дня = 3% радиуса. Основание логарифма
     не влияет на результат (это отношение), ln взят для определённости.
     Просрочка меряется тем же радиусом от |minutes| — «прошли центр». */
  function radiusForTime(minutesToDeadline, horizonDays) {
    if (!(horizonDays > 0)) horizonDays = 90;
    var days = Math.abs(minutesToDeadline) / 1440;
    var raw = 100 * Math.log(days + 1) / Math.log(horizonDays + 1);
    return Math.min(100, Math.max(0, raw));
  }

  /* Кольца масштаба: 3д/7д/30д + сам горизонт (если не дублируется).
     horizonDays = 7 → [3, 7]; 30 → [3, 7, 30]; 90 → [3, 7, 30, 90]. */
  function ringRadii(horizonDays) {
    if (!(horizonDays > 0)) horizonDays = 90;
    var base = [3, 7, 30, horizonDays];
    var seen = {};
    var out = [];
    for (var i = 0; i < base.length; i++) {
      var d = base[i];
      if (d <= 0 || d > horizonDays || seen[d]) continue;
      seen[d] = true;
      out.push({ days: d, radius: radiusForTime(d * 1440, horizonDays) });
    }
    return out;
  }

  /* ---------- Угол ----------
     θ = угол сектора (hash(ключ сектора) % 360) + джиттер задачи
     (hash(id) % 1000 / 1000 * spread − spread/2). customAngle (в градусах,
     из localStorage) переопределяет всё. Просроченные — θ + π (прошли
     центр насквозь), если нет customAngle. */
  function angleForTask(opts) {
    var sectorKey = opts.sectorKey || '—';
    var sectorAngle = (hashString(sectorKey) % 360) * Math.PI / 180;
    var jitterDeg = ((hashString(opts.taskId) % 1000) / 1000) * SPREAD_DEG - SPREAD_DEG / 2;
    var theta = sectorAngle + jitterDeg * Math.PI / 180;
    if (typeof opts.customAngleDeg === 'number' && isFinite(opts.customAngleDeg)) {
      theta = opts.customAngleDeg * Math.PI / 180;
    } else if (opts.isOverdue) {
      theta += Math.PI;
    }
    return theta;
  }

  /* Декартовы координаты. SVG: y растёт вниз, поэтому y = −r·sin(θ) —
     тогда θ=90° («вверх по математике») рисуется наверх. r — уже в
     единицах viewBox (0..100). −0 нормализуем в 0: строгое сравнение
     в тестах и ключи кэша не должны различать знак нуля. */
  function polarToCartesian(r, theta) {
    var x = r * Math.cos(theta);
    var y = -r * Math.sin(theta);
    return { x: x === 0 ? 0 : x, y: y === 0 ? 0 : y };
  }

  /* Размер сигнала: база по приоритету, небольшой рост к центру
     (× (1 + 0.3·(1 − r/100))) — горящее видно лучше. */
  function signalSize(priority, rPercent) {
    var base = PRIORITY_SIZE[priority] || PRIORITY_SIZE.medium;
    return base * (1 + 0.3 * (1 - rPercent / 100));
  }

  /* ---------- Сигнал из задачи ----------
     ctx: { nowMs, horizonDays, customAngles: {id: градусы},
            sectorKey(task) -> строка, colorOf(task) -> '#hex' }.
     Все вычисления времени — в UTC (Date.getTime, DST-сдвиги не влияют). */
  function buildSignal(task, ctx) {
    var dueMs = new Date(task.due_at).getTime();
    if (!isFinite(dueMs)) return null;
    var minutes = (dueMs - ctx.nowMs) / 60000;
    var isOverdue = minutes < 0;
    var custom = ctx.customAngles && typeof ctx.customAngles[task.id] === 'number'
      ? ctx.customAngles[task.id] : null;
    var r = radiusForTime(minutes, ctx.horizonDays);
    var theta = angleForTask({
      sectorKey: ctx.sectorKey(task),
      taskId: task.id,
      isOverdue: isOverdue,
      customAngleDeg: custom,
    });
    var xy = polarToCartesian(r, theta);
    var deg = normDeg(theta * 180 / Math.PI);
    return {
      taskId: task.id,
      title: task.title || '(без названия)',
      sectorKey: ctx.sectorKey(task),
      color: ctx.colorOf(task) || '#9E9E9E',
      priority: PRIORITIES.indexOf(task.priority) >= 0 ? task.priority : 'medium',
      dueAt: task.due_at,
      dueAllDay: !!task.due_all_day,
      minutesToDeadline: minutes,
      isOverdue: isOverdue,
      status: task.status,
      hasCustomAngle: custom !== null,
      radius: r,          // 0..100 — расстояние от центра
      angleDeg: deg,      // итоговый угол (с учётом overdue/custom)
      size: signalSize(task.priority, r),
      x: xy.x,
      y: xy.y,
    };
  }

  function normDeg(d) {
    d = d % 360;
    if (d < 0) d += 360;
    return d;
  }

  /* ---------- Селектор сигналов ----------
     tasks: state.tasks; filters: копия DEFAULT_FILTERS;
     opts: { horizonDays, maxSignals, currentUser, sectorKey, colorOf, customAngles }.
     Возвращает { signals, overflowCount } — signals уже отсортированы по
     срочности (просрочка-старейшая сверху, затем «скоро», затем дальние). */
  function selectRadarSignals(tasks, nowMs, filters, opts) {
    opts = opts || {};
    var horizonDays = opts.horizonDays > 0 ? opts.horizonDays : 90;
    var horizonMinutes = horizonDays * 1440;
    var cutoffMinutes = OVERDUE_CUTOFF_DAYS * 1440;
    var maxSignals = opts.maxSignals > 0 ? opts.maxSignals : MAX_SIGNALS;
    var hidden = filters.includeDone ? HIDDEN_STATUSES_ALWAYS : HIDDEN_STATUSES_DEFAULT;
    var f = filters || DEFAULT_FILTERS;

    var out = [];
    for (var i = 0; i < tasks.length; i++) {
      var t = tasks[i];
      if (!t.due_at) continue;                                  // без срока — не сигнал
      if (hidden.indexOf(t.status) >= 0) continue;              // done/archived
      var sig = buildSignal(t, {
        nowMs: nowMs,
        horizonDays: horizonDays,
        customAngles: opts.customAngles,
        sectorKey: opts.sectorKey,
        colorOf: opts.colorOf,
      });
      if (!sig) continue;                                       // битая дата
      if (sig.minutesToDeadline > horizonMinutes) continue;     // будущее за горизонтом
      if (sig.minutesToDeadline < -cutoffMinutes) continue;     // просрочка за срезом
      if (f.onlyOverdue && !sig.isOverdue) continue;
      if (f.priorities && f.priorities.length && f.priorities.indexOf(sig.priority) < 0) continue;
      if (f.sectors && f.sectors.length && f.sectors.indexOf(sig.sectorKey) < 0) continue;
      if (f.onlyMine && opts.currentUser != null && sig.sectorKey !== opts.currentUser) continue;
      // Слайдер «Ближе чем N дней»: режет только будущее (просрочка и так
      // ближе всех к центру); 0 = «только просрочка и текущий день».
      if (!sig.isOverdue && sig.minutesToDeadline > (f.maxDays != null ? f.maxDays : 90) * 1440) continue;
      out.push(sig);
    }
    out.sort(function (a, b) { return a.minutesToDeadline - b.minutesToDeadline; });
    if (out.length > maxSignals) {
      // Топ по близости к дедлайну остаётся сигналами, хвост — агрегат.
      var kept = out.slice(0, maxSignals);
      var overflow = out.length - maxSignals;
      kept.push(overflowSignal(out.slice(maxSignals), horizonDays));
      return { signals: kept, overflowCount: overflow };
    }
    return { signals: out, overflowCount: 0 };
  }

  // Синтетический агрегат «+N ещё»: на краю радара (r = горизонт),
  // угол — хеш от списка id, детерминирован.
  function overflowSignal(tasks, horizonDays) {
    var ids = tasks.map(function (t) { return t.taskId; }).sort().join(',');
    var theta = (hashString('overflow:' + ids) % 360) * Math.PI / 180;
    var r = 100;
    var xy = polarToCartesian(r, theta);
    return {
      taskId: '__overflow__',
      title: 'Ещё ' + tasks.length + ' за горизонтом выборки',
      sectorKey: '—',
      color: '#9E9E9E',
      priority: 'low',
      dueAt: '',
      dueAllDay: false,
      minutesToDeadline: horizonDays * 1440,
      isOverdue: false,
      status: '',
      hasCustomAngle: false,
      isOverflow: true,
      radius: r,
      angleDeg: normDeg(theta * 180 / Math.PI),
      size: 5,
      x: xy.x,
      y: xy.y,
      items: tasks,
    };
  }

  /* ---------- Коллизии ----------
     Сигналы с customAngle — неподвижные препятствия: не сдвигаются и
     не агрегируются (customAngle не участвует в collision).
     Шаг 1. По ИСХОДНЫМ позициям ищем кластеры свободных сигналов
     ( union-find, хорда < MIN_DIST ). Кластер из ≥4 — сворачиваем в
     агрегат «+N» (якорь — самый срочный): толпу не разводим.
     Шаг 2. Оставшиеся (кластеры 1–3) разводим за 3 итерации точным
     решением хорды: радиус неприкосновенен («обмануть время нельзя»),
     двигается только угол; позиция-кандидат ставит сигнал ровно на
     TARGET от пары; из двух кандидатов берём тот, что дальше от всех
     остальных (иначе Gauss-Seidel заталкивает сигнал в предыдущего).
     Радиус при разведении не меняется, «за viewBox» выйти нельзя.
     Возвращает { placed, aggregates, overlappingIds }. */
  function resolveCollisions(signals) {
    var placed = signals.map(function (s) { return Object.assign({}, s); });
    var free = [];
    var fixed = [];
    for (var i = 0; i < placed.length; i++) {
      (placed[i].hasCustomAngle ? fixed : free).push(placed[i]);
    }

    var theta = {}; // рабочие углы в радианах
    placed.forEach(function (s) { theta[s.taskId] = s.angleDeg * Math.PI / 180; });

    // Точная угловая разница двух радиус-векторов при хорде m:
    // chord² = r₁² + r₂² − 2·r₁r₂·cos(Δθ)  →  Δθ = acos(...).
    function chordAngle(ri, rj, m) {
      var c = (ri * ri + rj * rj - m * m) / (2 * ri * rj);
      if (c > 1) c = 1; else if (c < -1) c = -1;
      return Math.acos(c);
    }
    function angDiff(x) { // нормализация в (−π, π]
      x = x % (2 * Math.PI);
      if (x > Math.PI) x -= 2 * Math.PI;
      if (x <= -Math.PI) x += 2 * Math.PI;
      return x;
    }
    // Хорда между двумя сигналами по их текущим x/y (исходные позиции).
    function dist(s1, s2) {
      return Math.hypot(s1.x - s2.x, s1.y - s2.y);
    }
    function distAt(s, t, other) {
      var o = pos(other, theta);
      return Math.hypot(s.radius * Math.cos(t) - o.x, -s.radius * Math.sin(t) - o.y);
    }
    // Дистанция по ТЕКУЩИМ углам (копии сигналов держат исходные x/y —
    // они обновятся только в финализации).
    function liveDist(s1, s2) {
      var t1 = theta[s1.taskId], t2 = theta[s2.taskId];
      return Math.hypot(
        s1.radius * Math.cos(t1) - s2.radius * Math.cos(t2),
        -s1.radius * Math.sin(t1) + s2.radius * Math.sin(t2),
      );
    }
    // Минимальная дистанция сигнала (на угле t) до всех остальных, кроме pair.
    function minDistToOthers(s, t, exceptId) {
      var m = Infinity;
      for (var k = 0; k < placed.length; k++) {
        if (placed[k].taskId === s.taskId || placed[k].taskId === exceptId) continue;
        m = Math.min(m, distAt(s, t, placed[k]));
      }
      return m;
    }

    // Шаг 1: кластеры по исходным позициям.
    var parent = {};
    free.forEach(function (s) { parent[s.taskId] = s.taskId; });
    function find(x) {
      while (parent[x] !== x) { parent[x] = parent[parent[x]]; x = parent[x]; }
      return x;
    }
    for (var u = 0; u < free.length; u++) {
      for (var v = u + 1; v < free.length; v++) {
        if (dist(free[u], free[v]) < MIN_DIST) {
          parent[find(free[u].taskId)] = find(free[v].taskId);
        }
      }
    }
    var clusters = {};
    free.forEach(function (s) {
      var root = find(s.taskId);
      (clusters[root] = clusters[root] || []).push(s);
    });

    var aggregates = [];
    var aggregatedIds = {};
    var spreading = []; // кластеры 1–3: разводим
    Object.keys(clusters).forEach(function (root) {
      var members = clusters[root];
      if (members.length >= 4) {
        members.sort(function (x, y) { return x.minutesToDeadline - y.minutesToDeadline; });
        aggregates.push({
          anchor: members[0],
          items: members,
          count: members.length,
          x: members[0].x, y: members[0].y,
          radius: members[0].radius,
          size: Math.max(5, members[0].size + 1),
        });
        members.forEach(function (s) { aggregatedIds[s.taskId] = true; });
      } else {
        spreading = spreading.concat(members);
      }
    });

    // Шаг 2: разведение оставшихся (цель с запасом 2% — хвост итераций
    // не опускает хорду ниже MIN_DIST из-за ошибок округления).
    var TARGET = MIN_DIST * 1.02;
    for (var iter = 0; iter < COLLISION_ITERATIONS; iter++) {
      for (var a = 0; a < spreading.length; a++) {
        var si = spreading[a];
        for (var b = 0; b < placed.length; b++) {
          var sj = placed[b];
          if (si.taskId === sj.taskId || aggregatedIds[sj.taskId]) continue;
          if (liveDist(si, sj) >= TARGET) continue;
          var ti = theta[si.taskId], tj = theta[sj.taskId];
          var dth = chordAngle(Math.max(si.radius, 1e-6), Math.max(sj.radius, 1e-6), TARGET);
          // Два кандидата на окружности si, дающие хорду TARGET до sj;
          // выбираем максимум минимальной дистанции ко всем остальным
          // (иначе сигнал заталкивается в предыдущего разведённого),
          // при ничьей — минимальный сдвиг, при равенстве — «+».
          var c1 = tj + dth, c2 = tj - dth;
          var m1 = minDistToOthers(si, c1, sj.taskId);
          var m2 = minDistToOthers(si, c2, sj.taskId);
          var next;
          if (m1 > m2 + 1e-9) next = c1;
          else if (m2 > m1 + 1e-9) next = c2;
          else {
            var s1 = Math.abs(angDiff(c1 - ti)), s2 = Math.abs(angDiff(c2 - ti));
            if (Math.abs(s1 - s2) > 1e-9) next = s1 < s2 ? ti + angDiff(c1 - ti) : ti + angDiff(c2 - ti);
            else next = ti + angDiff(c1 - ti);
          }
          theta[si.taskId] = next;
        }
      }
    }

    // Финализация: угол/координаты разведённых, радиусы не тронуты.
    spreading.forEach(function (s) {
      s.angleDeg = normDeg(theta[s.taskId] * 180 / Math.PI);
      var xy = polarToCartesian(s.radius, theta[s.taskId]);
      s.x = xy.x; s.y = xy.y;
    });

    // Остаточные перекрытия (теоретически только у fixed-препятствий):
    // большие полупрозрачные, меньшие рисуются поверх (DOM-порядок —
    // по срочности, поэтому «поверхность» регулируем прозрачностью).
    var overlappingIds = {};
    for (var p = 0; p < placed.length; p++) {
      for (var q = p + 1; q < placed.length; q++) {
        if (aggregatedIds[placed[p].taskId] || aggregatedIds[placed[q].taskId]) continue;
        if (dist(placed[p], placed[q]) < MIN_DIST) {
          overlappingIds[placed[p].taskId] = true;
          overlappingIds[placed[q].taskId] = true;
        }
      }
    }

    var result = placed.filter(function (s) { return !aggregatedIds[s.taskId]; });
    return { placed: result, aggregates: aggregates, overlappingIds: overlappingIds };
  }

  function pos(s, theta) {
    var t = theta[s.taskId] !== undefined ? theta[s.taskId] : s.angleDeg * Math.PI / 180;
    return polarToCartesian(s.radius, t);
  }

  /* ---------- URL-состояние (без роутера: query-параметры) ----------
     Невалидные значения молча игнорируются — ссылка не должна ломать UI. */
  function filtersToQuery(f) {
    var p = [];
    var allPriorities = !f.priorities || f.priorities.length === 0
      || (f.priorities.length === PRIORITIES.length);
    if (f.sectors && f.sectors.length) p.push('assignees=' + encodeURIComponent(f.sectors.join(',')));
    if (!allPriorities && f.priorities.length) p.push('priorities=' + encodeURIComponent(f.priorities.join(',')));
    if (f.includeDone) p.push('includeDone=1');
    if (f.onlyOverdue) p.push('onlyOverdue=1');
    if (f.onlyMine) p.push('onlyMine=1');
    if (f.maxDays != null && f.maxDays !== 90) p.push('maxDays=' + parseInt(f.maxDays, 10));
    return p.join('&');
  }

  function parseQueryFilters(search) {
    var f = Object.assign({}, DEFAULT_FILTERS, { sectors: [], priorities: [] });
    if (!search) return f;
    var params;
    try { params = new URLSearchParams(search); } catch (e) { return f; }
    var raw;
    if ((raw = params.get('assignees')) != null) {
      f.sectors = raw.split(',').map(function (s) { return s.trim(); }).filter(Boolean);
    }
    if ((raw = params.get('priorities')) != null) {
      f.priorities = raw.split(',')
        .filter(function (p) { return PRIORITIES.indexOf(p) >= 0; });
    }
    if (params.get('includeDone') === '1') f.includeDone = true;
    if (params.get('onlyOverdue') === '1') f.onlyOverdue = true;
    if (params.get('onlyMine') === '1') f.onlyMine = true;
    var md = parseInt(params.get('maxDays'), 10);
    if (!isNaN(md) && md >= 0 && md <= 90) f.maxDays = md;
    return f;
  }

  /* Вью-параметр из URL: только известные виды, иначе null (не падаем). */
  var KNOWN_VIEWS = ['board', 'calendar', 'radar', 'activity'];
  function viewFromQuery(search) {
    if (!search) return null;
    var params;
    try { params = new URLSearchParams(search); } catch (e) { return null; }
    var v = params.get('view');
    return KNOWN_VIEWS.indexOf(v) >= 0 ? v : null;
  }

  /* Экспорт SVG: полный автономный XML (объявление + xmlns + фон),
     чтобы файл открывался вне страницы. Цвета/шрифт уже в атрибутах
     элементов (рендер их ставит), сюда добавляются только обёртка и фон. */
  function buildExportSvgMarkup(innerSvgMarkup, opts) {
    opts = opts || {};
    var w = opts.width || 600, h = opts.height || 600;
    var bg = opts.bg || '#0b0f1e';
    return '<?xml version="1.0" encoding="UTF-8"?>\n'
      + '<svg xmlns="http://www.w3.org/2000/svg" width="' + w + '" height="' + h
      + '" viewBox="-100 -100 200 200" font-family="system-ui, Segoe UI, sans-serif">\n'
      + '<rect x="-100" y="-100" width="200" height="200" fill="' + bg + '"/>\n'
      + innerSvgMarkup + '\n</svg>';
  }

  /* PNG недоступен (нет canvas.toBlob) — экспорт падает на SVG. */
  function chooseExportFormat(requested, hasCanvasToBlob) {
    if (requested === 'png' && !hasCanvasToBlob) return 'svg';
    return requested;
  }

  return {
    SPREAD_DEG: SPREAD_DEG,
    MIN_DIST: MIN_DIST,
    OVERDUE_CUTOFF_DAYS: OVERDUE_CUTOFF_DAYS,
    MAX_SIGNALS: MAX_SIGNALS,
    PRIORITIES: PRIORITIES,
    DEFAULT_FILTERS: DEFAULT_FILTERS,
    hashString: hashString,
    radiusForTime: radiusForTime,
    ringRadii: ringRadii,
    angleForTask: angleForTask,
    polarToCartesian: polarToCartesian,
    signalSize: signalSize,
    buildSignal: buildSignal,
    selectRadarSignals: selectRadarSignals,
    resolveCollisions: resolveCollisions,
    filtersToQuery: filtersToQuery,
    parseQueryFilters: parseQueryFilters,
    viewFromQuery: viewFromQuery,
    buildExportSvgMarkup: buildExportSvgMarkup,
    chooseExportFormat: chooseExportFormat,
    normDeg: normDeg,
  };
});
