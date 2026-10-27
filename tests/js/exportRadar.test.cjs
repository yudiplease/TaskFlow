/* Тесты экспорта радара (чистая часть: сборка автономного SVG и выбор формата).
   Сериализация живого DOM-SVG (XMLSerializer) и PNG через canvas — браузерные
   пути; здесь проверяется то, что доступно без DOM: обёртка XML и фолбэк. */
const { test } = require('node:test');
const assert = require('node:assert');
const C = require('../../web/static/radar-core.js');

const inner = '<circle class="radar-signal-dot" fill="#ff6b9d" r="4" cx="10" cy="-20"/>'
  + '<text font-family="system-ui, Segoe UI, sans-serif" fill="#ffffff" font-size="4.2" x="0" y="-95">90д</text>';

test('buildExportSvgMarkup: XML-объявление, xmlns, viewBox, фон', () => {
  const svg = C.buildExportSvgMarkup(inner, { width: 600, height: 600, bg: '#0b0f1e' });
  assert.ok(svg.startsWith('<?xml version="1.0" encoding="UTF-8"?>'));
  assert.ok(svg.includes('xmlns="http://www.w3.org/2000/svg"'));
  assert.ok(svg.includes('viewBox="-100 -100 200 200"'));
  assert.ok(svg.includes('<rect x="-100" y="-100" width="200" height="200" fill="#0b0f1e"/>'));
  assert.ok(svg.includes(inner));
});

test('buildExportSvgMarkup: теги сбалансированы, один корневой <svg>', () => {
  const svg = C.buildExportSvgMarkup('<g><circle r="1"/></g>');
  assert.strictEqual(svg.split('<svg').length - 1, 1);
  assert.ok(svg.trim().endsWith('</svg>'));
  const opens = (svg.match(/<(circle|text|g|rect|line)\b/g) || []).length;
  const selfClosing = (svg.match(/\/>/g) || []).length;
  const closes = (svg.match(/<\/(circle|text|g|rect|line)>/g) || []).length;
  // Каждый непарный тег самозакрыт: opens == selfClosing + closes.
  assert.strictEqual(opens, selfClosing + closes);
});

test('цвета и шрифты инлайнятся в разметку (файл автономен)', () => {
  const svg = C.buildExportSvgMarkup(inner);
  assert.ok(svg.includes('fill="#ff6b9d"'), 'цвет сигнала должен быть атрибутом');
  assert.ok(svg.includes('font-family="system-ui, Segoe UI, sans-serif"'));
});

test('chooseExportFormat: PNG без canvas.toBlob → фолбэк на SVG', () => {
  assert.strictEqual(C.chooseExportFormat('png', true), 'png');
  assert.strictEqual(C.chooseExportFormat('png', false), 'svg');
  assert.strictEqual(C.chooseExportFormat('svg', false), 'svg');
  assert.strictEqual(C.chooseExportFormat('svg', true), 'svg');
});
