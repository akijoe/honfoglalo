// A Flash-játék (swf/testclient.swf) és az oldal közti híd.
//   hfSound(...)   – a játék hangjai (_global.gsound -> sounds/manifest.json)
//   hfNet(...)     – szobák, többjátékos döntések, visszajelzés (a Go szerver /api/ végpontjai)
// A Flash ExternalInterface-szel hívja őket; a választ mindig azonnal kapja meg (a hálózati
// válaszok eseményként jönnek: hfNet('poll')).
'use strict';

(function () {
  const params = new URLSearchParams(location.search);
  const gamemode = params.get('gamemode');
  const base = location.href.replace(/[^/]*([?#].*)?$/, '');
  const joinCode = (params.get('join') || '').toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 8);
  // a többjátékos szerver címe (js/config.js); üres = ugyanaz a szerver, amelyik az oldalt adja
  const API = ((window.HF_CONFIG && window.HF_CONFIG.api) || '').replace(/\/?$/, m => (m || '/'));
  const apiURL = path => (API === '/' ? '' : API) + 'api/' + path;
  // A szoba-munkamenet ({code, seat, token}) a lapé (sessionStorage: frissítéskor megmarad, és egy
  // másik lap szobája nem keveredik bele), a böngésző utolsó szobája pedig a localStorage-ban is
  // megvan, hogy a lap bezárása után is vissza lehessen lépni. A playingKey jelzi, hogy ez a lap a
  // játékban volt (frissítéskor rögtön visszamegy).
  // teszteléshez: ?slot=x -> külön munkamenet, így egy lapon (iframe-ekben) több játékos is lehet
  const slot = (params.get('slot') || '').replace(/[^a-z0-9]/gi, '').slice(0, 8);
  const sessionKey = 'hfSession' + slot;      // localStorage: a böngésző utolsó szobája
  const tabKey = 'hfTabSession' + slot;       // sessionStorage: ennek a lapnak a szobája
  const playingKey = 'hfPlaying' + slot;

  function store(kind) {
    try { return window[kind]; } catch (e) { return null; }
  }
  function load(kind, key) {
    try { return JSON.parse(store(kind).getItem(key)); } catch (e) { return null; }
  }
  function save(kind, key, val) {
    try {
      if (val === null) store(kind).removeItem(key);
      else store(kind).setItem(key, JSON.stringify(val));
    } catch (e) { /* privát mód: nem baj */ }
  }

  // ---------------------------------------------------------------------------- hang

  // A hangokat a Web Audio játssza le: minden fájlt egyszer letöltünk és előre dekódolunk, így a
  // lejátszás azonnal, pontosan akkor indul, amikor a játék kéri, és a hangok egymásra lóghatnak.
  // (Egy új <audio> elem minden lejátszáskor újra lekérte a fájlt, és változó késéssel szólalt meg.)
  // Amíg egy fájl még nincs kész, vagy a böngésző nem tud Web Audiót, marad az <audio>.
  const savedVolume = Number(load('localStorage', 'hfVolume'));
  const sound = { files: {}, buffers: {}, loading: {}, playing: {}, last: {}, music: null, ctx: null, gain: null,
    volume: load('localStorage', 'hfVolume') === null ? 0.8 : savedVolume };
  const AudioCtx = window.AudioContext || window.webkitAudioContext;
  function audio() {
    if (!AudioCtx) return null;
    if (!sound.ctx) {
      try {
        sound.ctx = new AudioCtx();
        sound.gain = sound.ctx.createGain();
        sound.gain.gain.value = sound.volume;
        sound.gain.connect(sound.ctx.destination);
      } catch (e) { sound.ctx = null; return null; }
    }
    if (sound.ctx.state === 'suspended') sound.ctx.resume().catch(() => {});
    return sound.ctx;
  }
  // a böngésző csak egy kattintás / billentyű után enged hangot
  for (const ev of ['pointerdown', 'keydown', 'touchend']) {
    window.addEventListener(ev, () => { audio(); }, { capture: true, passive: true });
  }
  function preload(file) {
    const ctx = audio();
    if (!ctx || sound.buffers[file] || sound.loading[file]) return;
    sound.loading[file] = fetch('sounds/' + file)
      .then(r => r.arrayBuffer())
      .then(data => new Promise((ok, fail) => ctx.decodeAudioData(data, ok, fail)))
      .then(buf => { sound.buffers[file] = buf; })
      .catch(() => {});
  }
  fetch('sounds/manifest.json').then(r => r.ok ? r.json() : {}).then(m => {
    sound.files = m || {};
    for (const name in sound.files) preload(sound.files[name]);
  }).catch(() => {});

  if (params.has('debug')) window.hfSoundState = sound;   // hibakereséshez

  function track(name, item) {
    (sound.playing[name] = sound.playing[name] || []).push(item);
  }
  function untrack(name, item) {
    sound.playing[name] = (sound.playing[name] || []).filter(x => x !== item);
  }
  function stopAll(name) {
    for (const item of sound.playing[name] || []) item.stop();
    sound.playing[name] = [];
  }
  // delay: ennyi ms múlva szóljon (a játék előre időzített hangjai, pl. a tippek felfedése)
  function start(name, loop, delay) {
    const file = sound.files[name];
    if (!file) return;                                  // ehhez az eseményhez nincs hang
    // a játék néha ugyanabban a pillanatban többször kéri ugyanazt (pl. tippdobozonként): egyszer szóljon
    delay = Math.max(0, Number(delay) || 0);
    const when = performance.now() + delay;
    if (sound.last[name] && Math.abs(when - sound.last[name]) < 80) return;
    sound.last[name] = when;
    const ctx = audio();
    const buf = sound.buffers[file];
    if (ctx && buf && ctx.state === 'running') {
      const src = ctx.createBufferSource();
      src.buffer = buf;
      src.loop = loop;
      src.connect(sound.gain);
      const item = { stop: () => { try { src.stop(); } catch (e) { /* már leállt */ } } };
      src.onended = () => untrack(name, item);
      track(name, item);
      src.start(ctx.currentTime + delay / 1000);
      return;
    }
    if (delay > 0) {
      setTimeout(() => { sound.last[name] = 0; start(name, loop, 0); }, delay);
      return;
    }
    preload(file);
    const a = new Audio('sounds/' + file);
    a.loop = loop;
    a.volume = sound.volume;
    const item = { audio: a, stop: () => { a.pause(); a.currentTime = 0; } };
    a.addEventListener('ended', () => untrack(name, item));
    track(name, item);
    a.play().catch(() => {});
  }
  function setVolume(v) {
    sound.volume = Math.max(0, Math.min(1, Number(v) || 0));
    if (sound.gain) sound.gain.gain.value = sound.volume;
    for (const name in sound.playing) {
      for (const item of sound.playing[name]) if (item.audio) item.audio.volume = sound.volume;
    }
    save('localStorage', 'hfVolume', sound.volume);
  }
  window.hfSound = function (cmd, name, arg) {
    try {
      if (cmd === 'getVolume') return sound.volume;          // a játék hangerő-csúszkája (src/lobby.as)
      if (cmd === 'volume') setVolume(name);
      else if (cmd === 'play') start(name, Number(arg) > 1);  // gsound.play(név, ismétlés): tiktak 30, AddLoopSound 100
      else if (cmd === 'playAt') start(name, false, arg);     // gsound.playAt(név, ms): pontosan ms múlva
      else if (cmd === 'stop') stopAll(name);
      else if (cmd === 'changeMusic') { if (sound.music) stopAll(sound.music); sound.music = name; start(name, true); }
      else if (cmd === 'stopMusic') { if (sound.music) stopAll(sound.music); sound.music = null; }
    } catch (e) { /* a hang soha ne akassza meg a játékot */ }
  };

  // ---------------------------------------------------------------------------- látott kérdések

  // A böngésző emlékszik a legutóbb látott kérdésekre (src/questions.as), hogy a következő
  // játékokban előbb a még nem látottak jöjjenek. kind: 'mc' | 'tip', key: a kérdés rövid kulcsa.
  window.hfSeen = function (cmd, kind, key, cap) {
    try {
      const all = load('localStorage', 'hfSeen') || {};
      const list = Array.isArray(all[kind]) ? all[kind] : [];
      if (cmd === 'get') return list;
      if (cmd === 'add') {
        const k = String(key);
        const i = list.indexOf(k);
        if (i >= 0) list.splice(i, 1);
        list.push(k);
        while (list.length > Math.max(0, Number(cap) || 0)) list.shift();
        all[kind] = list;
        save('localStorage', 'hfSeen', all);
      }
    } catch (e) { /* tároló nélkül is megy, csak nem emlékszik */ }
    return null;
  };

  // ---------------------------------------------------------------------------- szövegbevitel érintőképernyőn

  // Telefonon és tableten a Flash kijelölt beviteli mezője (a tippdoboz, a név, a szobakód) helyére
  // egy valódi mező kerül (src/lobby.as: hf.touchText): a tippnél számbillentyűzettel, és nem tűnik
  // el gépelés közben, mint a Ruffle rejtett mezője iOS-en. ?touchtext: kipróbáláshoz asztali gépen.
  const touchText = params.has('touchtext') || !!(window.matchMedia && window.matchMedia('(pointer: coarse)').matches);
  const text = { el: null, box: null };

  function flashCall(fn, arg) {
    try { if (player && typeof player[fn] === 'function') return player[fn](arg); } catch (e) { /* a SWF még tölt */ }
    return null;
  }

  const cssColor = n => '#' + ('00000' + (Number(n) >>> 0).toString(16)).slice(-6);

  // a mező helye: a SWF színpada 600×500, a lejátszó ezt arányosan nagyítja
  function placeText() {
    const b = text.box;
    const el = text.el;
    if (!b || !el || !player) return;
    const r = player.getBoundingClientRect();
    const s = r.width / 600;
    const font = Math.max(1, b.size * s);
    const z = Math.max(1, 16 / font);       // 16 px alatti betűnél az iOS ránagyítana az oldalra
    Object.assign(el.style, {
      left: r.left + b.x * s + 'px', top: r.top + b.y * s + 'px',
      width: b.w * s * z + 'px', height: b.h * s * z + 'px',
      fontSize: font * z + 'px', transform: 'scale(' + 1 / z + ')',
    });
  }

  function openText(b) {
    closeText();
    const el = document.createElement(b.multiline ? 'textarea' : 'input');
    el.className = 'hftext';
    if (!b.multiline) el.type = 'text';
    if (b.numeric) {
      el.inputMode = 'numeric';
      el.pattern = '[0-9]*';
    }
    el.setAttribute('autocomplete', 'off');
    el.setAttribute('autocorrect', 'off');
    el.setAttribute('autocapitalize', b.numeric ? 'off' : /A-Z/.test(b.restrict) ? 'characters' : b.multiline ? 'sentences' : 'words');
    el.setAttribute('enterkeyhint', b.multiline ? 'enter' : b.numeric ? 'send' : 'go');
    el.spellcheck = b.multiline;
    if (b.maxChars > 0) el.maxLength = b.maxChars;
    el.value = b.value || '';
    Object.assign(el.style, {
      color: cssColor(b.color), background: b.background >= 0 ? cssColor(b.background) : 'transparent',
      textAlign: ['left', 'center', 'right'].includes(b.align) ? b.align : 'left',
      fontWeight: b.bold ? 'bold' : 'normal',
    });
    // a Flash restrict-je (pl. "0123456789", "A-Za-z0-9"): a többi karakter kimarad
    const bad = /^[A-Za-z0-9-]+$/.test(b.restrict || '') ? new RegExp('[^' + b.restrict + ']', 'g') : null;
    el.addEventListener('input', () => {
      let v = el.value;
      if (bad) v = v.replace(bad, '');
      if (b.maxChars > 0) v = v.slice(0, b.maxChars);
      if (v !== el.value) el.value = v;
      flashCall('hfTextSet', v);
    });
    // a billentyűk ne jussanak el a Ruffle-ig (az ablakon figyel, és pl. a Backspace-t letiltaná)
    for (const type of ['keydown', 'keyup', 'keypress', 'paste']) {
      el.addEventListener(type, e => e.stopPropagation());
    }
    el.addEventListener('keydown', e => {
      if (e.key === 'Enter' && !b.multiline) {
        e.preventDefault();
        flashCall('hfTextEnter');
      }
    });
    document.body.appendChild(el);
    text.el = el;
    text.box = b;
    placeText();
    document.body.classList.add('typing');
    showMaps(false);
    el.focus({ preventScroll: true });
  }

  function closeText() {
    if (text.el) {
      text.el.blur();
      text.el.remove();
    }
    text.el = null;
    text.box = null;
    document.body.classList.remove('typing');
  }

  window.hfText = function (cmd, b) {
    try {
      if (cmd === 'touch') return touchText;
      if (cmd === 'open' && b) openText(b);
      else if (cmd === 'close') closeText();
    } catch (e) { /* a Flash ne kapjon kivételt */ }
    return null;
  };
  window.addEventListener('resize', placeText);
  if (window.visualViewport) window.visualViewport.addEventListener('resize', placeText);

  // ---------------------------------------------------------------------------- térképek

  // A SWF 13 országtérképe (MAP_XX). Egyjátékos módban a böngésző emlékszik a választásra; a
  // szobában a főnök választ (a szerver a szoba állapotában küldi), a játék alatt nem váltható.
  const MAPS = [
    ['HU', 'Magyarország'], ['FR', 'Franciaország'], ['DE', 'Németország'], ['IT', 'Olaszország'],
    ['BE', 'Belgium'], ['CZ', 'Csehország'], ['SK', 'Szlovákia'], ['SI', 'Szlovénia'],
    ['RO', 'Románia'], ['RS', 'Szerbia'], ['BG', 'Bulgária'], ['UA', 'Ukrajna'], ['RU', 'Oroszország'],
  ];
  const mapName = id => (MAPS.find(m => m[0] === id) || MAPS[0])[1];
  const cleanMap = id => (MAPS.find(m => m[0] === String(id || '').toUpperCase()) || [''])[0];
  // ?map=XX: csak erre a betöltésre (kipróbáláshoz)
  const myMap = () => cleanMap(params.get('map')) || cleanMap(load('localStorage', 'hfMap')) || 'HU';
  // Hosszú hadjárat: 6 harci kör a 4 helyett (src/tweaks.as). A térképhez hasonlóan egyjátékos
  // módban a böngésző emlékszik rá, a szobában a főnök választja. ?long=1 / ?long=0: csak most.
  const myLong = () => (params.has('long') ? params.get('long') === '1' : load('localStorage', 'hfLong') === true);
  const longVar = on => (on ? '1' : '0');
  // a szoba állapota a Flash-nek: a térkép magyar nevével (src/lobby.as: szoba-ablak)
  const flashState = st => st && Object.assign({ mapName: mapName(st.map) }, st);

  // A főoldal ezt hívta a menüválasztáskor (konverziómérés) – itt nincs teendő.
  window.ConvRateSignal = function () {};

  // ---------------------------------------------------------------------------- hálózat

  const net = {
    session: null,        // {code, seat, token} (boot)
    es: null,
    state: null,          // a szoba utolsó állapota
    decs: [],             // a játék döntései, sorrendben (kulcsonként egy)
    seenKeys: new Set(),
    events: [],           // a Flash következő hfNet('poll')-jának
    sendChain: Promise.resolve(),
    inGame: false,
    sawLobby: false,      // ebben a lapban láttuk a szobát indulás előtt (az indulás után rögtön játék)
    heard: 0,             // az eseményfolyam utolsó üzenetének ideje (a szerver 15 mp-enként pingel)
  };

  function emit(ev) {
    net.events.push(ev);
    if (net.events.length > 500) net.events.splice(0, net.events.length - 500);
  }

  async function api(path, body) {
    let res;
    // egy beragadt kérés (pl. alvó Wi-Fi) ne tartsa fel örökre a döntések sorát (sendDecision)
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), 15000);
    try {
      res = await fetch(apiURL(path), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        signal: ctl.signal,
      });
    } catch (e) {
      throw new Error('Nem érem el a szervert.');
    } finally {
      clearTimeout(timer);
    }
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      const err = new Error(data.error || 'Hiba történt (' + res.status + ').');
      err.status = res.status;
      throw err;
    }
    return data;
  }

  function link() {
    return net.session ? base + '?join=' + net.session.code : '';
  }

  function connect() {
    disconnect();
    const s = net.session;
    if (!s) return;
    const es = new EventSource(apiURL('events') + '?code=' + encodeURIComponent(s.code) + '&token=' + encodeURIComponent(s.token));
    net.es = es;
    net.heard = Date.now();
    es.onmessage = function (e) {
      net.heard = Date.now();
      let ev;
      try { ev = JSON.parse(e.data); } catch (err) { return; }
      if (ev.type === 'state') onState(ev);
      else if (ev.type === 'chat') onChat(ev);
      else if (ev.type === 'closed') lost(ev.msg || 'A szoba megszűnt.');
      else if (ev.type === 'dec' && !net.seenKeys.has(ev.k)) {
        net.seenKeys.add(ev.k);
        net.decs.push({ k: ev.k, from: ev.from, d: ev.d });
      }
    };
    es.onerror = function () {
      // Hálózati hibánál az EventSource magától újracsatlakozik. Ha feladja (CLOSED: a szerver
      // hibát adott, vagy épp bezárul az oldal), megkérdezzük a szervert, élünk-e még a szobában.
      if (es.readyState === EventSource.CLOSED && net.es === es && !net.unloading) {
        net.es = null;
        setTimeout(recheck, 2000);
      }
    };
  }

  function recheck() {
    const s = net.session;
    if (!s || net.es || net.unloading) return;
    api('check', { code: s.code, token: s.token }).then(connect, e => {
      if (e.status === 403 || e.status === 404) lost(e.message);
      else setTimeout(recheck, 5000);
    });
  }

  // Egy csendben elhalt kapcsolaton (alvó telefon, Wi-Fi-váltás) az EventSource nyitottnak látszik,
  // de semmi sem jön rajta: a játék a következő döntésre várna örökké (nem jönne fel a kérdés). A
  // szerver 15 mp-enként pingel; ha 35 mp-ig semmi sem jött, újracsatlakozunk (a szerver az összes
  // döntést újraküldi, a már megvoltakat a seenKeys kiszűri). A lap előtérbe kerülésekor is megnézzük.
  function watchStream() {
    if (net.es && net.es.readyState !== EventSource.CLOSED && Date.now() - net.heard > 35000) connect();
  }
  setInterval(watchStream, 5000);
  document.addEventListener('visibilitychange', () => { if (!document.hidden) watchStream(); });
  // a háttérben megállt játék visszatéréskor ne játssza végig a lemaradt animációkat (src/mp.as: resume)
  document.addEventListener('visibilitychange', () => { if (!document.hidden && net.inGame) flashCall('hfResume'); });
  window.addEventListener('online', () => { if (net.es) connect(); });

  window.addEventListener('pagehide', () => { net.unloading = true; });
  window.addEventListener('pageshow', () => { net.unloading = false; });

  function disconnect() {
    if (net.es) { net.es.close(); net.es = null; }
  }

  function remember(s) {
    save('sessionStorage', tabKey, s);
    save('localStorage', sessionKey, s);
  }

  function forget() {
    disconnect();
    const last = load('localStorage', sessionKey);
    if (net.session && last && last.token === net.session.token) save('localStorage', sessionKey, null);
    net.session = null;
    net.state = null;
    save('sessionStorage', tabKey, null);
    save('sessionStorage', playingKey, null);
    save('sessionStorage', chatOffKey, null);
    chatReset();
    setTimeout(hubPoll, 1200);
    renderMaps();
  }

  function lost(msg) {
    forget();
    emit({ type: 'closed', msg: msg });
  }

  function onState(ev) {
    const s = net.session;
    if (!s) return;
    const me = ev.players.find(p => p.seat === s.seat);
    if (!me || me.left) { lost('Már nem vagy a szobában.'); return; }
    // új játék indult a szobában: a döntések elölről (a kulcsok újra 0-tól jönnek; server: handleStart)
    if (net.state && ev.game !== net.state.game) {
      net.decs = [];
      net.seenKeys = new Set();
    }
    net.state = ev;
    emit({ type: 'state', state: flashState(ev) });
    renderMaps();
    if (!ev.started) net.sawLobby = true;
    // indulás: aki a szobában várt, vagy ebben a lapban már játszott (frissítés), rögtön a játékba
    // kerül; aki később nyitja meg az oldalt, a szoba-ablakban választhat (Visszalépés / Kilépés)
    const playingHere = load('sessionStorage', playingKey) === s.code;
    if (ev.started && !net.inGame && (net.sawLobby || playingHere || net.rejoining)) startGame();
  }

  function setSession(data) {
    net.session = { code: data.code, seat: data.seat, token: data.token };
    remember(net.session);
    save('sessionStorage', playingKey, null);
    net.state = null;
    net.decs = [];
    net.seenKeys = new Set();
    chatReset();
    setTimeout(hubPoll, 1200);              // a Várók lista is frissüljön (a szerver másodpercenként számolja)
    emit({ type: 'joined', code: data.code, seat: data.seat, link: link() });
    connect();
  }

  function sendDecision(k, d) {
    const s = net.session;
    if (!s) return;
    // sorban, egyenként, hogy a saját döntéseink sorrendje megmaradjon
    // Egy elveszett döntésre a játék örökké várna (a házigazda döntéseit senki más nem küldi el),
    // ezért amíg a szoba él, újra és újra próbálkozunk. A végleges hibát (hibás lépés, túl hosszú
    // játék, nem vagy a szobában) nem ismételjük: az a többi döntést tartaná fel.
    net.sendChain = net.sendChain.then(async () => {
      for (let i = 0; net.session === s; i++) {
        try { await api('send', { code: s.code, token: s.token, k: k, d: d }); return; } catch (e) {
          if (e.status && e.status !== 429 && e.status < 500) return;
          await new Promise(r => setTimeout(r, Math.min(1000 * (i + 1), 5000)));
        }
      }
    });
  }

  function copyText(text) {
    try {
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(text).then(() => emit({ type: 'copied', ok: true }), () => emit({ type: 'copied', ok: fallbackCopy(text) }));
        return true;
      }
    } catch (e) { /* lent */ }
    emit({ type: 'copied', ok: fallbackCopy(text) });
    return true;
  }
  function fallbackCopy(text) {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    let ok = false;
    try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
    ta.remove();
    return ok;
  }

  function call(promise, okType) {
    promise.then(data => { if (okType) emit(Object.assign({ type: okType }, data)); })
      .catch(e => emit({ type: 'error', msg: e.message }));
  }

  window.hfNet = function (cmd, a, b) {
    try {
      switch (cmd) {
        case 'hello':
          return {
            name: load('localStorage', 'hfName') || '',
            join: joinCode,
            inLobby: !!net.session && !net.inGame,
            started: !!(net.state && net.state.started),
            code: net.session ? net.session.code : '',
            link: link(),
            seat: net.session ? net.session.seat : 0,
          };
        case 'poll': {
          const out = net.events;
          net.events = [];
          return out;
        }
        case 'create':        // b: a publikus váró neve (üres: privát szoba)
          save('localStorage', 'hfName', String(a));
          call(api('create', { name: String(a), map: myMap(), long: myLong(), public: !!b, title: b ? String(b) : '', client: clientId })
            .then(setSession));
          return true;
        case 'join':
          save('localStorage', 'hfName', String(b));
          // ha a játék már megy (visszalépés a névvel), rögtön a játékba
          net.rejoining = true;
          call(api('join', { code: String(a), name: String(b) }).then(setSession, e => {
            net.rejoining = false;
            throw e;
          }));
          return true;
        case 'start':
          if (net.session) call(api('start', { code: net.session.code, token: net.session.token }));
          return true;
        case 'leave':
          if (net.session) {
            const s = net.session;
            api('leave', { code: s.code, token: s.token }).catch(() => {});
          }
          forget();
          return true;
        case 'again':         // a játék végén: vissza a szobába
          if (net.session) {
            const s = net.session;
            api('again', { code: s.code, token: s.token }).then(backToRoom, e => emit({ type: 'error', msg: e.message }));
          }
          return true;
        case 'exit': {        // a játék végén: kilépés a szobából, a főoldalra (a kilépés megvárva)
          const s = net.session;
          const home = () => { location.href = location.pathname + (slot ? '?slot=' + slot : ''); };
          forget();
          if (!s) { home(); return true; }
          Promise.race([api('leave', { code: s.code, token: s.token }), new Promise(r => setTimeout(r, 1500))])
            .catch(() => {}).then(home);
          return true;
        }
        case 'rejoin':        // visszalépés egy folyamatban lévő játékba (az első állapot után indul)
          if (net.session && !net.inGame) {
            net.rejoining = true;
            connect();
          }
          return true;
        case 'state':
          return flashState(net.state);
        case 'copy':
          return copyText(link());
        case 'feedback':
          if (b) save('localStorage', 'hfName', String(b));
          call(api('feedback', { text: String(a), name: String(b || '') }), 'feedback');
          return true;
        // ---- a többjátékos játék közben
        case 'game':          // {seat, state}: indításkor
          return net.session ? { seat: net.session.seat, state: flashState(net.state) } : null;
        case 'decs':          // a döntések a-adiktól
          return net.decs.slice(Number(a) || 0);
        case 'send':          // döntés: k kulcs, b = {v: ..., ms: ...}
          sendDecision(Number(a), b);
          return true;
      }
    } catch (e) { /* a Flash ne kapjon kivételt */ }
    return null;
  };

  // ---------------------------------------------------------------------------- Ruffle

  window.RufflePlayer = window.RufflePlayer || {};
  window.RufflePlayer.config = {
    publicPath: 'vendor/ruffle/',
    autoplay: 'on',
    unmuteOverlay: 'hidden',
    splashScreen: false,
    letterbox: 'on',
    contextMenu: 'rightClickOnly',
    warnOnUnsupportedContent: false,
    logLevel: params.has('debug') ? 'info' : 'warn',
    polyfills: false,
  };

  let player = null;
  let swfLoaded = false;
  function loadSwf(flashvars) {
    swfLoaded = true;
    // Minden betöltéshez új lejátszó: ha a régibe töltenénk (a szobából indul a játék), annak a
    // fókusza megmaradna, de az új SWF nem kapná meg, így a billentyűzet nem jutna el hozzá.
    closeText();
    if (player) player.remove();
    player = window.RufflePlayer.newest().createPlayer();
    document.getElementById('game').appendChild(player);
    if (touchText) {
      // érintőképernyőn a beviteli mezőket az oldal adja (lent): a Ruffle rejtett mezője ne vegye el
      // a fókuszt (az hozná elő a betűs billentyűzetet)
      const vk = player.shadowRoot && player.shadowRoot.getElementById('virtual-keyboard');
      if (vk) vk.focus = () => {};
    }
    player.ruffle().load({
      url: 'swf/testclient.swf',
      base: base,
      // Az eredeti beágyazás paraméterei (honfoglalo.hu, 2013): questionxml, langid=hu, invitation=1.
      parameters: Object.assign({ questionxml: 'questions.xml', langid: 'hu' },
        params.has('debugclock') ? { debugclock: params.get('debugclock') } : {},
        params.has('debug') ? { debug: '1' } : {}, flashvars),
      allowScriptAccess: true,
      quality: 'high',
    });
  }

  // a játék végén "Vissza a váróba" (src/lobby.as: eredményablak): ha közben már új játék indult,
  // rögtön abba; különben a szoba-ablak, ahol a főnök újat indíthat
  function backToRoom() {
    const st = net.state;
    if (st && st.started && st.game !== net.playing) { startGame(); return; }
    net.inGame = false;
    net.sawLobby = true;
    save('sessionStorage', playingKey, null);
    loadSwf({ invitation: '1', map: myMap() });
    renderMaps();
  }

  function startGame() {
    net.inGame = true;
    net.playing = net.state ? net.state.game : 0;
    save('sessionStorage', playingKey, net.session.code);
    loadSwf({ invitation: '0', mp: '1', map: cleanMap(net.state && net.state.map) || 'HU', long: longVar(net.state && net.state.long) });
    renderMaps();
  }

  // ---------------------------------------------------------------------------- térképválasztó

  // A bal oldali sáv (index.html: #maps). Mikor mit lehet:
  //   többjátékos játék közben: semmit (a szoba térképe látszik)
  //   szobában, indulás előtt: csak a főnök választ, mindenki a szoba térképét látja
  //   különben (menü, bemutató, robotok): a saját választás; váltáskor a mostani mód újraindul
  function mapMode() {
    const st = net.state;
    if (net.inGame) return { current: cleanMap(st && st.map), long: !!(st && st.long), locked: 'Játék közben nem lehet térképet és szabályt váltani.' };
    if (net.session && st && !st.started) {
      if (st.boss === net.session.seat) return { current: cleanMap(st.map), long: !!st.long, room: true };
      const boss = st.players.find(p => p.seat === st.boss);
      return { current: cleanMap(st.map), long: !!st.long, locked: 'A térképet és a szabályt a szoba főnöke' + (boss ? ' (' + boss.name + ')' : '') + ' választja.' };
    }
    return { current: myMap(), long: myLong() };
  }

  function renderMaps() {
    const box = document.getElementById('maps');
    if (!box) return;
    const mode = mapMode();
    box.classList.toggle('locked', !!mode.locked);
    for (const b of box.querySelectorAll('.list button')) {
      const on = b.dataset.map === mode.current;
      b.classList.toggle('on', on);
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
      b.disabled = !!mode.locked;
    }
    for (const b of box.querySelectorAll('.rules button[data-long]')) {
      const on = (b.dataset.long === '1') === mode.long;
      b.classList.toggle('on', on);
      b.closest('.rule').classList.toggle('on', on);
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
      b.disabled = !!mode.locked;
    }
    box.querySelector('.note').textContent = mode.locked || '';
    renderChat();
    renderRooms();
  }

  function goHome() {
    if (net.inGame) {
      if (!window.confirm('A játék folytatódik, a helyeden addig robot játszik. A szoba kódjával (' +
          net.session.code + ') a főoldalról bármikor visszaléphetsz. Kilépsz?')) return;
      save('sessionStorage', playingKey, null);   // a főoldal ne lépjen vissza magától
      disconnect();
    } else if (gamemode !== null && !window.confirm('Kilépsz a főoldalra? A mostani játék véget ér.')) {
      return;
    }
    location.href = location.pathname + (slot ? '?slot=' + slot : '');
  }

  function pickMap(id) {
    const mode = mapMode();
    if (mode.locked || id === mode.current) return;
    save('localStorage', 'hfMap', id);
    if (mode.room) {
      const s = net.session;
      api('map', { code: s.code, token: s.token, map: id }).catch(e => emit({ type: 'error', msg: e.message }));
      return;                 // a szoba új állapota frissíti a sávot
    }
    if (gamemode !== null && !window.confirm('A játék újraindul ' + mapName(id) + ' térképével. Mehet?')) {
      save('localStorage', 'hfMap', mode.current);
      return;
    }
    // újratöltés: a mostani mód az új térképpel
    const q = new URLSearchParams(location.search);
    q.delete('map');
    const url = location.pathname + (q.toString() ? '?' + q : '');
    if (url === location.pathname + location.search) location.reload();
    else location.href = url;
  }

  // A logó (img/logo*.gif, tools/make_gifs.py): mindig a főoldalra visz. Rámutatáskor egyszer
  // végigfut rajta egy halvány csillanás: a GIF egyszer játszik le, ezért minden rámutatáskor új
  // (blob) címmel töltjük be, hogy elölről induljon. A telefonos sávnak más a háttere: "-m" változat.
  function buildLogo() {
    const a = document.getElementById('logo');
    const img = a.querySelector('img');
    const phone = window.matchMedia('(max-width: 700px)');
    const variant = () => (phone.matches ? '-m' : '');
    const still = () => { img.src = 'img/logo' + variant() + '.gif'; };
    const shine = {};       // változat -> a csillanó GIF (Blob)
    let url = null;
    const load = v => shine[v] || (shine[v] = fetch('img/logo-shine' + v + '.gif').then(r => r.blob()));
    still();
    if (phone.addEventListener) phone.addEventListener('change', still);
    a.addEventListener('click', e => {
      e.preventDefault();
      goHome();
    });
    a.addEventListener('mouseenter', () => {
      load(variant()).then(blob => {
        const old = url;
        url = URL.createObjectURL(blob);
        img.src = url;
        if (old) URL.revokeObjectURL(old);
      }).catch(() => {});
    });
    setTimeout(() => { load(variant()).catch(() => {}); }, 3000);     // hogy az első rámutatás se késsen
  }

  // telefonon (keskeny képernyőn) a sáv behúzható: a "Menü" fül nyitja, mellé koppintva bezárul
  function showMaps(open) {
    if (!open) hideRuleTip();
    document.body.classList.toggle('maps-open', open);
    const t = document.getElementById('mapsToggle');
    if (t) t.setAttribute('aria-expanded', open ? 'true' : 'false');
  }

  // A szabályok magyarázata (index.html: .rule data-desc): asztali gépen az "i" jel fölé víve,
  // telefonon az "i" jelre koppintva látszik; az újabb koppintás vagy máshová koppintás bezárja.
  const ruleTip = { row: null, pinned: false };

  function showRuleTip(row, pinned) {
    const el = document.getElementById('ruleTip');
    const title = document.createElement('b');
    title.textContent = row.querySelector('button[data-long]').textContent + ':';
    el.replaceChildren(title, ' ' + row.dataset.desc);
    for (const i of document.querySelectorAll('#maps .rules .info')) {
      i.setAttribute('aria-expanded', i.closest('.rule') === row ? 'true' : 'false');
    }
    ruleTip.row = row;
    ruleTip.pinned = pinned;
    // a sáv mellé jobbra (telefonon a nyitott sáv mellé), ha ott nincs hely, a sor alá
    const vw = document.documentElement.clientWidth;
    const r = row.getBoundingClientRect();
    const side = document.getElementById('maps').getBoundingClientRect().right + 8;
    const beside = vw - side - 8 >= 150;
    el.style.width = (beside ? Math.min(260, vw - side - 8) : vw - 16) + 'px';
    el.hidden = false;
    const top = beside ? r.top : r.bottom + 6;
    el.style.left = (beside ? side : 8) + 'px';
    el.style.top = Math.max(8, Math.min(top, window.innerHeight - el.offsetHeight - 8)) + 'px';
  }

  function hideRuleTip() {
    const el = document.getElementById('ruleTip');
    if (el) el.hidden = true;
    for (const i of document.querySelectorAll('#maps .rules .info')) i.setAttribute('aria-expanded', 'false');
    ruleTip.row = null;
    ruleTip.pinned = false;
  }

  function buildRuleTips(box) {
    for (const row of box.querySelectorAll('.rule')) {
      const mode = row.querySelector('button[data-long]');
      const info = row.querySelector('.info');
      info.addEventListener('mouseenter', () => { if (!ruleTip.pinned) showRuleTip(row, false); });
      info.addEventListener('mouseleave', () => { if (!ruleTip.pinned) hideRuleTip(); });
      info.addEventListener('focus', () => { if (!ruleTip.pinned) showRuleTip(row, false); });
      info.addEventListener('blur', () => { if (!ruleTip.pinned) hideRuleTip(); });
      info.addEventListener('click', () => {
        if (ruleTip.pinned && ruleTip.row === row) hideRuleTip();
        else showRuleTip(row, true);
      });
      mode.addEventListener('click', () => {
        hideRuleTip();
        showMaps(false);
        pickLong(mode.dataset.long === '1');
      });
      row.addEventListener('click', e => { if (e.target === row) mode.click(); });   // a sor üres része
    }
    document.addEventListener('pointerdown', e => { if (!e.target.closest || !e.target.closest('#maps .rule')) hideRuleTip(); }, true);
    document.addEventListener('keydown', e => { if (e.key === 'Escape') hideRuleTip(); });
    box.addEventListener('scroll', hideRuleTip);
    window.addEventListener('resize', () => { if (ruleTip.row) showRuleTip(ruleTip.row, ruleTip.pinned); });
  }

  // a szabály a robotjátékban és a szobában számít (a bemutató mindig villámháború)
  function pickLong(on) {
    const mode = mapMode();
    if (mode.locked || on === mode.long) return;
    save('localStorage', 'hfLong', on);
    if (mode.room) {
      const s = net.session;
      api('long', { code: s.code, token: s.token, long: on }).catch(e => emit({ type: 'error', msg: e.message }));
      return;
    }
    if (gamemode !== '1') { renderMaps(); return; }
    if (!window.confirm('A játék újraindul ' + (on ? 'hosszú hadjárattal' : 'villámháborúval') + '. Mehet?')) {
      save('localStorage', 'hfLong', mode.long);
      return;
    }
    const q = new URLSearchParams(location.search);
    q.delete('long');
    const url = location.pathname + (q.toString() ? '?' + q : '');
    if (url === location.pathname + location.search) location.reload();
    else location.href = url;
  }

  function buildMaps() {
    const box = document.getElementById('maps');
    if (!box) return;
    const toggle = document.getElementById('mapsToggle');
    toggle.addEventListener('click', () => showMaps(!document.body.classList.contains('maps-open')));
    document.addEventListener('pointerdown', e => {
      if (!box.contains(e.target) && e.target !== toggle) showMaps(false);
    }, true);
    buildLogo();
    buildRuleTips(box);
    const list = box.querySelector('.list');
    for (const [id, name] of MAPS) {
      const b = document.createElement('button');
      b.type = 'button';
      b.dataset.map = id;
      b.title = name;
      const img = document.createElement('img');
      img.src = 'img/maps/' + id + '.png';
      img.alt = '';
      const label = document.createElement('span');
      label.textContent = name;
      b.append(img, label);
      b.addEventListener('click', () => { showMaps(false); pickMap(id); });
      list.appendChild(b);
    }
    renderMaps();
  }

  // ---------------------------------------------------------------------------- Várók

  // A Várók (index.html: #rooms): a publikus szobák, ahová kód nélkül be lehet lépni, és hányan vannak
  // most a honlapon. A lap pár másodpercenként lekérdezi (server/hub.go); nem tart nyitva kapcsolatot,
  // mert HTTP/1.1-en a böngésző egy szerverhez csak 6-ot enged, és a szobák eseményfolyamának kell a
  // hely. A böngésző azonosítójával több lap egy játékosnak számít.
  const clientId = (() => {
    let id = load('localStorage', 'hfClient');
    if (typeof id !== 'string' || !/^[a-z0-9]{8,40}$/.test(id)) {
      id = (Math.random().toString(36).slice(2) + Date.now().toString(36)).slice(0, 24);
      save('localStorage', 'hfClient', id);
    }
    return id;
  })();
  const hub = { data: null, down: false, timer: 0 };   // data: {online, v, lobbies}
  // a figura a "Játék robotokkal" gombjáról (az eredeti SWF 760-as alakja): teli a foglalt hely, üres a szabad
  const FIGURE = 'M13.75 -29.8Q19.8 -36 28.35 -36.35Q37 -36.7 42.75 -30.75Q48.45 -24.8 47.65 -16.25Q46.85 -7.9 40.3 -2.35' +
    'L36.95 0.05L51.2 1.55Q51.9 14.2 48.55 25.8Q45.2 37.45 38.3 46.1Q35.35 -16.35 35.3 46.1Q38.65 53.35 38 60.55L37.7 62.8' +
    'L6.2 61.3L6.2 60.55Q6.2 52.4 9.5 45.1Q11.75 -13.25 6.05 45Q3.45 40.6 1.95 35.7Q-2 22.6 1.95 6.1L2.2 5.05Q9.75 2.55 17.4 1.3' +
    'Q14.6 -0.05 12.4 -2.35Q7.2 -7.75 7.5 -15.65Q7.75 -23.75 13.75 -29.8Z';

  function seatIcons(taken) {
    const ns = 'http://www.w3.org/2000/svg';
    const box = document.createElement('span');
    box.className = 'seats';
    box.title = taken + ' játékos van bent (legfeljebb 3)';
    for (let i = 0; i < 3; i++) {
      const svg = document.createElementNS(ns, 'svg');
      svg.setAttribute('viewBox', '-2 -40 56 106');
      svg.setAttribute('aria-hidden', 'true');
      const p = document.createElementNS(ns, 'path');
      p.setAttribute('d', FIGURE);
      p.setAttribute('class', i < taken ? 'in' : 'free');
      svg.append(p);
      box.append(svg);
    }
    return box;
  }
  const ruleName = long => (long ? 'hosszú hadjárat' : 'villámháború');

  // lekérdezés: látható lapon 6, háttérben 20 másodpercenként
  function hubPoll() {
    clearTimeout(hub.timer);
    const ctl = new AbortController();
    const abort = setTimeout(() => ctl.abort(), 10000);
    // v: a nálunk lévő lista változata; ha nem változott, a szerver csak a létszámot küldi (same)
    const v = hub.data ? '&v=' + hub.data.v : '';
    fetch(apiURL('hub') + '?id=' + encodeURIComponent(clientId) + v, { cache: 'no-store', signal: ctl.signal })
      .then(r => (r.ok ? r.json() : Promise.reject(new Error(r.status))))
      .then(d => {
        hub.data = { online: d.online, v: d.v, lobbies: d.same && hub.data ? hub.data.lobbies : (d.lobbies || []) };
        hub.down = false;
      }, () => { hub.down = true; })
      .then(() => {
        clearTimeout(abort);
        renderRooms();
        hub.timer = setTimeout(hubPoll, document.hidden ? 20000 : 6000);
      });
  }
  document.addEventListener('visibilitychange', () => { if (!document.hidden) hubPoll(); });

  function renderRooms() {
    const list = document.getElementById('roomList');
    if (!list) return;
    const d = hub.data;
    document.getElementById('online').textContent = d ? d.online + ' játékos online' : '';
    const note = text => {
      const p = document.createElement('p');
      p.className = 'empty';
      p.textContent = text;
      list.replaceChildren(p);
    };
    if (!d) { note(hub.down ? 'A szerver most nem érhető el.' : 'Betöltés...'); return; }
    if (!d.lobbies.length) {
      note('Most nincs nyitott publikus váró. Nyiss egyet: „Játék ismerősökkel”, és jelöld be a „Publikus váró” négyzetet.');
      return;
    }
    const mine = net.session ? net.session.code : '';
    list.replaceChildren(...d.lobbies.map(r => {
      const el = document.createElement('div');
      el.className = 'room' + (r.code === mine ? ' here' : '');
      const line = (cls, text) => {
        const div = document.createElement('div');
        div.className = cls;
        div.textContent = text;
        return div;
      };
      const b = document.createElement('button');
      b.type = 'button';
      if (r.code === mine) {
        b.textContent = 'Itt vagy';
        b.disabled = true;
      } else {
        b.textContent = 'Belépés';
        b.disabled = net.inGame;
        if (net.inGame) b.title = 'Játék közben nem lehet másik szobába lépni.';
        b.addEventListener('click', () => joinPublic(r));
      }
      const who = line('rm', r.players.join(', '));
      who.prepend(seatIcons(r.players.length));
      el.append(line('rt', r.title), who, line('rm', mapName(r.map) + ', ' + ruleName(r.long)), b);
      return el;
    }));
  }

  // belépés egy publikus szobába a listából: a Flash ablaka csak a nevedet kérdezi (src/lobby.as)
  function joinPublic(r) {
    if (net.inGame) return;
    showRooms(false);
    const viaLink = () => { location.href = location.pathname + '?join=' + r.code + (slot ? '&slot=' + slot : ''); };
    if (gamemode !== null) {
      if (window.confirm('A mostani játék véget ér. Belépsz ide: ' + r.title + '?')) viaLink();
      return;
    }
    if (net.session) {
      if (!window.confirm('Kilépsz a mostani szobádból (' + net.session.code + '), és belépsz ide: ' + r.title + '?')) return;
      const s = net.session;
      api('leave', { code: s.code, token: s.token }).catch(() => {});
      forget();
    }
    if (flashCall('hfJoin', { code: r.code, title: r.title }) !== true) viaLink();
  }

  // 1000 px alatt a Várók jobb oldalt behúzható ("Várók" fül), mellé koppintva bezárul
  function showRooms(open) {
    document.body.classList.toggle('rooms-open', open);
    const t = document.getElementById('roomsToggle');
    if (t) t.setAttribute('aria-expanded', open ? 'true' : 'false');
  }

  function buildRooms() {
    const box = document.getElementById('rooms');
    if (!box) return;
    const toggle = document.getElementById('roomsToggle');
    const chatBox = document.getElementById('chat');
    toggle.addEventListener('click', () => showRooms(!document.body.classList.contains('rooms-open')));
    document.addEventListener('pointerdown', e => {
      if (!box.contains(e.target) && !chatBox.contains(e.target) && e.target !== toggle) showRooms(false);
    }, true);
    renderRooms();
    hubPoll();
  }

  // ---------------------------------------------------------------------------- csevegés

  // Csevegés a szobában (index.html: #chat, server: /api/chat), a játék előtt és alatt, fentről lefelé
  // (a legújabb alul). Elküldéskor és egy másik játékos üzenetének érkezésekor a chat hang szól (a
  // játék hangerejével). A szerver a trágár szavakat kicsillagozza, a linkeket nem engedi. Az X-szel
  // ki lehet lépni a csevegésből: addig semmi sem látszik és nem szól (a többiek látják, hogy
  // kiléptél), visszalépéskor az egész előzmény visszajön. Ez a lapé, a szobáig megmarad.
  const SEATCOLORS = ['#8A7A64', '#C0242B', '#C9B320', '#3E9A2E'];   // az eredeti játékosszínek
  const chatOffKey = 'hfChatOff' + slot;      // sessionStorage: annak a szobának a kódja, ahol kiléptél
  const CHAT_KEEP = 80;
  const chat = { last: 0, items: [] };        // items: a szoba üzenetei (a kilépés alatt is gyűlnek)
  const inRoom = () => !!(net.session && net.state);
  const chatOff = () => inRoom() && load('sessionStorage', chatOffKey) === net.session.code;
  const SYS = { joined: ' belépett a váróba.', left: ' elhagyta a chatet.', back: ' visszatért a chatbe.' };

  function renderChat() {
    const input = document.getElementById('chatText');
    if (!input) return;
    const on = inRoom();
    const title = document.getElementById('chatTitle');
    if (on) {
      const code = document.createElement('b');
      code.textContent = net.session.code;
      title.replaceChildren('Szoba: ', code);
    } else {
      title.textContent = 'Csevegés';
    }
    const off = chatOff();
    document.getElementById('copy').hidden = !on;
    document.getElementById('chatClose').hidden = !on || off;
    document.getElementById('chatOff').hidden = !off;
    input.disabled = !on || off;
    input.placeholder = on ? 'Üzenet a szobának...' : 'Csatlakozz egy váróhoz!';
    document.getElementById('chatSend').disabled = !on || off;
  }

  // kilépés a csevegésből (X) és vissza: a többiek a csevegésben látják
  function setChatOff(off) {
    const s = net.session;
    if (!inRoom() || off === chatOff()) return;
    save('sessionStorage', chatOffKey, off ? s.code : null);
    chatRedraw();
    renderChat();
    api('chat', { code: s.code, token: s.token, presence: off ? 'left' : 'back' }).catch(() => {});
  }

  // a csevegés jele: szövegbuborék a játékos színében
  function bubble(color) {
    const ns = 'http://www.w3.org/2000/svg';
    const svg = document.createElementNS(ns, 'svg');
    svg.setAttribute('class', 'bub');
    svg.setAttribute('viewBox', '0 0 17 14');
    svg.setAttribute('aria-hidden', 'true');
    const body = document.createElementNS(ns, 'path');
    body.setAttribute('d', 'M8.5 1C4.4 1 1 3.3 1 6.1c0 1.6 1 3 2.7 3.9L2.8 13l3.5-1.9c.7.2 1.4.2 2.2.2 4.1 0 7.5-2.3 7.5-5.2S12.6 1 8.5 1z');
    body.setAttribute('fill', color);
    body.setAttribute('stroke', 'rgba(0, 0, 0, .6)');
    const shine = document.createElementNS(ns, 'ellipse');
    for (const [k, v] of [['cx', 7.2], ['cy', 4.1], ['rx', 4.3], ['ry', 1.5], ['fill', '#fff'], ['opacity', 0.45]]) shine.setAttribute(k, v);
    svg.append(body, shine);
    return svg;
  }

  // egy sor a csevegésben: {name, seat, text} üzenet, vagy {note} (rendszerüzenet, hiba)
  function chatRow(item) {
    const li = document.createElement('li');
    if (item.note) {
      li.className = 'sys';
      li.textContent = item.note;
      return li;
    }
    const who = document.createElement('span');
    who.className = 'who';
    who.textContent = item.name + ': ';
    const body = document.createElement('span');
    body.append(who, String(item.text));
    li.append(bubble(SEATCOLORS[item.seat] || SEATCOLORS[0]), body);
    return li;
  }

  // új sor alulra; ha az alján álltál (vagy te írtad), oda is görget
  function chatAdd(item, scroll) {
    chat.items.push(item);
    if (chat.items.length > CHAT_KEEP) chat.items.shift();
    const log = document.getElementById('chatLog');
    if (!log || chatOff()) return;
    const atEnd = log.scrollHeight - log.scrollTop - log.clientHeight < 40;
    log.append(chatRow(item));
    while (log.children.length > CHAT_KEEP) log.firstChild.remove();
    if (atEnd || scroll) log.scrollTop = log.scrollHeight;
  }

  // az egész csevegés újra (visszalépéskor); kilépve üres
  function chatRedraw() {
    const log = document.getElementById('chatLog');
    if (!log) return;
    log.replaceChildren(...(chatOff() ? [] : chat.items.map(chatRow)));
    log.scrollTop = log.scrollHeight;
  }

  function chatNote(text) {
    chatAdd({ note: text }, true);
  }

  function chatSound() {
    if (!chatOff()) start('chat');
  }

  // egy üzenet az eseményfolyamon; újracsatlakozáskor a korábbiak is jönnek (old), ezeket az
  // azonosítójuk alapján csak egyszer vesszük fel
  function onChat(ev) {
    if (!(ev.id > chat.last)) return;
    chat.last = ev.id;
    const mine = !!(net.session && ev.seat === net.session.seat);
    if (ev.sys) {
      if (SYS[ev.sys]) chatAdd({ note: ev.name + SYS[ev.sys] }, false);
      return;
    }
    chatAdd({ name: ev.name, seat: ev.seat, text: ev.text }, mine);
    if (!ev.old && !mine) chatSound();
  }

  function chatReset() {
    chat.last = 0;
    chat.items = [];
    chatRedraw();
  }

  function chatSend() {
    const input = document.getElementById('chatText');
    const text = input.value.trim();
    const s = net.session;
    if (!text || !inRoom() || chatOff()) return;
    input.value = '';
    chatSound();
    api('chat', { code: s.code, token: s.token, text: text }).catch(e => {
      chatNote(e.message);
      if (!input.value) input.value = text;      // újraküldhető
    });
  }

  function buildChat() {
    const form = document.getElementById('chatForm');
    if (!form) return;
    const input = document.getElementById('chatText');
    form.addEventListener('submit', e => { e.preventDefault(); chatSend(); });
    // a billentyűk ne jussanak el a Ruffle-ig (az ablakon figyel, és pl. a Backspace-t letiltaná)
    for (const type of ['keydown', 'keyup', 'keypress', 'paste']) {
      input.addEventListener(type, e => e.stopPropagation());
    }
    document.getElementById('chatClose').addEventListener('click', () => setChatOff(true));
    document.getElementById('chatBack').addEventListener('click', () => setChatOff(false));
    const copy = document.getElementById('copy');
    copy.addEventListener('click', () => {
      copyText(link());
      copy.textContent = 'Másolva';
      setTimeout(() => { copy.textContent = 'Link'; }, 2000);
    });
    renderChat();
  }

  function boot() {
    //   főoldal (nincs gamemode): menü – bemutató / robotok / ismerős / visszajelzés
    //   gamemode=1: "Játék robotokkal" – teljes játék oktató üzenetek nélkül, élesben számoló órával
    //   gamemode=0: bemutató játék menü nélkül (a játék végi menü "Bemutató játék" gombja)
    //   ?join=KÓD:  a főoldal, a szobához csatlakozás ablakával
    buildMaps();
    buildRooms();
    buildChat();
    if (gamemode === '1') { loadSwf({ invitation: '0', quickgame: '1', map: myMap(), long: longVar(myLong()) }); return; }
    if (gamemode === '0') { loadSwf({ invitation: '0', map: myMap() }); return; }
    const menu = () => loadSwf({ invitation: '1', map: myMap() });
    let own = load('sessionStorage', tabKey);
    if (own && joinCode && joinCode !== own.code) {
      net.session = own;
      forget();             // egy másik szoba meghívója: a régi szobát elengedjük
      own = null;
    }
    const last = load('localStorage', sessionKey);
    const s = own || (last && (!joinCode || joinCode === last.code) ? last : null);
    if (!s) { menu(); return; }
    // Van szobánk: ennek a lapnak a sajátja, vagy a böngésző utolsó szobája (pl. bezárt lap után).
    // Az utóbbit csak akkor vesszük át, ha most nincs élő kapcsolata - különben egy másik lapé, és
    // ez a lap új látogató (a meghívóval új játékosként léphet be).
    api('check', { code: s.code, token: s.token }).then(st => {
      if (s !== own && st.live) { menu(); return; }
      net.session = s;
      remember(s);
      if (st.started && load('sessionStorage', playingKey) === s.code) {
        // frissítés játék közben: rögtön vissza a játékba (a menü nem töltődik be előbb; a SWF a
        // naplóból felzárkózik). Ha valamiért nem sikerül, a menü jön a szoba-ablakkal.
        connect();
        setTimeout(() => { if (!swfLoaded) menu(); }, 8000);
        return;
      }
      if (st.started) {
        // a játék megy, de nem ebben a lapban játszottunk: a szoba-ablak kínálja fel a visszalépést
        // (addig élő kapcsolat nélkül, így robot lép helyettünk)
        net.state = st;
        emit({ type: 'state', state: flashState(st) });
        renderMaps();
      } else {
        connect();
      }
      menu();
    }, e => {
      if (e.status === 403 || e.status === 404) {
        net.session = s;
        forget();
      } else if (s === own) {
        net.session = s;
        connect();
      }
      menu();
    });
  }

  window.addEventListener('DOMContentLoaded', boot);
})();
