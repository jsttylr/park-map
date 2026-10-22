package match

import (
	"strings"
	"testing"
)

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
		{"New River Trail State Park", "New River Trail State Park", true},
		{"Devil’s River State Trail", "Devils River State Trail", true},
		{"Wilderness Road State Park", "Wilderness Road State Park", true},
		{"Lake Park", "Lake Monroe", false},
		{"Chesapeake and Delaware Canal Conservation Area", "C&D Canal Conservation Area", true},
		{"Apple Orchard Falls National Recreation Trail", "Appalachian National Scenic Trail", false},
	}
	for _, c := range cases {
		if got := SharesName(c.a, c.b); got != c.want {
			t.Errorf("SharesName(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestParts(t *testing.T) {
	for _, c := range []struct {
		name string
		want string
	}{
		{"George Washington & Jefferson National Forest", "george washington national forest|jefferson national forest"},
		{"George Washington and Jefferson National Forests", "george washington national forest|jefferson national forest"},
		{"Jefferson National Forest Wildlife Management Area", ""},
		{"Booker T. Washington National Monument", ""},
		{"Big and Little Lake State Park", ""},
	} {
		if got := strings.Join(Parts(c.name), "|"); got != c.want {
			t.Errorf("%q: got %q", c.name, got)
		}
	}
	if SingularKey("Jefferson National Forests") != "jefferson national forest" {
		t.Error("SingularKey")
	}
}

func TestCoreWithin(t *testing.T) {
	if !CoreWithin("White River National Wildlife Refuge", "Dale Bumpers White River National Wildlife Refuge") {
		t.Error("White River")
	}
	if CoreWithin("Willamette State Fish Hatchery", "Willamette National Forest") {
		t.Error("hatchery")
	}
}
