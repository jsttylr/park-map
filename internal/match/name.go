// Package match pairs POTA park references with PAD-US boundary polygons.
package match

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
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
	"habitat": true, "protection": true, "dedicated": true, "blm": true, "herd": true,
}

// Tokens returns the normalized words of a name with abbreviations expanded.
func Tokens(s string) []string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "c&d", "chesapeake and delaware")
	s = strings.ReplaceAll(s, "&", " and ")
	// Apostrophes, and Hawaiian ʻokina however they're typed ("Koke`e",
	// "ʻEwa"), join the word; accents and macrons drop ("Pāʻulaʻula").
	s = strings.NewReplacer("'", "", "’", "", "‘", "", "`", "", "ʻ", "", "ʼ", "").Replace(norm.NFD.String(s))
	s = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, s)
	var out []string
	for _, w := range strings.Fields(s) {
		if w == "country" && len(out) > 0 && out[len(out)-1] == "back" {
			out[len(out)-1] = "backcountry" // "Back Country Byway"
		} else if x, ok := abbrev[w]; ok {
			out = append(out, strings.Fields(x)...)
		} else if x := strings.TrimSuffix(w, "s"); x != w && generic[x] && !generic[w] {
			out = append(out, x) // "Battlefields" is the kind of land "Battlefield" is
		} else {
			out = append(out, w)
		}
	}
	return out
}

// initials joins each run of single letters into one word, so "D.O.D.
// Ponds" reads like "DOD Ponds". It reports whether anything was joined.
// Names are compared both ways, since "W.C. Putnam" also needs its "c" to
// match "William C. Putnam".
func initials(ws []string) ([]string, bool) {
	var out []string
	joined := false
	for i := 0; i < len(ws); i++ {
		j := i
		for j < len(ws) && len(ws[j]) == 1 && unicode.IsLetter(rune(ws[j][0])) {
			j++
		}
		if j-i >= 2 {
			out = append(out, strings.Join(ws[i:j], ""))
			i, joined = j-1, true
			continue
		}
		out = append(out, ws[i])
	}
	return out, joined
}

// variants gives a pair of names' tokens as written, then with initials
// joined if that changes either.
func variants(a, b string) [][2][]string {
	ta, tb := Tokens(a), Tokens(b)
	out := [][2][]string{{ta, tb}}
	ja, oka := initials(ta)
	jb, okb := initials(tb)
	if oka || okb {
		out = append(out, [2][]string{ja, jb})
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
// A name that sets the park on a lake ("Paynetown on Monroe Lake State
// Recreation Area") is also scored without the lake, so it matches the unit
// itself better than the whole reservoir.
func Similarity(a, b string) float64 {
	s := 0.0
	for _, v := range variants(a, b) {
		ta, tb := v[0], v[1]
		s = max(s, similarity(ta, tb))
		if t, ok := withoutSetting(ta); ok {
			s = max(s, similarity(t, tb))
		}
		if t, ok := withoutSetting(tb); ok {
			s = max(s, similarity(ta, t))
		}
	}
	return s
}

// withoutSetting drops the "on ..." part of a name, keeping the kind of land
// at the end.
func withoutSetting(t []string) ([]string, bool) {
	end := len(t)
	for end > 0 && generic[t[end-1]] {
		end--
	}
	for i := 1; i < end-1; i++ {
		if t[i] == "on" {
			return append(append([]string{}, t[:i]...), t[end:]...), true
		}
	}
	return nil, false
}

func similarity(ta, tb []string) float64 {
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
// the same words apart from generic ones ("New River Trail"), or are the same
// name made only of generic words.
func SharesName(a, b string) bool {
	for _, v := range variants(a, b) {
		if sharesName(v[0], v[1]) {
			return true
		}
	}
	return false
}

func sharesName(ta, tb []string) bool {
	ca, cb := core(ta), core(tb)
	if len(ca) == 0 && len(cb) == 0 {
		return strings.Join(ta, " ") == strings.Join(tb, " ") // "Wilderness State Park" is all generic words
	}
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

// IsByway reports whether a name is a scenic byway's ("Logan Canyon National
// Scenic Byway", "Camden Expedition Scenic Highway"). Like a trail, a byway
// is activated along its road.
func IsByway(name string) bool {
	t := Tokens(name)
	for i, w := range t {
		if w == "byway" || w == "byways" || w == "highway" && i > 0 && t[i-1] == "scenic" {
			return true
		}
	}
	return false
}

// IsWildRiver reports whether a name is a wild and scenic river's, which is
// activated along the river like a trail.
func IsWildRiver(name string) bool {
	t := Tokens(name)
	for i := 0; i+3 < len(t); i++ {
		if t[i] == "wild" && t[i+1] == "and" && t[i+2] == "scenic" && (t[i+3] == "river" || t[i+3] == "rivers") {
			return true
		}
	}
	return false
}

// IsRoute reports whether a park is a trail, byway or wild and scenic river:
// a route activated along its length rather than an area.
func IsRoute(name string) bool { return IsTrail(name) || IsByway(name) || IsWildRiver(name) }

// RouteFits reports whether an outline could be a park by the kind of route
// each is. A route park only takes an outline of its own kind (a trail's, a
// byway's, a river corridor's), not the forest or reservation it runs
// through; any other park takes anything.
func RouteFits(park, area string) bool {
	if !IsRoute(park) {
		return true
	}
	return IsTrail(park) && IsTrail(area) || IsByway(park) && IsByway(area) || IsWildRiver(park) && isRiverCorridor(area)
}

// isRiverCorridor reports whether a name is a protected river's ("Bad Branch
// Wild River", "Saint Croix National Scenic Riverway", "Buffalo National
// River") rather than land along it ("Merced River ACEC").
func isRiverCorridor(name string) bool {
	t := Tokens(name)
	for i, w := range t {
		if w == "river" || w == "rivers" || w == "riverway" || w == "riverways" {
			for _, b := range t[:i] {
				if b == "wild" || b == "scenic" || b == "national" {
					return true
				}
			}
		}
	}
	return false
}

// EndsInTrail reports whether a name's last word is "Trail" or "Trails"
// ("Katy Trail", "Ice Age National Scenic Trail"), so the park is the trail
// itself rather than land named after one ("Scioto Trail State Forest").
func EndsInTrail(name string) bool {
	if i := strings.Index(name, "("); i > 0 {
		name = name[:i]
	}
	t := Tokens(name)
	return len(t) > 0 && (t[len(t)-1] == "trail" || t[len(t)-1] == "trails")
}

// routeWords say what kind of route a trail is, not which one.
var routeWords = map[string]bool{
	"trail": true, "trails": true, "rail": true, "path": true, "greenway": true, "loop": true,
	"spur": true, "system": true, "bicycle": true, "bike": true, "route": true, "blm": true,
	"interpretive": true,
}

// roadWords mark a street rather than a trail ("Santa Fe Drive").
var roadWords = map[string]bool{
	"drive": true, "road": true, "street": true, "avenue": true, "highway": true, "boulevard": true,
}

// Conflicts reports whether two names that share a word still name different
// places: each has a distinctive word the other lacks ("Moab Rim Trail" and
// "Hunter Rim Trail", "South Caineville Mesa" and "North Caineville Mesa"),
// or b is a road and a is not.
func Conflicts(a, b string) bool {
	words := func(s string) map[string]bool {
		m := core(Tokens(s))
		for w := range m {
			if routeWords[w] {
				delete(m, w)
			}
		}
		return m
	}
	wa, wb := words(a), words(b)
	onlyA, onlyB := false, false
	for w := range wa {
		onlyA = onlyA || !wb[w]
	}
	for w := range wb {
		onlyB = onlyB || !wa[w]
		if roadWords[w] && !wa[w] {
			return true
		}
	}
	return onlyA && onlyB
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
