// Menü és robotjáték (a játék fő kódja után fut, a game.* függvények már léteznek).
//
// Mindig:
//   a kezdőmenü 4. gombja (eredetileg Regisztráció) a "Visszajelzés" ablakot nyitja,
//   a 3. gomb ("Gyors játék egy ismerőssel") a szoba-ablakot (src/lobby.as)
// A robotjátékban és a többjátékos játékban:
//   nincsenek kék oktató üzenetek; a játék végén egy ablak mutatja az eredményt, utána a menü jön
//   az óra élesben számol: területválasztásnál véletlen terület, feleletválasztósnál nincs válasz,
//   tippnél a beírt szám megy el, üres mezőnél 0 (az eredeti kliens kikapcsolt "rendes játék" kezelése)

(function () {
    var hf = _global.hf;
    hf.restoreIndexOf();      // src/core.as: csak a játék indulókódjának idejére volt átállítva
    var full = hf.mode == 'robots' || hf.mode == 'mp';

    // teszteléshez: ?debugclock=N -> N másodperces óra mindenhol
    if (Number(_level0.debugclock) > 0) {
        gcfg.TIMEOUT_SELECT = Number(_level0.debugclock);
        gcfg.TIMEOUT_ANSWER = Number(_level0.debugclock);
        gcfg.TIMEOUT_TIP = Number(_level0.debugclock);
    }

    // ------------------------------------------------------------------ térkép

    // A SWF-ben 13 ország térképe van (HU, FR, BG, BE, RS, RU, CZ, DE, UA, RO, SI, IT, SK), az eredeti
    // próbajáték a magyart használta. A térképet az oldal adja (js/hf.js: térképválasztó, map=XX).
    if (_level0.map != undefined) {
        game.hfOrigActivateMap = game.ActivateMap;
        game.ActivateMap = function (amapid) {
            var want = String(_level0.map).toUpperCase();
            if (game.FindMapIndex(want) >= 0) {
                amapid = want;
            }
            return game.hfOrigActivateMap(amapid);
        };
    }

    // ------------------------------------------------------------------ hosszú hadjárat

    // A próbajáték csak a normál szabályt játszotta: 4 harci kör, az utolsóban a pontok sorrendjében
    // támadnak, és bárhová lehet támadni. A SWF-ben a teljes kliens hosszú hadjárata is benne maradt
    // (gcfg.longrulesorder: 6 kör, mindenki minden helyről egyszer támad; a GAMEPHASE "WARLONG"
    // képkockája 6 körjelzővel és 6 támadási sorrenddel), csak a játékmenet nem használta.
    // long=1 (js/hf.js): ez a szabály a robot- és a többjátékos játékban. Itt is csak az utolsó kör
    // szabad támadás. A bemutató mindig normál (az oktató szövegei arra szólnak).
    hf.long = full && _level0.long == '1';
    hf.warRounds = hf.long ? 6 : 4;
    if (hf.long) {
        game.hfOrigInitGameLong = game.InitGame;
        game.InitGame = function () {
            game.hfOrigInitGameLong();
            var order = String(gcfg.longrulesorder);
            game.warorder = [];
            for (var i = 0; i < order.length; i++) {
                game.warorder.push(Number(order.charAt(i)));
            }
        };

        // a 4. állapot 1. fázisa: ki támad következőnek (az eredeti 4 körös, pontsorrendes változata helyett)
        game.hfOrigHandleGamePhaseLong = game.HandleGamePhase;
        game.HandleGamePhase = function () {
            if (game.state != 4 || game.phase != 1) {
                game.hfOrigHandleGamePhaseLong();
                return;
            }
            while (true) {
                game.lpnum++;
                if (game.lpnum <= 3) {
                    game.nextplayer = game.warorder[(game.gameround - 1) * 3 + game.lpnum - 1];
                    if (game.points[game.nextplayer] != 0) {
                        game.JumpPhase(2);
                        return;
                    }
                } else {
                    game.lpnum = 0;
                    game.gameround++;
                    if (game.gameround > hf.warRounds) {
                        game.SetNextState(game.CalculateEqualPoints() > 0 ? 5 : 15, 1, 0);
                        game.NextPhase();
                        return;
                    }
                }
            }
        };

        // Az eredeti a gameround alapján dönti el, hová lehet támadni (a 4. körtől bárhová), és hová
        // támadnak a robotok (a 3. körben a bázisuk mellé, az utolsóban a te váradra). Ezek a hívások
        // idejére a hosszú hadjárat körét a normál szabály megfelelő körére fordítjuk: az utolsó
        // előtti körök a 3., az utolsó a 4. (Mindkét függvény szinkron, közben semmi sem lép.)
        var asNormalRound = function (fn) {
            return function (a, b) {
                var r = game.gameround;
                if (game.state == 4 && r >= 4) {
                    game.gameround = r >= hf.warRounds ? 4 : 3;
                }
                var res = fn(a, b);
                game.gameround = r;
                return res;
            };
        };
        game.GetAvailableArea = asNormalRound(game.GetAvailableArea);
        game.GetRobotSelAreaPointsWar = asNormalRound(game.GetRobotSelAreaPointsWar);

        game.UpdateAttackOrder = function () {
            for (var r = 1; r <= hf.warRounds; r++) {
                var ao = gmap.GAMEPHASE['AO' + r];
                for (var i = 1; i <= 3; i++) {
                    ao['PCOLOR' + i].gotoAndStop(game.warorder[(r - 1) * 3 + i - 1]);
                }
            }
        };

        // a harc kijelzője: a WAR4 képkocka helyett a 6 körös WARLONG (az eredeti WAR4 ága szerint)
        game.hfOrigSetupPhaseDisplay = game.SetupPhaseDisplay;
        game.SetupPhaseDisplay = function (afirst) {
            if (game.state != 4) {
                game.hfOrigSetupPhaseDisplay(afirst);
                return;
            }
            var ph = gmap.GAMEPHASE;
            var cur = game.gameround;
            ph.gotoAndStop('WARLONG');
            ph.ROUNDNAME = glang.fn_turnname(game.state);
            game.SetupRoundProgress(ph.ROUNDPB, cur - (afirst ? 1 : 0), hf.warRounds);
            game.UpdateAttackOrder();
            for (var r = 1; r <= hf.warRounds; r++) {
                var ao = ph['AO' + r];
                if (r < cur) {
                    ao.gotoAndStop('FINISHED');
                } else if (r > cur || afirst) {
                    ao.gotoAndStop('PLAYER0');
                } else {
                    ao.gotoAndStop('PLAYER' + game.game_lpnum);
                }
            }
        };
    }

    // ------------------------------------------------------------------ pontszámláló

    // A játékosdobozok pontszáma fehér, mint a többi szám. A számláló "hardlight" keverési módban
    // van a sötét dobozon; a Ruffle ezt sötétre rajzolja, ezért normál módra váltunk.
    game.hfOrigInitMap = game.InitMap;
    game.InitMap = function (mapobj) {
        game.hfOrigInitMap(mapobj);
        hf.later(100, function () {
            hf.fitBoxNames();          // a változóhoz kötött név a következő képkockán frissül
        });
        for (var p = 1; p <= 3; p++) {
            var anim = gmap.playerbox[p].SCOREDISPLAY.SCOREANIM;
            anim.blendMode = 'normal';
            for (var d = 1; d <= 4; d++) {
                for (var n = 0; n <= 10; n++) {
                    var t = anim.SCORE['DIGIT' + d]['N' + n];
                    var f = t.getNewTextFormat();
                    f.color = 0xFFFFFF;
                    t.setNewTextFormat(f);
                    t.textColor = 0xFFFFFF;
                }
            }
        }
    };

    // ------------------------------------------------------------------ válaszdobozok

    // A feleletválasztós válaszok mezője egy sornyi magas (22 px-es betű), de sortörő: a hosszabb
    // válasz második sora levágódott ("olasz származású" a "...cukrász feltalálójáról" helyett). Ha
    // nem fér el egy sorban, kisebb betűvel egy sorba írjuk; ha úgy túl apró lenne, két sorba, és a
    // mező is magasabb lesz. A szöveg függőlegesen középen marad (mint a game.SetQTMText-ben).
    game.hfOrigSetQTMText = game.SetQTMText;
    game.SetQTMText = function (amov, atext) {
        game.hfOrigSetQTMText(amov, atext);
        if (amov._parent.answernumber == undefined) {
            return;
        }
        var t = amov.TEXT;
        if (t.hfSize == undefined) {
            t.hfSize = t.getTextFormat().size;
            t.hfHeight = t._height;
        }
        var f = t.getTextFormat();
        f.size = t.hfSize;
        t.wordWrap = false;
        t._height = t.hfHeight;
        t.setTextFormat(f);
        var room = t._width / (t._xscale / 100) - 6;
        if (t.textWidth > room) {
            var one = Math.floor(t.hfSize * room / t.textWidth);
            if (one >= 15) {
                f.size = one;
                t.setTextFormat(f);
            } else {
                t.wordWrap = true;
                f.size = 15;
                t.setTextFormat(f);
                while (f.size > 11 && t.textHeight > f.size * 2.6) {
                    f.size--;
                    t.setTextFormat(f);
                }
                t._height = Math.max(t.hfHeight, (t.textHeight + 6) * t._yscale / 100);
            }
        }
        t._y = amov.origheight / 2 - t.textHeight / 2 - 4;
    };

    // ------------------------------------------------------------------ nevek

    // A nevek a térképen (a vár fölött), a tippkártyákon és a harc fejlécében egysoros magasságú,
    // de sortörő mezőkben vannak: a kétszavas név második szava a második, már nem látható sorba
    // került. Egy sorban hagyjuk, és ha így nem fér el, kisebb betűvel írjuk.
    function fitName(t) {
        if (t == undefined) {
            return;
        }
        if (t.hfSize == undefined) {
            t.hfSize = t.getTextFormat().size;
            t.wordWrap = false;
            t.multiline = false;
        }
        var f = t.getTextFormat();
        f.size = t.hfSize;
        t.setTextFormat(f);
        var room = t._width / (t._xscale / 100) - 6;
        if (t.textWidth > room) {
            f.size = Math.max(10, Math.floor(t.hfSize * room / t.textWidth));
            t.setTextFormat(f);
        }
    }

    // a játékosdoboz neve (a NAME változóhoz kötött, névtelen mező a doboz 16. mélységén)
    function boxNameField(box) {
        var found = undefined;
        for (var k in box) {
            var o = box[k];
            if (found == undefined && o instanceof TextField && o.variable == 'NAME') {
                found = o;
            }
        }
        if (found == undefined && box.getInstanceAtDepth(16) instanceof TextField) {
            found = box.getInstanceAtDepth(16);
        }
        return found;
    }
    hf.fitBoxNames = function () {
        for (var p = 1; p <= 3; p++) {
            fitName(boxNameField(gmap.playerbox[p]));
        }
    };

    game.hfOrigUpdateBase = game.UpdateBase;
    game.UpdateBase = function (pnum) {
        game.hfOrigUpdateBase(pnum);
        var area = game.bases[pnum].area;
        if (area > 0) {
            fitName(gmap.MAP['CASTLE' + area].OWNERNAME);
        }
    };

    game.hfOrigDrawPlayerTips = game.DrawPlayerTips;
    game.DrawPlayerTips = function () {
        game.hfOrigDrawPlayerTips();
        for (var i = 1; i <= 3; i++) {
            fitName(gmap.QUESTIONWIN['TIPBOX' + i].PLAYERNAME);
        }
    };

    game.hfOrigDrawWarHeader = game.DrawWarHeader;
    game.DrawWarHeader = function () {
        game.hfOrigDrawWarHeader();
        fitName(gmap.QUESTIONWIN.OFFENDER);
        fitName(gmap.QUESTIONWIN.DEFENDER);
    };

    // ------------------------------------------------------------------ hangok

    // A próbajáték néhány eseményhez nem kért hangot; ezek a hívások itt vannak:
    //   game_start     a játék kezdete (bázissorsolás)
    //   castle_appear  egy bázis (vár) lehelyezése: ilyenkor ez szól a terület kiszíneződése helyett
    //   answer_pick    valaki (te vagy egy ellenfél) válaszol vagy tippel (lent: válaszhangok)
    //   game_end       a játék vége
    // A megye kiszíneződésének és a bázis lehelyezésének hangja a végén már tartalmazza a pontszám
    // változásának hangját, ezért közvetlenül utánuk (a pontszám kb. 0,8 mp-cel később változik)
    // a playerbox_scorechange nem szól külön. A game_end eleje a körváltás hangja, ezért az utolsó
    // kör végén a turnwindow_roundchange nem szól (csak ha döntetlen miatt még egy tippkérdés jön).
    var fillTime = -100000;
    var lastRoundEnd = false;
    var cardsAppearing = false;
    hf.soundName = function (n) {
        if (hf.mp && hf.mp.catchingUp) {
            return null;                // többjátékos felzárkózás közben csend
        }
        if (n == 'question_resultsappear' && cardsAppearing) {
            return null;                // a tippkártyák megjelenése néma (lent: tippek eredménye)
        }
        if (n == 'turnwindow_roundchange' && lastRoundEnd) {
            lastRoundEnd = false;
            if (!tieFollows()) {
                return null;
            }
        }
        if (n == 'map_territoryfill' && game.state == 1) {
            n = 'castle_appear';
        }
        if (n == 'map_territoryfill' || n == 'castle_appear') {
            fillTime = getTimer();
        }
        if (n == 'playerbox_scorechange' && getTimer() - fillTime < 2500) {
            return null;
        }
        return n;
    };

    // az utolsó harci kör vége (game.SqSetupRoundEnd: a körjelző lekapcsolása) - utána vége a harcnak
    game.hfOrigSqSetupRoundEnd = game.SqSetupRoundEnd;
    game.SqSetupRoundEnd = function () {
        lastRoundEnd = game.state == 4 && (game.gameround >= hf.warRounds || hf.soleOwner() > 0);
        game.hfOrigSqSetupRoundEnd();
    };

    // döntetlen miatti tippkérdés jön-e (game.CalculateEqualPoints, mellékhatások nélkül)
    function tieFollows() {
        var p = game.points;
        return (p[1] > 0 && (p[1] == p[2] || p[1] == p[3])) || (p[2] > 0 && p[2] == p[3]);
    }

    // ------------------------------------------------------------------ válaszhangok

    // Az answer_pick abban a pillanatban szól, amikor valaki válaszol vagy tippel: te a gomb
    // megnyomásakor, egy ellenfél a többjátékos játékban, amikor a válasza beérkezik (src/mp.as). A
    // robotok is gondolkodnak (A.thinkTime): egyjátékos módban az eredmény megvárja őket, mint egy
    // élő ellenfelet (közben a várakozó doboz), és a tippek gyorsasági sorrendje a valódi
    // válaszidőket követi. A lejárt idő nem válasz, ilyenkor nincs hang. Amikor egy tippkártyán
    // előkerül az érték (a tiéd is), a question_resultsappear szól (tip_appear, sounds/manifest.json).
    var A = {due: [], times: {}};   // due: [{seat, t}] a robotok még hátralévő válaszai
    hf.answers = A;

    // a kérdésben részt vevő többi játékos (tippkérdés vagy feleletválasztós)
    A.others = function (tip) {
        var list = [];
        for (var p = 1; p <= 3; p++) {
            var inq = tip ? (game.tipplayers & (1 << (p - 1))) != 0 : (p == game.offender || p == game.defender);
            if (inq && p != game.IAM) {
                list.push(p);
            }
        }
        return list;
    };

    // Egy robot gondolkodási ideje egy kérdésnél (ms). Előbb elolvassa a kérdést (2 mp és a
    // szöveg hosszával arányos idő), aztán gondolkodik: a hátralévő idő egy véletlen részéig (két
    // véletlen szám átlaga, így a közepes idők a gyakoribbak). A játék elején, amíg mindenki
    // belejön, még lassabb. A lejárt idő előtt mindig válaszol.
    A.thinkTime = function (secs, tip) {
        var q = tip ? game.tipquestiontag : game.questiontag;
        var len = q == undefined ? 60 : String(q.QUESTION).length;
        if (!tip && q != undefined) {
            len += (String(q.A1) + q.A2 + q.A3 + q.A4).length / 2;
        }
        var limit = secs * 1000 - 800;
        var read = Math.min(limit * 0.55, 2000 + len * 25);
        var u = (random(1000) + random(1000)) / 2000;
        var t = read + (limit - read) * u * (tip ? 0.85 : 0.5);
        if (game.state == 3 && game.gameround <= 2) {
            t += 1200 - (game.gameround - 1) * 600;     // az első két tippkérdés
        } else if (game.state == 4 && game.gameround == 1) {
            t += 1000;                                  // az első harci kör
        }
        return Math.round(Math.min(limit, t));
    };

    // A próbajáték robotjai annál pontosabbak, minél jobban vezetsz (game.CalcRobotCompensation,
    // -1..1): 800 pont előnynél a tippjük szinte hibátlan volt, a feleletválasztósnál 90%-ban jó. A
    // kiegyenlítés itt a fele: a tippek szórása a tartomány 25-75%-a, a jó válasz esélye 30-70%.
    if (hf.mode != 'mp') {
        game.hfOrigCalcRobotCompensation = game.CalcRobotCompensation;
        game.CalcRobotCompensation = function (pnum) {
            return game.hfOrigCalcRobotCompensation(pnum) / 2;
        };
    }

    // a "Béla válaszát várjuk..." / "A többiek válaszát várjuk..." szöveg
    A.waitText = function (seats) {
        if (seats.length == 1) {
            return getlang('gameinfo_waitingforanswer', game.player_name[seats[0]]);
        }
        return getlang('gameinfo_waitoppanswers');
    };

    // egyjátékos mód: a kérdés kezdetén (az óra indulásakor) a robotok kisorsolják a válaszidejüket
    A.begin = function (robots, secs, tip) {
        A.due = [];
        A.times = {};
        A.clockStart = getTimer();
        for (var i = 0; i < robots.length; i++) {
            A.due.push({seat: robots[i], t: getTimer() + A.thinkTime(secs, tip)});
        }
    };
    A.tick = function () {              // src/core.as: 100 ms-onként
        var now = getTimer();
        for (var i = A.due.length - 1; i >= 0; i--) {
            if (A.due[i].t <= now) {
                A.times[A.due[i].seat] = now;
                if (hf.tipCards) {
                    hf.tipCards.set(A.due[i].seat, now - A.clockStart);
                }
                A.due.splice(i, 1);
                gsound.play('answer_pick');
            }
        }
    };
    A.pendingSeats = function () {
        var list = [];
        for (var i = 0; i < A.due.length; i++) {
            list.push(A.due[i].seat);
        }
        return list;
    };

    if (hf.mode != 'mp') {
        var origStartTip = game.StartTip;
        game.StartTip = function () {
            origStartTip.call(game);
            A.begin(A.others(true), gcfg.TIMEOUT_TIP, true);
            TC.reset();
        };
        // a saját tipped ideje (ha lejárt az idő, és üres volt a mező: -1)
        var origSendMyTip = game.SendMyTip;
        game.SendMyTip = function (atip, ahuman) {
            if (game.myanswer == -1) {
                TC.set(game.IAM, game.clocktimeout && hf.noTip ? -1 : getTimer() - A.clockStart);
            }
            origSendMyTip.call(game, atip, ahuman);
        };
        var origStartAnswer = game.StartAnswer;
        game.StartAnswer = function () {
            origStartAnswer.call(game);
            A.begin(A.others(false), gcfg.TIMEOUT_ANSWER, false);
        };
        // a tippek gyorsasági sorrendje (az eredetiben véletlen): ki mikor válaszolt; aki nem
        // válaszolt (lejárt az ideje), a végére kerül
        var origTipWinOrder = game.CalculateTipWinOrder;
        game.CalculateTipWinOrder = function () {
            var list = [];
            for (var i = 0; i < game.tiporder.length; i++) {
                var p = game.tiporder[i];
                list.push({p: p, i: i, t: A.times[p] == undefined ? 1000000000 : A.times[p]});
            }
            list.sort(function (a, b) {
                if (a.t != b.t) {
                    return a.t < b.t ? -1 : 1;
                }
                return a.i - b.i;
            });
            for (var j = 0; j < list.length; j++) {
                game.tiporder[j] = list[j].p;
            }
            origTipWinOrder.call(game);
        };
    }

    game.hfOrigBtnSendMyTipPressed = game.btnSendMyTipPressed;
    game.btnSendMyTipPressed = function (atip) {
        if (game.myanswer == -1) {
            gsound.play('answer_pick');
            A.times[game.IAM] = getTimer();
        }
        game.hfOrigBtnSendMyTipPressed(atip);
    };

    // Tippbeírás közben minden kattintás után a tippmezőé a fókusz. A Ruffle a lejátszóba tett
    // legelső kattintáskor (a robotjáték újratöltéssel indul, így ez gyakran az első tippkérdés
    // mezője) a billentyűzetet a saját elemére veszi át; a Flash szerint a mező fókuszban marad, így
    // senki sem adja vissza, és a leütések elvesznek. A fókusz le- és visszarakása visszaadja.
    // (Érintőképernyőn az oldal saját beviteli mezője kezeli a tippet: src/lobby.as, hf.touchText.)
    var tipFocus = {};
    tipFocus.onMouseUp = function () {
        var box = gmap.QUESTIONWIN.MYTIPBOX;
        if (hf.touchText && hf.touchText.on) {
            return;
        }
        if (game.myanswer != -1 || box == undefined || !box._visible || !gmap.QUESTIONWIN._visible) {
            return;
        }
        // a setFocus az egész szöveget kijelölné (a következő leütés felülírná): ahol a kattintás
        // hagyta a kurzort / kijelölést, ott marad; ha a mező nem volt fókuszban, a végére kerül
        var b = Selection.getBeginIndex();
        var e = Selection.getEndIndex();
        if (b < 0) {
            b = box.length;
            e = box.length;
        }
        Selection.setFocus(null);
        Selection.setFocus(box);
        Selection.setSelection(b, e);
    };
    Mouse.addListener(tipFocus);

    game.hfOrigAnswerPressed = game.btnAnswerPressed;
    game.btnAnswerPressed = function (ans) {
        if (ans > 0 && game.state == 4 && game.phase == 11 && !game.warobserver) {
            gsound.play('answer_pick');
        }
        game.hfOrigAnswerPressed(ans);
    };

    // Az eredmény hangját (nyertél / vesztettél / nem voltál benne) a próbajáték rossz adatból
    // választotta, mert a teljes kliens két mezőjét nem töltötte ki; itt kitöltjük őket.
    // a játékos statisztikája az eredménytábla "Válaszaid" oldalához
    hf.stats = {mcAll: 0, mcGood: 0, tipAll: 0, tipWon: 0, tipSecond: 0, bestDiff: -1, bestTip: 0, bestAnswer: 0};

    game.hfOrigSqSetupAnswerResult = game.SqSetupAnswerResult;
    game.SqSetupAnswerResult = function () {
        gmap.QUESTIONWIN.myanswer = game.warobserver ? -1 : Number(game.answers[game.IAM]);
        if (!game.warobserver) {
            hf.stats.mcAll++;
            if (Number(game.answers[game.IAM]) == game.goodanswerpos) {
                hf.stats.mcGood++;
            }
        }
        game.hfOrigSqSetupAnswerResult();
    };

    // ------------------------------------------------------------------ tippek eredménye

    // Amint beküldted a tippedet, látszik a kártyád (a tipped értékével) és azoké, akik már
    // tippeltek (az értékük még rejtve, de a tippelési idejük - egyenlő eltérésnél ez dönt), és aki
    // ezután tippel, annak is előjön a kártyája. Ha mindenki tippelt, a többi kártya azonnal ott
    // van, és tippelési sorrendben előkerülnek a többiek értékei, mindegyik question_resultsappear
    // hanggal (tip_appear). Az eredetiben a kártyák csak a végén, 2,5 mp alatt jöttek elő.
    var protoDelay = CSqControl.prototype.AddDelay;
    var protoPlaySound = CSqControl.prototype.AddPlaySound;
    var protoFadeIn = CSqControl.prototype.AddFadeIn;
    var TC = {times: {}, active: false};    // times[hely]: ms az óra indulásától, -1: lejárt
    hf.tipCards = TC;

    function isTipPlayer(p) {
        return (game.tipplayers & (1 << (p - 1))) != 0;
    }

    // "05.85"; lejárt idő: "--.--"
    function formatTime(ms) {
        if (ms == undefined || ms < 0) {
            return '--.--';
        }
        var cs = Math.round(ms / 10);
        var sec = Math.floor(cs / 100);
        var frac = cs % 100;
        return (sec < 10 ? '0' : '') + sec + '.' + (frac < 10 ? '0' : '') + frac;
    }

    // a kártya alsó sorában jobbra a tippelési idő egy stopperóra mellett (az első híváskor létrehozva)
    function cardTime(box, ms) {
        if (box.hfTime == undefined) {
            // a kártya 182 képpont széles: balra a név, jobbra az óra és az idő
            box.PLAYERNAME._width = 112;
            var icon = box.createEmptyMovieClip('hfClock', 50);
            icon._x = 126;
            icon._y = 58;
            icon.lineStyle(1.3, 0xE8D7AE, 100);
            icon.beginFill(0x2A170C, 100);
            icon.moveTo(5.5, 0);
            icon.curveTo(5.5, 5.5, 0, 5.5);
            icon.curveTo(-5.5, 5.5, -5.5, 0);
            icon.curveTo(-5.5, -5.5, 0, -5.5);
            icon.curveTo(5.5, -5.5, 5.5, 0);
            icon.endFill();
            icon.moveTo(0, -5.5);
            icon.lineTo(0, -7.5);
            icon.moveTo(0, 0);
            icon.lineTo(0, -3.5);
            icon.moveTo(0, 0);
            icon.lineTo(2.5, 1.3);
            box.hfTime = hf.ui.text(box, 132, 47, 48, 24, '', 15, 0xF2E6C9, true, 'right');
        }
        box.hfTime.text = formatTime(ms);
        fitName(box.PLAYERNAME);
    }

    // a kártyák kirajzolása: a tippelők közül azok, akiknek ismert az ideje, idő szerinti sorrendben
    // (a lejártak a végén); az értékek rejtve maradnak
    TC.draw = function (all) {
        var list = [];
        for (var p = 1; p <= 3; p++) {
            if (isTipPlayer(p) && (all || TC.times[p] != undefined)) {
                var t = TC.times[p];
                list.push({p: p, t: (t == undefined || t < 0) ? 1000000000 : t});
            }
        }
        list.sort(function (a, b) {
            if (a.t != b.t) {
                return a.t < b.t ? -1 : 1;
            }
            return a.p - b.p;
        });
        var saveOrder = game.tiporder;
        var saveCount = game.tipcount;
        if (!all) {
            game.tiporder = [];
            for (var i = 0; i < list.length; i++) {
                game.tiporder.push(list[i].p);
            }
            game.tipcount = list.length;
        }
        cardsAppearing = true;
        game.DrawPlayerTips();
        cardsAppearing = false;
        game.tiporder = saveOrder;
        game.tipcount = saveCount;
        for (var j = 0; j < list.length; j++) {
            var box = gmap.QUESTIONWIN['TIPBOX' + game.tipboxorder[list[j].p]];
            box.TIPVALMOV._visible = list[j].p == game.IAM;     // a saját tipped rögtön látszik
            box._alpha = 100;
            cardTime(box, TC.times[list[j].p]);
        }
    };

    // a kérdés eleje (az óra indulása): még senki sem tippelt
    TC.reset = function () {
        TC.times = {};
        TC.active = false;
    };
    // a saját tipped után (vagy ha nem tippelsz): az eredményablak, a kártyák a beérkezés szerint
    TC.start = function () {
        TC.active = true;
        TC.draw(false);
    };
    TC.set = function (seat, ms) {
        if (TC.times[seat] != undefined) {
            return;
        }
        TC.times[seat] = ms;
        if (TC.active) {
            TC.draw(false);
        }
    };

    // mindenki tippelt: a hiányzó kártyák azonnal (a sorrend már a végleges game.tiporder)
    game.hfOrigSqSetupTipResult1 = game.SqSetupTipResult1;
    game.SqSetupTipResult1 = function (tag) {
        TC.draw(true);
        TC.active = false;
        gsqc.Clear();
        gsqc.AddDelay(150);
    };

    // harci tippverseny: a próbajáték a player_tips[1..tipcount].player mezőben keresi magát
    game.hfOrigSqSetupTipResult2 = game.SqSetupTipResult2;
    game.SqSetupTipResult2 = function (tag) {
        var inTip = (game.tipplayers & (1 << (game.IAM - 1))) != 0;
        for (var p = 1; p <= 3; p++) {
            game.player_tips[p].player = (p == 1 && inTip) ? game.IAM : 0;
        }
        if (inTip) {
            var st = hf.stats;
            st.tipAll++;
            if (game.tipwinorder[0] == game.IAM) {
                st.tipWon++;
            } else if (game.tipwinorder[1] == game.IAM && game.state == 3) {
                st.tipSecond++;
            }
            var mine = Number(game.player_tips[game.IAM].value);
            var ans = Number(game.tipquestiontag.ANSWER);
            if (st.bestDiff < 0 || Math.abs(mine - ans) < st.bestDiff) {
                st.bestDiff = Math.abs(mine - ans);
                st.bestTip = mine;
                st.bestAnswer = ans;
            }
        }
        // A felfedés üteme pontos időpontokhoz kötve (a sorozat lépései nem késleltetik): az első
        // érték 0,4 mp múlva, utána 0,6 mp-enként (az eredetiben 0,5), a helyes válasz az utolsó után
        // 1,6 mp-cel. Az értékek 0,3 mp alatt tűnnek elő, a sorozatot ez nem tartja fel.
        var base = 0;
        var n = 0;
        var first = true;
        var times = [];                                 // a hangok időpontjai (base-hez képest)
        // várakozás egy időpontig (base-hez képest), aztán action
        function at(ms, action) {
            var o = protoDelay.call(gsqc, 0);
            o.Start = function () {
            };
            o.Step = function () {
                if (getTimer() >= base + ms) {
                    if (action) {
                        action();
                    }
                    this.sqc.AnimFinished('DELAY');
                }
            };
            return o;
        }
        gsqc.AddDelay = function (ms) {
            if (first) {                                // a sorozat eleje: innen mérünk
                first = false;
                var o = protoDelay.call(gsqc, 0);
                o.Start = function () {
                    base = getTimer() + 400;
                    // a hangokat az oldal pontosan időzíti; a képek a képkockákhoz igazodnak
                    for (var i = 0; i < times.length; i++) {
                        gsound.playAt(times[i].snd, base + times[i].ms - getTimer());
                    }
                };
                o.Step = function () {
                    this.sqc.AnimFinished('DELAY');
                };
                return o;
            }
            if (ms == 200) {                            // az értékek közti szünet: nincs
                return protoDelay.call(gsqc, 0);
            }
            return protoDelay.call(gsqc, ms);
        };
        gsqc.AddPlaySound = function (snd, loop) {
            if (snd == 'tip_appear') {
                times.push({snd: snd, ms: n * 600});
                n++;
                return at(times[times.length - 1].ms, null);
            }
            if (snd == 'question_goodappear') {
                times.push({snd: snd, ms: (n - 1) * 600 + 1600});
                return at(times[times.length - 1].ms, null);
            }
            return protoPlaySound.call(gsqc, snd, loop);
        };
        // az érték előtűnése nem tartja fel a sorozatot (a következő érték időpontja már kötött)
        gsqc.AddFadeIn = function (obj, ms, fadein) {
            if (ms != 300 || !fadein) {
                return protoFadeIn.call(gsqc, obj, ms, fadein);
            }
            var o = protoDelay.call(gsqc, 0);
            o.Start = function () {
                var t0 = getTimer();
                obj._alpha = 0;
                obj._visible = true;
                obj.onEnterFrame = function () {
                    obj._alpha = Math.min(100, (getTimer() - t0) / 3);
                    if (obj._alpha >= 100) {
                        obj.onEnterFrame = undefined;
                    }
                };
            };
            o.Step = function () {
                this.sqc.AnimFinished('DELAY');
            };
            return o;
        };
        game.hfOrigSqSetupTipResult2(tag);
        gsqc.AddDelay = protoDelay;
        gsqc.AddFadeIn = protoFadeIn;
        gsqc.AddPlaySound = protoPlaySound;
    };

    // ------------------------------------------------------------------ menü

    hf.menuWaiting = function () {
        return (game.state == 1 && game.phase == 2) || game.state == 16;
    };

    game.hfOrigSqSetupGameMenu = game.SqSetupGameMenu;
    game.SqSetupGameMenu = function () {
        game.hfOrigSqSetupGameMenu();
        gmov.GAMESELECTWIN.BTN4.tooltiptext = 'Visszajelzés';
        // meghívó linkkel (?join=KÓD) vagy egy szobából frissítve rögtön a szoba-ablak jön
        if (!hf.autoLobbyDone) {
            hf.autoLobbyDone = true;
            var h = hf.net.call('hello');
            if (h && (h.join || h.inLobby)) {
                hf.lobby.open();
            }
        }
    };

    game.hfOrigGameMenuSelected = game.GameMenuSelected;
    game.GameMenuSelected = function (amenu) {
        if (amenu == 3) {
            hf.lobby.open();
            return;
        }
        if (amenu == 4) {
            hf.feedback.open();
            return;
        }
        if (amenu == 1 && game.state == 1) {
            gsound.play('game_start');
        }
        if (amenu == 1 && game.state != 1) {     // a játék végi menüből: új bemutató játék
            getURL('?gamemode=0', '_self');
            return;
        }
        if (amenu == 2) {
            getURL('?gamemode=1', '_self');
            return;
        }
        game.hfOrigGameMenuSelected(amenu);
    };

    // ennyi ideig gondolkodik egy robot a területválasztáson (ms)
    hf.robotThinkTime = function () {
        return 800 + random(1200);
    };
    var robotThinking = false;

    // ------------------------------------------------------------------ korai győzelem

    // Ha valaki az összes ellenfele várát lerombolta, minden terület az övé: a játéknak vége. Az
    // eredeti ilyenkor tovább sorsolta a köröket, és a győztes nem létező ("undefined") játékosokat
    // támadott, a pontszámok is elromlottak. A kiesés sorrendje a végeredményben számít: aki később
    // esett ki, az előrébb végez (src/lobby.as).
    hf.soleOwner = function () {
        var owner = game.areas[1].owner;
        for (var i = 2; i <= gmap.areanum; i++) {
            if (game.areas[i].owner != owner) {
                return 0;
            }
        }
        return owner > 0 ? owner : 0;
    };
    hf.outOrder = [];          // a kiesett játékosok, a kiesés sorrendjében
    function noteKnockouts() {
        for (var p = 1; p <= 3; p++) {
            var known = false;
            for (var i = 0; i < hf.outOrder.length; i++) {
                if (hf.outOrder[i] == p) {
                    known = true;
                }
            }
            if (!known && game.points[p] <= 0) {
                hf.outOrder.push(p);
            }
        }
    }

    game.hfOrigHandleGamePhase = game.HandleGamePhase;
    game.HandleGamePhase = function () {
        if (game.state == 4 && game.phase == 40) {
            noteKnockouts();
        }
        if (game.state == 4 && game.phase == 1 && hf.soleOwner() > 0) {
            game.SetNextState(15, 1, 0);
            game.NextPhase();
            return;
        }
        // a főoldalon a bázissorsolás a menü mögött zajlik: ott a "Bemutató játék" gomb indítja a játékot
        if (game.state == 1 && game.phase == 0 && hf.mode != 'menu') {
            gsound.play('game_start');
        }
        if (game.state == 15 && game.phase == 2) {
            gsound.play('game_end');
        }
        // amíg egy ablak nyitva van, a menü sem tűnik el, a bemutató játék nem indul el
        if (game.state == 1 && game.phase == 2 && hf.ui.isOpen()) {
            return;
        }
        if (game.state == 15 && game.phase == 3) {
            // a játék végén az eredménytábla (src/lobby.as), utána a kezdőmenü (bemutató / robotok /
            // ismerős / visszajelzés). A menü beúszása végén a gsqc game.NextPhase()-t hív: a 16-os
            // állapotot az eredeti HandleGamePhase nem ismeri, így ez nem ismétlődik.
            game.SetNextState(16, 1, 0);
            hf.results.show(function () {
                game.SqSetupGameMenu();
                gsqc.Start();
                gmov.GAMESELECTWIN.BTN2.onRollOver();
            });
            return;
        }
        if (hf.mp) {
            hf.mp.handlePhase();
            return;
        }
        // egyjátékos módban az eredmény megvárja a még gondolkodó robotokat (0,6 mp után a
        // várakozó doboz mutatja, kire várunk), mint a többjátékos játékban az élő ellenfeleket
        var s = game.state;
        var ph = game.phase;
        if (((s == 3 && ph == 4) || (s == 4 && ph == 22) || (s == 5 && ph == 4) || (s == 4 && ph == 12)) && A.due.length > 0) {
            var since = getTimer();
            if (ph != 12 && !TC.active) {
                game.DrawQuestionWin(s == 4 ? 'WARTIPRESULT' : '3TIPRESULT');
                TC.start();
            }
            var poll = function () {
                if (A.due.length == 0) {
                    hf.wait.hide();
                    game.HandleGamePhase();         // ugyanez a fázis, most már mindenki válaszolt
                    return;
                }
                if (getTimer() - since > 600) {
                    hf.wait.show(A.waitText(A.pendingSeats()));
                }
                hf.later(100, poll);
            };
            poll();
            return;
        }
        // egyjátékos módban a robot is "gondolkodik" egy kicsit, mielőtt területet választ vagy
        // támad; addig a várakozó doboz mutatja, ki lép (src/lobby.as: hf.wait)
        if ((game.state == 3 || game.state == 4) && game.phase == 8 && game.nextplayer != game.IAM && !robotThinking) {
            robotThinking = true;
            hf.wait.show(hf.wait.selectMsg(game.nextplayer));
            hf.later(hf.robotThinkTime(), function () {
                robotThinking = false;
                hf.wait.hide();
                game.hfOrigHandleGamePhase();       // a robot választása (a fázis még mindig 8)
            });
            return;
        }
        game.hfOrigHandleGamePhase();
    };

    if (!full) {
        // a bemutató búcsúszövege a regisztráció helyett a többjátékos módra hív
        game.hfOrigAddTutorial = game.AddTutorial;
        game.AddTutorial = function (apos, amsg, abtn) {
            if (game.state == 15) {
                amsg = 'Gratulálunk! Sikeresen megküzdöttél virtuális ellenfeleiddel. Hívd meg a barátaidat a "Gyors játék egy ismerőssel" gombbal, és játssz valódi emberek ellen!';
                abtn = getlang('next');
            }
            game.hfOrigAddTutorial(apos, amsg, abtn);
        };
        return;
    }

    // ------------------------------------------------------------------ teljes játék

    // nincsenek oktató ablakok (a játék végén az eredménytábla jön, src/lobby.as)
    game.AddTutorial = function (apos, amsg, abtn) {
        if (game.state == 15 && hf.mode == 'mp') {
            hf.net.call('leave');       // a szoba a játék végén megszűnik ennek a játékosnak
        }
    };

    // Az óraütem (gtimer) ez után a hívás után kikapcsolja az órát (gtimer.clock_on = false). Ha a
    // lejárt lépés után azonnal a következő óra indulna (felzárkózáskor a köztes fázisok egy
    // pillanat alatt lefutnak), az új óra sosem járna le, és a játék ennél a játékosnál megállna.
    // Ezért a lépés egy ütemmel később megy el - ha közben nem léptél (kattintással), azaz a játék
    // még ugyanabban a fázisban van.
    game.OnClockTimeout = function (param) {
        game.clocktimeout = true;
        game.StopClock();
        var st = game.state;
        var ph = game.phase;
        hf.later(0, function () {
            if (game.state == st && game.phase == ph) {
                clockTimeoutStep(param);
            }
        });
    };

    function clockTimeoutStep(param) {
        if (param == 1) {
            game.btnAreaPressed(0);
        } else if (param == 2) {
            game.btnAnswerPressed(0);
        } else if (param == 3) {
            // a beírt szám megy el (akkor is, ha nem küldted el); üres mezőnél 0, ez nem számít tippnek
            var typed = '';
            var s = String(gmap.QUESTIONWIN.MYTIP);
            for (var i = 0; i < s.length; i++) {
                if ('0123456789'.indexOf(s.charAt(i)) >= 0) {
                    typed += s.charAt(i);
                }
            }
            hf.noTip = typed == '';
            gmap.QUESTIONWIN.MYTIP = hf.noTip ? '0' : typed;
            game.SendMyTip(gmap.QUESTIONWIN.MYTIP, false);
        }
    }

    // nincs "Lejárt az időd. A rendes játékban..." próbajáték-tipp
    game.OnClockTimeoutEnd = function () {
    };

    // nincs "Gyorsjáték indítása" gomb
    game.hfOrigSqSetupGameStart = game.SqSetupGameStart;
    game.SqSetupGameStart = function () {
        game.hfOrigSqSetupGameStart();
        gmap.BTNQUICKGAME._x = -3000;
        gmov.BTNQUICKGAME._x = -3000;
    };
})();
