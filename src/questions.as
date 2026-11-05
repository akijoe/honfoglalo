// Kérdésválasztás minden módban (bemutató, robotjáték, többjátékos).
//
//   - véletlenszerű (a Ruffle véletlenszám-generátora minden betöltéskor más magot kap): előbb egy
//     kategóriát sorsol (CAT, questions.xml; az előző kérdésétől eltérőt, ha lehet), aztán abból
//     egy kérdést, így minden kategória nagyjából egyforma gyakran jön, akárhány kérdés van benne
//   - egy játékon belül nincs ismétlés, amíg el nem fogynak a kérdések
//   - ez a böngésző megjegyzi a legutóbb látott kérdéseket (js/hf.js: hfSeen, localStorage), és
//     amíg van még nem látott kérdés, azok közül választ; a lista a kérdések 60%-áig nő, így
//     mindig marad friss kérdés, és a régen látottak idővel visszakerülnek
//   - a válaszok sorrendje is véletlen
//
// A többjátékos játékban a házigazda választ (src/mp.as), a kiválasztott kérdést mindenki ugyanúgy
// alkalmazza (Q.apply), így a "már volt" lista minden gépen azonos marad. A játék elején minden gép
// elküldi, mely kérdéseket látta már (Q.mySeen, kérdésenként egy bit), és a választás azokat
// részesíti előnyben, amelyeket a legkevesebb játékos látott: nem csak a házigazda emlékezete számít.

(function () {
    var hf = _global.hf;
    var EI = flash.external.ExternalInterface;
    var Q = {};
    hf.questions = Q;
    var used = {mc: {}, tip: {}};      // ebben a játékban már feltett kérdések (index -> true)
    var usedCount = {mc: 0, tip: 0};
    var seen = {};                     // fajta -> {kulcs: true}, a böngésző emlékezete
    var lastCat = '';                  // az előző kérdés kategóriája

    function kindOf(atip) {
        return atip ? 'tip' : 'mc';
    }

    function listOf(atip) {
        return atip ? gqs.tipquestions : gqs.questions;
    }

    // rövid kulcs a kérdés szövegéből (a localStorage-ba ez kerül)
    Q.key = function (tag) {
        var s = String(tag.QUESTION);
        var h = 5381;
        for (var i = 0; i < s.length; i++) {
            h = (h * 33 + s.charCodeAt(i)) % 2147483647;
        }
        return h.toString(36);
    };

    // A kulcsok kérdésenként egyszer számolódnak: mind az 1000 kérdés szövegén végigmenni a Ruffle-ben
    // kb. 0,2 mp, és a választás minden kérdésnél ezt tenné (a többjátékos házigazdánál ez akasztotta
    // meg a játékot). Betöltés után kis adagokban, 100 ms-onként előre kiszámoljuk őket.
    var keys = {mc: [], tip: []};          // fajta -> index -> kulcs
    function keyAt(kind, i) {
        var k = keys[kind][i];
        if (k == undefined) {
            k = Q.key(listOf(kind == 'tip')[i]);
            keys[kind][i] = k;
        }
        return k;
    }
    var warmed = {mc: 0, tip: 0};
    var ready = false;                 // minden kulcs megvan (Q.mySeen ezután olcsó)
    Q.ready = function () {
        return ready;
    };
    function warm() {
        var budget = 40;
        var kinds = ['tip', 'mc'];
        var left = false;
        for (var j = 0; j < kinds.length; j++) {
            var kind = kinds[j];
            var list = listOf(kind == 'tip');
            var n = list == undefined ? 0 : list.length;
            while (budget > 0 && warmed[kind] < n) {
                keyAt(kind, warmed[kind]);
                warmed[kind]++;
                budget--;
            }
            if (n == 0 || warmed[kind] < n) {
                left = true;                   // még nem töltődtek be, vagy van még hátra
            }
        }
        if (left) {
            hf.later(100, warm);
        } else {
            ready = true;
        }
    }
    hf.later(500, warm);

    function seenOf(kind) {
        if (seen[kind] == undefined) {
            seen[kind] = {};
            var arr = EI.call('hfSeen', 'get', kind);
            if (arr) {
                for (var i = 0; i < arr.length; i++) {
                    seen[kind][arr[i]] = true;
                }
            }
        }
        return seen[kind];
    }

    function catOf(tag) {
        return tag.CAT == undefined ? '-' : String(tag.CAT);
    }

    // ------------------------------------------------------------------ a többiek emlékezete

    // A látott kérdések kérdésenként egy bitje, hexában (1000 kérdés: 250 karakter), és a lista
    // hossza: ha egy másik gépen más a questions.xml, a sorszámok nem egyeznek, és nem használjuk.
    var HEX = '0123456789abcdef';
    function seenBits(kind) {
        var list = listOf(kind == 'tip');
        var s = seenOf(kind);
        var out = '';
        for (var i = 0; i < list.length; i += 4) {
            var v = 0;
            for (var b = 0; b < 4; b++) {
                v = v * 2 + (i + b < list.length && s[keyAt(kind, i + b)] ? 1 : 0);
            }
            out += HEX.charAt(v);
        }
        return out;
    }
    Q.mySeen = function () {
        return {n: [gqs.questions.length, gqs.tipquestions.length], mc: seenBits('mc'), tip: seenBits('tip')};
    };

    var others = {};                   // hely -> {mc: [index -> true], tip: [...]}: a többiek látták
    function unbits(hex, n) {
        var out = [];
        var str = String(hex);
        for (var i = 0; i < n; i++) {
            var v = HEX.indexOf(str.charAt(Math.floor(i / 4)));
            if (v >= 0 && (v >> (3 - i % 4)) % 2 == 1) {
                out[i] = true;
            }
        }
        return out;
    }
    Q.setOthers = function (seat, d) {
        if (d == undefined || d.n == undefined || Number(d.n[0]) != gqs.questions.length || Number(d.n[1]) != gqs.tipquestions.length) {
            return;
        }
        others[seat] = {mc: unbits(d.mc, gqs.questions.length), tip: unbits(d.tip, gqs.tipquestions.length)};
    };

    // hányan látták már az i. kérdést (ez a gép és a többiek)
    function seenCount(kind, s, i) {
        var n = s[keyAt(kind, i)] ? 1 : 0;
        for (var seat in others) {
            if (others[seat][kind][i]) {
                n++;
            }
        }
        return n;
    }

    // ------------------------------------------------------------------ témakörök

    // A kiválasztott témakörök (src/tweaks.as: hf.cats) kérdései a teljes lista előtt fogynak el, ezért
    // a sorrend: megengedett és még nem volt -> megengedett, de már volt -> bármelyik. A középső lépés
    // a "már volt" lista törlése, amint a megengedettek elfogytak (allowedUsed eléri allowedTotal-t);
    // ezt a Q.apply teszi, nem a Q.pick: a Q.apply minden gépen ugyanúgy fut, így a lista a
    // többjátékos játékban is mindenhol azonos marad (kérdést csak a házigazda választ). A Q.pick így
    // csak akkor nem talál szabad megengedett kérdést, ha a témakörökben ilyen fajta (tipp /
    // feleletválasztós) nincs: ekkor szűrés nélkül választ, hogy a játék ne akadjon el.
    // A sorszámok (used, Q.mySeen, Q.setOthers) mindig a teljes listára vonatkoznak.
    var allowedTotal = {};             // fajta -> hány megengedett kérdés van (egyszer számolva)
    var allowedUsed = {mc: 0, tip: 0}; // fajta -> ebből hány volt már (a used-dal együtt nullázódik)
    function allowed(tag) {
        return hf.cats == undefined || hf.cats[catOf(tag)] == true;
    }
    function allowedCount(kind, list) {
        if (allowedTotal[kind] == undefined) {
            var n = 0;
            for (var i = 0; i < list.length; i++) {
                if (allowed(list[i])) {
                    n++;
                }
            }
            allowedTotal[kind] = n;
        }
        return allowedTotal[kind];
    }

    // egy még fel nem tett kérdés indexe: előbb kategória, aztán kérdés; azok előnyben, amelyeket a
    // legkevesebben láttak (egyjátékos módban: a nem látottak)
    Q.pick = function (atip) {
        var kind = kindOf(atip);
        var list = listOf(atip);
        var u = used[kind];
        var s = seenOf(kind);
        var byCat = {};
        var cats = [];
        var counts = [];
        var least = 1000;
        // ilyen fajta megengedett kérdés nincs: szűrés nélkül
        var filter = hf.cats != undefined && allowedCount(kind, list) > 0;
        if (filter && allowedUsed[kind] >= allowedCount(kind, list)) {
            u = {};                    // megengedett, de már volt (rendesen a Q.apply nullázza)
        }
        for (var i = 0; i < list.length; i++) {
            if (!u[i] && (!filter || allowed(list[i]))) {
                counts[i] = seenCount(kind, s, i);
                if (counts[i] < least) {
                    least = counts[i];
                }
            }
        }
        for (var q = 0; q < list.length; q++) {
            var c = catOf(list[q]);
            if (!u[q] && (!filter || allowed(list[q]))) {
                if (byCat[c] == undefined) {
                    byCat[c] = {free: [], fresh: []};
                    cats.push(c);
                }
                byCat[c].free.push(q);
                if (counts[q] == least) {
                    byCat[c].fresh.push(q);
                }
            }
        }
        if (cats.length == 0) {
            return 0;
        }
        // azok a kategóriák, ahol van a legkevesebbek látta kérdés; az előző kérdés kategóriája csak
        // akkor, ha nincs más
        var pool = [];
        for (var j = 0; j < cats.length; j++) {
            if (byCat[cats[j]].fresh.length > 0) {
                pool.push(cats[j]);
            }
        }
        if (pool.length == 0) {
            pool = cats;
        }
        var other = [];
        for (var k = 0; k < pool.length; k++) {
            if (pool[k] != lastCat) {
                other.push(pool[k]);
            }
        }
        if (other.length > 0) {
            pool = other;
        }
        var b = byCat[pool[random(pool.length)]];
        var qs = b.fresh.length > 0 ? b.fresh : b.free;
        return qs[random(qs.length)];
    };

    // a válaszok véletlen sorrendje: shuffle[hely - 1] = az eredeti válasz sorszáma (1 = helyes)
    Q.shuffle = function () {
        var rest = [1, 2, 3, 4];
        var s = [];
        while (rest.length > 0) {
            s.push(rest.splice(random(rest.length), 1)[0]);
        }
        return s;
    };

    Q.apply = function (atip, idx, shuffle) {
        var kind = kindOf(atip);
        var list = listOf(atip);
        idx = Number(idx);
        if (isNaN(idx) || idx < 0 || idx >= list.length) {
            idx = 0;
        }
        var tag = list[idx];
        lastCat = catOf(tag);
        if (!used[kind][idx]) {
            used[kind][idx] = true;
            usedCount[kind]++;
            if (allowed(tag)) {
                allowedUsed[kind]++;
            }
            var total = allowedCount(kind, list);
            if (usedCount[kind] >= list.length || (total > 0 && allowedUsed[kind] >= total)) {   // elfogytak: újra mind
                used[kind] = {};
                usedCount[kind] = 0;
                allowedUsed[kind] = 0;
            }
        }
        var k = keyAt(kind, idx);
        seenOf(kind)[k] = true;
        EI.call('hfSeen', 'add', kind, k, Math.floor(list.length * 0.6));
        tag.selectcount++;
        if (atip) {
            game.questiontag = undefined;
            game.tipquestiontag = tag;
            return;
        }
        game.questiontag = tag;
        game.tipquestiontag = undefined;
        if (!shuffle || shuffle.length != 4) {
            shuffle = [1, 2, 3, 4];
        }
        game.answershuffle = [];
        game.goodanswerpos = 0;
        for (var p = 1; p <= 4; p++) {
            var a = Number(shuffle[p - 1]);
            game.answershuffle.push(a);
            if (a == 1) {
                game.goodanswerpos = p;
            }
        }
    };

    gqs.SelectQuestion = function (atip) {
        if (hf.mp) {
            hf.mp.applyQuestion(atip);        // a házigazda választotta
            return;
        }
        Q.apply(atip, Q.pick(atip), atip ? null : Q.shuffle());
    };
})();
