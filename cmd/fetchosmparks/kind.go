package main

import "github.com/jsttylr/park-map/internal/match"

// kinds name the sort of place a name's words describe. OpenStreetMap has a
// park, a historic district and a forest for nearly every name, so an area
// must be the same sort of place as the park ("Willamette State Fish
// Hatchery" is not "Willamette National Forest"). Checked in order: the
// first phrase found decides.
var kinds = []struct {
	kind  string
	words []string
}{
	{"hatchery", []string{"hatchery"}},
	{"historic", []string{"historic", "historical", "history", "museum", "tomb", "monument"}},
	{"river", []string{"wild and scenic river"}},
	{"wilderness", []string{"wilderness"}},
	{"refuge", []string{"wildlife refuge"}},
	{"airstrip", []string{"airstrip", "airport", "airfield"}},
	{"park", []string{"recreation management area"}}, // BLM's, not a wildlife area
	{"wildlife", []string{"wildlife management area", "wildlife area", "game land", "game lands", "fishing lake", "fish and wildlife area", "management area", "wildlife"}},
	{"forest", []string{"forest"}},
	{"trail", []string{"trail", "trails"}},
	{"park", []string{"park", "recreation", "reserve", "reservation", "preserve", "sanctuary", "beach", "natural area", "nature", "conservation area", "access"}},
}

// kind returns the sort of place a name is, or "" when its words don't say.
func kind(name string) string {
	t := match.Tokens(name)
	for _, k := range kinds {
		for _, w := range k.words {
			if hasPhrase(t, match.Tokens(w)) {
				return k.kind
			}
		}
	}
	return ""
}

func hasPhrase(t, p []string) bool {
	for i := 0; i+len(p) <= len(t); i++ {
		ok := true
		for j, w := range p {
			ok = ok && t[i+j] == w
		}
		if ok {
			return true
		}
	}
	return false
}

// sameKind reports whether an area could be the park by the sort of place
// each name describes. A name that says nothing either way fits anything.
func sameKind(park, area string) bool {
	a, b := kind(park), kind(area)
	return a == "" || b == "" || a == b
}
