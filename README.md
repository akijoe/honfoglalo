# Honfoglaló – a régi próbajáték, többjátékos móddal

A honfoglalo.hu főoldalán 2009 októberétől 2013 májusáig futó Flash próbajáték, **az eredeti SWF-fel**
(a 2011. októberi mentésből), a [Ruffle](https://ruffle.rs) Flash-emulátorban, egy kis Go szerverrel a
többjátékos szobákhoz. Hogy miből derül ki az időszak, lásd: [Mettől meddig volt ez a dizájn](#mettől-meddig-volt-ez-a-dizájn).

## Indítás

Go 1.22 vagy újabb kell hozzá. A projekt mappájából:

```bash
go run ./server
```

Utána nyisd meg: <http://localhost:8765/>

A szerver minden hálózati címen figyel, így a szobák linkje a helyi hálózaton (vagy egy domain
mögött) a barátaidnál is működik. Csak a saját gépre: `go run ./server -addr 127.0.0.1:8765`.

| Kapcsoló | |
|---|---|
| `-addr` | figyelési cím (alapból `:8765`) |
| `-root` | a projekt mappája (alapból `.`) |
| `-feedback` | a visszajelzések fájlja (alapból `feedback.jsonl`) |
| `-static=false` | csak az API; a játék fájljai máshonnan jönnek (lásd: [Élesítés](#élesítés-cloudflare-pages--vps)) |
| `-origins` | mely oldalak hívhatják az API-t egy másik címről (CORS), vesszővel; `*` = bármelyik |
| `-trust-proxy` | proxy mögött a játékos címe a `CF-Connecting-IP` / `X-Forwarded-For` fejlécből jön (a korlátozásokhoz) |

## A menü

| Gomb | Mi történik |
|---|---|
| Bemutató játék | az eredeti oktató játék a kék tippekkel |
| Gyors játék robotokkal | teljes játék Jani és Éva ellen, oktató üzenetek nélkül, élesben számoló órával |
| Gyors játék egy ismerőssel | szoba létrehozása vagy csatlakozás egy szobához (lent) |
| Visszajelzés (jobb lent, az egykori regisztráció helyén) | rövid üzenet a szervernek |

### Térkép

Az oldal bal szélén (keskeny, álló képernyőn felül) választható a térkép: a SWF mind a 13 országa
játszható (Magyarország, Franciaország, Németország, Olaszország, Belgium, Csehország, Szlovákia,
Szlovénia, Románia, Szerbia, Bulgária, Ukrajna, Oroszország), a régiók szomszédsági adataival együtt.

- Egyjátékos módban (menü, bemutató, robotok) a böngésző megjegyzi a választást; váltáskor a mostani
  mód újraindul az új térképen (futó játéknál előbb rákérdez).
- Egy szobában a főnök választ az indulás előtt (a szoba létrehozásakor a saját választása lesz a
  kezdő térkép); a többiek a szoba térképét látják, a szoba-ablakban is. Játék közben nem váltható.
- A kis képeket (`img/maps/`) a `tools/map_thumbs.py` rajzolja a SWF térképeiből.

Az óra lejártakor a robot- és a többjátékos játékban: területválasztásnál (foglalás és támadás)
véletlen terület, feleletválasztósnál nincs válasz, tippnél 0.

A játék végén (mindhárom módban) a teljes kliens „A játék végeredménye” ablakának mintájára rajzolt
ablak jön: a *Végeredmény* fülön a helyezések a játékosok színével, a *Válaszaid* fülön a saját
statisztikád (jó válaszok, megnyert tippek, legpontosabb tipp). Az **Új játék** gomb ugyanolyan
játékot indít (többjátékosban a szoba-ablakot nyitja), az **OK** után a menü jön. A teljes kliens
ablakának harmadik füle (Aranytallérok) és a „Szerzek még tallért!” gomb itt nincs, mert ebben a
változatban nincs tallér.

## Többjátékos játék

- **Szoba létrehozása:** csak egy név kell. Utána megjelenik a szoba kódja és egy másolható link
  (`http://…/?join=KÓD`), ezt kell elküldeni a barátoknak.
- **Csatlakozás:** név és szobakód, vagy egyszerűen a link megnyitása (a kód ki van töltve).
- A szobában látszik, ki lépett be (legfeljebb 3 játékos). A **főnök** (aki létrehozta) indítja a
  játékot, ha legalább ketten vannak; az üresen maradt hely robotja Jani vagy Éva.
- Minden gépen ugyanaz az eredeti játék fut. Ami véletlen vagy egy játékos döntése (bázisok,
  kérdések, területek, válaszok, tippek), azt egy gép dönti el, és a szerver mindenkinek ugyanabban
  a sorrendben küldi el. A tippversenyben itt a valódi gyorsaság számít.
- Ha valaki kilép vagy elmegy a kapcsolata, helyette robot játszik tovább; ha valaki 40 másodpercig
  nem lép (pl. háttérbe tette a lapot), helyette a lejárt idő szerinti lépés megy el.
- **Kilépés, szobakód:** játék közben az oldal bal sávjában (keskeny képernyőn felül) mindig ott a
  **Kilépés a főoldalra** gomb és a szoba kódja (a **Link** gomb a meghívót másolja). Kilépés után a
  helyeden robot játszik tovább, és a főoldalon a szoba-ablak kínálja fel a visszalépést.
- **Visszalépés:** aki frissíti a lapot, rögtön visszakerül a helyére (a menü be sem töltődik). A
  kimaradt lépéseket a játék kép és hang nélkül, néhány másodperc alatt lejátssza (felzárkózás), a
  térkép és a pontok így pontosan ugyanott tartanak, mint a többieknél, és onnan élőben megy tovább.
  A szoba a laphoz tartozik: egy másik lap ugyanabban a böngészőben csak akkor lép a helyére, ha az
  eredeti lap már nincs a játékban (pl. bezárták); különben új látogató, aki a meghívóval új
  játékosként léphet be. Ha a böngésző elfelejtette a szobát, vagy másik gépről jönnél vissza, a
  szoba kódjával és **ugyanazzal a névvel** a „Belépés” visszavisz a régi helyedre (ha ott épp nincs
  élő kapcsolat; a régi belépő érvényét veszti).

### A szerver (`server/`)

Nincs adatbázis: a szobák állapota a memóriában van, előzmények nincsenek. Egy szoba megszűnik,
ha 15 percig senki sincs benne (vagy 8 óra után). Sok szoba futhat egyszerre; egy szoba néhány
kilobájt, a játékosonként nyitott kapcsolat egy Server-Sent Events folyam.

| Végpont | |
|---|---|
| `POST /api/create` `{name}` | új szoba → `{code, seat, token}` |
| `POST /api/join` `{code, name}` | csatlakozás → `{code, seat, token}`; futó játéknál visszalépés ugyanazzal a névvel |
| `GET /api/events?code&token` | eseményfolyam: a szoba állapota és a játék döntései |
| `POST /api/start`, `/api/leave` `{code, token}` | indítás (főnök), kilépés |
| `POST /api/map` `{code, token, map}` | a szoba térképe (csak a főnök, indulás előtt; `/api/create` is fogad `map`-et) |
| `POST /api/check` `{code, token}` | él-e még a hely; a szoba állapota és `live` (van-e most élő kapcsolata), kapcsolódás nélkül |
| `GET /api/health` | `ok` és a szobák / kapcsolatok száma (felügyelethez) |
| `POST /api/send` `{code, token, k, d}` | egy döntés; kulcsonként az első érvényes |
| `POST /api/feedback` `{name, text}` | visszajelzés → `feedback.jsonl` (soronként egy JSON) |

### Védelem

Egyszerű korlátok a visszaélések ellen (`server/limits.go`; egy komoly támadás ellen a Cloudflare vagy a
tárhely védelme kell):

| Mire | Korlát |
|---|---|
| szoba létrehozása, csatlakozás | IP-nként percenként 30 |
| nyitott szobák | IP-nként egyszerre 5 (a megszűnt szobák helyet adnak) |
| API-hívások | IP-nként 10 másodpercenként 300 |
| élő kapcsolatok (eseményfolyamok) | IP-nként 20, szobánként 12, összesen 20 000 |
| visszajelzés | IP-nként 15 másodpercenként 1, összesen percenként 30, a fájl legfeljebb 20 MB |
| kérés | törzs 8 KB, fejléc 16 KB, beolvasás 20 mp; játékonként legfeljebb 5000 döntés |

Proxy mögött (`-trust-proxy`) a korlátok a játékos valódi címére vonatkoznak.

### Mennyit bír?

Mérés (2026-10-12, `GOMAXPROCS=1`, egy CPU-magra szorítva, 300 egyidejű játék = 900 élő kapcsolat,
játékonként 5 másodpercenként egy döntés, ami a valódi játéknál sűrűbb): a szerver egy mag 0,6%-át
használta, kapcsolatonként kb. 25 kB memóriát, a döntések 0,15 ms (p99: 0,27 ms) alatt értek
mindhárom játékoshoz. Ebből egy 1 vCPU / 1 GB RAM-os VPS-en a memória kb. 5000 egyidejű játékig
(15 000 játékosig) elég; HTTPS-proxyval (Caddy, nginx) ennek nagyjából a fele. Kapcsolatonként egy
fájlleíró kell: a Go induláskor magától a rendszer felső határáig emeli a korlátot, ezt csak akkor kell
állítani (`ulimit -Hn`, systemd alatt `LimitNOFILE`), ha a felső határ néhány ezer alatt van.

Ha a szerver a játék fájljait is kiszolgálja, a szűk keresztmetszet inkább a forgalom: egy új látogató
első betöltése kb. 6,1 MB (ebből 5 MB a Ruffle wasm-fájlja gzippel; a tartalom-hash nevű Ruffle-fájlokat a
böngésző egy évig tárolja, így a visszatérő látogató legfeljebb kb. 1 MB-ot tölt le); 1 TB havi kerettel ez
kb. 160 000 új látogató. Ha a játék a Cloudflare Pages-ről jön (lent), a VPS-hez csak a többjátékos
forgalom jut el, ami játékonként néhány tíz kilobájt, így ott a memória a határ.

A régi próbajáték-végpontokat is kiszolgálja (`gettgquestions.php`, `quickgame.php`, `registration.php`),
a fájlok közül csak a játékhoz kellőket (`index.html`, `js/`, `swf/`, `vendor/ruffle/`, `sounds/`, `dat/`, `img/`,
`questions.xml`). Teszt: `go test ./server`.

## Élesítés: Cloudflare Pages + VPS

A játék (egyjátékos módok, menü, kérdések, hangok) statikus fájlokból áll, ezt a Cloudflare Pages
szolgálja ki ingyen és korlátlan forgalommal. A VPS-en csak a Go szerver fut API-módban, a szobákhoz
és a visszajelzésekhez. Ha a VPS nem érhető el, az egyjátékos játék akkor is működik.

**1. A szerver a VPS-en** (Linux, amd64; a fordítás a saját gépen is mehet):

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o honfoglalo-server ./server
```

- másold fel a `honfoglalo-server`-t a VPS `/usr/local/bin/` mappájába;
- `deploy/honfoglalo.service`: írd át benne az `-origins` értékét a Pages-címedre (pl.
  `https://honfoglalo.pages.dev`, több cím vesszővel), majd
  `sudo cp deploy/honfoglalo.service /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable --now honfoglalo`;
- `deploy/Caddyfile`: az `api.example.com` helyére a VPS-re mutató domain (vagy aldomain) kerül; a Caddy
  magától kér HTTPS-tanúsítványt. `sudo cp deploy/Caddyfile /etc/caddy/Caddyfile && sudo systemctl reload caddy`;
- ellenőrzés: `curl https://api.example.com/api/health`.

A szolgáltatás a `127.0.0.1:8765` címen, saját (dinamikus) felhasználóval fut, a visszajelzéseket a
`/var/lib/honfoglalo/feedback.jsonl` fájlba írja. Ha a domain a Cloudflare proxyja (narancs felhő)
mögött van, a Caddyfile `header_up -CF-Connecting-IP` sorát töröld.

**2. A játék a Cloudflare Pages-en:**

```bash
python3 tools/build_site.py --api https://api.example.com/
```

Ez a `dist/` mappába rakja össze az oldalt (kb. 30 MB, 33 fájl; a legnagyobb a 14 MB-os Ruffle wasm, a Pages fájlonként 25 MiB-ot enged): a `js/config.js`-be kerül a szerver címe, a
`sounds/manifest.json` a hangmappából készül, a `_headers` fájl pedig a tartalom-hash nevű Ruffle-fájlokat
egy évre cache-elhetővé teszi. Feltöltés: `npx wrangler pages deploy dist --project-name honfoglalo`,
vagy a Cloudflare felületén a Git-tárolóból (build parancs: `python3 tools/build_site.py`, kimeneti mappa:
`dist`, környezeti változó: `HF_API_URL=https://api.example.com/`).

A meghívó linkek (`?join=KÓD`) a Pages-címre mutatnak. HTTPS-en is minden működik: az eredeti kód a
`https://` címet helyi fájlnak hitte volna, ezt a `src/core.as` a játék indulásáig áthidalja.

Egy gépen, egyben (fejlesztéshez vagy ha nincs szükség a szétválasztásra) továbbra is elég a
`go run ./server`.

**A mostani élesítés** (2026-10-13): a játék a Cloudflare Pages `honfoglalo` projektjében
(`https://honfoglalo.pages.dev`, saját domain: `honfoglalo.online`), a szerver az AWS VPS-en
(`https://api.honfoglalo.online`, Ubuntu 24.04, Caddy + systemd). Frissítés:

```bash
# a játék (az SWF, a kérdések, a hangok, az oldal) -> Cloudflare Pages
python3 tools/build_site.py --api https://api.honfoglalo.online/
npx wrangler@4 pages deploy dist --project-name honfoglalo --branch main --commit-dirty=true

# a szerver -> VPS
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o honfoglalo-server ./server
scp -i ~/.ssh/my_key.pem honfoglalo-server ubuntu@16.192.74.233:/tmp/
ssh -i ~/.ssh/my_key.pem ubuntu@16.192.74.233 'sudo install -m 755 /tmp/honfoglalo-server /usr/local/bin/ && sudo systemctl restart honfoglalo'
```

A szerver újraindításakor a futó szobák megszűnnek (a memóriában vannak), ezért azt csendes időben érdemes.

## Kérdések

A játék a `questions.xml`-t tölti be: 715 feleletválasztós és 245 tippkérdés, nyolc kategóriában,
kategóriánként nagyjából egyforma számban:

| Kategória (`CAT`) | Feleletválasztós | Tipp |
|---|---|---|
| Földrajz (`foldrajz`) | 92 | 30 |
| Sport (`sport`) | 87 | 32 |
| Irodalom (`irodalom`) | 117 | 30 |
| Művészet (`muveszet`) | 92 | 29 |
| Természettudományok – biológia, kémia (`termtud`) | 80 | 28 |
| Matematika, fizika (`matfiz`) | 82 | 35 |
| Életmód, mindennapok (`eletmod`) | 86 | 31 |
| Szórakozás – zene, film, popkultúra (`szorakozas`) | 79 | 30 |

A források: `questions_2013.csv` (a próbajáték eredeti kérdései, `honfoglalo.hu/gettgquestions.php`) és
`questions_extra.csv` (a 2026-ban írt kérdések, többségük közepes és nehéz). A `questions.xml` ezekből épül:

```bash
python3 tools/import_questions.py --rebuild
```

A tisztán történelmi kérdések (királyok, csaták, évszámok) és a nagyon könnyűek (pl. hány lába van a
póknak) a fájlok végén `#`-tel kikapcsolva megvannak; a `#` törlésével és egy kategória alá írva
visszakapcsolhatók.

A kérdés mindig véletlenszerű, a bemutatóban, a robotjátékban és a többjátékos játékban is
(`src/questions.as`): a játék előbb egy kategóriát sorsol (az előző kérdésétől eltérőt), aztán abból egy
kérdést, így minden kategória egyforma gyakran jön, akárhány kérdés van benne. Egy játékon belül nincs
ismétlés, a válaszok sorrendje is véletlen, és a böngésző megjegyzi a legutóbb látott kérdéseket, így a
következő játékokban előbb a még nem látottak jönnek. Többjátékos játékban a házigazda gépe választ, és
mindenki ugyanazt a kérdést kapja.

A mai Honfoglaló (Triviador) kérdései nincsenek benne: azok csak bejelentkezés után, a mobilalkalmazásban
érhetők el. Saját kérdések hozzáadása (a meglévőket kihagyja):

```bash
python3 tools/import_questions.py kerdesek.csv
```

CSV-sorok (`;`, `,` vagy tabulátor, `#`-tel kezdődő sor = megjegyzés): `# kategória: Sport` (a következő
kérdések kategóriája), `kérdés;helyes;rossz;rossz;rossz` (feleletválasztós) vagy `kérdés;szám` /
`kérdés;szám;min;max` (tippkérdés, a min–max a robotok tippjeihez kell). JSON-t is elfogad (`category`
mezővel). A válasz egy sorban, kb. 22 karakterben fér el a gombon.

## Hangok

Az eredeti 2011-es hangfájl (`client/sounds.swf`) elveszett; a `sounds/` mappában a 2026-ban
összegyűjtött hangok vannak, a játék hangneveire átnevezve. Egy hanghoz elég a fájlt a hang nevén
(`<hangnév>.mp3`, `.ogg` vagy `.wav`) a `sounds/` mappába tenni: a szerver a mappából állítja össze a
listát (`/sounds/manifest.json`). A `sounds/manifest.json` fájlba csak akkor kell írni, ha egy hang
más nevű fájlt kap; most így szól a `tiktak_2` a `tiktak_3.mp3`-mal, a `tip_appear` a `question_resultsappear.mp3`-mal
és a `question_second` a `question_lose.mp3`-mal.

| Fájl (`sounds/`) | Mikor szól | Eredeti neve |
|---|---|---|
| `game_start.mp3` | a játék kezdete (bázissorsolás; a főoldalon a Bemutató játék gombra) | startgame_or_bazisfoglalas_kezdes |
| `castle_appear.mp3` | egy bázis lehelyezése a játék elején | base_init |
| `tiktak_1.mp3` | az óra első fele | tiktak |
| `tiktak_3.mp3` | az óra második fele (`tiktak_2` és `tiktak_3` is ez) | tiktak_double |
| `map_selectmouseover.mp3` | egér egy választható megye fölött | hover_sound |
| `map_select.mp3` | megyét választ valaki | pick_megye |
| `map_territoryfill.mp3` | egy megye új gazdához kerül | megye_claim |
| `map_attackfail.mp3` | a támadó elveszti a választott megyét (sikertelen támadás) | megye_unpick |
| `answer_pick.mp3` | valaki válaszol vagy tippel: te, egy ellenfél vagy egy robot (lent) | pick_answer |
| `answer_sent.mp3` | jelenleg nem szól | answer_sent |
| `question_resultsappear.mp3` | előkerül egy ellenfél tippkártyájának értéke (`tip_appear`); a feleletválasztós kérdés eredménye | everyone_picked_number |
| `question_goodappear.mp3` | megjelenik a tippkérdés helyes válasza | reveal_number_answer |
| `question_win.mp3` | te nyerted a kérdést | hallelujah |
| `question_lose.mp3` | elvesztetted a kérdést; második lettél a tippben (`question_second`) | lose_answer |
| `playerbox_scorechange.mp3` | változik egy játékos pontszáma (ha nem egy foglalás miatt, lásd lent) | – |
| `castleattack_in.mp3` | sikeres vártámadás: a vár kinagyítva előjön, 0,84 mp-nél a robbanás, aztán visszaáll | boom |
| `turnwindow_roundchange.mp3` | körváltás a harcban (az utolsó kör végén nem, lent) | round_start |
| `game_end.mp3` | a játék vége (a körváltás hangjával kezdődik) | end_of_game |

Az `answer_pick` abban a pillanatban szól, amikor valaki válaszol vagy tippel: te a gomb megnyomásakor,
egy ellenfél akkor, amikor a válasza beérkezik. A lejárt idő nem válasz, akkor nincs hang. A robotok
gondolkodnak (1,2 mp és az óra 45%-a között): ha előbb válaszolsz, a játék megvárja őket, mint egy élő
ellenfelet (0,6 mp után a homokórás doboz mutatja, kire vár), és a tippek gyorsasági sorrendje a valódi
válaszidőket követi. Többjátékos játékban a robot döntését a házigazda gépe küldi el a gondolkodási idő
után, így mindenkinél egyszerre szól.

Amint beküldted a tippedet, előjön a kártyád a tipped értékével, és azoké, akik már tippeltek (az
értékük még rejtve); mindenki más kártyája abban a pillanatban, amikor tippel. A kártyán ott a
tippelési idő (pl. `05.85`, lejárt időnél `--.--`): egyenlő eltérésnél ez dönt. Ha mindenki tippelt,
tippelési sorrendben előkerülnek a többiek értékei, mindegyik `question_resultsappear` hanggal
(pontosan 0,6 mp-enként), és az utolsó után 1,6 mp-cel jön a helyes
válasz (`question_goodappear`).

A `map_territoryfill.mp3` és a `castle_appear.mp3` végén már benne van a pontszám változásának hangja,
ezért közvetlenül utánuk (2,5 mp-en belül) a `playerbox_scorechange` nem szól külön. A `game_end.mp3`
eleje a körváltás hangja, ezért az utolsó kör végén a `turnwindow_roundchange` nem szól (csak ha
döntetlen miatt még egy tippkérdés jön).

A vártámadás egyetlen hang: a játék a vár kinagyításakor kéri a `castleattack_in`-t, 0,8 mp múlva
az `explosion`-t, a vár visszaállásakor a `castleattack_out`-ot. A `castleattack_in.mp3` mindhármat
tartalmazza (a robbanás 0,84 mp-nél egybeesik a képpel), ezért az `explosion` és a `castleattack_out`
szándékosan üres.

**Hiányzó hangok** (ezeknél csend van):

| Hangnév | Mikor szólna |
|---|---|
| `host_tiktak_3` | egyszeri figyelmeztetés, amikor az óra utolsó negyedébe ér |
| `question_neutral` | a kérdés, amelyben nem vettél részt, eldőlt |

A teljes kliens további hangneveit (`question_win2` = győzelem halleluja nélkül, `turnwindow_appear`,
`turnwindow_appear_battle`) a próbajáték nem kéri.

A hangokat a böngésző a játék indulásakor egyszer letölti és előre dekódolja (Web Audio), így pontosan
akkor szólalnak meg, amikor a játék kéri, és egymásra lóghatnak. Ha a játék ugyanazt a hangot egyszerre
többször kéri (pl. tippdobozonként), csak egyszer szól.
A hangerőt a játékosdobozodban lévő hangszóró-gombbal lehet állítani.

## Mi változott az SWF-ben

A `swf/testclient.swf`-et a `tools/patch_swf.py` állítja elő az eredetiből (`archive/swf/testclient.swf`).
A grafika és a játékszabályok nem változtak; a kiegészítések ActionScript-forrásként vannak a `src/`
mappában, a `tools/as1c.py` (kis AS1 → AVM1 fordító) fordítja őket:

- `src/core.as` – hang-híd (`_global.gsound` → `js/hf.js`), hálózati híd (`hfNet`), HTTPS-áthidalás
- `src/tweaks.as` – menü (visszajelzés, szobák), robotjáték (nincs oktatás, élő óra), hangok, statisztika
- `src/lobby.as` – a Flash-ablakok: szoba létrehozása / csatlakozás / várakozás, visszajelzés,
  hangerő, a játék végeredménye
- `src/questions.as` – véletlen kérdésválasztás
- `src/mp.as` – a többjátékos játék: a döntések szinkronja, a játékosok helye (`game.IAM`), felzárkózás

Kisebb kiegészítések a felületen:
- a nevek (a vár fölött, a tippkártyákon, a harc fejlécében, a játékosdobozokban) egy sorban maradnak, és
  ha nem férnek el, kisebb betűvel jelennek meg (a kétszavas névnek eddig csak az első szava látszott);
- a játékosdobozok pontszáma fehér (a számláló „hardlight” keverési módját a Ruffle sötétre rajzolta);
- hangszóró-gomb a saját játékosdoboz bal alsó sarkában: kattintásra hangerő-csúszka nyílik, mindig
  legfelül van (a szoba- és visszajelzés-ablakok fölött is), a hangerőt a böngésző megjegyzi;
- az eredeti kliens kék **várakozó doboza** forgó homokórával, a piros utasításdoboz helyén, valahányszor
  másra kell várni: „Éva választ területet”, „Béla támad”, „Béla válaszát várjuk...”, „A többiek válaszát
  várjuk...” (a lang.json `gameinfo_*` szövegei). A próbajátékban csak a piros, órás doboz maradt meg, a
  kéket egy felvétel alapján rajzolja újra a `src/lobby.as`. A robotok területválasztás és támadás előtt
  0,8–2 mp-ig „gondolkodnak” (eddig azonnal léptek), közben ez a doboz látszik; többjátékosban a
  házigazda gépe tartja ki ezt az időt, így mindenkinél egyszerre látszik.

Az oldal (`index.html`, `js/config.js`, `js/hf.js`; háttér: `img/bg.png`, ikon: `favicon.ico`) tölti be a Ruffle-t, és köti össze a játékot a
szerverrel (a szerver címe a `js/config.js`-ben van; üresen = ugyanaz a cím, ahonnan az oldal jön).
Teszteléshez: `?debugclock=3` minden órát 3 másodpercre rövidít, a `?slot=a` / `?slot=b` külön
szoba-munkamenetet ad, így egy böngészőlapon (két iframe-ben) két játékos is lehet, a `?debug` pedig
bekapcsolja a Ruffle naplóját és a `hfDbg('game.state')` lekérdezést a lejátszó elemén
(`hfDbg('call:game.btnAnswerPressed', 2)` meg is hív egy függvényt). `?map=FR` erre a betöltésre más
térképet ad (a választót nem írja át).

A robotok tudása az eredeti próbajátékét követi: alkalmazkodnak az állásodhoz, és nem foglalják el a várat,
ha már két tornya ledőlt (a többjátékos játékban a robotok semlegesek).

### Nem használt elemek az SWF-ben

- A 12 további országtérkép (az eredeti próbajáték csak a magyart használta) most már a térképválasztóból
  játszható (fent).
- `KOSTENLOS`: egy „ingyenes” felirat a menüablakon, csak a német nyelvű változatban látszott.
- A teljes kliens végeredmény-ablaka (`GAMEOVERWIN`) csak névként szerepel a kódban, a grafikája nincs
  a próbajátékban; ezért rajzolja a `src/lobby.as` újra.

## Mettől meddig volt ez a dizájn

A honfoglalo.hu főoldalának web.archive.org-mentései szerint a próbajáték (`swf/testclient.swf`) a
**2009. október 10. és 14. közötti** oldalcserétől a **2013. május 4. és 11. közötti** oldalcseréig
szerepelt a főoldalon (a mentések között nincs sűrűbb):

| Mentés | A főoldal |
|---|---|
| 2009-10-10 | a régi oldal, még próbajáték nélkül |
| 2009-10-14 | új nyitóoldal („Kvíz, stratégia, játék :: Nyitóoldal”), rajta a `swf/testclient.swf` |
| 2010–2013, havonta | ugyanez az oldal, ugyanazzal a próbajátékkal |
| 2013-05-04 | még a próbajáték |
| 2013-05-11 | új oldal („Szervusz Honfoglaló!” belépés), próbajáték nélkül |

Mindkét váltás az egész játékot érintette, nem csak a főoldalt: 2009. október 15–25. között jelennek
meg az archívumban az új kliens betöltőoldalai (`client_frame.php`, `client_load.php`,
`clientquery_sysconf.php`), 2013. május közepén pedig egy új betöltő (`client/loader_3_hf.swf`). A
próbajáték tehát a 2009 és 2013 közötti teljes kliens grafikáját mutatja (barna-arany felület, a
játékosszínek piros, sárga és zöld). A magyar Wikipédia szerint később a sárga helyett kék lett a
harmadik szín, a böngészős játék pedig az Adobe Flash megszűnésével, 2021. január 21. után már nem volt
játszható.

Megkötések: a SWF-et magát egyszer mentették, 2011. október 29-én (2013. június végén már 404), így a
2009-es és a 2013-as próbajáték apróságokban eltérhetett ettől. Ugyanez a próbajáték szolgált a külföldi
változatoknál is (Triviador, korábban conQUIZtador, Dobyvatel): 13 ország térképe van benne, és az
alapértelmezett nyelve a szlovák.

## Mi honnan van (web.archive.org)

| Fájl | Forrás |
|---|---|
| `archive/swf/testclient.swf` | `honfoglalo.hu/swf/testclient.swf` (2011-10-29) – maga a játék |
| `archive/xml/gettgquestions_*.xml` | a próbajáték kérdései (2009, 2013) |
| `dat/lang.json` | `honfoglalo.hu/dat/lang.json` (2013-01-30) – a feliratok |
| `archive/swf/client3_hu_335.swf` | a 2004-es teljes kliens (forrásanyag) |

A `tools/fetch_archive.sh` mindent újra letölt, a `tools/fetch_ruffle.sh` a Ruffle 0.7.1-et (integritás-ellenőrzéssel).

## Eszközök (`tools/`)

- `patch_swf.py`, `as1c.py` – az SWF építése a `src/` forrásaiból
- `build_site.py` – a statikus oldal összeállítása a Cloudflare Pages-hez (`dist/`)
- `map_thumbs.py` – a térképválasztó kis képei (`img/maps/`) a SWF térképeiből
- `import_questions.py` – a `questions.xml` építése (`--rebuild`), kérdések hozzáadása CSV-ből vagy JSON-ból
- `extract_sounds.py` – hangok kibontása egy SWF-ből (ha az eredeti `sounds.swf` előkerülne)
- `swfx.py`, `swftree.py`, `shape2svg.py`, `avm1.py`, `avm1dec.py` – SWF bontás és ActionScript-visszafejtés
- `wb.sh`, `fetch_archive.sh`, `fetch_ruffle.sh` – letöltés
