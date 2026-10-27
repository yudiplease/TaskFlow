/* Тесты URL-состояния фильтров (RadarCore.filtersToQuery/parseQueryFilters/viewFromQuery). */
const { test } = require('node:test');
const assert = require('node:assert');
const C = require('../../web/static/radar-core.js');

test('сериализация → десериализация: полный круг', () => {
  const f = {
    sectors: ['Анна', 'Михаил'],
    priorities: ['high', 'urgent'],
    includeDone: true,
    onlyOverdue: false,
    onlyMine: true,
    maxDays: 30,
  };
  const q = C.filtersToQuery(f);
  const back = C.parseQueryFilters('?' + q);
  assert.deepStrictEqual(back.sectors, ['Анна', 'Михаил']);
  assert.deepStrictEqual(back.priorities, ['high', 'urgent']);
  assert.strictEqual(back.includeDone, true);
  assert.strictEqual(back.onlyMine, true);
  assert.strictEqual(back.onlyOverdue, false);
  assert.strictEqual(back.maxDays, 30);
});

test('пустые фильтры → чистый URL без лишних параметров', () => {
  assert.strictEqual(C.filtersToQuery(C.DEFAULT_FILTERS), '');
});

test('все приоритеты выбранные = как пустые (не пишем шум в URL)', () => {
  const f = { ...C.DEFAULT_FILTERS, priorities: [...C.PRIORITIES] };
  assert.strictEqual(C.filtersToQuery(f), '');
});

test('невалидные приоритеты игнорируются', () => {
  const f = C.parseQueryFilters('?priorities=critical,high, nonsense');
  assert.deepStrictEqual(f.priorities, ['high']);
});

test('невалидный maxDays (мусор/за пределами) игнорируется', () => {
  assert.strictEqual(C.parseQueryFilters('?maxDays=abc').maxDays, 90);
  assert.strictEqual(C.parseQueryFilters('?maxDays=500').maxDays, 90);
  assert.strictEqual(C.parseQueryFilters('?maxDays=-3').maxDays, 90);
  assert.strictEqual(C.parseQueryFilters('?maxDays=45').maxDays, 45);
});

test('битый query-строка не роняет парсер', () => {
  const f = C.parseQueryFilters('?priorities=%ZZ&maxDays=30');
  assert.strictEqual(f.maxDays, 30);
});

test('флаги: только 1 = истина', () => {
  const f = C.parseQueryFilters('?onlyMine=1&onlyOverdue=0&includeDone=yes');
  assert.strictEqual(f.onlyMine, true);
  assert.strictEqual(f.onlyOverdue, false);
  assert.strictEqual(f.includeDone, false);
});

test('viewFromQuery: известные виды пропускает, мусор → null', () => {
  assert.strictEqual(C.viewFromQuery('?view=radar'), 'radar');
  assert.strictEqual(C.viewFromQuery('?view=board'), 'board');
  assert.strictEqual(C.viewFromQuery('?view=hack=1'), null);
  assert.strictEqual(C.viewFromQuery(''), null);
  assert.strictEqual(C.viewFromQuery('?other=1'), null);
});

test('порча URL: view=radar без фильтров → дефолтные фильтры (всё в горизонте)', () => {
  const f = C.parseQueryFilters('?view=radar');
  assert.deepStrictEqual(f, C.DEFAULT_FILTERS);
});
