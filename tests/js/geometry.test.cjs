/* Тесты геометрии радара (RadarCore): радиусы, углы, координаты.
   Запуск: node --test tests/js/ */
const { test } = require('node:test');
const assert = require('node:assert');
const C = require('../../web/static/radar-core.js');

const DAY = 1440; // минут в сутках

test('radiusForTime: 0 минут → 0 (центр = «сейчас»)', () => {
  assert.strictEqual(C.radiusForTime(0, 90), 0);
});

test('radiusForTime: ровно горизонт (90д) → 100', () => {
  assert.ok(Math.abs(C.radiusForTime(90 * DAY, 90) - 100) < 1e-9);
});

test('radiusForTime: будущее за горизонтом (180д при 90) → clamp 100', () => {
  assert.ok(Math.abs(C.radiusForTime(180 * DAY, 90) - 100) < 1e-9);
});

test('radiusForTime: просрочка −90д при горизонте 90 → 100 (та же формула от |days|)', () => {
  assert.ok(Math.abs(C.radiusForTime(-90 * DAY, 90) - 100) < 1e-9);
});

// Контрольные точки ТЗ: r(3д)≈30.9, r(7д)≈46.4, r(30д)≈76.0.
// По точной формуле 100·ln(d+1)/ln(91): 30.73, 46.10, 76.13 —
// значения ТЗ оценочные, допускаем ±0.6.
test('radiusForTime: кольца 3д/7д/30д на горизонте 90', () => {
  assert.ok(Math.abs(C.radiusForTime(3 * DAY, 90) - 30.9) < 0.6, C.radiusForTime(3 * DAY, 90));
  assert.ok(Math.abs(C.radiusForTime(7 * DAY, 90) - 46.4) < 0.6, C.radiusForTime(7 * DAY, 90));
  assert.ok(Math.abs(C.radiusForTime(30 * DAY, 90) - 76.0) < 0.6, C.radiusForTime(30 * DAY, 90));
});

test('ringRadii: 4 кольца на горизонте 90, 3 кольца на горизонте 30', () => {
  const r90 = C.ringRadii(90).map((x) => x.days);
  assert.deepStrictEqual(r90, [3, 7, 30, 90]);
  const r30 = C.ringRadii(30).map((x) => x.days);
  assert.deepStrictEqual(r30, [3, 7, 30]);
});

test('angleForTask: детерминирован между вызовами', () => {
  const a = C.angleForTask({ sectorKey: 'Анна', taskId: 't1', isOverdue: false });
  const b = C.angleForTask({ sectorKey: 'Анна', taskId: 't1', isOverdue: false });
  assert.strictEqual(a, b);
});

test('angleForTask: разные задачи одного сектора — в пределах спреда ±15°', () => {
  for (let i = 0; i < 50; i++) {
    const a = C.angleForTask({ sectorKey: 'Анна', taskId: 'task-' + i, isOverdue: false });
    const sector = (C.hashString('Анна') % 360) * Math.PI / 180;
    const diff = Math.abs(a - sector) * 180 / Math.PI;
    assert.ok(diff <= 15.0001 || diff >= 344.9999, `jitter ${diff}° вне спреда`);
  }
});

test('angleForTask: customAngle переопределяет всё', () => {
  const a = C.angleForTask({ sectorKey: 'Анна', taskId: 't1', isOverdue: true, customAngleDeg: 270 });
  assert.ok(Math.abs(a - 270 * Math.PI / 180) < 1e-9);
});

test('angleForTask: просрочка → θ + π', () => {
  const onTime = C.angleForTask({ sectorKey: 'Анна', taskId: 't1', isOverdue: false });
  const late = C.angleForTask({ sectorKey: 'Анна', taskId: 't1', isOverdue: true });
  const diff = Math.abs(late - onTime) % (2 * Math.PI);
  assert.ok(Math.abs(diff - Math.PI) < 1e-9 || Math.abs(diff - Math.PI) > 2 * Math.PI - 1e-9,
    'разница должна быть π');
});

test('polarToCartesian: контрольные точки', () => {
  assert.deepStrictEqual(C.polarToCartesian(0, 0), { x: 0, y: 0 });
  const e = C.polarToCartesian(100, 0);          // θ=0 → вправо
  assert.ok(Math.abs(e.x - 100) < 1e-9 && Math.abs(e.y) < 1e-9);
  const n = C.polarToCartesian(100, Math.PI / 2); // θ=90° → вверх (SVG y вниз!)
  assert.ok(Math.abs(n.x) < 1e-9 && Math.abs(n.y + 100) < 1e-9);
});

test('signalSize: растёт к центру на 30% максимум', () => {
  assert.ok(Math.abs(C.signalSize('low', 100) - 1.5) < 1e-9);
  assert.ok(Math.abs(C.signalSize('low', 0) - 1.5 * 1.3) < 1e-9);
  assert.ok(Math.abs(C.signalSize('urgent', 50) - 6 * 1.15) < 1e-9);
  assert.ok(C.signalSize('unknown-priority', 100) === C.signalSize('medium', 100));
});

test('normDeg: нормализация в [0, 360)', () => {
  assert.strictEqual(C.normDeg(-90), 270);
  assert.strictEqual(C.normDeg(450), 90);
  assert.strictEqual(C.normDeg(0), 0);
});
