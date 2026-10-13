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
// alkalmazza (Q.apply), így a "már volt" lista minden gépen azonos marad.

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

    // egy még fel nem tett kérdés indexe: előbb kategória, aztán kérdés; a nem látottak előnyben
    Q.pick = function (atip) {
        var kind = kindOf(atip);
        var list = listOf(atip);
        var u = used[kind];
        var s = seenOf(kind);
        var byCat = {};
        var cats = [];
        for (var i = 0; i < list.length; i++) {
            if (!u[i]) {
                var c = catOf(list[i]);
                if (byCat[c] == undefined) {
                    byCat[c] = {free: [], fresh: []};
                    cats.push(c);
                }
                byCat[c].free.push(i);
                if (!s[Q.key(list[i])]) {
                    byCat[c].fresh.push(i);
                }
            }
        }
        if (cats.length == 0) {
            return 0;
        }
        // azok a kategóriák, ahol van még nem látott kérdés; az előző kérdés kategóriája csak akkor,
        // ha nincs más
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
            if (usedCount[kind] >= list.length) {      // elfogytak: újra mind
                used[kind] = {};
                usedCount[kind] = 0;
            }
        }
        var k = Q.key(tag);
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
