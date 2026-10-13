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
  };

  function emit(ev) {
    net.events.push(ev);
    if (net.events.length > 500) net.events.splice(0, net.events.length - 500);
  }

  async function api(path, body) {
    let res;
    try {
      res = await fetch(apiURL(path), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
    } catch (e) {
      throw new Error('Nem érem el a szervert.');
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
    es.onmessage = function (e) {
      let ev;
      try { ev = JSON.parse(e.data); } catch (err) { return; }
      if (ev.type === 'state') onState(ev);
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
    emit({ type: 'joined', code: data.code, seat: data.seat, link: link() });
    connect();
  }

  function sendDecision(k, d) {
    const s = net.session;
    if (!s) return;
    // sorban, egyenként, hogy a saját döntéseink sorrendje megmaradjon
    net.sendChain = net.sendChain.then(async () => {
      for (let i = 0; i < 5; i++) {
        try { await api('send', { code: s.code, token: s.token, k: k, d: d }); return; } catch (e) {
          if (/nem vagy|Nincs ilyen/.test(e.message)) return;
          await new Promise(r => setTimeout(r, 1000 * (i + 1)));
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
        case 'create':
          save('localStorage', 'hfName', String(a));
          call(api('create', { name: String(a), map: myMap() }).then(setSession));
          return true;
        case 'join':
          save('localStorage', 'hfName', String(b));
          // ha a játék már megy (visszalépés a névvel), rögtön a játékba
          net.rejoining = true;
          call(api('join', { code: String(a), name: String(b) }).then(setSession));
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
    if (!player) {
      player = window.RufflePlayer.newest().createPlayer();
      document.getElementById('game').appendChild(player);
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

  function startGame() {
    net.inGame = true;
    save('sessionStorage', playingKey, net.session.code);
    loadSwf({ invitation: '0', mp: '1', map: cleanMap(net.state && net.state.map) || 'HU' });
    renderMaps();
  }

  // ---------------------------------------------------------------------------- térképválasztó

  // A bal oldali sáv (index.html: #maps). Mikor mit lehet:
  //   többjátékos játék közben: semmit (a szoba térképe látszik)
  //   szobában, indulás előtt: csak a főnök választ, mindenki a szoba térképét látja
  //   különben (menü, bemutató, robotok): a saját választás; váltáskor a mostani mód újraindul
  function mapMode() {
    const st = net.state;
    if (net.inGame) return { current: cleanMap(st && st.map), locked: 'Játék közben nem lehet térképet váltani.' };
    if (net.session && st && !st.started) {
      if (st.boss === net.session.seat) return { current: cleanMap(st.map), room: true };
      const boss = st.players.find(p => p.seat === st.boss);
      return { current: cleanMap(st.map), locked: 'A térképet a szoba főnöke' + (boss ? ' (' + boss.name + ')' : '') + ' választja.' };
    }
    return { current: myMap() };
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
    box.querySelector('.note').textContent = mode.locked || (mode.room ? 'A szoba térképe: mindenki ezen játszik.' : '');

    // főoldal gomb (a főoldalon a menüben nincs rá szükség) és a szoba kódja (a visszalépéshez)
    const home = document.getElementById('home');
    home.hidden = gamemode === null && !net.inGame;
    home.textContent = net.inGame ? 'Kilépés a főoldalra' : 'Főoldal';
    const room = document.getElementById('room');
    room.hidden = !(net.session && net.state);
    if (!room.hidden) room.querySelector('b').textContent = net.session.code;
    box.querySelector('.side').hidden = home.hidden && room.hidden;
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

  function buildMaps() {
    const box = document.getElementById('maps');
    if (!box) return;
    document.getElementById('home').addEventListener('click', goHome);
    document.getElementById('copy').addEventListener('click', () => {
      copyText(link());
      const note = box.querySelector('.note');
      note.textContent = 'A meghívó link a vágólapra került.';
      setTimeout(renderMaps, 2500);
    });
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
      b.addEventListener('click', () => pickMap(id));
      list.appendChild(b);
    }
    renderMaps();
  }

  function boot() {
    //   főoldal (nincs gamemode): menü – bemutató / robotok / ismerős / visszajelzés
    //   gamemode=1: "Gyors játék robotokkal" – teljes játék oktató üzenetek nélkül, élesben számoló órával
    //   gamemode=0: bemutató játék menü nélkül (a játék végi menü "Bemutató játék" gombja)
    //   ?join=KÓD:  a főoldal, a szobához csatlakozás ablakával
    buildMaps();
    if (gamemode === '1') { loadSwf({ invitation: '0', quickgame: '1', map: myMap() }); return; }
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
