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
            M.humans[p.seat] = true;
            M.names[p.seat] = p.name;
            M.online[p.seat] = p.online && !p.left;
        }
        M.online[M.seat] = true;
    };
    M.applyState(info ? info.state : null);

    net.handler = function (ev) {
        if (ev.type == 'state') {
            M.applyState(ev.state);
        } else if (ev.type == 'closed') {
            hf.ui.notice(ev.msg);
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

    M.have = function (seat) {
        return M.log[M.g * 4 + seat] != undefined;
    };

    // ------------------------------------------------------------------ várakozás

    // gate: megvárja a megadott helyek döntéseit (entries: {seat, make, dflt, delay}), aztán
    // done(értékek). A várakozó dobozt (hf.wait) 0,6 mp várakozás után mutatja, a szokásos
    // szöveggel ("Béla válaszát várjuk..."); opts.msg: más szöveg, opts.now: rögtön látszik.
    // delay: a robot (vagy a helyettesített játékos) döntését a házigazda ennyi ms után küldi el.
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
        var waited = getTimer() - w.since;
        var others = [];
        for (var m = 0; m < missing.length; m++) {
            var e = missing[m];
            var who = M.producer(e.seat);
            if (who == M.seat) {
                if (e.seat != M.seat && e.make && !(e.delay > waited) && !M.robotThinking(e.seat)) {
                    M.send(e.seat, e.make());     // robot, kilépett játékos vagy a házigazda döntése
                }
                if (e.seat > 0 && e.seat != M.seat) {
                    others.push(e.seat);          // a robot még gondolkodik
                }
            } else {
                others.push(e.seat);
                // aki túl sokáig nem lép, helyette a házigazda lép (mintha lejárt volna az ideje)
                if (e.seat > 0 && waited > 40000 && M.host() == M.seat && e.dflt) {
                    M.send(e.seat, e.dflt());
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

    M.tick = function () {
        var decs = net.call('decs', M.cursor);
        if (decs) {
            for (var i = 0; i < decs.length; i++) {
                M.cursor++;
                var d = decs[i];
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
        game.UpdateAreas();
        game.UpdatePlayerScores();
        game.SaveLastState();
        game.SetupPhaseDisplay(false);
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
                plan[s] = getTimer() + hf.answers.thinkTime(secs);
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
            game.player_name[s] = M.humans[s] ? M.names[s] : hf.robotName(s);
        }
        game.MYOPP1 = opp[0];
        game.MYOPP2 = opp[1];
        M.started = true;
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
        if (!((game.state == 3 || game.state == 4) && game.phase == 8 && game.nextplayer == game.IAM)) {
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
        if (game.clocktimeout) {
            ms = 60000;
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

        // bázisok (1. állapot): a házigazda sorsolja
        if (s == 1 && ph == 1 && game.nextplayer < 3) {
            var bp = game.nextplayer + 1;
            M.gate([{seat: 0, make: function () {
                return {v: game.hfOrigRandomSelectArea(game.GetAvailableArea(bp))};
            }}], function (v) {
                M.forceArea = Number(v[0].v);
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
            });
            return;
        }

        // területválasztás: aki soron van, maga választ (a többiek a 9. fázisban várnak rá)
        if ((s == 3 || s == 4) && ph == 8) {
            if (game.nextplayer == game.IAM && !M.have(game.IAM)) {
                M.finishCatchUp();
                orig();
            } else {
                game.JumpPhase(9);
            }
            return;
        }
        if ((s == 3 || s == 4) && ph == 9) {
            var np = game.nextplayer;
            M.gate([{seat: np, make: robotSelect(np), dflt: randomSelect(np), delay: hf.robotThinkTime()}], function (v) {
                game.player_selections[np] = Number(v[0].v);
                orig();
            }, {msg: hf.wait.selectMsg(np), now: true});
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
            });
            return;
        }

        orig();
    };
})();
