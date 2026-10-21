package match

import "testing"

func TestSimilarity(t *testing.T) {
	cases := []struct {
		a, b     string
		min, max float64
	}{
		{"Acadia National Park", "Acadia National Park", 0.99, 1},
		{"Bear Creek State Park", "Bear Creek SP", 0.99, 1},
		{"Mt. Hood National Forest", "Mount Hood National Forest", 0.99, 1},
		{"Isle Royale", "Isle Royale National Park", 0.8, 1},
		{"Bear Creek State Park", "Pine Lake State Park", 0, 0.2},
		{"Okefenokee National Wildlife Refuge", "Okefenokee NWR", 0.99, 1},
	}
	for _, c := range cases {
		s := Similarity(c.a, c.b)
		if s < c.min || s > c.max {
			t.Errorf("Similarity(%q, %q) = %.2f, want %.2f..%.2f", c.a, c.b, s, c.min, c.max)
		}
	}
}

func TestSharesName(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Bear Creek State Park", "Bear Creek SP", true},
		{"Radnor Lake State Park", "Radnor Lake", true},
		{"St. Clair Flats State Wildlife Area", "St. Johns Marsh", false},
		{"William G. Milliken State Harbor", "Tricentennial State Park And Harbor", false},
		{"Capitol State Park", "Capital Park", false},
		{"Lake Monroe State Fishing Lake", "Monroe Lake", true},
		{"Cornelius Creek National Recreation Trail", "Appalachian National Scenic Trail", false},
		{"Appalachian Trail National Scenic Trail", "Appalachian National Scenic Trail", true},
	}
	for _, c := range cases {
		if got := SharesName(c.a, c.b); got != c.want {
			t.Errorf("SharesName(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
