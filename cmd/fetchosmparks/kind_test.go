package main

import "testing"

func TestSameKind(t *testing.T) {
	for _, c := range []struct {
		park, area string
		want       bool
	}{
		{"Willamette State Fish Hatchery", "Willamette National Forest", false},
		{"Aniakchak National Wild and Scenic River", "Aniakchak National Preserve", false},
		{"Lincoln Tomb State Historic Site", "Lincoln Park", false},
		{"Meade State Fishing Lake", "Meade State Park", false},
		{"Kuliʻouʻou Forest Reserve", "Kuliʻouʻou Park", false},
		{"McCurtain County Wildlife Management Area", "McCurtain County Wilderness Area", false},
		{"Hillsdale Preserve Management Area", "Hillsdale Historic and Archeological District", false},
		{"Milliken State Park", "William G. Milliken State Park", true},
		{"Breakheart State Reserve", "Breakheart Reservation", true},
		{"Fort Clark State Historic Site", "Fort Clark Historic Site", true},
		{"Olympic State Wildlife Area", "Olympic Wildlife Area", true},
		{"Mt Hope on Salamonie Lake State Recreation Area", "Mount Hope State Recreation Area", true},
		{"Elkhorn Crossing State Park", "Elkhorn Crossing", true},
	} {
		if got := sameKind(c.park, c.area); got != c.want {
			t.Errorf("sameKind(%q, %q) = %v (%q, %q), want %v", c.park, c.area, got, kind(c.park), kind(c.area), c.want)
		}
	}
}
