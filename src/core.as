// A legelső kód (az 1. képkocka elején, a játék saját kódja előtt fut).
//
//   _global.hf      – a kiegészítések névtere
//   _global.gsound  – hang-híd: a próbajáték a teljes kliens hangobjektumát hívja, ami itt nem
//                     létezett; a hívások az oldalhoz mennek (js/hf.js: hfSound)
//   hf.net          – az oldal hálózati hídja (js/hf.js: hfNet); 100 ms-onként lekérdezi az eseményeket

(function () {
    var EI = flash.external.ExternalInterface;
    var hf = {};
    _global.hf = hf;

    // a beágyazás paraméterei (js/hf.js: loadSwf)
    if (_level0.mp == '1') {
        hf.mode = 'mp';            // többjátékos játék egy szobában
    } else if (_level0.quickgame == '1') {
        hf.mode = 'robots';        // "Játék robotokkal"
    } else if (_level0.invitation == '1') {
        hf.mode = 'menu';          // főoldal: a menüvel kezdődik
    } else {
        hf.mode = 'tutorial';      // bemutató játék
    }

    // Az eredeti kód induláskor azt nézi, hogy a SWF címe http://-vel kezdődik-e. Ha nem (https://),
    // helyi fájlnak hiszi magát: a feliratokat egy megszűnt szerverről töltené (és el sem indulna), a
    // robotjátékban is a menüt nyitná. Amíg a játék saját indulókódja fut, a https:// címet is
    // http://-nek látja; utána a src/tweaks.as visszaállítja az eredeti indexOf-ot.
    var indexOf = String.prototype.indexOf;
    var httpsAsHttp = function (s, from) {
        if (s == 'http://' && from == undefined && indexOf.call(this, 'https://') == 0) {
            return 0;
        }
        return indexOf.apply(this, arguments);
    };
    String.prototype.indexOf = httpsAsHttp;
    hf.restoreIndexOf = function () {
        String.prototype.indexOf = indexOf;
    };

    var gs = {};
    _global.gsound = gs;
    gs.play = function (n, l) {
        if (hf.soundName) {
            n = hf.soundName(n);        // src/tweaks.as
        }
        if (n != null) {
            EI.call('hfSound', 'play', n, l);
        }
    };
    // ms múlva, pontosan (az oldal Web Audióval időzíti, nem a képkockákhoz igazodik)
    gs.playAt = function (n, ms) {
        if (hf.soundName) {
            n = hf.soundName(n);
        }
        if (n != null) {
            EI.call('hfSound', 'playAt', n, ms);
        }
    };
    gs.stop = function (n) { EI.call('hfSound', 'stop', n); };
    gs.changeMusic = function (n) { EI.call('hfSound', 'changeMusic', n); };
    gs.stopMusic = function (f) { EI.call('hfSound', 'stopMusic', f); };

    // hf.later(ms, fn): késleltetett hívás (a 100 ms-os ütemre)
    var timers = [];
    hf.later = function (ms, fn) {
        timers.push({t: getTimer() + ms, fn: fn});
    };
    function runTimers() {
        var now = getTimer();
        var due = [];
        for (var i = timers.length - 1; i >= 0; i--) {
            if (timers[i].t <= now) {
                due.unshift(timers[i].fn);
                timers.splice(i, 1);
            }
        }
        for (var j = 0; j < due.length; j++) {
            due[j]();
        }
    }

    var net = {};
    hf.net = net;
    net.handler = null;            // az éppen nyitott ablak eseménykezelője
    net.call = function (cmd, a, b) {
        return EI.call('hfNet', cmd, a, b);
    };
    net.tick = function () {
        var evs = net.call('poll');
        if (evs) {
            for (var i = 0; i < evs.length; i++) {
                if (net.handler) {
                    net.handler(evs[i]);
                }
            }
        }
        if (hf.ui) {
            hf.ui.tick();
        }
        if (hf.mp) {
            hf.mp.tick();
        }
        if (hf.answers) {
            hf.answers.tick();      // src/tweaks.as: a robotok válaszhangja
        }
        runTimers();
    };
    net.timer = setInterval(net.tick, 100);

    // hibakereséshez (?debug): hfDbg('gmap.MYBOX._x'), hfDbg('call:game.btnAnswerPressed', 2)
    if (_level0.debug == '1') {
        hf.testHttpsAsHttp = function (url) {
            return httpsAsHttp.call(url, 'http://') + ' ' + httpsAsHttp.call(url, 'p', 2) + ' ' + String(_root._url);
        };
        EI.addCallback('hfDbg', null, function (path, arg) {
            // hfDbg('call:game.btnAnswerPressed', 2) meghívja a függvényt
            var call = String(path).indexOf('call:') == 0;
            if (call) {
                path = String(path).substr(5);
            }
            var parts = String(path).split('.');
            var o = _root[parts[0]] != undefined ? _root : _global;
            if (parts[0] == '_global' || parts[0] == '_root') {
                o = parts[0] == '_global' ? _global : _root;
                parts.shift();
            }
            var owner = o;
            for (var i = 0; i < parts.length; i++) {
                owner = o;
                o = o[parts[i]];
            }
            if (call) {
                o = owner[parts[parts.length - 1]](arg);
            }
            return typeof o + ': ' + String(o);
        });
    }
})();
