/* Тесты разведения коллизий (RadarCore.resolveCollisions). */
const { test } = require('node:test');
const assert = require('node:assert');
const C = require('../../web/static/radar-core.js');

// Хелпер: сигнал в заданной точке (радиус в %, угол в градусах).
function sig(id, radius, angleDeg, opts = {}) {
  const xy = C.polarToCartesian(radius, angleDeg * Math.PI / 180);
  return {
    taskId: id,
    title: id,
    sectorKey: 's',
    color: '#9E9E9E',
    priority: 'medium',
    minutesToDeadline: opts.minutes != null ? opts.minutes : -radius * 10,
    isOverdue: !!opts.overdue,
    radius,
    angleDeg,
    size: opts.size != null ? opts.size : 2.5,
    x: xy.x, y: xy.y,
    hasCustomAngle: !!opts.custom,
  };
}

function dist(a, b) {
  return Math.hypot(a.x - b.x, a.y - b.y);
}

test('две точки в 1 px → разведены на ≥ 4 px (MIN_DIST)', () => {
  // Один радиус, углы почти совпадают → дуговая дистанция мала.
  const a = sig('a', 50, 10);
  const b = sig('b', 50, 10.2);
  assert.ok(dist(a, b) < 1, 'подготовка: точки должны пересекаться');
  const r = C.resolveCollisions([a, b]);
  assert.strictEqual(r.placed.length, 2);
  assert.ok(dist(r.placed[0], r.placed[1]) >= C.MIN_DIST,
    'после разведения дистанция ' + dist(r.placed[0], r.placed[1]));
});

test('радиус при разведении сохраняется (двигается только угол)', () => {
  const a = sig('a', 60, 10);
  const b = sig('b', 60, 10.1);
  const r = C.resolveCollisions([a, b]);
  for (const s of r.placed) {
    assert.ok(Math.abs(s.radius - 60) < 1e-9, 'радиус изменился — так нельзя');
  }
});

test('три в одной точке → разведены (или агрегированы)', () => {
  const arr = ['x1', 'x2', 'x3'].map((id) => sig(id, 40, 77));
  const r = C.resolveCollisions(arr);
  if (r.aggregates.length === 0) {
    // разведены: все попарные дистанции ≥ MIN_DIST
    for (let i = 0; i < r.placed.length; i++) {
      for (let j = i + 1; j < r.placed.length; j++) {
        assert.ok(dist(r.placed[i], r.placed[j]) >= C.MIN_DIST - 1e-6);
      }
    }
  } else {
    assert.ok(r.placed.length + r.aggregates[0].count === 3);
  }
});

test('пять в одной точке → агрегат «+N»', () => {
  const arr = ['p1', 'p2', 'p3', 'p4', 'p5'].map((id) => sig(id, 40, 77, { minutes: -id.length }));
  const r = C.resolveCollisions(arr);
  assert.strictEqual(r.aggregates.length, 1, 'должен собраться один агрегат');
  assert.strictEqual(r.aggregates[0].count, 5);
  assert.strictEqual(r.placed.length, 0, 'все пять ушли в агрегат');
  // Якорь — самый срочный (минимальный minutesToDeadline).
  const minutes = r.aggregates[0].items.map((s) => s.minutesToDeadline);
  assert.strictEqual(Math.min(...minutes), r.aggregates[0].anchor.minutesToDeadline);
});

test('коллизии не выводят сигнал за viewBox (радиус ≤ 100)', () => {
  const arr = ['c1', 'c2', 'c3'].map((id) => sig(id, 99.5, 5));
  const r = C.resolveCollisions(arr);
  for (const s of r.placed) {
    assert.ok(Math.abs(s.x) <= 100 + 1e-6 && Math.abs(s.y) <= 100 + 1e-6,
      `x=${s.x} y=${s.y} вне viewBox`);
  }
});

test('customAngle не участвует в коллизии: он неподвижен, другой отъезжает', () => {
  const fixed = sig('fixed', 50, 45, { custom: true });
  const free = sig('free', 50, 45.05);
  const r = C.resolveCollisions([fixed, free]);
  const fixedAfter = r.placed.find((s) => s.taskId === 'fixed');
  const freeAfter = r.placed.find((s) => s.taskId === 'free');
  assert.strictEqual(fixedAfter.angleDeg, 45, 'custom-угол изменился — нельзя');
  assert.ok(dist(fixedAfter, freeAfter) >= C.MIN_DIST,
    'свободный сигнал должен отъехать от custom');
});

test('детерминированность: одинаковый вход → одинаковый выход', () => {
  const arr = ['d1', 'd2', 'd3', 'd4'].map((id) => sig(id, 30, 100));
  const r1 = C.resolveCollisions(arr.map((s) => ({ ...s })));
  const r2 = C.resolveCollisions(arr.map((s) => ({ ...s })));
  assert.deepStrictEqual(
    r1.placed.map((s) => [s.taskId, s.angleDeg]),
    r2.placed.map((s) => [s.taskId, s.angleDeg]),
  );
});
