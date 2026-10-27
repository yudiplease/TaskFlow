/* Сервис звуков чиби-персонажа (Ася).
   Пришёл на смену диктору (TTS): вместо синтеза речи — короткие милые
   звуки «няя», «хи-хи», «угу». Автономный модуль без зависимостей.
   Файлы — web/static/sounds/chibi/*.mp3 (тихие заглушки, пользователь
   кладёт свои; отсутствующий файл = тихий warning в консоль, без падений).
   Настройки пользователя — в localStorage под ключом chibi.sound.settings,
   применяются моментально через updateSettings(). */
(() => {
  'use strict';

  const SETTINGS_KEY = 'chibi.sound.settings';
  const DEFAULT_SETTINGS = { enabled: true, volume: 0.6, clickOnly: false };

  /* Маппинг событий приложения → категория звука.
     cooldownMs — минимальная пауза между срабатываниями одного события,
     чтобы при быстрых кликах не было «пулемёта». */
  const CHIBI_SOUND_MAP = {
    click:   { category: 'giggle', cooldownMs: 500 },
    success: { category: 'hum',    cooldownMs: 0 },
    error:   { category: 'nya',    cooldownMs: 1000 },
    appear:  { category: 'nya',    cooldownMs: 0 },
    idle:    { category: 'hum',    cooldownMs: 30000 },
  };

  // «Фоновые» события: глохнут, когда включён режим «Только по клику».
  const BACKGROUND_EVENTS = ['appear', 'idle'];

  // Пул файлов по категориям (все < 2 сек, MP3; OGG-fallback не нужен).
  // Добавить вариант — добавить файл и путь сюда.
  const SOUND_FILES = {
    nya:    ['/sounds/chibi/nya-01.mp3', '/sounds/chibi/nya-02.mp3'],
    giggle: ['/sounds/chibi/giggle-01.mp3', '/sounds/chibi/giggle-02.mp3'],
    hum:    ['/sounds/chibi/hum-01.mp3'],
    squee:  ['/sounds/chibi/squee-01.mp3'],
  };

  // Событие idle звучит, только если пользователь столько миллисекунд
  // не взаимодействовал со страницей (и вкладка активна).
  const IDLE_AFTER_MS = 60000;

  const clampVolume = (v) => Math.min(1, Math.max(0, v));

  function loadSettings(key) {
    try {
      const raw = JSON.parse(localStorage.getItem(key) || '{}');
      return {
        enabled: typeof raw.enabled === 'boolean' ? raw.enabled : DEFAULT_SETTINGS.enabled,
        volume: Number.isFinite(raw.volume) ? clampVolume(raw.volume) : DEFAULT_SETTINGS.volume,
        clickOnly: typeof raw.clickOnly === 'boolean' ? raw.clickOnly : DEFAULT_SETTINGS.clickOnly,
      };
    } catch {
      return { ...DEFAULT_SETTINGS };
    }
  }

  class ChibiSoundPlayer {
    constructor({ files = SOUND_FILES, settingsKey = SETTINGS_KEY } = {}) {
      this.settingsKey = settingsKey;
      this.settings = loadSettings(settingsKey);
      this.files = files;
      this.pool = {};          // category -> Array<{ audio, ok }>
      this.current = null;     // Audio, который играет прямо сейчас
      this.lastPlayedAt = {};  // event -> ts последнего срабатывания (кулдауны)
      this.lastActivityAt = Date.now();
      this.unlocked = false;   // autoplay policy: до первого жеста молчим
      this._buildPool();
      this._listenUserActivity();
    }

    /* ---------- Пул ---------- */

    // Пул создаётся один раз на старте: никаких new Audio() на каждый клик.
    _buildPool() {
      for (const [category, paths] of Object.entries(this.files)) {
        this.pool[category] = paths.map((path) => {
          const audio = new Audio();
          audio.preload = 'auto';
          audio.volume = this.settings.volume;
          audio.src = path;
          audio.load();
          const entry = { audio, ok: true };
          audio.addEventListener('error', () => {
            entry.ok = false;
            console.warn(`[chibi-sounds] не загрузился ${path} — категория «${category}» может молчать`);
          });
          return entry;
        });
      }
    }

    _listenUserActivity() {
      // Первый жест разблокирует звук (autoplay policy), дальше просто
      // отслеживаем активность для idle-события.
      const mark = () => {
        this.unlocked = true;
        this.lastActivityAt = Date.now();
      };
      for (const type of ['pointerdown', 'keydown']) {
        window.addEventListener(type, mark, { capture: true, passive: true });
      }
    }

    /* ---------- Настройки ---------- */

    getSettings() {
      return { ...this.settings };
    }

    setEnabled(on) { this.updateSettings({ enabled: !!on }); }

    setVolume(v) { this.updateSettings({ volume: clampVolume(Number(v)) }); }

    setClickOnly(on) { this.updateSettings({ clickOnly: !!on }); }

    // Применяет патч моментально (громкость пула, глушение играющего)
    // и persist-ит в localStorage.
    updateSettings(patch) {
      const next = { ...this.settings, ...patch };
      if (typeof next.enabled !== 'boolean') next.enabled = DEFAULT_SETTINGS.enabled;
      next.volume = Number.isFinite(Number(next.volume))
        ? clampVolume(Number(next.volume))
        : DEFAULT_SETTINGS.volume;
      if (typeof next.clickOnly !== 'boolean') next.clickOnly = DEFAULT_SETTINGS.clickOnly;
      this.settings = next;
      try {
        localStorage.setItem(this.settingsKey, JSON.stringify(next));
      } catch { /* приватный режим — работаем без persist */ }
      for (const entries of Object.values(this.pool)) {
        for (const { audio } of entries) audio.volume = next.volume;
      }
      if (!next.enabled) this.stop();
      document.dispatchEvent(new CustomEvent('tf-chibi-settings', { detail: this.getSettings() }));
    }

    /* ---------- Проигрывание ---------- */

    // Стратегия наложения: ПРЕРЫВАТЬ. Что-то уже играет — глушим и
    // запускаем новое: персонаж реагирует на самое свежее событие.
    // (Альтернатива — игнорировать новый звук, пока играет старый.)
    stop() {
      if (!this.current) return;
      try {
        this.current.pause();
        this.current.currentTime = 0;
      } catch { /* ignore */ }
      this.current = null;
    }

    // Играет случайный вариант категории из пула. true — если звук пошёл.
    playRandom(category) {
      if (!this.unlocked) return false;          // autoplay policy
      if (!this.settings.enabled) return false;  // выключено в настройках
      const entries = (this.pool[category] || []).filter((e) => e.ok);
      if (!entries.length) return false;         // fallback: пустой пул — тихо
      // Случайный вариант; если выпал играющий и есть альтернативы — соседний.
      let entry = entries[Math.floor(Math.random() * entries.length)];
      if (entries.length > 1 && entry.audio === this.current) {
        entry = entries[(entries.indexOf(entry) + 1) % entries.length];
      }
      this.stop();
      const { audio } = entry;
      try {
        audio.currentTime = 0;
        audio.volume = this.settings.volume;
      } catch { /* ignore */ }
      const p = audio.play();
      if (p && typeof p.catch === 'function') {
        p.catch(() => { /* autoplay/декодирование — тихо игнорируем */ });
      }
      this.current = audio;
      return true;
    }

    play(category) {
      return this.playRandom(category);
    }

    /* ---------- События с кулдаунами ---------- */

    // Проигрывает событие по CHIBI_SOUND_MAP: кулдаун на «пулемёт»,
    // режим «Только по клику» глушит фоновые события,
    // idle требует активной вкладки и долгого бездействия.
    playEvent(eventName) {
      const conf = CHIBI_SOUND_MAP[eventName];
      if (!conf) return false;
      if (this.settings.clickOnly && BACKGROUND_EVENTS.includes(eventName)) return false;

      const now = Date.now();
      if (conf.cooldownMs > 0 && now - (this.lastPlayedAt[eventName] || 0) < conf.cooldownMs) {
        return false;
      }
      if (eventName === 'idle') {
        if (document.visibilityState !== 'visible') return false;
        if (now - this.lastActivityAt < IDLE_AFTER_MS) return false;
      }
      const played = this.play(conf.category);
      if (played) this.lastPlayedAt[eventName] = now;
      return played;
    }
  }

  const chibiSounds = new ChibiSoundPlayer();

  // Для настроек (app.js) и внешних хуков.
  window.chibiSounds = chibiSounds;
  window.ChibiSoundPlayer = ChibiSoundPlayer;
  window.ChibiSoundMap = CHIBI_SOUND_MAP;
})();
