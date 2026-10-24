package main

import "testing"

func TestNames(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{titled("CHINCOTEAGUE NATIONAL WILDLIFE REFUGE"), "Chincoteague National Wildlife Refuge"},
		{titled("SENECAVILLE DAM - ROAD SIDE PARK"), "Senecaville Dam - Road Side Park"},
		{titled("Browns Park"), "Browns Park"},
		{blmRec(attrs{"FET_NAME": "Browns Park", "FET_SUBTYPE": "SRMA"}), "Browns Park BLM Recreation Management Area"},
		{blmRec(attrs{"FET_NAME": "Red Cliffs Recreation Area", "FET_SUBTYPE": "Campground"}), "Red Cliffs Recreation Area BLM"},
		{field("HMA_NAME", "BLM Herd Management Area")(attrs{"HMA_NAME": "Sinbad"}), "Sinbad BLM Herd Management Area"},
		{field("NLCS_NAME", "Wilderness Study Area")(attrs{"NLCS_NAME": "Bull Canyon Wilderness Study Area"}), "Bull Canyon Wilderness Study Area"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestMDName(t *testing.T) {
	for in, want := range map[string]string{
		"SASSAFRAS NRMA":     "SASSAFRAS NATURAL RESOURCES MANAGEMENT AREA",
		"POPES CREEK WMA":    "POPES CREEK WILDLIFE MANAGEMENT AREA",
		"BOHEMIA RIVER SP":   "BOHEMIA RIVER STATE PARK",
		"SOMERS COVE MARINA": "SOMERS COVE MARINA",
	} {
		if got := mdName(attrs{"DNRName": in}); got != want {
			t.Errorf("mdName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStateNames(t *testing.T) {
	oh := spelled("Name_Label", map[string]string{"WA": "Wildlife Area", "DNP": "State Nature Preserve"})
	for _, c := range []struct{ got, want string }{
		{oh(attrs{"Name_Label": "Aquilla WA"}), "Aquilla Wildlife Area"},
		{oh(attrs{"Name_Label": "Baker Woods DNP"}), "Baker Woods State Nature Preserve"},
		{oh(attrs{"Name_Label": "Buckeye Lake Canal Fishing Access"}), "Buckeye Lake Canal Fishing Access"},
		{kyName(attrs{"AREANAME": "Redbird WMA"}), "Redbird Wildlife Management Area"},
		{ilName(attrs{"Name": "Devil's Island", "CLASSIFICA": "SFWA"}), "Devil's Island State Fish and Wildlife Area"},
		{ilName(attrs{"Name": "Pere Marquette State Park", "CLASSIFICA": "SP"}), "Pere Marquette State Park"},
		{ilName(attrs{"Name": "Burning Star No. 5", "CLASSIFICA": "NC"}), "Burning Star No. 5"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}
