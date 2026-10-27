/* Тесты селектора сигналов (RadarCore.selectRadarSignals). */
const { test } = require('node:test');
const assert = require('node:assert');
const C = require('../../web/static/radar-core.js');

const NOW = new Date('2026-10-27T12:00:00Z').getTime();
const MIN = 60000;
const DAY = 1440;

const opts = {
  horizonDays: 90,
  currentUser: 'Вы',
  sectorKey: (t) => (t.assignee && t.assignee.trim()) || '—',
  colorOf: (t) => t.color || null,
  customAngles: {},
};

function mk(id, patch = {}) {
  return Object.assign({
    id,
    title: 'Задача ' + id,
    status: 'todo',
    priority: 'medium',
    assignee: 'Анна',
    color: null,
    due_at: new Date(NOW + 3 * DAY * MIN).toISOString(),
    due_all_day: false,
  }, patch);
}

test('без due_at — не сигнал', () => {
  const r = C.selectRadarSignals([mk('a', { due_at: null })], NOW, C.DEFAULT_FILTERS, opts);
  assert.strictEqual(r.signals.length, 0);
});

test('битая дата due_at — не сигнал и не падение', () => {
  const r = C.selectRadarSignals([mk('a', { due_at: 'не дата' })], NOW, C.DEFAULT_FILTERS, opts);
  assert.strictEqual(r.signals.length, 0);
});

test('done скрыт по умолчанию, виден с includeDone', () => {
  const t = mk('a', { status: 'done' });
  assert.strictEqual(C.selectRadarSignals([t], NOW, C.DEFAULT_FILTERS, opts).signals.length, 0);
  const r = C.selectRadarSignals([t], NOW, { ...C.DEFAULT_FILTERS, includeDone: true }, opts);
  assert.strictEqual(r.signals.length, 1);
});

test('archived скрыт всегда', () => {
  const t = mk('a', { status: 'archived' });
  assert.strictEqual(
    C.selectRadarSignals([t], NOW, { ...C.DEFAULT_FILTERS, includeDone: true }, opts).signals.length, 0);
});

test('будущее за горизонтом (91д при 90) — не сигнал', () => {
  const r = C.selectRadarSignals([mk('a', { due_at: new Date(NOW + 91 * DAY * MIN).toISOString() })],
    NOW, C.DEFAULT_FILTERS, opts);
  assert.strictEqual(r.signals.length, 0);
});

test('просрочка старше cutoff (31д) — не сигнал', () => {
  const r = C.selectRadarSignals([mk('a', { due_at: new Date(NOW - 31 * DAY * MIN).toISOString() })],
    NOW, C.DEFAULT_FILTERS, opts);
  assert.strictEqual(r.signals.length, 0);
});

test('due_all_day: minutesToDeadline — до конца дня (23:59 по локальному времени)', () => {
  // stored instant для all-day = 23:59 локального дня создателя (миграция 005).
  const endOfDay = new Date(new Date().setHours(23, 59, 0, 0)).toISOString();
  const r = C.selectRadarSignals([mk('a', { due_at: endOfDay, due_all_day: true })], Date.now(),
    C.DEFAULT_FILTERS, opts);
  assert.strictEqual(r.signals.length, 1);
  const m = r.signals[0].minutesToDeadline;
  assert.ok(m > 0 && m <= 1440, `остаток до 23:59 должен быть в пределах суток, получено ${m}`);
  assert.strictEqual(r.signals[0].dueAllDay, true);
});

test('фильтр по исполнителю (роль проектов в TaskFlow)', () => {
  const tasks = [mk('a', { assignee: 'Анна' }), mk('b', { assignee: 'Михаил' }), mk('c', { assignee: '' })];
  const r = C.selectRadarSignals(tasks, NOW, { ...C.DEFAULT_FILTERS, sectors: ['Анна'] }, opts);
  assert.deepStrictEqual(r.signals.map((s) => s.sectorKey), ['Анна']);
});

test('фильтр по приоритету', () => {
  const tasks = [mk('a', { priority: 'urgent' }), mk('b', { priority: 'low' })];
  const r = C.selectRadarSignals(tasks, NOW, { ...C.DEFAULT_FILTERS, priorities: ['urgent'] }, opts);
  assert.deepStrictEqual(r.signals.map((s) => s.priority), ['urgent']);
});

test('только мои задачи (assignee == currentUser)', () => {
  const tasks = [mk('a', { assignee: 'Вы' }), mk('b', { assignee: 'Анна' })];
  const r = C.selectRadarSignals(tasks, NOW, { ...C.DEFAULT_FILTERS, onlyMine: true }, opts);
  assert.deepStrictEqual(r.signals.map((s) => s.sectorKey), ['Вы']);
});

test('только просроченные', () => {
  const tasks = [
    mk('a', { due_at: new Date(NOW - 2 * DAY * MIN).toISOString() }),
    mk('b'),
  ];
  const r = C.selectRadarSignals(tasks, NOW, { ...C.DEFAULT_FILTERS, onlyOverdue: true }, opts);
  assert.strictEqual(r.signals.length, 1);
  assert.ok(r.signals[0].isOverdue);
});

test('слайдер «ближе чем N дней» режет будущее, просрочку не трогает', () => {
  const tasks = [
    mk('future', { due_at: new Date(NOW + 20 * DAY * MIN).toISOString() }),
    mk('soon', { due_at: new Date(NOW + 5 * DAY * MIN).toISOString() }),
    mk('late', { due_at: new Date(NOW - 5 * DAY * MIN).toISOString() }),
  ];
  const r = C.selectRadarSignals(tasks, NOW, { ...C.DEFAULT_FILTERS, maxDays: 7 }, opts);
  assert.deepStrictEqual(r.signals.map((s) => s.taskId).sort(), ['late', 'soon']);
});

test('больше maxSignals — топ по близости + агрегат переполнения', () => {
  const tasks = [];
  for (let i = 0; i < 305; i++) {
    // Все в пределах горизонта 90д (иначе отсекутся селектором до топ-N).
    tasks.push(mk('t' + i, { due_at: new Date(NOW + ((i % 80) + 1) * DAY * MIN).toISOString() }));
  }
  const r = C.selectRadarSignals(tasks, NOW, C.DEFAULT_FILTERS, opts);
  assert.strictEqual(r.overflowCount, 5);
  const overflow = r.signals.find((s) => s.taskId === '__overflow__');
  assert.ok(overflow, 'синтетический агрегат переполнения должен быть');
  const real = r.signals.filter((s) => s.taskId !== '__overflow__');
  assert.strictEqual(real.length, 300);
  // Топ-300 по близости: среди оставшихся нет дальше 80-го дня.
  assert.ok(Math.max(...real.map((s) => s.minutesToDeadline)) <= 80 * DAY * MIN + 1);
});

test('сортировка по срочности: старейшая просрочка первой', () => {
  const tasks = [
    mk('soon', { due_at: new Date(NOW + 1 * DAY * MIN).toISOString() }),
    mk('late2', { due_at: new Date(NOW - 10 * DAY * MIN).toISOString() }),
    mk('late1', { due_at: new Date(NOW - 20 * DAY * MIN).toISOString() }),
  ];
  const r = C.selectRadarSignals(tasks, NOW, C.DEFAULT_FILTERS, opts);
  assert.deepStrictEqual(r.signals.map((s) => s.taskId), ['late1', 'late2', 'soon']);
});

test('цвет: task.color, иначе фолбэк серый из ядра', () => {
  const r = C.selectRadarSignals(
    [mk('a', { color: '#ff6b9d' }), mk('b', { color: null })], NOW, C.DEFAULT_FILTERS, opts);
  assert.strictEqual(r.signals.find((s) => s.taskId === 'a').color, '#ff6b9d');
  assert.strictEqual(r.signals.find((s) => s.taskId === 'b').color, '#9E9E9E');
});

test('customAngle из opts попадает в сигнал', () => {
  const r = C.selectRadarSignals([mk('a')], NOW, C.DEFAULT_FILTERS,
    { ...opts, customAngles: { a: 270 } });
  assert.ok(Math.abs(r.signals[0].angleDeg - 270) < 1e-6);
  assert.strictEqual(r.signals[0].hasCustomAngle, true);
});
