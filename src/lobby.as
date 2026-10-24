// Ablakok Flash-ben: szoba (létrehozás / csatlakozás / várakozás) és visszajelzés.
// Egyszerű, rajzolt ablakok a játék betűtípusával (Book Antiqua, a SWF-be ágyazva).

(function () {
    var hf = _global.hf;
    var FONT = 'Book Antiqua';
    var W = 600;
    var H = 500;
    var C = {
        panel: 0x3B2213, border: 0xC89B4A, title: 0xFFE39A, text: 0xF3DDB0, dim: 0xC2A27A,
        input: 0xFFF6DD, inputText: 0x2D190E, btn: 0x7A4E1D, btnHi: 0xA9732C, btnText: 0xFFF3D0,
        err: 0xFF9C7A, ok: 0xB8E08A
    };
    var SEATCOLOR = [0, 0xC0242B, 0xC9B320, 0x3E9A2E];   // az eredeti játékosszínek: piros, sárga, zöld

    // tartalék robotnév (a szobában a robotok nevét a szerver adja: server/lobby.go, robotName)
    hf.robotName = function (seat) {
        return getlang(seat == 3 ? 'tutorial_opponent_2' : 'tutorial_opponent_1');
    };

    var ui = {};
    hf.ui = ui;
    var panel = null;
    var panelNo = 0;        // az ablakok sorszáma (az új ablak ugyanazon a néven jön létre, mint a régi)
    var onEnter = null;

    // ------------------------------------------------------------------ rajzolás

    function rect(mc, x, y, w, h, r, fill, alpha, line) {
        if (line == undefined) {
            mc.lineStyle();
        } else {
            mc.lineStyle(2, line, 100);
        }
        mc.beginFill(fill, alpha);
        roundPath(mc, x, y, w, h, r);
        mc.endFill();
    }

    // függőleges színátmenet c1-ből (fent) c2-be (lent)
    function grad(mc, x, y, w, h, r, c1, c2, line) {
        if (line == undefined) {
            mc.lineStyle();
        } else {
            mc.lineStyle(2, line, 100);
        }
        mc.beginGradientFill('linear', [c1, c2], [100, 100], [0, 255],
            {matrixType: 'box', x: x, y: y, w: w, h: h, r: Math.PI / 2});
        roundPath(mc, x, y, w, h, r);
        mc.endFill();
    }

    function roundPath(mc, x, y, w, h, r) {
        mc.moveTo(x + r, y);
        mc.lineTo(x + w - r, y);
        mc.curveTo(x + w, y, x + w, y + r);
        mc.lineTo(x + w, y + h - r);
        mc.curveTo(x + w, y + h, x + w - r, y + h);
        mc.lineTo(x + r, y + h);
        mc.curveTo(x, y + h, x, y + h - r);
        mc.lineTo(x, y + r);
        mc.curveTo(x, y, x + r, y);
    }

    function circle(mc, x, y, r, fill) {
        mc.lineStyle(1, 0x000000, 60);
        mc.beginFill(fill, 100);
        mc.moveTo(x + r, y);
        var n = 8;
        for (var i = 1; i <= n; i++) {
            var a = i * Math.PI * 2 / n;
            var m = (i - 0.5) * Math.PI * 2 / n;
            var k = r / Math.cos(Math.PI / n);
            mc.curveTo(x + Math.cos(m) * k, y + Math.sin(m) * k, x + Math.cos(a) * r, y + Math.sin(a) * r);
        }
        mc.endFill();
    }

    ui.text = function (mc, x, y, w, h, str, size, color, bold, align, multi) {
        var d = mc.getNextHighestDepth();
        mc.createTextField('t' + d, d, x, y, w, h);
        var t = mc['t' + d];
        var f = new TextFormat();
        f.font = FONT;
        f.size = size;
        f.color = color;
        f.bold = bold == true;
        f.align = align ? align : 'left';
        t.embedFonts = true;
        t.selectable = false;
        t.multiline = multi == true;
        t.wordWrap = multi == true;
        t.setNewTextFormat(f);
        t.text = str;
        return t;
    };

    ui.input = function (mc, x, y, w, h, str, maxChars, multi) {
        var t = ui.text(mc, x, y, w, h, str, 16, C.inputText, false, 'left', multi);
        t.type = 'input';
        t.selectable = true;
        t.background = true;
        t.backgroundColor = C.input;
        t.border = true;
        t.borderColor = C.border;
        t.maxChars = maxChars;
        return t;
    };

    ui.button = function (mc, x, y, w, h, label, onClick) {
        var d = mc.getNextHighestDepth();
        var b = mc.createEmptyMovieClip('b' + d, d);
        b._x = x;
        b._y = y;
        var draw = function (hi) {
            b.clear();
            rect(b, 0, 0, w, h, 7, hi ? C.btnHi : C.btn, 100, C.border);
        };
        draw(false);
        b.label = ui.text(b, 0, Math.round((h - 23) / 2), w, 26, label, 15, C.btnText, true, 'center');
        b.onRollOver = function () {
            draw(true);
        };
        b.onRollOut = function () {
            draw(false);
        };
        b.onDragOut = b.onRollOut;
        b.onRelease = function () {
            draw(false);
            onClick();
        };
        b.setOn = function (on) {
            b.enabled = on;
            b._alpha = on ? 100 : 40;
        };
        return b;
    };

    // link: aláhúzott, kattintható felirat (új lapon nyitja meg az url-t)
    ui.link = function (mc, x, y, label, url) {
        var d = mc.getNextHighestDepth();
        var l = mc.createEmptyMovieClip('l' + d, d);
        l._x = x;
        l._y = y;
        var t = ui.text(l, 0, 0, 200, 22, label, 14, C.title);
        var f = t.getTextFormat();
        f.underline = true;
        t.setTextFormat(f);
        t._width = t.textWidth + 6;
        l.onRelease = function () {
            getURL(url, '_blank');
        };
        return l;
    };

    // kis ikonok a szoba-ablak soraiban: korona (a szoba főnöke) és robot (a főnök betette robot)
    function drawCrown(mc) {
        mc.clear();
        mc.lineStyle(1, 0x6B5A48, 100);
        mc.beginFill(0xC2A27A, 100);
        mc.moveTo(0, 12);
        mc.lineTo(17, 12);
        mc.lineTo(17, 10);
        mc.lineTo(15, 2);
        mc.lineTo(11.5, 7);
        mc.lineTo(8.5, 0);
        mc.lineTo(5.5, 7);
        mc.lineTo(2, 2);
        mc.lineTo(0, 10);
        mc.lineTo(0, 12);
        mc.endFill();
    }

    function drawRobot(mc) {
        mc.clear();
        mc.lineStyle(1.4, 0xC2A27A, 100);
        mc.moveTo(7, 0.5);
        mc.lineTo(7, 3);
        mc.beginFill(0x6B5A48, 100);
        roundPath(mc, 1, 3, 12, 10, 3);
        mc.endFill();
        mc.lineStyle();
        mc.beginFill(0xFFE39A, 100);
        roundPath(mc, 3.6, 6, 2.4, 2.4, 1);
        roundPath(mc, 8, 6, 2.4, 2.4, 1);
        mc.endFill();
        mc.lineStyle(1, 0xC2A27A, 100);
        mc.moveTo(4.5, 10.5);
        mc.lineTo(9.5, 10.5);
    }

    // ikon (betöltött kép, legfeljebb 42×28 képpont) és mellette felirat: set(kép címe, felirat)
    ui.iconLabel = function (mc, x, y, w) {
        var d = mc.getNextHighestDepth();
        var c = mc.createEmptyMovieClip('i' + d, d);
        c._x = x;
        c._y = y;
        var label = ui.text(c, 48, 3, w - 48, 24, '', 15, C.text);
        var url = '';
        var o = {clip: c};
        o.width = function () {
            return 48 + Math.round(label.textWidth) + 4;
        };
        o.set = function (u, text) {
            label.text = text;
            if (u == url) {
                return;
            }
            url = u;
            var holder = c.createEmptyMovieClip('img', 1);
            var mcl = new MovieClipLoader();
            mcl.addListener({onLoadInit: function (t) {
                t.forceSmoothing = true;
                var k = Math.min(28 / t._height, 42 / t._width);
                t._xscale = k * 100;
                t._yscale = k * 100;
                t._x = Math.round((42 - t._width) / 2);
                t._y = Math.round((28 - t._height) / 2);
            }});
            mcl.loadClip(u, holder);
        };
        return o;
    };

    // jelölőnégyzet: kattintásra (a felirat is kattintható) vált, onChange(be van-e jelölve)
    ui.checkbox = function (mc, x, y, w, label, on, onChange) {
        var d = mc.getNextHighestDepth();
        var c = mc.createEmptyMovieClip('c' + d, d);
        c._x = x;
        c._y = y;
        c.on = on == true;
        ui.text(c, 26, 0, w - 26, 24, label, 15, C.text);
        var draw = function (hi) {
            c.clear();
            rect(c, 0, 0, w, 24, 0, 0x000000, 0);           // a felirat is kattintható
            rect(c, 0, 3, 18, 18, 4, hi ? C.btnHi : C.btn, 100, C.border);
            if (c.on) {
                c.lineStyle(3, C.title, 100);
                c.moveTo(4, 12);
                c.lineTo(8, 17);
                c.lineTo(15, 7);
            }
        };
        draw(false);
        c.onRollOver = function () {
            draw(true);
        };
        c.onRollOut = function () {
            draw(false);
        };
        c.onDragOut = c.onRollOut;
        c.onRelease = function () {
            c.on = !c.on;
            draw(true);
            onChange(c.on);
        };
        return c;
    };

    // beviteli mező be- és kikapcsolása (kikapcsolva halványabb, nem írható)
    ui.setInputOn = function (t, on) {
        t.type = on ? 'input' : 'dynamic';
        t.selectable = on;
        t.backgroundColor = on ? C.input : 0xB9A98A;
        t.textColor = on ? C.inputText : 0x6B5A48;
    };

    // ------------------------------------------------------------------ ablak

    function menuWaiting() {
        return game.state == 16 || (hf.mode == 'menu' && (game.state == 1 || game.state == 11));
    }

    ui.open = function (w, h, title) {
        ui.close();
        var p = _root.createEmptyMovieClip('hfPanel', 20000);
        panel = p;
        panelNo++;
        var bg = p.createEmptyMovieClip('bg', 0);
        rect(bg, 0, 0, W, H, 0, 0x000000, 55);
        bg.onRelease = function () {
        };
        bg.useHandCursor = false;
        var box = p.createEmptyMovieClip('box', 1);
        box._x = Math.round((W - w) / 2);
        box._y = Math.round((H - h) / 2);
        rect(box, 0, 0, w, h, 12, C.panel, 97, C.border);
        box.titleText = ui.text(box, 0, 12, w, 34, title, 22, C.title, true, 'center');
        gmov.GAMESELECTWIN._visible = false;
        hidePhase();
        return box;
    };

    // A főoldalon a menü mögött az eredeti játék bázist sorsol (díszletként): amíg egy ablak nyitva
    // van, a fázis felirata ("Bázis sorsolás") ne látsszon át rajta.
    var phaseWasVisible = null;
    function hidePhase() {
        if (hf.mode != 'menu') {
            return;
        }
        if (phaseWasVisible == null) {
            phaseWasVisible = gmap.GAMEPHASE._visible;
        }
        gmap.GAMEPHASE._visible = false;
    }
    function restorePhase() {
        if (phaseWasVisible != null) {
            gmap.GAMEPHASE._visible = phaseWasVisible;
            phaseWasVisible = null;
        }
    }

    ui.close = function () {
        if (panel) {
            panel.removeMovieClip();
            panel = null;
            panelNo++;
        }
        hf.net.handler = null;
        onEnter = null;
        restorePhase();
        if (menuWaiting()) {
            gmov.GAMESELECTWIN._visible = true;
            gmov.GAMESELECTWIN._alpha = 100;
        }
    };

    ui.isOpen = function () {
        return panel != null;
    };

    // 100 ms-onként (src/core.as): a menü beúszása ne jöjjön át az ablakon
    ui.tick = function () {
        if (panel) {
            gmov.GAMESELECTWIN._visible = false;
            if (phaseWasVisible != null) {
                gmap.GAMEPHASE._visible = false;     // a sorsolás fázisváltása újra előhozná
            }
        }
        touch.tick();
    };

    // játék közbeni jelzés a jobb felső sarokban (pl. hogy a szoba megszűnt, vagy valaki kilépett);
    // ms: ennyi idő múlva eltűnik (különben ott marad). A hosszabb szöveg két sorba tördelődik.
    ui.notice = function (msg, ms) {
        ui.hideNotice();
        var n = _root.createEmptyMovieClip('hfNotice', 19990);
        var t = ui.text(n, 10, 5, 250, 24, msg, 15, C.title, true, 'center');
        var w = Math.min(270, Math.round(t.textWidth) + 34);
        var h = 32;
        if (t.textWidth > 250) {
            t.multiline = true;
            t.wordWrap = true;
            t._height = 44;
            t.text = msg;
            h = 52;
        }
        t._x = Math.round((w - 250) / 2);
        rect(n, 0, 0, w, h, 9, C.panel, 92, C.border);
        n._x = 592 - w;
        n._y = 10;
        n._alpha = 0;
        var until = ms > 0 ? getTimer() + ms : 0;
        n.onEnterFrame = function () {
            if (until > 0 && getTimer() > until) {
                n._alpha -= 8;
                if (n._alpha <= 0) {
                    n.removeMovieClip();
                }
            } else if (n._alpha < 100) {
                n._alpha = Math.min(100, n._alpha + 12);
            }
        };
    };

    ui.hideNotice = function () {
        _root.hfNotice.removeMovieClip();
    };

    // ------------------------------------------------------------------ várakozás: homokóra

    // Az eredeti kliens várakozó doboza: amikor valaki másra kell várni (más választ területet vagy
    // támad, vagy a többiek válaszát várjuk), a piros utasításdoboz helyén egy kék doboz áll, a bal
    // oldali körében forgó homokórával, mellette a lang.json gameinfo_* szövegével. A próbajátékban
    // csak a piros, órás doboz van meg, ezt egy felvétel alapján rajzoljuk újra.
    var wait = {};
    hf.wait = wait;
    var WX = 305;           // a kör közepe: ahol a piros utasításdoboz "!" köre
    var WY = 53;
    var WR = 31;
    var WEND = 590;         // a doboz jobb széle

    function circlePath(mc, x, y, r) {
        var n = 8;
        var k = r / Math.cos(Math.PI / n);
        mc.moveTo(x + r, y);
        for (var i = 1; i <= n; i++) {
            var a = i * Math.PI * 2 / n;
            var m = (i - 0.5) * Math.PI * 2 / n;
            mc.curveTo(x + Math.cos(m) * k, y + Math.sin(m) * k, x + Math.cos(a) * r, y + Math.sin(a) * r);
        }
    }

    function vgrad(mc, colors, ratios, x, y, w, h, alphas) {
        if (alphas == undefined) {
            alphas = [];
            for (var i = 0; i < colors.length; i++) {
                alphas.push(100);
            }
        }
        mc.beginGradientFill('linear', colors, alphas, ratios, {matrixType: 'box', x: x, y: y, w: w, h: h, r: Math.PI / 2});
    }

    // fa talp (a homokóra teteje és alja)
    function hourglassCap(g, y) {
        g.lineStyle(1, 0x4A2806, 90);
        vgrad(g, [0xF0BE6E, 0xB8742A, 0x6E3C0A], [0, 110, 255], -11, y, 22, 4.5);
        roundPath(g, -11, y, 22, 4.5, 2);
        g.endFill();
    }

    function drawHourglass(g) {
        // felső üveg: tele homokkal
        g.lineStyle(1, 0x7D6D4A, 70);
        vgrad(g, [0xFFFDF0, 0xEDE2B6, 0xC8B27A], [0, 120, 255], -10, -13, 20, 13);
        g.moveTo(-8.5, -13);
        g.lineTo(8.5, -13);
        g.curveTo(11, -11, 10, -7);
        g.curveTo(8.6, -2.2, 1.4, -0.6);
        g.lineTo(-1.4, -0.6);
        g.curveTo(-8.6, -2.2, -10, -7);
        g.curveTo(-11, -11, -8.5, -13);
        g.endFill();
        // alsó üveg: üres, kékes
        g.lineStyle(1, 0x5D7397, 70);
        vgrad(g, [0xEAF3FF, 0xADC3DF, 0x7F9BC3], [0, 120, 255], -10, 0.6, 20, 12.4);
        g.moveTo(-1.4, 0.6);
        g.lineTo(1.4, 0.6);
        g.curveTo(8.6, 2.2, 10, 7);
        g.curveTo(11, 11, 8.5, 13);
        g.lineTo(-8.5, 13);
        g.curveTo(-11, 11, -10, 7);
        g.curveTo(-8.6, 2.2, -1.4, 0.6);
        g.endFill();
        // csillanás az üvegen
        g.lineStyle(1.5, 0xFFFFFF, 75);
        g.moveTo(-6.6, -11);
        g.curveTo(-8.4, -7, -5, -3.6);
        g.moveTo(-5, 3.6);
        g.curveTo(-8.4, 7, -6.6, 11);
        hourglassCap(g, -17.5);
        hourglassCap(g, 13);
    }

    function drawWaitBox(b) {
        var y0 = WY - 28;
        var h = 56;
        // a doboz: sötétkék, világosabb kerettel
        b.lineStyle(3, 0x14204A, 100);
        roundPath(b, WX, y0, WEND - WX, h, 8);
        b.lineStyle(2, 0x4D67B8, 100);
        vgrad(b, [0x14408F, 0x08347E, 0x002A6E], [0, 90, 255], WX, y0, WEND - WX, h);
        roundPath(b, WX, y0, WEND - WX, h, 8);
        b.endFill();
        b.lineStyle(1, 0x2E5BAA, 100);
        b.moveTo(WX, y0 + h - 3);
        b.lineTo(WEND - 6, y0 + h - 3);
        // a kör: sötét szél, világoskék gyűrű, sötétkék belső, fent fényes csillanás
        b.lineStyle();
        b.beginFill(0x14204A, 100);
        circlePath(b, WX, WY, WR + 1.5);
        b.endFill();
        vgrad(b, [0x7E95E2, 0x4A62C6, 0x2B3FA6], [0, 120, 255], WX - WR, WY - WR, WR * 2, WR * 2);
        circlePath(b, WX, WY, WR);
        b.endFill();
        b.beginFill(0x0B1670, 100);
        circlePath(b, WX, WY, WR - 4.5);
        b.endFill();
        vgrad(b, [0x1E258E, 0x1C3AA8, 0x2E5FD0], [0, 140, 255], WX - WR, WY - WR + 5, WR * 2, WR * 2 - 10);
        circlePath(b, WX, WY, WR - 5.5);
        b.endFill();
        vgrad(b, [0xFFFFFF, 0xFFFFFF], [0, 255], WX - 21, WY - 25, 42, 22, [58, 8]);
        b.moveTo(WX - 21, WY - 6);
        b.curveTo(WX - 21, WY - 25, WX, WY - 25);
        b.curveTo(WX + 21, WY - 25, WX + 21, WY - 6);
        b.curveTo(WX, WY - 1, WX - 21, WY - 6);
        b.endFill();
    }

    // show(szöveg): megjeleníti (vagy, ha már látszik, átírja a szövegét)
    wait.show = function (msg) {
        var b = _root.hfWait;
        if (b == undefined) {
            b = _root.createEmptyMovieClip('hfWait', 19980);
            drawWaitBox(b);
            var g = b.createEmptyMovieClip('glass', 10);
            g._x = WX;
            g._y = WY;
            drawHourglass(g);
            b.msg = ui.text(b, WX + 38, WY - 24, WEND - WX - 46, 48, '', 15, 0xF2F5FF, true, 'center', true);
            var t0 = getTimer();
            b._alpha = 0;
            // fél fordulat 0,45 mp alatt, utána 0,15 mp szünet (állva)
            b.onEnterFrame = function () {
                var t = getTimer() - t0;
                b._alpha = Math.min(100, t / 2);
                var k = Math.floor(t / 600);
                var p = t - k * 600;
                var e = p < 150 ? 0 : 0.5 - 0.5 * Math.cos(Math.PI * (p - 150) / 450);
                g._rotation = (k * 180 + e * 180) % 360;
            };
        }
        if (b.msg.text != msg) {
            b.msg.text = msg;
            b.msg._y = Math.round(WY - (b.msg.textHeight + 4) / 2) - 1;
        }
    };

    wait.hide = function () {
        _root.hfWait.removeMovieClip();
    };

    wait.isShown = function () {
        return _root.hfWait != undefined;
    };

    // a lépő játékos neve: "Béla támad" (harc) / "Béla választ területet" (felosztás) / "Béla választ
    // bázist" (a hosszú hadjárat eleje)
    wait.selectMsg = function (pnum) {
        var key = game.state == 4 ? 'gameinfo_oppattacks' : game.state == 1 ? 'gameinfo_selectingbase' : 'gameinfo_selectingarea';
        return getlang(key, game.player_name[pnum]);
    };

    // amikor a saját órád indul (te jössz), a várakozó doboz eltűnik
    game.hfOrigStartClock = game.StartClock;
    game.StartClock = function (ainstructwin, ainfotxt, atype, aseconds) {
        wait.hide();
        game.hfOrigStartClock(ainstructwin, ainfotxt, atype, aseconds);
    };

    var keys = {};
    keys.onKeyDown = function () {
        if (panel && onEnter && Key.getCode() == 13) {
            onEnter();
        }
    };
    Key.addListener(keys);

    // ------------------------------------------------------------------ szövegbevitel érintőképernyőn

    // Telefonon a Ruffle egy rejtett szövegmezővel hozza elő a billentyűzetet: az mindig betűs, és
    // iOS-en folyton eltűnik. Érintőképernyőn ezért az oldal egy valódi beviteli mezőt tesz a
    // kijelölt Flash-mező helyére (js/hf.js: hfText), a tippdoboznál számbillentyűzettel. Amit
    // beírsz, a Flash-mezőbe kerül; az Enter azt teszi, amit a játékban (tipp elküldése, belépés).
    var EIT = flash.external.ExternalInterface;
    var touch = {cur: null, owner: null};
    hf.touchText = touch;
    touch.on = EIT.call('hfText', 'touch') == true;

    function tipBox() {
        return gmap.QUESTIONWIN.MYTIPBOX;
    }

    touch.show = function (t) {
        var a = {x: t._x, y: t._y};
        var b = {x: t._x + t._width, y: t._y + t._height};
        t._parent.localToGlobal(a);
        t._parent.localToGlobal(b);
        var f = t.getNewTextFormat();
        touch.cur = t;
        touch.owner = t == tipBox() ? 'tip' : panelNo;
        EIT.call('hfText', 'open', {
            x: a.x, y: a.y, w: b.x - a.x, h: b.y - a.y,
            size: f.size * (b.y - a.y) / t._height * t._yscale / 100,
            value: t.text, color: t.textColor, background: t.background ? t.backgroundColor : -1,
            align: f.align, bold: f.bold == true, font: f.font, multiline: t.multiline == true,
            maxChars: t.maxChars > 0 ? t.maxChars : 0, restrict: t.restrict ? t.restrict : '',
            numeric: t.restrict == '0123456789'
        });
    };

    touch.hide = function () {
        if (touch.cur != null) {
            touch.cur = null;
            touch.owner = null;
            EIT.call('hfText', 'close');
        }
    };

    // a mező még ott van-e: a tipp még nincs elküldve, ill. ugyanaz az ablak van nyitva
    function alive(t) {
        if (touch.owner == 'tip') {
            return t == tipBox() && game.myanswer == -1 && gmap.QUESTIONWIN._visible;
        }
        return panel != null && touch.owner == panelNo && t.type == 'input';
    }

    touch.tick = function () {
        if (touch.cur != null && !alive(touch.cur)) {
            touch.hide();
        }
    };

    if (touch.on) {
        var focusWatch = {};
        focusWatch.onSetFocus = function (oldF, newF) {
            if (newF instanceof TextField && newF.type == 'input' && newF != touch.cur) {
                touch.show(newF);
            }
        };
        Selection.addListener(focusWatch);

        EIT.addCallback('hfTextSet', null, function (v) {
            var t = touch.cur;
            if (t == null) {
                return;
            }
            t.text = String(v);
            if (t.variable != null && t.variable != '') {
                t._parent[t.variable] = t.text;      // a tippdoboz a MYTIP változóhoz kötött
            }
            if (t.onChanged) {
                t.onChanged(t);
            }
        });

        EIT.addCallback('hfTextEnter', null, function () {
            var t = touch.cur;
            if (t == null) {
                return;
            }
            if (touch.owner == 'tip') {
                game.btnSendMyTipPressed(gmap.QUESTIONWIN.MYTIP);     // mint az Enter (gsys.OnKeyEnter)
            } else if (panel && onEnter) {
                onEnter();
            }
        });
    }

    function status(t, msg, color) {
        t.textColor = color ? color : C.dim;
        t.text = msg ? msg : '';
    }

    // ------------------------------------------------------------------ hangerő

    // Hangszóró-gomb a saját játékosdoboz üres bal alsó sarkában, mindig legfelül (az ablakok fölött
    // is); kattintásra csúszka nyílik mellette. A hangerőt az oldal tárolja (js/hf.js: hfSound).
    var EI = flash.external.ExternalInterface;
    var vol = {};
    hf.volume = vol;

    vol.build = function () {
        var level = Number(EI.call('hfSound', 'getVolume'));
        if (isNaN(level)) {
            level = 0.8;
        }
        var v = _root.createEmptyMovieClip('hfVolume', 30000);
        v._x = 16;
        v._y = 462;
        var btn = v.createEmptyMovieClip('btn', 1);
        var pop = v.createEmptyMovieClip('pop', 2);
        pop._x = 34;
        pop._visible = false;
        rect(pop, 0, 0, 128, 28, 8, C.panel, 96, C.border);
        var fill = pop.createEmptyMovieClip('fill', 1);
        var knob = pop.createEmptyMovieClip('knob', 2);
        circle(knob, 0, 0, 7, C.title);
        knob._y = 14;
        var hit = pop.createEmptyMovieClip('hit', 3);
        rect(hit, 6, 0, 116, 28, 0, 0x000000, 0);
        hit.useHandCursor = false;
        var hover = false;
        var dragging = false;
        var idle = 0;

        function drawButton() {
            btn.clear();
            rect(btn, 0, 0, 30, 26, 7, hover ? C.btnHi : C.panel, 92, C.border);
            btn.lineStyle(0, 0, 0);
            btn.beginFill(C.title, 100);         // hangszóró
            btn.moveTo(6, 10);
            btn.lineTo(10, 10);
            btn.lineTo(15, 5);
            btn.lineTo(15, 21);
            btn.lineTo(10, 16);
            btn.lineTo(6, 16);
            btn.lineTo(6, 10);
            btn.endFill();
            if (level <= 0) {                    // némítva: X
                btn.lineStyle(2, C.err, 100);
                btn.moveTo(18, 9);
                btn.lineTo(25, 17);
                btn.moveTo(25, 9);
                btn.lineTo(18, 17);
                return;
            }
            btn.lineStyle(2, C.title, 100);      // hanghullámok
            btn.moveTo(18, 9);
            btn.curveTo(21, 13, 18, 17);
            if (level > 0.5) {
                btn.moveTo(21, 6);
                btn.curveTo(26, 13, 21, 20);
            }
        }

        function drawLevel() {
            fill.clear();
            rect(fill, 12, 11, 104, 6, 3, 0x6B5A48, 100);
            if (level > 0) {
                rect(fill, 12, 11, Math.max(6, 104 * level), 6, 3, C.title, 100);
            }
            knob._x = 12 + 104 * level;
            drawButton();
        }

        function setFromMouse() {
            level = Math.round(Math.max(0, Math.min(1, (pop._xmouse - 12) / 104)) * 20) / 20;
            EI.call('hfSound', 'volume', level);
            drawLevel();
        }

        btn.onRollOver = function () {
            hover = true;
            drawButton();
        };
        btn.onRollOut = function () {
            hover = false;
            drawButton();
        };
        btn.onDragOut = btn.onRollOut;
        btn.onRelease = function () {
            pop._visible = !pop._visible;
            idle = 0;
        };
        hit.onPress = function () {
            dragging = true;
            setFromMouse();
        };
        hit.onRelease = function () {
            dragging = false;
        };
        hit.onReleaseOutside = hit.onRelease;
        pop.onMouseMove = function () {
            if (dragging) {
                setFromMouse();
            }
        };
        // a nyitott csúszka magától becsukódik, ha az egér 2 mp-ig máshol van
        v.onEnterFrame = function () {
            if (!pop._visible || dragging || v.hitTest(_root._xmouse, _root._ymouse, false)) {
                idle = 0;
                return;
            }
            idle++;
            if (idle > 40) {
                pop._visible = false;
            }
        };
        drawLevel();
    };
    vol.build();

    // ------------------------------------------------------------------ a játék végeredménye

    // A teljes kliens "A játék végeredménye" ablakának mintájára (annak grafikája nem maradt fenn):
    // fejléc, bal oldalt fülek, soronként helyezés, név, pont, a játékos színével aláhúzva.
    var results = {};
    hf.results = results;

    function yellowButton(mc, x, y, w, h, label, size, onClick) {
        var d = mc.getNextHighestDepth();
        var b = mc.createEmptyMovieClip('y' + d, d);
        b._x = x;
        b._y = y;
        var draw = function (hi) {
            b.clear();
            grad(b, 0, 0, w, h, Math.min(10, Math.round(h / 2)), hi ? 0xFFE98A : 0xF8D64E, hi ? 0xE3B232 : 0xC9961C, 0x6E4E0E);
        };
        draw(false);
        ui.text(b, 0, Math.round((h - size * 1.5) / 2), w, Math.round(size * 1.7), label, size, 0x2D190E, true, 'center');
        b.onRollOver = function () {
            draw(true);
        };
        b.onRollOut = function () {
            draw(false);
        };
        b.onDragOut = b.onRollOut;
        b.onRelease = function () {
            draw(false);
            onClick();
        };
        return b;
    }

    results.show = function (onOk) {
        var W2 = 470;
        var H2 = 330;
        var b = ui.open(W2, H2, '');
        b.clear();
        grad(b, 0, 0, W2, H2, 12, 0x4C2C17, 0x2C180C, 0x8C6532);
        grad(b, 6, 6, W2 - 12, 30, 8, 0x2B170B, 0x1A0D05);
        ui.text(b, 0, 9, W2, 26, 'A játék végeredménye', 17, 0xEAD9B5, true, 'center');

        var pages = [b.createEmptyMovieClip('page1', 50), b.createEmptyMovieClip('page2', 51)];
        var tabs = [];
        function showPage(n) {
            for (var i = 0; i < pages.length; i++) {
                pages[i]._visible = i == n;
                drawTab(tabs[i], i == n);
            }
        }
        function drawTab(t, on) {
            t.clear();
            if (on) {
                rect(t, 0, 0, 112, 26, 6, 0x1C0E06, 100, 0x8C6532);
            } else {
                grad(t, 0, 0, 112, 26, 6, 0x8E4A32, 0x66311F);
            }
            t.label.textColor = on ? 0xF5C83A : 0xEBCDA5;
        }
        function makeTab(n, label) {
            var t = b.createEmptyMovieClip('tab' + n, 60 + n);
            t._x = 12;
            t._y = 48 + n * 32;
            t.label = ui.text(t, 0, 3, 112, 22, label, 14, 0xEBCDA5, true, 'center');
            t.onRelease = function () {
                showPage(n);
            };
            return t;
        }
        tabs.push(makeTab(0, 'Végeredmény'));
        tabs.push(makeTab(1, 'Válaszaid'));

        // 1. oldal: helyezések (a kimaradó helyek nélkül)
        var p1 = pages[0];
        var order = [];
        for (var o = 1; o <= 3; o++) {
            if (!hf.absent[o]) {
                order.push(o);
            }
        }
        // egyenlő pontnál (a kiesettek: 0) aki később esett ki, előrébb végez
        var outAt = function (p) {
            for (var k = 0; k < hf.outOrder.length; k++) {
                if (hf.outOrder[k] == p) {
                    return k;
                }
            }
            return 99;
        };
        order.sort(function (x, y) {
            if (game.points[x] != game.points[y]) {
                return game.points[x] > game.points[y] ? -1 : 1;
            }
            if (outAt(x) != outAt(y)) {
                return outAt(x) > outAt(y) ? -1 : 1;
            }
            return x - y;
        });
        var X = 136;
        var RW = W2 - X - 12;
        for (var i = 0; i < order.length; i++) {
            var pn = order[i];
            var y = 50 + i * 42;
            var me = pn == game.IAM;
            grad(p1, X, y, RW, 32, 7, 0x3B2414, 0x22140A, me ? 0xFFFFFF : undefined);
            var col = gcfg.playercolorcodes[pn];
            p1.lineStyle();
            p1.beginGradientFill('linear', [col, col], [100, 0], [0, 255],
                {matrixType: 'box', x: X + 4, y: y + 30, w: RW * 0.75, h: 5, r: 0});
            p1.moveTo(X + 4, y + 30);
            p1.lineTo(X + 4 + RW * 0.75, y + 30);
            p1.lineTo(X + 4 + RW * 0.75, y + 35);
            p1.lineTo(X + 4, y + 35);
            p1.lineTo(X + 4, y + 30);
            p1.endFill();
            ui.text(p1, X + 12, y + 5, 40, 24, (i + 1) + '.', 17, 0xF5C83A, true, 'left');
            ui.text(p1, X + 44, y + 6, RW - 130, 24, game.player_name[pn], 16, 0xFFFFFF, true, 'center');
            ui.text(p1, X + RW - 90, y + 5, 78, 24, String(game.points[pn]), 17, 0xFFFFFF, true, 'right');
        }
        var msg = order[0] == game.IAM ? 'Gratulálunk, megnyerted a játékot!' : game.player_name[order[0]] + ' nyerte a játékot.';
        ui.text(p1, X, 186, RW, 44, msg, 15, 0xEAD9B5, true, 'center', true);

        // 2. oldal: a saját válaszaid
        var p2 = pages[1];
        var st = hf.stats;
        var lines = [];
        lines.push('Feleletválasztós kérdések: ' + st.mcGood + ' jó válasz ' + st.mcAll + ' kérdésből');
        lines.push('Tippkérdések: ' + st.tipWon + ' megnyert ' + st.tipAll + ' kérdésből' + (st.tipSecond > 0 ? ', ' + st.tipSecond + ' második hely' : ''));
        if (st.bestDiff >= 0) {
            lines.push('Legpontosabb tipped: ' + st.bestTip + ' (a helyes válasz: ' + st.bestAnswer + ')');
        }
        if (st.mcAll + st.tipAll == 0) {
            lines = ['Ebben a játékban nem válaszoltál kérdésre.'];
        }
        ui.text(p2, X + 6, 56, RW - 12, 150, lines.join('\n\n'), 15, 0xEAD9B5, false, 'left', true);

        if (hf.mode == 'mp') {
            // többjátékos módban: vissza a szobába (a főnök új játékot indíthat), vagy kilépés a főoldalra
            yellowButton(b, X + 30, H2 - 64, RW - 60, 42, 'Vissza a váróba', 19, function () {
                hf.net.call('again');
            });
            yellowButton(b, 12, H2 - 44, 112, 28, 'Kilépés', 15, function () {
                hf.net.call('exit');
            });
        } else {
            yellowButton(b, X + 30, H2 - 64, RW - 60, 42, 'Új játék', 19, function () {
                getURL(hf.mode == 'robots' ? '?gamemode=1' : '?gamemode=0', '_self');
            });
            yellowButton(b, 12, H2 - 44, 112, 28, 'OK', 15, function () {
                ui.close();
                onOk();
            });
        }
        showPage(0);
    };

    // ------------------------------------------------------------------ szoba: belépés

    var lobby = {};
    hf.lobby = lobby;

    lobby.open = function (msg) {
        var h = hf.net.call('hello');
        if (h && h.inLobby) {
            lobby.room();
            return;
        }
        var b = ui.open(400, 374, 'Játék ismerősökkel');
        ui.text(b, 30, 58, 120, 24, 'Neved:', 16, C.text);
        var name = ui.input(b, 140, 56, 230, 26, h ? h.name : '', 16);
        // publikus váró: a Várók listájában bárki megtalálja, és kód nélkül beléphet (a neve kell)
        var titleEdited = false;
        var defaultTitle = function () {
            var n = name.text.split('  ').join(' ');
            return n.split(' ').join('') == '' ? '' : n + ' lobbyja';
        };
        var pub = ui.checkbox(b, 30, 92, 340, 'Publikus váró (a Várók listájában látszik)', false, function (on) {
            ui.setInputOn(title, on);
            if (on) {
                title.text = defaultTitle();
            } else {
                title.text = '';            // kivételkor a név törlődik (újra bejelölve az alapértelmezett jön)
            }
            titleEdited = false;
        });
        ui.text(b, 30, 124, 120, 24, 'Váró neve:', 16, C.text);
        var title = ui.input(b, 140, 122, 230, 26, '', 28);
        ui.setInputOn(title, false);
        name.onChanged = function () {
            if (pub.on && !titleEdited) {
                title.text = defaultTitle();
            }
        };
        title.onChanged = function () {
            titleEdited = true;
        };
        var create = ui.button(b, 30, 160, 340, 34, 'Új szoba létrehozása', function () {
            go('create');
        });
        ui.text(b, 30, 208, 340, 22, 'vagy csatlakozz egy barátod szobájához:', 15, C.dim, false, 'center');
        ui.text(b, 30, 242, 120, 24, 'Szobakód:', 16, C.text);
        var code = ui.input(b, 140, 240, 110, 26, h ? h.join : '', 8);
        code.restrict = 'A-Za-z0-9';
        var join = ui.button(b, 262, 238, 108, 30, 'Belépés', function () {
            go('join');
        });
        var st = ui.text(b, 20, 282, 360, 40, '', 14, C.dim, false, 'center', true);
        ui.button(b, 30, 326, 120, 32, 'Vissza', function () {
            ui.close();
        });
        status(st, msg, C.err);

        var busy = false;
        function go(what) {
            if (busy) {
                return;
            }
            var n = name.text;
            if (n.split(' ').join('') == '') {
                status(st, 'Előbb add meg a neved!', C.err);
                Selection.setFocus(name);
                return;
            }
            if (what == 'join') {
                var c = code.text.toUpperCase();
                if (c.length < 4) {
                    status(st, 'Írd be a szobakódot, amit a barátodtól kaptál!', C.err);
                    Selection.setFocus(code);
                    return;
                }
                hf.net.call('join', c, n);
                status(st, 'Csatlakozás...');
            } else {
                if (pub.on && title.text.split(' ').join('') == '') {
                    title.text = defaultTitle();
                }
                hf.net.call('create', n, pub.on ? title.text : '');
                status(st, 'Szoba létrehozása...');
            }
            busy = true;
            create.setOn(false);
            join.setOn(false);
        }
        onEnter = function () {
            go(code.text != '' ? 'join' : 'create');
        };
        hf.net.handler = function (ev) {
            if (ev.type == 'joined') {
                lobby.room();
            } else if (ev.type == 'error') {
                busy = false;
                create.setOn(true);
                join.setOn(true);
                status(st, ev.msg, C.err);
            }
        };
        Selection.setFocus(name.text == '' ? name : code);
    };

    // Belépés egy szobába csak a név megadásával: a Várók listájából (js/hf.js: hfJoin, {code, title};
    // a publikus szoba kódja nem látszik, a csevegés fejlécében megvan), vagy meghívó linkkel
    // (?join=KÓD, sub: az alcím).
    lobby.joinPublic = function (code, title, sub) {
        var h = hf.net.call('hello');
        var b = ui.open(380, 214, title);
        ui.text(b, 24, 50, 332, 22, sub != undefined ? sub : 'Belépés a publikus váróba', 15, C.dim, false, 'center');
        ui.text(b, 30, 92, 100, 24, 'Neved:', 16, C.text);
        var name = ui.input(b, 120, 90, 230, 26, h ? h.name : '', 16);
        var st = ui.text(b, 20, 124, 340, 40, '', 14, C.dim, false, 'center', true);
        ui.button(b, 30, 166, 120, 32, 'Mégse', function () {
            ui.close();
        });
        var join = ui.button(b, 210, 166, 140, 32, 'Belépés', function () {
            go();
        });
        var busy = false;
        function go() {
            if (busy) {
                return;
            }
            if (name.text.split(' ').join('') == '') {
                status(st, 'Előbb add meg a neved!', C.err);
                Selection.setFocus(name);
                return;
            }
            busy = true;
            join.setOn(false);
            status(st, 'Belépés...');
            hf.net.call('join', code, name.text);
        }
        onEnter = go;
        hf.net.handler = function (ev) {
            if (ev.type == 'joined') {
                lobby.room();
            } else if (ev.type == 'error') {
                busy = false;
                join.setOn(true);
                status(st, ev.msg, C.err);
            }
        };
        Selection.setFocus(name);
        Selection.setSelection(name.text.length, name.text.length);
    };

    EI.addCallback('hfJoin', null, function (r) {
        if (hf.mode != 'menu' || r == undefined) {
            return false;
        }
        lobby.joinPublic(String(r.code), String(r.title));
        return true;
    });

    // ------------------------------------------------------------------ szoba: várakozás

    lobby.room = function () {
        var h = hf.net.call('hello');
        var b = ui.open(420, 384, 'Szoba: ' + h.code);
        var invite = ui.text(b, 24, 52, 372, 22, 'Küldd el ezt a linket a barátaidnak:', 15, C.text);
        var link = ui.text(b, 24, 76, 266, 26, h.link, 14, C.inputText);
        link.background = true;
        link.backgroundColor = C.input;
        link.border = true;
        link.borderColor = C.border;
        link.selectable = true;
        var copy = ui.button(b, 298, 74, 98, 30, 'Másolás', function () {
            hf.net.call('copy');
        });
        ui.text(b, 24, 112, 372, 22, 'Játékosok:', 15, C.text);
        var rows = [];
        // a főnök a játék előtt robotot tehet egy üres helyre, és kiveheti onnan (server: handleRobot).
        // Egy kattintás után a gombok egy kicsit pihennek (a szerver is korlátozza: robotFlood).
        var robotRest = false;
        function robotButtons(on) {
            for (var r = 1; r <= 3; r++) {
                rows[r].add.setOn(on);
                rows[r].del.setOn(on);
            }
        }
        function robotButton(seat, add) {
            var y = 112 + seat * 30;
            var btn = ui.button(b, 300, y, 96, 26, add ? '+ Robot' : 'Kivesz', function () {
                if (robotRest) {
                    return;
                }
                robotRest = true;
                robotButtons(false);
                hf.net.call('robot', seat, add);
                hf.later(1200, function () {
                    robotRest = false;
                    robotButtons(true);
                });
            });
            btn._visible = false;
            return btn;
        }
        for (var s = 1; s <= 3; s++) {
            var y = 112 + s * 30;
            var dot = b.createEmptyMovieClip('dot' + s, b.getNextHighestDepth());
            dot._x = 40;
            dot._y = y + 12;
            var icon = b.createEmptyMovieClip('icon' + s, b.getNextHighestDepth());
            icon._y = y + 6;
            rows[s] = {dot: dot, icon: icon, name: ui.text(b, 56, y, 200, 26, '', 16, C.text),
                tag: ui.text(b, 56, y, 200, 26, '', 14, C.dim), add: robotButton(s, true), del: robotButton(s, false)};
        }
        // a sor: név, mögötte a korona vagy a robot ikonja, aztán a címke (te, nem elérhető, robot)
        function setRow(row, name, color, iconKind, tag) {
            row.name.textColor = color;
            row.name.text = name;
            row.tag.textColor = color;
            var x = 56 + Math.round(row.name.textWidth) + 10;
            row.icon.clear();
            if (iconKind == 'crown') {
                drawCrown(row.icon);
            } else if (iconKind == 'robot') {
                drawRobot(row.icon);
            }
            row.icon._x = x;
            if (iconKind != '') {
                x += 24;
            }
            row.tag._x = x;
            row.tag._y = row.name._y + 2;
            row.tag.text = tag;
        }
        // a szoba szabálya és térképe: ugyanazok az ikonok, mint az oldal bal sávjában (img/rules, img/maps)
        var ruleIcon = ui.iconLabel(b, 24, 230, 190);
        var mapIcon = ui.iconLabel(b, 214, 230, 190);
        var st = ui.text(b, 20, 262, 380, 64, '', 14, C.dim, false, 'center', true);
        ui.button(b, 24, 332, 120, 34, 'Kilépés', function () {
            hf.net.call('leave');
            lobby.open();
        });
        var start = ui.button(b, 256, 332, 140, 34, 'Indítás', function () {
            hf.net.call('start');
            status(st, 'Indítás...');
        });
        start._visible = false;
        var rejoin = ui.button(b, 226, 332, 170, 34, 'Visszalépés', function () {
            hf.net.call('rejoin');
            status(st, 'Visszalépés a játékba...');
        });
        rejoin._visible = false;

        function draw(state) {
            if (!state) {
                status(st, 'Kapcsolódás a szobához...');
                return;
            }
            var me = h.seat;
            var boss = state.boss == me;
            var edit = boss && !state.started;      // robot be / ki
            var count = 0;
            for (var s = 1; s <= 3; s++) {
                var row = rows[s];
                row.dot.clear();
                circle(row.dot, 0, 0, 7, 0x6B5A48);
                setRow(row, 'szabad hely', C.dim, '', '');      // ha üres marad, kimarad a játékból
                row.add._visible = edit;
                row.del._visible = false;
            }
            for (var i = 0; i < state.players.length; i++) {
                var p = state.players[i];
                var r = rows[p.seat];
                count++;
                r.dot.clear();
                circle(r.dot, 0, 0, 7, SEATCOLOR[p.seat]);
                r.add._visible = false;
                r.del._visible = edit && p.robot;
                // játék közben aki kilépett vagy elment a kapcsolata, helyett robot játszik
                var away = state.started && !p.robot && (p.left || !p.online);
                var tags = [];
                if (p.seat == me) {
                    tags.push('te');
                }
                if (p.robot || away) {
                    tags.push('robot');
                } else if (!p.online) {
                    tags.push('nem elérhető');
                }
                setRow(r, p.name, C.text, p.seat == state.boss ? 'crown' : (p.robot ? 'robot' : ''), tags.length > 0 ? '(' + tags.join(', ') + ')' : '');
                r.tag.textColor = C.dim;
            }
            // publikus váró: a neve a címben; a kódja és a linkje nem látszik (a csevegés fejlécében megvan)
            if (state.public) {
                b.titleText.text = state.title;
                invite.text = 'Publikus váró: bárki beléphet a Várók listájából.';
                link._visible = false;
                copy._visible = false;
            }
            ruleIcon.set('img/rules/' + (state.long ? 'long' : 'blitz') + '.png', state.long ? 'Hosszú hadjárat' : 'Villámháború');
            mapIcon.set('img/maps/' + state.map + '.png', state.mapName);
            var w1 = ruleIcon.width();
            var x0 = Math.round((420 - w1 - 28 - mapIcon.width()) / 2);       // a kettő együtt középen
            ruleIcon.clip._x = x0;
            mapIcon.clip._x = x0 + w1 + 28;
            start._visible = boss && !state.started;
            start.setOn(count >= 2);
            rejoin._visible = state.started;
            if (state.started) {
                status(st, 'A játék már folyik. Visszaléphetsz (ahonnan most tart a játék), vagy kiléphetsz: ekkor helyetted robot játszik tovább.');
            } else if (boss) {
                // a térképet és a szabályt a főnök az oldal bal oldali sávjában választja (js/hf.js)
                var wait = state.public ? 'Várjuk, hogy belépjenek a játékosok... ' : 'Várjuk, hogy a barátaid belépjenek a linkkel... ';
                status(st, (count >= 3 ? 'Ha mindenki megvan, indítsd el a játékot!' : count == 2 ? 'Ketten is indíthatjátok: az üres hely kimarad a játékból.' : wait + 'Robotot is betehetsz.') +
                    ' A térképet és a szabályt a bal oldali sávban választhatod.');
            } else {
                status(st, 'Várjuk, hogy a szoba főnöke elindítsa a játékot...');
            }
        }
        draw(hf.net.call('state'));
        hf.net.handler = function (ev) {
            if (ev.type == 'state') {
                draw(ev.state);
            } else if (ev.type == 'error') {
                status(st, ev.msg, C.err);
            } else if (ev.type == 'copied') {
                status(st, ev.ok ? 'A link a vágólapra került.' : 'Nem sikerült másolni: jelöld ki a linket, és másold ki kézzel.', ev.ok ? C.ok : C.err);
            } else if (ev.type == 'closed') {
                lobby.open(ev.msg);
            }
        };
    };

    // ------------------------------------------------------------------ visszajelzés

    var feedback = {};
    hf.feedback = feedback;

    feedback.open = function () {
        var h = hf.net.call('hello');
        var b = ui.open(440, 422, 'Visszajelzés');
        ui.text(b, 24, 50, 392, 44, 'Írd meg, mi tetszik, mi nem, vagy ha hibát találtál. Köszönjük!', 15, C.text, false, 'left', true);
        ui.text(b, 24, 100, 140, 24, 'Neved (nem kötelező):', 14, C.dim);
        var name = ui.input(b, 180, 98, 236, 26, h ? h.name : '', 16);
        var text = ui.input(b, 24, 134, 392, 130, '', 2000, true);
        ui.text(b, 24, 270, 392, 22, 'Nagyon köszi az eddigi visszajelzéseket!', 14, C.text);
        ui.text(b, 24, 290, 392, 22, 'Szinte minden bugot javítottunk, de az új feature-ök miatt', 14, C.text);
        var more = ui.text(b, 24, 310, 392, 22, 'lehetnek újak, több infó:', 14, C.text);
        ui.link(b, 24 + Math.round(more.textWidth) + 8, 310, 'patch notes', 'https://github.com/akijoe/honfoglalo/releases');
        var st = ui.text(b, 20, 334, 400, 36, '', 14, C.dim, false, 'center', true);
        ui.button(b, 24, 374, 120, 34, 'Mégse', function () {
            ui.close();
        });
        var send = ui.button(b, 276, 374, 140, 34, 'Küldés', function () {
            if (text.text.split(' ').join('').split('\r').join('').split('\n').join('') == '') {
                status(st, 'Előbb írj pár szót!', C.err);
                Selection.setFocus(text);
                return;
            }
            send.setOn(false);
            status(st, 'Küldés...');
            hf.net.call('feedback', text.text, name.text);
        });
        hf.net.handler = function (ev) {
            if (ev.type == 'feedback') {
                status(st, 'Köszönjük, megkaptuk!', C.ok);
                text.type = 'dynamic';
                var started = getTimer();
                b.onEnterFrame = function () {
                    if (getTimer() - started > 1500) {
                        b.onEnterFrame = undefined;
                        ui.close();
                    }
                };
            } else if (ev.type == 'error') {
                send.setOn(true);
                status(st, ev.msg, C.err);
            }
        };
        Selection.setFocus(text);
    };
})();
