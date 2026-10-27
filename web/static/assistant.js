/* Ася — интерактивная ассистентка TaskFlow.
   Автономный модуль: сама создаёт свой DOM, стили — в assistant.css.
   События приложения приходят через tf-toast (диспатчит app.js в toast()).
   Звучит короткими звуками «няя»/«хи-хи» — сервис в chibi-sounds.js
   (window.chibiSounds), настройки — в модалке «Настройки» app.js.
   Внешних зависимостей нет — работает офлайн в десктоп-версии. */
(() => {
  'use strict';

  // На узких экранах не мешаем (дублирует media-правило css).
  if (window.innerWidth < 861) return;

  const STORAGE_KEY = 'tf-assistant';

  const PHRASES = {
    intro: 'Я Ася, твоя ассистентка в TaskFlow 🚀',
    greet: (h) =>
      h < 5  ? 'Полночь? Я не сплю — у меня внутри setInterval 😄' :
      h < 11 ? 'Доброе утро! И кофе, и задачи — всё готово ☕' :
      h < 17 ? 'Добрый день! Как продвигается доска?' :
      h < 23 ? 'Добрый вечер! Наверстаем упущенное?' :
               'Уже поздно… завтра всё успеем 🌙',
    created: ['Ура, новая задача! Записала ✨', 'В колонке пополнение! Не потеряю 📌', 'Отмечено в журнале. Удачи! 🍀'],
    updated: ['Поправила ✏️', 'Сохранено! Всё как ты сказал(а).', 'Обновление зафиксировано 📝'],
    deleted: ['Пуф — как не бывало 🌪️', 'Убрала. Будем помнить… секунду… забыла 😌', 'DELETE выполнен. Аккуратнее! 😅'],
    done: ['УРА! Закрыта 🎉 Так держать!', 'Готово? Готово! Обожаю этот момент ✨', 'Плюс одна к «сделано сегодня» 💪', 'Доска стала чуточку зеленее 💚'],
    moved: ['Переставила карточку 🔀', 'Новое место — записала 📋'],
    error: ['Ой… {msg} 😨', 'Что-то не так: {msg}', 'Хм, сервер ворчит: {msg}'],
    remindOverdue: ['⏰ Срок горит! {msg}', 'Напоминаю: {msg}', 'Тик-так… {msg}'],
    remindSoon: ['Дружеское напоминание 🙂 {msg}', 'Календарь шепчет: {msg}'],
    overdueNag: ['Псст: просроченных — {n} 😟 Глянем?', 'Меня это беспокоит: {n} задач просрочено…', '{n} задач за красной чертой ⏰'],
    productive: ['Сегодня закрыто уже {n} 🔥 Продуктивно!'],
    tips: [
      'Совет: клавиша «/» открывает поиск по задачам.',
      'Карточки можно перетаскивать между колонками — история сохранится.',
      'Двойной клик по дню в календаре создаёт задачу на эту дату.',
      'Колокольчик справа сверху — там напоминания о дедлайнах.',
      'Фильтр «только просроченные» спасает в понедельник 🙃',
      'Кликни по мне ещё раз — я умею махать рукой 👋',
      'Задачу без срока можно перетащить из ленты прямо в день календаря.',
      'Прогресс по доске — в панельке слева. Я слежу за ним! 📊',
    ],
    clickWave: ['Привет-привет! 👋', 'Ты позвал(а)? Я тут! 😊', 'Служу трекеру! 🫡'],
    clickWink: ['😉 Всё под контролем!', 'Я слежу за дедлайнами. Тихо-тихо 👀'],
    clickBounce: ['Йоу! 🤸', 'Энергия +100! ⚡', 'Настроение: сделать всё! 💪'],
    noServer: 'Сервер не отвечает… глянь индикатор внизу слева 🙁',
  };

  const pick = (arr) => arr[Math.floor(Math.random() * arr.length)];
  const rand = (min, max) => min + Math.random() * (max - min);

  /* ---------- DOM ---------- */
  const container = document.createElement('div');
  container.className = 'tf-assistant tf-off';
  container.dataset.mood = 'happy';
  container.innerHTML = `
    <div class="tf-bubble"></div>
    <button class="tf-hide-btn" title="Свернуть Асю">✕</button>
    <svg class="tf-stage" viewBox="0 0 200 240" width="196" height="235" aria-label="Ася — ассистентка">
      <defs>
        <linearGradient id="tfg-hair" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#b79bff"/>
          <stop offset="100%" stop-color="#7b5cff"/>
        </linearGradient>
        <linearGradient id="tfg-hair2" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#d07dff"/>
          <stop offset="100%" stop-color="#9b6dff"/>
        </linearGradient>
        <radialGradient id="tfg-iris" cx="50%" cy="42%" r="65%">
          <stop offset="0%" stop-color="#9d84ff"/>
          <stop offset="55%" stop-color="#6a49e8"/>
          <stop offset="100%" stop-color="#4527b8"/>
        </radialGradient>
        <linearGradient id="tfg-dress" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#2e3770"/>
          <stop offset="100%" stop-color="#181f3e"/>
        </linearGradient>
      </defs>

      <ellipse id="tf-shadow" cx="100" cy="232" rx="44" ry="7" fill="rgba(0,0,0,0.32)"/>

      <g id="tf-float">
        <!-- задние волосы -->
        <path id="tf-hair-back" d="M100 22 C 54 22 36 64 40 108 C 43 140 36 168 29 191 C 28 195 32 199 36 197 C 56 207 144 207 164 197 C 168 199 172 195 171 191 C 164 168 157 140 160 108 C 164 64 146 22 100 22 Z" fill="url(#tfg-hair)"/>

        <!-- тело -->
        <g id="tf-body">
          <line x1="87" y1="200" x2="87" y2="222" stroke="#ffdcc2" stroke-width="10" stroke-linecap="round"/>
          <line x1="113" y1="200" x2="113" y2="222" stroke="#ffdcc2" stroke-width="10" stroke-linecap="round"/>
          <ellipse cx="86" cy="227" rx="9.5" ry="5.5" fill="#3d3577"/>
          <ellipse cx="114" cy="227" rx="9.5" ry="5.5" fill="#3d3577"/>
          <path d="M100 144 C 84 144 76 151 72 162 C 68 176 65 190 62 202 C 87 211 113 211 138 202 C 135 190 132 176 128 162 C 124 151 116 144 100 144 Z" fill="url(#tfg-dress)" stroke="rgba(255,255,255,0.06)"/>
          <path d="M88 148 L100 162 L112 148" fill="none" stroke="#e8ecf5" stroke-width="4" stroke-linecap="round" stroke-linejoin="round"/>
          <circle cx="100" cy="178" r="7" fill="#0f1526" stroke="#6d7dff" stroke-width="1.6"/>
          <text x="100" y="181.4" font-size="9" text-anchor="middle">🚀</text>
          <path d="M80 154 C 67 160 58 170 53 181" fill="none" stroke="#ffe3d0" stroke-width="9" stroke-linecap="round"/>
          <g id="tf-arm-wave">
            <path d="M120 154 C 133 160 142 170 147 181" fill="none" stroke="#ffe3d0" stroke-width="9" stroke-linecap="round"/>
          </g>
        </g>

        <!-- голова -->
        <g id="tf-head">
          <ellipse cx="100" cy="97" rx="51" ry="49" fill="#ffe3d0"/>
          <ellipse class="tf-blush" cx="64" cy="116" rx="8.5" ry="4.6" fill="#ff8fae" opacity="0.45"/>
          <ellipse class="tf-blush" cx="136" cy="116" rx="8.5" ry="4.6" fill="#ff8fae" opacity="0.45"/>

          <g id="tf-mouths">
            <path id="tf-mouth-smile" d="M92 124 Q 100 132 108 124" fill="none" stroke="#c94f74" stroke-width="3" stroke-linecap="round"/>
            <path id="tf-mouth-big" d="M90 121 Q 100 136 110 121 Q 100 126 90 121 Z" fill="#e0567e"/>
            <path id="tf-mouth-worry" d="M92 129 Q 100 121 108 129" fill="none" stroke="#c94f74" stroke-width="3" stroke-linecap="round"/>
            <path id="tf-mouth-neutral" d="M95 125 Q 100 129 105 125" fill="none" stroke="#c94f74" stroke-width="3" stroke-linecap="round"/>
            <ellipse id="tf-mouth-o" cx="100" cy="126" rx="4.2" ry="5" fill="#e0567e"/>
          </g>

          <!-- глаза: внешний слой двигается за курсором, внутренний моргает -->
          <g class="tf-eye-track" id="tf-eye-track-l">
            <g class="tf-eye-shape" id="tf-eye-shape-l">
              <ellipse cx="79" cy="103" rx="10.5" ry="13.5" fill="url(#tfg-iris)"/>
              <ellipse cx="79" cy="103" rx="4.6" ry="6.4" fill="#241256"/>
              <circle cx="75.6" cy="97.5" r="3.4" fill="#ffffff"/>
              <circle cx="82.4" cy="107.5" r="1.7" fill="#ffffff" opacity="0.75"/>
              <path d="M68.5 94 Q 79 87 89.5 94" fill="none" stroke="#2e2060" stroke-width="3.4" stroke-linecap="round"/>
            </g>
          </g>
          <g class="tf-eye-track" id="tf-eye-track-r">
            <g class="tf-eye-shape" id="tf-eye-shape-r">
              <ellipse cx="121" cy="103" rx="10.5" ry="13.5" fill="url(#tfg-iris)"/>
              <ellipse cx="121" cy="103" rx="4.6" ry="6.4" fill="#241256"/>
              <circle cx="117.6" cy="97.5" r="3.4" fill="#ffffff"/>
              <circle cx="124.4" cy="107.5" r="1.7" fill="#ffffff" opacity="0.75"/>
              <path d="M110.5 94 Q 121 87 131.5 94" fill="none" stroke="#2e2060" stroke-width="3.4" stroke-linecap="round"/>
            </g>
          </g>

          <path class="tf-brow tf-brow-l" d="M67 84 Q 77 78 87 83" fill="none" stroke="#6b46d6" stroke-width="3.2" stroke-linecap="round"/>
          <path class="tf-brow tf-brow-r" d="M113 83 Q 123 78 133 84" fill="none" stroke="#6b46d6" stroke-width="3.2" stroke-linecap="round"/>

          <!-- чёлка: шапочка + пряди-зубчики + боковые локоны -->
          <path d="M50 96 C 48 58 70 37 100 37 C 130 37 152 58 150 96 C 140 80 134 72 127 67 C 119 61 110 59 100 59 C 90 59 81 61 73 67 C 66 72 60 80 50 96 Z" fill="url(#tfg-hair2)"/>
          <path d="M60 78 C 62 90 66 99 72 103 C 74 92 74 80 71 68 C 67 71 63 74 60 78 Z" fill="url(#tfg-hair2)"/>
          <path d="M78 64 C 79 80 83 93 89 98 C 92 87 93 74 91 60 Z" fill="url(#tfg-hair2)"/>
          <path d="M104 62 C 103 76 105 90 111 96 C 115 85 116 72 114 58 Z" fill="url(#tfg-hair2)"/>
          <path d="M124 66 C 122 79 124 91 130 97 C 134 87 136 75 134 62 Z" fill="url(#tfg-hair2)"/>
          <path d="M52 82 C 41 100 39 130 47 154 C 50 161 60 160 61 152 C 56 130 57 104 64 84 Z" fill="url(#tfg-hair2)"/>
          <path d="M148 82 C 159 100 161 130 153 154 C 150 161 140 160 139 152 C 144 130 143 104 137 84 Z" fill="url(#tfg-hair2)"/>
          <path d="M68 50 Q 100 41 132 50" fill="none" stroke="#d9c9ff" stroke-width="4" stroke-linecap="round" opacity="0.4"/>

          <!-- ahoge и заколка-сердечко -->
          <path id="tf-ahoge" d="M100 38 C 96 20 110 12 121 20" fill="none" stroke="url(#tfg-hair2)" stroke-width="5" stroke-linecap="round"/>
          <path d="M138 86.5 c -2.8 -5.4 -10.6 -2.6 -8 3.4 c 1.7 4 8 7 8 7 c 0 0 6.3 -3 8 -7 c 2.6 -6 -5.2 -8.8 -8 -3.4 z" fill="#ff6b9d"/>
        </g>
      </g>
    </svg>
  `;
  document.body.appendChild(container);

  const bubble = container.querySelector('.tf-bubble');
  const stage = container.querySelector('.tf-stage');
  const head = container.querySelector('#tf-head');
  const eyeTrackL = container.querySelector('#tf-eye-track-l');
  const eyeTrackR = container.querySelector('#tf-eye-track-r');

  /* ---------- Настроение ---------- */
  let moodTimer = null;
  function setMood(mood, { revertMs = 0 } = {}) {
    clearTimeout(moodTimer);
    container.dataset.mood = mood;
    if (revertMs) moodTimer = setTimeout(() => { container.dataset.mood = 'happy'; }, revertMs);
  }

  /* ---------- Звуки чиби (вместо диктора) ---------- */
  // Пул Audio, кулдауны, «Только по клику» и громкость — в chibi-sounds.js.
  // Если скрипт не загрузился, Ася просто молчит: текстовые реплики остаются.
  const chibi = window.chibiSounds || null;
  const chibiEvent = (name) => { if (chibi) chibi.playEvent(name); };

  /* ---------- Реплики (печатная машинка) ---------- */
  let typeTimer = null;
  let hideTimer = null;
  let lastBubbleAt = 0;

  function say(text, { mood = 'happy', revertMs = 2600 } = {}) {
    clearTimeout(typeTimer);
    clearTimeout(hideTimer);
    setMood(mood, { revertMs });
    bubble.classList.add('show');
    bubble.textContent = '';
    let i = 0;
    const step = () => {
      bubble.textContent = text.slice(0, ++i);
      if (i < text.length) typeTimer = setTimeout(step, 16);
    };
    step();
    lastBubbleAt = Date.now();
    const hold = Math.min(9500, Math.max(3500, 2600 + text.length * 55));
    hideTimer = setTimeout(() => bubble.classList.remove('show'), hold);
  }

  // Реакции на события не должны сыпаться в очередь (демо-засев шлёт 10 тостов подряд).
  function react(text, opts = {}) {
    const now = Date.now();
    if (now - lastBubbleAt < 2500) return;
    say(text, opts);
  }
  const fill = (tpl, map) => tpl.replace(/\{(\w+)\}/g, (_, k) => map[k] ?? '');

  /* ---------- Трекинг курсора глазами ---------- */
  let tx = 0, ty = 0, cx = 0, cy = 0;
  window.addEventListener('mousemove', (e) => {
    const r = stage.getBoundingClientRect();
    const dx = e.clientX - (r.left + r.width / 2);
    const dy = e.clientY - (r.top + r.height * 0.35);
    const dist = Math.hypot(dx, dy) || 1;
    const k = Math.min(1, 150 / dist);
    tx = (dx / dist) * 2.6 * k;
    ty = (dy / dist) * 2.2 * k;
  });
  (function trackLoop() {
    cx += (tx - cx) * 0.12;
    cy += (ty - cy) * 0.12;
    const t = `translate(${cx.toFixed(2)}px, ${cy.toFixed(2)}px)`;
    eyeTrackL.style.transform = t;
    eyeTrackR.style.transform = t;
    head.style.transform = `rotate(${(cx * 0.9).toFixed(2)}deg)`;
    requestAnimationFrame(trackLoop);
  })();

  /* ---------- Моргание ---------- */
  function scheduleBlink() {
    setTimeout(() => {
      if (!document.hidden) {
        container.classList.add('blink');
        setTimeout(() => container.classList.remove('blink'), 130);
        if (Math.random() < 0.25) {
          setTimeout(() => {
            container.classList.add('blink');
            setTimeout(() => container.classList.remove('blink'), 130);
          }, 260);
        }
      }
      scheduleBlink();
    }, rand(2400, 6200));
  }
  scheduleBlink();

  /* ---------- Классы-анимации с автоснятием ---------- */
  function pulse(cls, ms) {
    container.classList.add(cls);
    setTimeout(() => container.classList.remove(cls), ms);
  }
  const wave = () => pulse('waving', 2100);
  const bounce = () => pulse('bouncing', 800);
  const shake = () => pulse('shaking', 560);
  const wink = () => pulse('wink', 750);

  function sparkles(count = 8) {
    const colors = ['#ffd166', '#ff8fae', '#9be7ff', '#c4b5fd'];
    const glyphs = ['✦', '✧', '♥', '✨'];
    for (let i = 0; i < count; i++) {
      const s = document.createElement('span');
      s.className = 'tf-spark';
      s.textContent = pick(glyphs);
      s.style.left = rand(30, 150) + 'px';
      s.style.bottom = rand(60, 130) + 'px';
      s.style.fontSize = rand(10, 17) + 'px';
      s.style.color = pick(colors);
      s.style.setProperty('--dx', rand(-26, 26) + 'px');
      s.style.animationDelay = rand(0, 250) + 'ms';
      s.addEventListener('animationend', () => s.remove());
      container.appendChild(s);
    }
  }

  function celebrate() {
    setMood('excited', { revertMs: 3000 });
    bounce();
    sparkles();
  }

  /* ---------- Реакции на события приложения ---------- */
  const BOOT_MS = 12000; // за это время сервер может засеять демо-задачи — не комментируем каждую
  const bootedAt = Date.now();
  let lastOverdue = null;
  let lastOverdueNagAt = 0;
  let lastNoServerAt = 0;

  window.addEventListener('tf-toast', (e) => {
    const { message, type } = e.detail || {};
    if (!message) return;

    if (type === 'error') {
      setMood('worried', { revertMs: 6000 });
      shake();
      chibiEvent('error');
      react(fill(pick(PHRASES.error), { msg: message.slice(0, 90) }), { mood: 'worried', revertMs: 6000 });
      return;
    }
    if (message.startsWith('⏰')) {
      react(fill(pick(PHRASES.remindOverdue), { msg: message.replace(/^⏰\s*/, '') }), { mood: 'surprised', revertMs: 3000 });
      return;
    }
    if (message.startsWith('🗓️')) {
      react(fill(pick(PHRASES.remindSoon), { msg: message.replace(/^🗓️\s*/, '') }));
      return;
    }
    if (message.includes('→ Готово')) {
      celebrate();
      chibiEvent('success');
      react(pick(PHRASES.done), { mood: 'excited', revertMs: 3000 });
      return;
    }
    if (message.includes('→')) {
      react(pick(PHRASES.moved));
      return;
    }
    if (message.includes('создана')) {
      if (Date.now() - bootedAt < BOOT_MS) return;
      react(pick(PHRASES.created));
      return;
    }
    if (message.includes('обновлена')) { react(pick(PHRASES.updated)); return; }
    if (message.includes('удалена'))  { react(pick(PHRASES.deleted)); return; }
  });

  /* ---------- Статистика: тревога о просрочках ---------- */
  async function fetchStats() {
    try {
      const c = new AbortController();
      const t = setTimeout(() => c.abort(), 5000);
      const res = await fetch('/api/stats', { signal: c.signal });
      clearTimeout(t);
      if (!res.ok) throw new Error(res.status);
      return await res.json();
    } catch {
      return null;
    }
  }

  function checkStats(s) {
    if (!s) {
      if (Date.now() - lastNoServerAt > 120000) {
        lastNoServerAt = Date.now();
        setMood('worried', { revertMs: 6000 });
        say(PHRASES.noServer, { mood: 'worried', revertMs: 6000 });
      }
      return;
    }
    const overdue = s.overdue || 0;
    if (lastOverdue !== null && overdue > lastOverdue && Date.now() - lastOverdueNagAt > 180000) {
      lastOverdueNagAt = Date.now();
      setMood('worried', { revertMs: 7000 });
      react(fill(pick(PHRASES.overdueNag), { n: overdue }), { mood: 'worried', revertMs: 7000 });
    }
    lastOverdue = overdue;
  }

  /* ---------- Клик по Асе ---------- */
  let lastClickAt = 0;
  stage.addEventListener('pointerdown', (e) => {
    e.stopPropagation();
    const now = Date.now();
    if (now - lastClickAt < 600) return;
    lastClickAt = now;
    chibiEvent('click');
    const roll = Math.random();
    if (roll < 0.4) {
      wave();
      say(pick(PHRASES.clickWave));
    } else if (roll < 0.65) {
      wink();
      say(pick(PHRASES.clickWink));
    } else {
      celebrate();
      say(pick(PHRASES.clickBounce), { mood: 'excited', revertMs: 3000 });
    }
  });

  /* ---------- Болтовня в простое ---------- */
  let lastTipIdx = -1;
  function scheduleTip() {
    setTimeout(() => {
      // Пауза затянулась: «угу» + совет. chibi-sounds сам проверит
      // активность (60с бездействия), кулдаун 30с и активность вкладки.
      if (!document.hidden && Date.now() - lastBubbleAt > 12000) {
        chibiEvent('idle');
        let idx;
        do { idx = Math.floor(Math.random() * PHRASES.tips.length); } while (idx === lastTipIdx);
        lastTipIdx = idx;
        say(PHRASES.tips[idx]);
      }
      scheduleTip();
    }, rand(45000, 90000));
  }

  /* ---------- Спрятать модалку — Ася отходит ---------- */
  const modal = document.getElementById('modal-backdrop');
  if (modal) {
    new MutationObserver(() => {
      const open = !modal.classList.contains('hidden');
      if (open) container.classList.add('tf-aside');
      else setTimeout(() => {
        if (modal.classList.contains('hidden')) container.classList.remove('tf-aside');
      }, 150);
    }).observe(modal, { attributes: true, attributeFilter: ['class'] });
  }

  /* ---------- Свернуть / вернуть ---------- */
  const restore = document.createElement('button');
  restore.className = 'tf-restore hidden';
  restore.title = 'Вернуть Асю';
  restore.textContent = '🎀';
  restore.addEventListener('click', () => {
    localStorage.setItem(STORAGE_KEY, 'on');
    restore.classList.add('hidden');
    container.classList.remove('tf-off');
    document.body.classList.add('tf-assist-on');
    document.body.classList.remove('tf-assist-off');
    chibiEvent('appear'); // клик по кнопке — жест есть, звук разрешён
    say('Я тут! Скучала 🎀');
  });
  document.body.appendChild(restore);

  container.querySelector('.tf-hide-btn').addEventListener('click', (e) => {
    e.stopPropagation();
    localStorage.setItem(STORAGE_KEY, 'off');
    container.classList.add('tf-off');
    restore.classList.remove('hidden');
    document.body.classList.remove('tf-assist-on');
    document.body.classList.add('tf-assist-off');
  });

  function showOff() {
    container.classList.remove('tf-off');
    document.body.classList.add('tf-assist-on');
    document.body.classList.remove('tf-assist-off');
  }

  /* ---------- Запуск ---------- */
  function init() {
    showOff();
    setTimeout(async () => {
      wave();
      chibiEvent('appear'); // при загрузке страницы жеста ещё не было — пул молчит
      const s = await fetchStats();
      let hello = PHRASES.intro + ' ' + PHRASES.greet(new Date().getHours());
      if (s) {
        if (s.overdue > 0) hello += ` Кстати, просроченных: ${s.overdue} — не откладывай 😟`;
        else if (s.done_today > 0) hello += ` Сегодня уже закрыто ${s.done_today} 💪`;
      }
      say(hello, { mood: 'happy' });
      checkStats(s);
      setInterval(() => checkStats(fetchStats()), 60000);
      scheduleTip();
    }, 1100);
  }

  if (localStorage.getItem(STORAGE_KEY) === 'off') {
    restore.classList.remove('hidden');
    document.body.classList.add('tf-assist-off');
  } else {
    init();
  }

  // Для отладки и внешних хуков.
  window.tfAssistant = { say, celebrate, wave, wink, bounce };
})();
