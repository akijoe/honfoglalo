package main

import (
	"regexp"
	"strings"
	"unicode"
)

// Egyszerű trágárságszűrő (magyar és angol) a csevegéshez, a szobák nevéhez és a játékosnevekhez.
// Szavanként vizsgál: a szót kisbetűsítve, ékezetek nélkül és a "leet" írást (0->o, 1->i, 3->e,
// 4->a, 5->s, 7->t, @->a, $->s) feloldva, és - ahol ez nem okoz tévedést - az ismétlődő betűket
// összevonva ("fuuuck"). A tiltott szót az első betűje után csillagokra cseréli. Csak a szokásos
// eseteket fogja meg (szóközökkel széthúzott szavakat nem), és kerüli az ártatlan szavakat
// (faszén, baszk, szarvas, segít).

type badWord struct {
	root   string
	how    byte     // 'c': bárhol a szóban, 'p': a szó elején, 'w': csak maga a szó
	except []string // ezekkel kezdődő szavak rendben vannak
	exact  bool     // az ismétlődő betűket nem vonjuk össze (pl. "segg" -> "seg" = segít)
}

var badWords = []badWord{
	// magyar
	{root: "kurva", how: 'c'},
	{root: "geci", how: 'c'},
	{root: "picsa", how: 'c'},
	{root: "fasz", how: 'c', except: []string{"faszen", "faszerk", "faszersz", "faszob", "faszall", "faszarit", "faszam", "faszab", "faszeg", "faszel", "faszur"}},
	{root: "basz", how: 'c', except: []string{"baszk", "basszus", "basszist"}},
	{root: "bazdmeg", how: 'c'},
	{root: "bazmeg", how: 'c'},
	{root: "buzi", how: 'p'},
	{root: "kocsog", how: 'c'},
	{root: "csicska", how: 'c'},
	{root: "ribanc", how: 'c'},
	{root: "segg", how: 'p', exact: true},
	{root: "szar", how: 'w'},
	{root: "szaros", how: 'w'},
	{root: "szarsag", how: 'w'},
	{root: "szarhazi", how: 'w'},
	{root: "fos", how: 'w'},
	{root: "fosos", how: 'w'},
	{root: "pina", how: 'w'},
	{root: "punci", how: 'w'},
	{root: "nyomorek", how: 'w'},
	// angol
	{root: "fuck", how: 'c'},
	{root: "fuk", how: 'w'},
	{root: "shit", how: 'c'},
	{root: "bitch", how: 'c'},
	{root: "cunt", how: 'c'},
	{root: "asshole", how: 'c'},
	{root: "motherf", how: 'c'},
	{root: "nigger", how: 'c'},
	{root: "nigga", how: 'c'},
	{root: "faggot", how: 'c'},
	{root: "fag", how: 'w'},
	{root: "dick", how: 'w'},
	{root: "dicks", how: 'w'},
	{root: "dickhead", how: 'w'},
	{root: "cock", how: 'w'},
	{root: "cocks", how: 'w'},
	{root: "cocksucker", how: 'c'},
	{root: "pussy", how: 'w'},
	{root: "slut", how: 'p'},
	{root: "whore", how: 'p'},
	{root: "wanker", how: 'p'},
	{root: "twat", how: 'w'},
	{root: "bastard", how: 'p'},
	{root: "retard", how: 'p'},
	{root: "prick", how: 'w'},
}

var foldRune = map[rune]rune{
	'á': 'a', 'é': 'e', 'í': 'i', 'ó': 'o', 'ö': 'o', 'ő': 'o', 'ú': 'u', 'ü': 'u', 'ű': 'u',
	'à': 'a', 'â': 'a', 'ä': 'a', 'è': 'e', 'ê': 'e', 'ë': 'e', 'ì': 'i', 'î': 'i', 'ï': 'i',
	'ò': 'o', 'ô': 'o', 'õ': 'o', 'ù': 'u', 'û': 'u',
	'0': 'o', '1': 'i', '3': 'e', '4': 'a', '5': 's', '7': 't', '@': 'a', '$': 's',
}

// a szó részének számít (a "leet" jelek is)
func wordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '@' || r == '$'
}

func foldWord(w string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(w) {
		if f, ok := foldRune[r]; ok {
			r = f
		}
		b.WriteRune(r)
	}
	return b.String()
}

func squeeze(s string) string {
	var b strings.Builder
	var last rune = -1
	for _, r := range s {
		if r != last {
			b.WriteRune(r)
		}
		last = r
	}
	return b.String()
}

func (bw badWord) matches(w string) bool {
	for _, e := range bw.except {
		if strings.HasPrefix(w, e) {
			return false
		}
	}
	switch bw.how {
	case 'p':
		return strings.HasPrefix(w, bw.root)
	case 'w':
		return w == bw.root
	}
	return strings.Contains(w, bw.root)
}

func isBad(word string) bool {
	w := foldWord(word)
	sq := squeeze(w)
	for _, bw := range badWords {
		if bw.matches(w) {
			return true
		}
		if !bw.exact && sq != w {
			sb := badWord{root: squeeze(bw.root), how: bw.how}
			for _, e := range bw.except {
				sb.except = append(sb.except, squeeze(e))
			}
			if sb.matches(sq) {
				return true
			}
		}
	}
	return false
}

// hasProfanity: van-e benne tiltott szó (a nevekben és a szobanevekben nem lehet).
func hasProfanity(s string) bool {
	return cleanProfanity(s) != s
}

// cleanProfanity: a tiltott szavak az első betűjük után csillagokra cserélve (csevegés).
func cleanProfanity(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if !wordRune(rs[i]) {
			b.WriteRune(rs[i])
			i++
			continue
		}
		j := i
		for j < len(rs) && wordRune(rs[j]) {
			j++
		}
		word := string(rs[i:j])
		if isBad(word) {
			b.WriteRune(rs[i])
			b.WriteString(strings.Repeat("*", j-i-1))
		} else {
			b.WriteString(word)
		}
		i = j
	}
	return b.String()
}

// Linkek: a csevegésben, a szobanevekben és a nevekben nem lehet linket küldeni, a rövid formát
// (facebook.com, bit.ly/x) sem. A gyakori végződéseket (TLD) figyeli; a magyar mondatokban gyakori
// rövid szavakat (is, no, in) kihagyja, hogy a pont utáni hiányzó szóköz ne számítson linknek.
var linkRe = regexp.MustCompile(`(?i)(https?://|www\.|[\p{L}\p{N}][\p{L}\p{N}-]*\.(com|net|org|hu|io|ly|me|co|gg|tv|info|biz|eu|de|uk|ru|xyz|app|dev|link|site|online|shop|store|tk|ml|ga|cf|gq|cc|us|ca|fr|it|es|pl|nl|ro|sk|cz|at|ch|be|se|dk|fi|pt|gr|tr|ua|cn|jp|kr|br|ai|gl|ws|fm|club|top|live|news|blog|page|world|today|fun|space|website|tech|pro|cloud|games|chat|social|xxx|cam|vip|bet|casino|porn|sex)([^\p{L}\p{N}]|$))`)

func hasLink(s string) bool {
	return linkRe.MatchString(s)
}
