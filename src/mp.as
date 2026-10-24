// Többjátékos játék (csak mp=1 módban).
//
// Minden résztvevő gépén ugyanaz a játék fut (az eredeti próbajáték kódja). Ami a játékban
// véletlen vagy egy játékos döntése, azt egyetlen gép dönti el, és a szerveren keresztül mindenki
// ugyanazt kapja meg, ugyanabban a sorrendben:
//
//   bázisok helye, a kérdések (és a válaszok sorrendje)   -> a "házigazda"
//   területválasztás, feleletválasztós válasz, tipp       -> az a játékos, akié a lépés
//   robot vagy kilépett játékos lépése                   -> a házigazda (a robotok szabályai szerint)
//
// Ketten is játszhatnak: a szoba üresen hagyott helye (se ember, se robot) kimarad a játékból (M.absent):
// nincs bázisa, nem tippel, nem támad, és nem is támadható; a doboza sem látszik.
//
// A házigazda a legkisebb sorszámú, elérhető ember. A döntések kulcsa csoport * 4 + hely
// (hely 0 = házigazda): a csoportszámláló minden gépen ugyanúgy lép, a szerver kulcsonként az első
// beérkezett döntést tartja meg. Ha valaki sokáig nem lép (pl. háttérbe tette a lapot), a házigazda
// az óra lejártának megfelelő lépést küldi helyette.
//
// Felzárkózás: ha egy gép lemaradt (visszalépett a játékba, vagy háttérbe tett lap után tér vissza),
// a már ismert lépéseken animációk és hangok nélkül, egy pillanat alatt fut át, aztán mindent
// újrarajzol, és onnan valós időben folytatja - a többieknek nem kell rá várniuk.

(function () {
    var hf = _global.hf;
    if (hf.mode != 'mp') {
        return;
    }
    var net = hf.net;
    var M = {};
    hf.mp = M;

    M.log = {};            // kulcs -> döntés ({v: ..., ms: ..., s: [...]})
    M.cursor = 0;          // ennyi döntést vettünk már át az oldaltól
    M.g = 0;               // a következő döntéscsoport
    M.wait = null;         // amire most várunk: {entries, keys, done, since, shown}
    M.sent = {};
    M.humans = [false, false, false, false];
    M.online = [false, false, false, false];
    M.names = ['', '', '', ''];
    M.absent = hf.absent;   // src/tweaks.as; a játék indulásakor üres helyek (lent)
    M.started = false;
    M.maxG = -1;           // a legnagyobb ismert döntéscsoport
    M.catchingUp = false;  // felzárkózás: animációk és hangok nélkül
    M.ans = null;          // a nyitott kérdés: {g, tip, start, who, heard, plan, sent} (válaszhangok)

    var info = net.call('game');
    M.seat = info ? Number(info.seat) : 1;

    M.applyState = function (st) {
        if (!st) {
            return;
        }
        for (var s = 1; s <= 3; s++) {
            M.online[s] = false;
        }
        for (var i = 0; i < st.players.length; i++) {
            var p = st.players[i];
            M.humans[p.seat] = !p.robot;      // a főnök betette robotért a házigazda lép
            M.names[p.seat] = p.name;
            M.online[p.seat] = p.online && !p.left && !p.robot;
        }
        M.online[M.seat] = true;
        M.refreshNames();
    };

    // A játékos neve: aki kilépett vagy elment a kapcsolata (helyette robot játszik), annak a neve
    // mögött "(robot)" áll, amíg vissza nem tér - a dobozán, a várán, a tippkártyákon, a harc
    // fejlécében, a várakozó dobozban és az eredménytáblán is (mind a game.player_name-ből).
    M.nameOf = function (s) {
        if (M.absent[s]) {
            return '';
        }
        var n = M.names[s] != '' ? M.names[s] : hf.robotName(s);
        return M.humans[s] && !M.online[s] && s != M.seat ? n + ' (robot)' : n;
    };
    M.refreshNames = function () {
        if (!M.started) {
            return;
        }
        var changed = false;
        for (var s = 1; s <= 3; s++) {
            var n = M.nameOf(s);
            if (game.player_name[s] != n) {
                game.player_name[s] = n;
                changed = true;
                if (gmap.playerbox[s] != undefined) {
                    gmap.playerbox[s].NAME = n;
                }
                var area = game.bases[s].area;
                var castle = gmap.MAP['CASTLE' + area];
                if (area > 0 && castle.OWNERNAME.text != '') {
                    castle.OWNERNAME.text = n;
                    hf.fitName(castle.OWNERNAME);
                }
            }
        }
        if (changed) {
            hf.later(100, hf.fitBoxNames);      // a változóhoz kötött név a következő képkockán frissül
        }
    };
    M.applyState(info ? info.state : null);

    // a játék résztvevői a szoba induláskori állapotából (játék közben a helyek nem változnak)
    if (info && info.state) {
        var inGame = [false, false, false, false];
        for (var ip = 0; ip < info.state.players.length; ip++) {
            inGame[info.state.players[ip].seat] = true;
        }
        for (var sa = 1; sa <= 3; sa++) {
            M.absent[sa] = !inGame[sa] && sa != M.seat;
        }
    }

    net.handler = function (ev) {
        if (ev.type == 'state') {
            M.applyState(ev.state);
        } else if (ev.type == 'closed') {
            hf.ui.notice(ev.msg);
        } else if (ev.type == 'presence') {
            hf.ui.notice(ev.msg, 7000);     // kilépett, elment a kapcsolata, visszatért (js/hf.js)
        }
    };

    // hol tart a játék (a publikus Várók listájához, server: handleProgress); a házigazda jelenti
    var progress = '';
    M.reportProgress = function () {
        if (!M.started || M.catchingUp || M.host() != M.seat) {
            return;
        }
        var now = game.state + ':' + game.gameround;
        if (now != progress) {
            progress = now;
            net.call('progress', game.state, game.gameround);
        }
    };

    // a házigazda: a legkisebb sorszámú elérhető ember
    M.host = function () {
        for (var s = 1; s <= 3; s++) {
            if (M.humans[s] && M.online[s]) {
                return s;
            }
        }
        return M.seat;
    };

    // ki küldi a hely (0 = házigazda) döntését: az ember maga, vagy helyette a házigazda
    M.producer = function (seat) {
        if (seat > 0 && M.humans[seat] && M.online[seat]) {
            return seat;
        }
        return M.host();
    };

    M.send = function (seat, d) {
        M.sendKey(M.g * 4 + seat, d);
    };

    M.sendKey = function (k, d) {
        if (M.sent[k]) {
            return;
        }
        M.sent[k] = true;
        net.call('send', k, d);
    };

    // a döntést csak akkor számolja ki (make), ha még nem küldtük el: a várakozás minden ütemben
    // ellenőriz, amíg a döntés a szerverről vissza nem ér, és pl. a kérdésválasztás nem olcsó
    M.sendOnce = function (seat, make) {
        if (!M.sent[M.g * 4 + seat]) {
            M.send(seat, make());
        }
    };

    M.have = function (seat) {
        return M.log[M.g * 4 + seat] != undefined;
    };

    // ------------------------------------------------------------------ várakozás

    // gate: megvárja a megadott helyek döntéseit (entries: {seat, make, dflt, delay}), aztán
    // done(értékek). A várakozó dobozt (hf.wait) 0,6 mp várakozás után mutatja, a szokásos
    // szöveggel ("Béla válaszát várjuk..."); opts.msg: más szöveg, opts.now: rögtön látszik.
    // delay: a robot (vagy a helyettesített játékos) döntését a házigazda ennyi ms után küldi el.
    // opts.limit, opts.from: a játékosok órája (ms) és a kezdete (getTimer), lásd M.check.
    var LATE = 6000;           // ennyit várunk az óra lejárta után egy lassú kapcsolatú játékosra
    var TAKEOVER = 8000;       // ha a házigazda sem lép helyette, ennyivel később bármelyik ember
    var HOSTWAIT = 15000;      // a házigazda döntése (kérdés, bázis) ennyi ms után bárkitől jöhet
    M.gate = function (entries, done, opts) {
        var keys = [];
        for (var i = 0; i < entries.length; i++) {
            keys.push(M.g * 4 + entries[i].seat);
        }
        M.wait = {entries: entries, keys: keys, done: done, since: getTimer(), shown: false, opts: opts ? opts : {}};
        M.check();
    };

    M.check = function () {
        var w = M.wait;
        if (!w) {
            return;
        }
        var missing = [];
        for (var i = 0; i < w.keys.length; i++) {
            if (M.log[w.keys[i]] == undefined) {
                missing.push(w.entries[i]);
            }
        }
        if (missing.length == 0) {
            var vals = [];
            for (var j = 0; j < w.keys.length; j++) {
                vals.push(M.log[w.keys[j]]);
            }
            M.wait = null;
            M.g++;
            M.hideWait();
            w.done(vals);
            return;
        }
        var now = getTimer();
        var waited = now - w.since;
        // a játékos ideje: az órája (opts.limit, ms) attól, hogy a kérdés vagy a lépése elkezdődött
        // (opts.from), plusz LATE a lassú kapcsolatnak; ismeretlen lépésnél 40 mp
        var late = (w.opts.limit != undefined ? w.opts.limit + LATE : 40000) - (now - (w.opts.from != undefined ? w.opts.from : w.since));
        var others = [];
        for (var m = 0; m < missing.length; m++) {
            var e = missing[m];
            var who = M.producer(e.seat);
            if (who == M.seat) {
                if (e.seat != M.seat && e.make && !(e.delay > waited) && !M.robotThinking(e.seat)) {
                    M.sendOnce(e.seat, e.make);   // robot, kilépett játékos vagy a házigazda döntése
                }
                if (e.seat > 0 && e.seat != M.seat) {
                    others.push(e.seat);          // a robot még gondolkodik
                }
            } else {
                others.push(e.seat);
                // Aki az ideje lejárta után sem lép (rossz kapcsolat, háttérbe tett lap), helyette a
                // házigazda lép, mintha lejárt volna az órája: a játék nem vár rá tovább. Ha maga a
                // házigazda nem lép (pl. elaludt a telefonja, de a kapcsolata még élőnek látszik),
                // TAKEOVER ms-mal később bármelyik másik ember lép helyette, a házigazda dolgait (kérdés,
                // bázis) is elvégezve. A szerveren az első döntés számít, így ez nem ütközik.
                if (e.seat > 0 && e.dflt && late < 0 && (M.host() == M.seat || late < -TAKEOVER)) {
                    M.sendOnce(e.seat, e.dflt);
                } else if (e.seat == 0 && e.make && waited > HOSTWAIT) {
                    M.sendOnce(0, e.make);
                }
            }
        }
        // (a szöveg követi, kikre várunk még: "A többiek..." -> "Béla válaszát várjuk...")
        if (others.length > 0 && (w.opts.now || waited > 600) && !M.catchingUp) {
            w.shown = true;
            M.showWait(others, w.opts.msg);
        }
    };

    M.showWait = function (seats, msg) {
        if (msg == undefined) {
            var named = [];
            for (var i = 0; i < seats.length; i++) {
                if (seats[i] > 0) {
                    named.push(seats[i]);
                }
            }
            if (named.length == 1) {
                msg = getlang('gameinfo_waitingforanswer', game.player_name[named[0]]);
            } else {
                msg = getlang('gameinfo_waitoppanswers');
            }
        }
        hf.wait.show(msg);
    };

    M.hideWait = function () {
        hf.wait.hide();
    };

    // A játék elején minden gép elküldi, mely kérdéseket látta már (src/questions.as: Q.mySeen), hogy
    // a kérdésválasztás azokat részesítse előnyben, amelyeket a legkevesebben láttak. Ezek a döntések
    // nem a játék menetéhez tartoznak: a kulcsuk SEENKEY + hely, a csoportszámláló nem számol velük.
    var SEENKEY = 1000000;
    M.seenSent = false;
    M.shareSeen = function () {
        if (!M.seenSent && M.started && hf.questions.ready()) {
            M.seenSent = true;
            M.sendKey(SEENKEY + M.seat, hf.questions.mySeen());
        }
    };

    M.tick = function () {
        M.shareSeen();
        var decs = net.call('decs', M.cursor);
        if (decs) {
            for (var i = 0; i < decs.length; i++) {
                M.cursor++;
                var d = decs[i];
                if (d.k >= SEENKEY) {
                    if (d.k - SEENKEY != M.seat) {
                        hf.questions.setOthers(d.k - SEENKEY, d.d);
                    }
                    continue;
                }
                if (M.log[d.k] == undefined && d.d) {
                    M.log[d.k] = d.d;
                    if (Math.floor(d.k / 4) > M.maxG) {
                        M.maxG = Math.floor(d.k / 4);
                    }
                    M.answerArrived(d.k);
                }
            }
        }
        if (M.started && !M.catchingUp && M.behind()) {
            M.catchingUp = true;
        }
        M.hideAbsent();
        M.reportProgress();
        M.robotAnswers();
        M.check();
        if (M.catchingUp && pending) {
            M.drive();              // egy félbehagyott felzárkózás folytatása
        }
    };

    // ------------------------------------------------------------------ felzárkózás

    // lemaradás: a többiek már legalább két döntéscsoporttal előrébb járnak
    M.behind = function () {
        return M.maxG > M.g + 1;
    };

    // felzárkózás közben a játék animációsorai (gsqc) nem futnak le: a következő fázis rögtön jön.
    // A fázisokat egy ciklus hajtja (nem egymásba hívva), hogy ne fogyjon el a hívási verem.
    var seqStart = CSqControl.prototype.Start;
    var pending = false;
    var driving = false;
    gsqc.Start = function () {
        if (!M.catchingUp) {
            seqStart.apply(gsqc, arguments);
            return;
        }
        gsqc.Clear();
        pending = true;
        M.drive();
    };

    M.drive = function () {
        if (driving) {
            return;
        }
        driving = true;
        var n = 0;
        while (pending && M.catchingUp && n < 300) {
            pending = false;
            n++;
            game.NextPhase();
        }
        driving = false;
        if (pending) {
            return;                 // a keret elfogyott: a következő tick folytatja
        }
        if (M.catchingUp && n > 0) {
            M.finishCatchUp();      // a játék megállt: a többiekre vagy erre a gépre vár
        }
    };

    // A háttérbe tett lapon a böngésző megállítja a lejátszót, és vele a játékot. Visszatéréskor
    // (js/hf.js: hfResume), ha közben megjöttek a döntések vagy épp animáció fut, nem játsszuk végig a
    // lemaradt animációkat: a felzárkózás rögtön oda visz, ahol a játék bevitelre vagy új döntésre
    // vár. Különben a többiek másodpercekig várnának erre a gépre (pl. a következő területválasztásra).
    M.resume = function () {
        if (!M.started || M.catchingUp) {
            return;
        }
        var decs = net.call('decs', M.cursor);
        if (!gsqc.running && !(decs && decs.length > 0)) {
            return;
        }
        M.catchingUp = true;
        if (gsqc.running) {
            gsqc.Clear();
            pending = true;
        }
        M.tick();
    };
    flash.external.ExternalInterface.addCallback('hfResume', null, function () {
        M.resume();
        return true;
    });

    // a felzárkózás vége: minden a játék mostani állapota szerint újrarajzolva
    M.finishCatchUp = function () {
        if (!M.catchingUp) {
            return;
        }
        M.catchingUp = false;
        gmap.MAP._visible = true;
        gmap.MAP._alpha = 100;
        gmap.GAMEPHASE._visible = true;
        gmap.GAMEPHASE._alpha = 100;
        for (var p = 1; p <= 3; p++) {
            gmap.playerbox[p]._visible = true;
            gmap.playerbox[p]._alpha = 100;
            gmap['SELECTION' + p]._visible = false;
        }
        M.hideAbsent();
        game.UpdateAreas();
        game.UpdatePlayerScores();
        game.SaveLastState();
        game.SetupPhaseDisplay(false);
    };

    // ------------------------------------------------------------------ üres hely

    // a kimaradó játékos doboza nem látszik (a beúsztatásból is kimarad; a biztonság kedvéért 100
    // ms-onként is elrejtjük)
    M.hideAbsent = function () {
        for (var p = 1; p <= 3; p++) {
            if (M.absent[p] && gmap.playerbox[p]._visible) {
                gmap.playerbox[p]._visible = false;
            }
        }
    };
    var multiFadeIn = CSqControl.prototype.AddMultiFadeIn;
    gsqc.AddMultiFadeIn = function (objs, ms, fadein) {
        var keep = [];
        for (var i = 0; i < objs.length; i++) {
            var absent = false;
            for (var p = 1; p <= 3; p++) {
                if (M.absent[p] && objs[i] == gmap.playerbox[p]) {
                    absent = true;
                }
            }
            if (!absent) {
                keep.push(objs[i]);
            }
        }
        return multiFadeIn.call(gsqc, keep, ms, fadein);
    };

    // a tippkérdésen csak a résztvevők tippelnek (a terjeszkedésnél az eredeti mindenkit hív: 7)
    var prepareTips = game.PrepareTips;
    game.PrepareTips = function () {
        prepareTips();
        for (var p = 1; p <= 3; p++) {
            if (M.absent[p]) {
                game.tipplayers = game.tipplayers & ~(1 << (p - 1));
            }
        }
    };

    // a harci sorrend kijelzőjén a kimaradó játékos körei nem látszanak (a játék átugorja őket)
    var updateAttackOrder = game.UpdateAttackOrder;
    game.UpdateAttackOrder = function () {
        updateAttackOrder();
        var rounds = hf.warRounds;
        for (var r = 1; r <= rounds; r++) {
            for (var i = 1; i <= 3; i++) {
                var pc = gmap.GAMEPHASE['AO' + r]['PCOLOR' + i];
                pc._visible = !M.absent[game.warorder[(r - 1) * 3 + i - 1]];
            }
        }
    };

    // ------------------------------------------------------------------ válaszhangok

    // Egy kérdés ideje alatt (a kérdés megjelenésétől az eredményig) minden ellenfél válaszakor szól az
    // answer_pick, amikor a döntése beérkezik. A robotok (és akit a házigazda helyettesít) is
    // gondolkodnak: a házigazda gépe a kérdés kezdetétől számított gondolkodási idő után küldi el a
    // döntésüket (src/tweaks.as: hf.answers.thinkTime), így mindenkinél egyszerre szól, és az
    // eredmény megvárja őket.
    M.openAnswers = function (tip) {
        if (M.ans && M.ans.g == M.g) {
            return;
        }
        var seats = hf.answers.others(tip);
        var secs = tip ? gcfg.TIMEOUT_TIP : gcfg.TIMEOUT_ANSWER;
        var who = {};
        var plan = {};
        for (var i = 0; i < seats.length; i++) {
            var s = seats[i];
            who[s] = true;
            if (!(M.humans[s] && M.online[s])) {
                plan[s] = getTimer() + hf.answers.thinkTime(secs, tip);
            }
        }
        M.ans = {g: M.g, tip: tip, start: getTimer(), who: who, heard: {}, plan: plan, sent: {}};
        if (tip) {
            hf.tipCards.reset();
        }
        for (var j = 1; j <= 3; j++) {
            M.answerArrived(M.g * 4 + j);       // ami már korábban beérkezett
        }
    };

    // a házigazda: a robotok döntése, amikor letelt a gondolkodási idejük (M.tick)
    M.robotAnswers = function () {
        var a = M.ans;
        if (!a) {
            return;
        }
        for (var key in a.plan) {
            var s = Number(key);
            var k = a.g * 4 + s;
            if (!a.sent[s] && M.log[k] == undefined && getTimer() >= a.plan[s] && M.producer(s) == M.seat) {
                a.sent[s] = true;
                M.sendKey(k, a.tip ? robotTip(s)() : robotAnswer());
            }
        }
    };

    // gondolkodik-e még a robot (addig a várakozás sem küldi el helyette a döntést)
    M.robotThinking = function (seat) {
        var a = M.ans;
        return a != null && a.g == M.g && a.plan[seat] != undefined && !a.sent[seat] && getTimer() < a.plan[seat];
    };

    M.answerArrived = function (k) {
        var a = M.ans;
        var seat = k % 4;
        var d = M.log[k];
        if (!a || d == undefined || Math.floor(k / 4) != a.g || !a.who[seat] || a.heard[seat]) {
            return;
        }
        a.heard[seat] = true;
        if (a.tip) {
            hf.tipCards.set(seat, Number(d.ms) >= 60000 ? -1 : Number(d.ms));   // a kártyája előjön
        }
        // lejárt idő: feleletválasztósnál 0 válasz, tippnél 60 000 ms - ez nem válasz, nincs hang
        if (a.tip ? Number(d.ms) >= 60000 : Number(d.v) == 0) {
            return;
        }
        gsound.play('answer_pick');
    };

    M.closeAnswers = function () {
        M.ans = null;
    };

    // ------------------------------------------------------------------ robotok (a házigazda gépén)

    // a robotok ne a házigazdát vegyék célba: game.IAM = 0 alatt nincs "kitüntetett" ember
    function asRobot(f) {
        var save = game.IAM;
        game.IAM = 0;
        var r = f();
        game.IAM = save;
        return r;
    }

    function robotSelect(np) {
        return function () {
            return {v: asRobot(function () {
                return game.RobotSelectArea(np, game.GetAvailableArea(np));
            })};
        };
    }

    function randomSelect(np) {
        return function () {
            return {v: game.hfOrigRandomSelectArea(game.GetAvailableArea(np))};
        };
    }

    // a robot tippje; a gyorsasága: mennyi idő telt el a kérdés kezdete óta
    function robotTip(p) {
        return function () {
            return {v: asRobot(function () {
                return game.CalculateRobotTip(p);
            }), ms: M.ans ? getTimer() - M.ans.start : 2000 + random(6000)};
        };
    }

    function noTip() {
        return {v: 0, ms: 60000};
    }

    function robotAnswer() {
        var good = game.goodanswerpos;
        if (random(100) < 55) {
            return {v: good};
        }
        var a = 1 + random(3);
        if (a >= good) {
            a++;
        }
        return {v: a};
    }

    function noAnswer() {
        return {v: 0};
    }

    // hosszú hadjárat: a bázisválasztás sorrendje a házigazda döntése (src/tweaks.as: hf.baseOrder)
    var randomBaseOrder = hf.baseOrder;
    if (randomBaseOrder != undefined) {
        hf.baseOrder = function () {
            var o = M.forceOrder;
            var seen = {};
            var out = [];
            for (var i = 0; o != undefined && i < 3; i++) {
                var p = Number(o[i]);
                if (p >= 1 && p <= 3 && !seen[p]) {
                    seen[p] = true;
                    out.push(p);
                }
            }
            return out.length == 3 ? out : [1, 2, 3];
        };
    }

    // ------------------------------------------------------------------ kérdések

    // a házigazda választ (src/questions.as), mindenki ugyanazt alkalmazza
    function pickTip() {
        return {v: hf.questions.pick(true)};
    }

    function pickQuestion() {
        return {v: hf.questions.pick(false), s: hf.questions.shuffle()};
    }

    M.applyQuestion = function (atip) {
        if (atip) {
            hf.questions.apply(true, M.forceTip);
        } else {
            hf.questions.apply(false, M.forceQ.v, M.forceQ.s);
        }
    };

    // ------------------------------------------------------------------ a játék kiegészítései

    game.hfOrigInitGame = game.InitGame;
    game.InitGame = function () {
        game.hfOrigInitGame();
        var me = M.seat;
        game.IAM = me;
        var opp = [];
        for (var s = 1; s <= 3; s++) {
            if (s != me) {
                opp.push(s);
            }
        }
        game.MYOPP1 = opp[0];
        game.MYOPP2 = opp[1];
        M.started = true;
        for (var n = 1; n <= 3; n++) {
            game.player_name[n] = M.nameOf(n);
        }
        M.catchingUp = M.behind();
    };

    game.hfOrigRandomSelectArea = game.RandomSelectArea;
    game.RandomSelectArea = function (avail) {
        if (M.forceArea != undefined) {
            var a = M.forceArea;
            M.forceArea = undefined;
            return a;
        }
        return game.hfOrigRandomSelectArea(avail);
    };

    // az eredeti az 1. játékos területeit kínálja fel
    game.StartSelectArea = function () {
        game.selectablearea = game.GetAvailableArea(game.IAM);
        game.ShowAreaButtons(game.selectablearea);
        game.ShowSelectableAreas(game.selectablearea);
        game.StartClock(gmap.INSANIM, getlang('select_area'), 1, gcfg.TIMEOUT_SELECT);
    };

    game.btnAreaPressed = function (anum) {
        if (!((game.state == 1 || game.state == 3 || game.state == 4) && game.phase == 8 && game.nextplayer == game.IAM)) {
            return;
        }
        game.StopSelection();
        if (anum == 0) {
            anum = game.hfOrigRandomSelectArea(game.GetAvailableArea(game.IAM));
        }
        M.send(game.IAM, {v: anum});
        game.NextPhase();                       // -> 9: megvárja, hogy a lépés visszaérjen
    };

    game.hfOrigBtnAnswerPressed = game.btnAnswerPressed;
    game.btnAnswerPressed = function (ans) {
        if (!(game.state == 4 && game.phase == 11) || game.warobserver) {
            return;
        }
        M.send(game.IAM, {v: Number(ans)});
        game.hfOrigBtnAnswerPressed(ans);      // megjelöli a választ, -> 12: vár a többiekre
    };

    game.hfOrigStartTip = game.StartTip;
    game.StartTip = function () {
        M.tipStart = getTimer();
        game.hfOrigStartTip();
    };

    game.SendMyTip = function (atip, ahuman) {
        if (game.myanswer != -1) {
            return;
        }
        var s = String(atip);
        var d = '';
        for (var i = 0; i < s.length && d.length < 12; i++) {
            if ('0123456789'.indexOf(s.charAt(i)) >= 0) {
                d += s.charAt(i);
            }
        }
        if (d == '') {
            d = '0';
        }
        game.myanswer = Number(d);
        game.player_tips[game.IAM].value = game.myanswer;      // a kártyádon rögtön látszik
        var ms = getTimer() - M.tipStart;
        if (game.clocktimeout && hf.noTip) {
            ms = 60000;                         // lejárt az idő, és üres volt a mező: nem tippelt
        }
        game.StopClock();
        M.send(game.IAM, {v: game.myanswer, ms: ms});
        hf.tipCards.set(game.IAM, ms >= 60000 ? -1 : ms);
        game.NextPhase();                       // -> tipperedmény: vár a többiekre
    };

    // a tippek sorrendje a valódi gyorsaság szerint (a próbajátékban véletlen volt)
    game.CalculateRobotTips = function () {
        var t = M.tips;
        M.tips = null;
        var list = [];
        for (var i = 0; i < t.seats.length; i++) {
            var p = t.seats[i];
            game.player_tips[p].value = Number(t.vals[i].v);
            list.push({p: p, ms: Number(t.vals[i].ms)});
            hf.tipCards.times[p] = Number(t.vals[i].ms) >= 60000 ? -1 : Number(t.vals[i].ms);
        }
        list.sort(function (a, b) {
            if (a.ms != b.ms) {
                return a.ms < b.ms ? -1 : 1;
            }
            return a.p - b.p;
        });
        game.tiporder = [];
        for (var j = 0; j < list.length; j++) {
            game.tiporder.push(list[j].p);
        }
        game.tipcount = game.tiporder.length;
        game.CalculateTipWinOrder();
    };

    game.CalculateRobotAnswers = function () {
    };

    game.CalcRobotCompensation = function (pnum) {
        return 0;
    };

    // ------------------------------------------------------------------ a játék menete

    function orig() {
        game.hfOrigHandleGamePhase();
    }

    function isTipPlayer(p) {
        return (game.tipplayers & (1 << (p - 1))) != 0;
    }

    M.handlePhase = function () {
        var s = game.state;
        var ph = game.phase;

        // bázisok (1. állapot), villámháború: a házigazda sorsolja; a kimaradó játékos nem kap bázist
        while (s == 1 && ph == 1 && game.nextplayer < 3 && !hf.long && M.absent[game.nextplayer + 1]) {
            game.nextplayer++;
        }
        if (s == 1 && ph == 1 && game.nextplayer < 3 && !hf.long) {
            var bp = game.nextplayer + 1;
            M.gate([{seat: 0, make: function () {
                return {v: game.hfOrigRandomSelectArea(game.GetAvailableArea(bp))};
            }}], function (v) {
                M.forceArea = Number(v[0].v);
                orig();
            });
            return;
        }

        // hosszú hadjárat: a bázisválasztás sorrendje (a házigazda sorsolja; src/tweaks.as)
        if (s == 1 && ph == 0 && hf.long) {
            M.gate([{seat: 0, make: function () {
                return {v: randomBaseOrder()};
            }}], function (v) {
                M.forceOrder = v[0].v;
                orig();
            });
            return;
        }

        // tippkérdés: terjeszkedés, döntetlen csata, végső döntetlen
        if ((s == 3 && ph == 2) || (s == 4 && ph == 20) || (s == 5 && ph == 2)) {
            M.gate([{seat: 0, make: pickTip}], function (v) {
                M.forceTip = v[0].v;
                orig();
            });
            return;
        }

        // feleletválasztós kérdés
        if (s == 4 && ph == 10) {
            M.gate([{seat: 0, make: pickQuestion}], function (v) {
                M.forceQ = v[0];
                orig();
            });
            return;
        }

        // tipp beírása: ha a lépésünk már megvan (újracsatlakozás), nem kérdezzük újra
        if ((s == 3 && ph == 3) || (s == 4 && ph == 21) || (s == 5 && ph == 3)) {
            M.openAnswers(true);
            if (isTipPlayer(game.IAM) && M.have(game.IAM)) {
                game.JumpPhase(ph + 1);
                return;
            }
            if (isTipPlayer(game.IAM)) {
                M.finishCatchUp();
            }
            orig();
            return;
        }

        // tippek eredménye: mindenki tippje kell
        if ((s == 3 && ph == 4) || (s == 4 && ph == 22) || (s == 5 && ph == 4)) {
            var seats = [];
            var entries = [];
            for (var p = 1; p <= 3; p++) {
                if (isTipPlayer(p)) {
                    seats.push(p);
                    entries.push({seat: p, make: robotTip(p), dflt: noTip});
                }
            }
            game.DrawQuestionWin(s == 4 ? 'WARTIPRESULT' : '3TIPRESULT');
            M.openAnswers(true);            // ha a tippbeírás kimaradt (nem tippelünk)
            hf.tipCards.start();            // a kártyák: a saját és aki már tippelt
            M.gate(entries, function (v) {
                M.closeAnswers();
                M.tips = {seats: seats, vals: v};
                orig();
            }, {limit: gcfg.TIMEOUT_TIP * 1000, from: M.ans.start});
            return;
        }

        // bázis- (hosszú hadjárat) és területválasztás: aki soron van, maga választ (a többiek a 9.
        // fázisban várnak rá)
        if ((s == 1 || s == 3 || s == 4) && ph == 8) {
            if (game.nextplayer == game.IAM && !M.have(game.IAM)) {
                M.finishCatchUp();
                orig();
            } else {
                game.JumpPhase(9);
            }
            return;
        }
        if ((s == 1 || s == 3 || s == 4) && ph == 9) {
            var np = game.nextplayer;
            // a robot a bázisát véletlenül választja (mint az eredeti sorsolás)
            var make = s == 1 ? randomSelect(np) : robotSelect(np);
            M.gate([{seat: np, make: make, dflt: randomSelect(np), delay: hf.robotThinkTime()}], function (v) {
                game.player_selections[np] = Number(v[0].v);
                orig();
            }, {msg: hf.wait.selectMsg(np), now: true, limit: gcfg.TIMEOUT_SELECT * 1000});
            return;
        }

        // feleletválasztós válasz: a támadó és a védő
        if (s == 4 && ph == 11) {
            M.openAnswers(false);
            if (!game.warobserver && M.have(game.IAM)) {
                game.JumpPhase(12);
                return;
            }
            if (!game.warobserver) {
                M.finishCatchUp();
            }
            orig();
            return;
        }
        if (s == 4 && ph == 12) {
            var off = game.offender;
            var def = game.defender;
            M.openAnswers(false);
            M.gate([{seat: off, make: robotAnswer, dflt: noAnswer}, {seat: def, make: robotAnswer, dflt: noAnswer}], function (v) {
                M.closeAnswers();
                game.answers[off] = Number(v[0].v);
                game.answers[def] = Number(v[1].v);
                orig();
            }, {limit: gcfg.TIMEOUT_ANSWER * 1000, from: M.ans.start});
            return;
        }

        orig();
    };
})();
