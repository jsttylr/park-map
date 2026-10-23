// Package match pairs POTA park references with PAD-US boundary polygons.
package match

import (
	"strings"
	"unicode"
)

// abbrev expands short forms that show up in POTA and PAD-US names.
var abbrev = map[string]string{
	"natl": "national", "nat'l": "national", "sp": "state park", "sf": "state forest",
	"nf": "national forest", "nwr": "national wildlife refuge", "wma": "wildlife management area",
	"nm": "national monument", "nhs": "national historic site", "nra": "national recreation area",
	"np": "national park", "mt": "mount", "mtn": "mountain", "ft": "fort", "pt": "point",
	"st": "saint", "ste": "sainte", "co": "county", "rec": "recreation", "hist": "historic",
	"is": "island", "isl": "island", "cr": "creek", "ck": "creek",
}

// generic words describe the kind of land, not which place it is.
var generic = map[string]bool{
	"national": true, "state": true, "park": true, "parks": true, "forest": true, "forests": true,
	"recreation": true, "recreational": true, "area": true, "areas": true, "wildlife": true,
	"refuge": true, "management": true, "preserve": true, "natural": true, "historic": true,
	"historical": true, "site": true, "monument": true, "memorial": true, "wilderness": true,
	"conservation": true, "reserve": true, "game": true, "land": true, "lands": true,
	"county": true, "regional": true, "municipal": true, "city": true, "the": true, "of": true,
	"and": true, "at": true, "nature": true, "center": true, "sanctuary": true, "unit": true,
	"fish": true, "hunting": true, "public": true, "access": true, "community": true,
	"battlefield": true, "seashore": true, "lakeshore": true, "scenic": true, "wild": true,
	"heritage": true, "corridor": true, "district": true, "metro": true, "metropark": true,
	"habitat": true, "protection": true, "dedicated": true,
}

// Tokens returns the normalized words of a name with abbreviations expanded.
func Tokens(s string) []string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "c&d", "chesapeake and delaware")
	s = strings.ReplaceAll(s, "&", " and ")
	s = strings.NewReplacer("'", "", "’", "").Replace(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, s)
	var out []string
	for _, w := range strings.Fields(s) {
		if x, ok := abbrev[w]; ok {
			out = append(out, strings.Fields(x)...)
		} else {
			out = append(out, w)
		}
	}
	return out
}

// Key is a normalized full name, used to gather parcels of the same unit.
func Key(s string) string { return strings.Join(Tokens(s), " ") }

func core(t []string) map[string]bool {
	m := map[string]bool{}
	for _, w := range t {
		if !generic[w] {
			m[w] = true
		}
	}
	return m
}

func set(t []string) map[string]bool {
	m := map[string]bool{}
	for _, w := range t {
		m[w] = true
	}
	return m
}

func dice(a, b map[string]bool) float64 {
	if len(a)+len(b) == 0 {
		return 0
	}
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return 2 * float64(n) / float64(len(a)+len(b))
}

// Similarity scores two place names from 0 to 1. The distinctive words
// ("Acadia", "Bear Creek") count most; generic words ("State Park") add a little.
func Similarity(a, b string) float64 {
	ta, tb := Tokens(a), Tokens(b)
	ca, cb := core(ta), core(tb)
	full := dice(set(ta), set(tb))
	if len(ca) == 0 || len(cb) == 0 {
		return full
	}
	return 0.8*dice(ca, cb) + 0.2*full
}

// weak words are distinctive enough to keep in a name's core but too common to
// prove on their own that two names mean the same place ("St. Clair Flats" and
// "St. Johns Marsh" share only "saint").
var weak = map[string]bool{
	"saint": true, "sainte": true, "mount": true, "mountain": true, "fort": true, "point": true,
	"lake": true, "lakes": true, "river": true, "creek": true, "island": true, "islands": true,
	"valley": true, "north": true, "south": true, "east": true, "west": true, "upper": true,
	"lower": true, "new": true, "little": true, "big": true, "great": true, "old": true,
	"bay": true, "harbor": true, "beach": true, "spring": true, "springs": true, "pond": true,
	"trail": true, "trails": true, "greenway": true, "parkway": true, "byway": true, "path": true,
	"canal": true, "route": true, "road": true,
}

// SharesName reports whether two names have a distinctive word in common, or
// the same words apart from generic ones ("New River Trail").
func SharesName(a, b string) bool {
	ca, cb := core(Tokens(a)), core(Tokens(b))
	same := len(ca) > 0 && len(ca) == len(cb)
	for w := range ca {
		if cb[w] && !weak[w] {
			return true
		}
		same = same && cb[w]
	}
	return same
}

// Parts splits a combined park name into the names of the units it joins:
// "George Washington & Jefferson National Forest" gives "george washington
// national forest" and "jefferson national forest" (as Keys, with plurals
// made singular). The kind of land at the end applies to every part. Names
// without "and" give nothing.
func Parts(name string) []string {
	t := strings.Fields(SingularKey(name))
	end := len(t)
	for end > 0 && generic[t[end-1]] && t[end-1] != "and" {
		end--
	}
	suffix := t[end:]
	if len(suffix) == 0 {
		return nil
	}
	var parts [][]string
	cur := []string{}
	for _, w := range t[:end] {
		if w == "and" {
			parts = append(parts, cur)
			cur = []string{}
			continue
		}
		cur = append(cur, w)
	}
	parts = append(parts, cur)
	if len(parts) < 2 {
		return nil
	}
	var out []string
	for _, p := range parts {
		strong := false
		for _, w := range p {
			strong = strong || !generic[w] && !weak[w]
		}
		if !strong {
			return nil
		}
		out = append(out, strings.Join(append(append([]string{}, p...), suffix...), " "))
	}
	return out
}

// SingularKey is Key with plural kinds of land made singular ("forests").
func SingularKey(s string) string {
	t := Tokens(s)
	for i, w := range t {
		if x := strings.TrimSuffix(w, "s"); x != w && generic[x] {
			t[i] = x
		}
	}
	return strings.Join(t, " ")
}

// CoreWithin reports whether every distinctive word of a is in b, so b's
// name fully covers a's ("White River" within "Dale Bumpers White River").
func CoreWithin(a, b string) bool {
	ca, cb := core(Tokens(a)), core(Tokens(b))
	if len(ca) == 0 {
		return false
	}
	for w := range ca {
		if !cb[w] {
			return false
		}
	}
	return true
}

// IsTrail reports whether a name is a trail's. A trail park is only the trail,
// so it must not take the outline of the forest or park it runs through.
func IsTrail(name string) bool {
	for _, w := range Tokens(name) {
		if w == "trail" || w == "trails" {
			return true
		}
	}
	return false
}

// Distinctive returns the words of a name that pick out the place, leaving
// out generic and weak ones, for finding names worth comparing.
func Distinctive(name string) []string {
	var out []string
	for w := range core(Tokens(name)) {
		if !weak[w] {
			out = append(out, w)
		}
	}
	return out
}
