package main

import "strings"

const (
	blm    = "https://gis.blm.gov/arcgis/rest/services/"
	nps    = "https://services1.arcgis.com/fBc8EJBxQRMcHlei/arcgis/rest/services/"
	fws    = "https://services.arcgis.com/QVENGdaPbd4LUkLV/arcgis/rest/services/"
	usace  = "https://services7.arcgis.com/n1YM8pTrFmm7L4hs/arcgis/rest/services/"
	usbr   = "https://services1.arcgis.com/ixD30sld6F8MQ7V5/arcgis/rest/services/"
	blmHub = "https://services1.arcgis.com/KbxwQRRfWyEYLgp4/arcgis/rest/services/"
)

// named gives every feature of a layer the same name, for layers that are one
// trail and carry no name of their own.
func named(n string) func(attrs) string { return func(attrs) string { return n } }

// blmRec names a BLM recreation polygon the way POTA does: "Browns Park BLM
// Recreation Management Area" for a special or extensive recreation
// management area, "Red Cliffs Recreation Area BLM" otherwise.
func blmRec(a attrs) string {
	n := a.s("FET_NAME")
	if n == "" {
		return ""
	}
	switch strings.ToUpper(a.s("FET_SUBTYPE")) {
	case "SRMA", "ERMA", "RMZ":
		return n + " BLM Recreation Management Area"
	}
	return n + " BLM"
}

// mdName spells out the kind of land at the end of a Maryland DNR name
// ("SASSAFRAS NRMA").
func mdName(a attrs) string {
	n := a.s("DNRName")
	i := strings.LastIndex(n, " ")
	if i < 0 {
		return n
	}
	kinds := map[string]string{
		"SP": "State Park", "SF": "State Forest", "WMA": "Wildlife Management Area", "NRMA": "Natural Resources Management Area",
		"NA": "Natural Area", "NEA": "Natural Environment Area", "FMA": "Fish Management Area", "SB": "State Battlefield",
	}
	if k, ok := kinds[strings.ToUpper(n[i+1:])]; ok {
		return n[:i] + " " + strings.ToUpper(k)
	}
	return n
}

// kyName spells out "WMA" in Kentucky's hunting area names ("Redbird WMA").
func kyName(a attrs) string {
	if n, ok := strings.CutSuffix(a.s("AREANAME"), " WMA"); ok {
		return n + " Wildlife Management Area"
	}
	return a.s("AREANAME")
}

// sources are the agency layers, all public domain US government data. Each
// is listed with its service so it can be checked in a browser.
var sources = []source{
	// Bureau of Land Management: about a quarter of the parks PAD-US
	// doesn't draw are BLM recreation and herd management areas.
	{Label: "BLM recreation areas", URL: blm + "recreation/BLM_Natl_Recs_poly/MapServer/0",
		Name: blmRec, State: "ADMIN_ST", Manager: "BLM", Designation: "REC", Class: "BLM Recreation"},
	{Label: "BLM herd management areas", URL: blm + "range/BLM_Natl_WHB/MapServer/0",
		Name: field("HMA_NAME", "BLM Herd Management Area"), State: "ADMIN_ST", Manager: "BLM", Designation: "HMA", Class: "BLM Herd"},
	{Label: "BLM areas of critical environmental concern", URL: blm + "lands/BLM_Natl_ACEC/MapServer/0",
		Name: field("ACEC_NAME", "BLM"), State: "ADMIN_ST", Manager: "BLM", Designation: "ACEC", Class: "BLM ACEC"},
	{Label: "BLM national monuments and conservation areas", URL: blm + "lands/BLM_Natl_NLCS_Generalized/FeatureServer/0",
		Name: field("NCA_NAME", ""), State: "STATE_ADMN", Manager: "BLM", Designation: "NCA", Class: "BLM NLCS"},
	{Label: "BLM wilderness", URL: blm + "lands/BLM_Natl_NLCS_Generalized/FeatureServer/1",
		Name: field("NLCS_NAME", ""), State: "ADMIN_ST", Manager: "BLM", Designation: "WA", Class: "BLM NLCS"},
	{Label: "BLM wilderness study areas", URL: blm + "lands/BLM_Natl_NLCS_Generalized/FeatureServer/2",
		Name: field("NLCS_NAME", "Wilderness Study Area"), State: "ADMIN_ST", Manager: "BLM", Designation: "WSA", Class: "BLM NLCS"},

	// Fish and Wildlife Service: the land each refuge and hatchery owns or
	// holds an interest in, then the wider approved boundaries.
	{Label: "FWS refuge and hatchery land", URL: fws + "FWSInterest_Simplified_Authoritative/FeatureServer/0",
		Name: field("ORGNAME", ""), Loc: field("UNITNAME", ""), Manager: "FWS", Designation: "NWR", Class: "FWS Interest"},
	{Label: "FWS approved boundaries", URL: fws + "FWSApproved_Authoritative/FeatureServer/0",
		Name: field("ORGNAME", ""), Manager: "FWS", Designation: "NWR", Class: "FWS Approved"},

	// Army Corps of Engineers recreation areas, each by its own name (state
	// parks on Corps lakes are often one of them; PAD-US has the lakes
	// themselves), and Reclamation recreation management areas.
	{Label: "Army Corps recreation areas", URL: usace + "usace_recreation_areas/FeatureServer/0",
		Name: field("RECPROJECTSITEAREANAME", ""), Manager: "USACE", Designation: "REC", Class: "USACE Recreation"},
	{Label: "Reclamation recreation areas", URL: usbr + "ReclamationRecreationManagementAreas/FeatureServer/0",
		Name: field("assetname", ""), Manager: "USBR", Designation: "REC", Class: "USBR Recreation"},

	// National Park Service unit boundaries (newer than PAD-US for some
	// units) and national heritage areas.
	{Label: "NPS unit boundaries", URL: nps + "NPS_Land_Resources_Division_Boundary_and_Tract_Data_Service/FeatureServer/2",
		Name: field("UNIT_NAME", ""), State: "STATE", Manager: "NPS", Designation: "NPS", Class: "NPS Boundary"},
	{Label: "NPS national heritage areas", URL: nps + "National_Heritage_Areas_Updated2023/FeatureServer/0",
		Name: field("RESNAME", ""), Manager: "NPS", Designation: "NHA", Class: "NPS Heritage Area"},

	// State maps, from Virginia outward. Virginia's conservation lands
	// database (DCR Natural Heritage) gathers every agency's land; only the
	// state parks, natural area preserves and forests (DCR and the Department
	// of Forestry, free to share for non-profit use) and federal land are
	// taken, since other contributors' terms limit sharing.
	{Label: "Virginia conservation lands", URL: "https://www.dcr.virginia.gov/natural-heritage/document/conslands.zip", Zip: true,
		Where: "MAAGENCY IN ('VA Dept of Conservation and Recreation', 'VA Dept of Forestry') OR MAAGENCY LIKE 'US %' OR MAAGENCY LIKE 'National %'",
		Name:  field("LABEL", ""), State: "VA", Manager: "VA DCR", Designation: "STATE", Class: "VA Conservation Lands"},

	// Maryland DNR's own land (parks, forests, wildlife and natural resources
	// management areas) and its State Wildlands, which POTA lists as natural
	// areas.
	{Label: "Maryland DNR lands", URL: "https://mdgeodata.md.gov/imap/rest/services/Environment/MD_ProtectedLands/FeatureServer/0",
		Where: "DNRType = 'DNR Owned Property'", Name: mdName, State: "MD", Manager: "MD DNR", Designation: "STATE", Class: "MD DNR Lands"},
	{Label: "Maryland State Wildlands", URL: "https://dnr.geodata.md.gov/dnrdata/rest/services/AIMS/Wildlands_Preservation_System/MapServer/0",
		Name: field("WNAME", "Wildland"), State: "MD", Manager: "MD DNR", Designation: "STATE", Class: "MD Wildlands"},

	// Tennessee's state natural areas and state park lands (TDEC).
	{Label: "Tennessee state natural areas", URL: "https://tdeconline.tn.gov/arcgis/rest/services/TN_Natural_Areas/FeatureServer/0",
		Name: field("SNA_NAME", ""), Loc: field("SITE_NAME", ""), State: "TN", Manager: "TDEC", Designation: "STATE", Class: "TN Natural Areas"},
	{Label: "Tennessee state park lands", URL: "https://tdeconline.tn.gov/arcgis/rest/services/TDEC_Public_Lands/FeatureServer/0",
		Name: field("NAME_1", ""), Loc: field("NAME_2", ""), State: "TN", Manager: "TDEC", Designation: "STATE", Class: "TN State Park Lands"},

	// North Carolina's game lands (Wildlife Resources Commission).
	{Label: "North Carolina game lands", URL: "https://services1.arcgis.com/YfqBAUM5nWR3yhGP/ArcGIS/rest/services/gamelands_general/FeatureServer/21",
		Name: field("GML_HAB", "Game Land"), State: "NC", Manager: "NCWRC", Designation: "STATE", Class: "NC Game Lands"},

	// Kentucky's public hunting areas (wildlife management areas and others,
	// KDFWR), state nature preserves and state forests.
	{Label: "Kentucky public hunting areas", URL: "https://kygisserver.ky.gov/arcgis/rest/services/WGS84WM_Services/Ky_Public_Hunting_Areas_WGS84WM/MapServer/1",
		Name: kyName, State: "KY", Manager: "KDFWR", Designation: "STATE", Class: "KY Public Hunting Areas"},
	{Label: "Kentucky state nature preserves", URL: "https://kygisserver.ky.gov/arcgis/rest/services/WGS84WM_Services/Ky_KNP_State_Nature_Preserves_WGS84WM/MapServer/0",
		Name: field("MANAME", ""), State: "KY", Manager: "KY OKNP", Designation: "STATE", Class: "KY Nature Preserves"},
	{Label: "Kentucky state forests", URL: "https://kygisserver.ky.gov/arcgis/rest/services/WGS84WM_Services/Ky_StateForests_WGS84WM/MapServer/1",
		Name: field("NAME", "State Forest"), State: "KY", Manager: "KY DOF", Designation: "STATE", Class: "KY State Forests"},

	// West Virginia's public lands compilation (2020), state land only.
	{Label: "West Virginia state lands", URL: "https://services1.arcgis.com/cTNi34MxOdcfum3A/ArcGIS/rest/services/WV_Master_PublicLands_042120/FeatureServer/0",
		Where: "Mang_Type = 'STAT'", Name: field("Unit_Nm", ""), State: "WV", Manager: "WV DNR", Designation: "STATE", Class: "WV Public Lands"},

	// Official routes of the national historic and scenic trails, mostly old
	// roads and water routes that OpenStreetMap doesn't map as one trail.
	{Label: "NPS Captain John Smith Chesapeake NHT", URL: nps + "CAJO_Captain_John_Smith_Chesapeake_NHT_Centerline_ln/FeatureServer/0",
		Name: named("Captain John Smith Chesapeake National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS Star-Spangled Banner NHT", URL: nps + "Sta_Spangled_Banner_National_Historic_Trail_Official_Centerline/FeatureServer/0",
		Name: named("Star-Spangled Banner National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS Selma to Montgomery NHT", URL: nps + "Selma_to_Montgomery_National_Historic_Trail_Official/FeatureServer/0",
		Name: named("Selma to Montgomery National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS Oregon NHT", URL: nps + "OREG_NHT/FeatureServer/0",
		Name: named("Oregon National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS California NHT", URL: nps + "CALI_NHT/FeatureServer/0",
		Name: named("California National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS Mormon Pioneer NHT", URL: nps + "MOPI_NHT/FeatureServer/0",
		Name: named("Mormon Pioneer National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS Pony Express NHT", URL: nps + "POEX_NHT/FeatureServer/0",
		Name: named("Pony Express National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS Santa Fe NHT", URL: nps + "SAFE_NHT/FeatureServer/0",
		Name: named("Santa Fe National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS Trail of Tears NHT", URL: nps + "TRTE_NHT/FeatureServer/0",
		Name: named("Trail of Tears National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS Old Spanish NHT", URL: nps + "OLSP_NHT/FeatureServer/0",
		Name: named("Old Spanish National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS El Camino Real de Tierra Adentro NHT", URL: nps + "ELCA_NHT/FeatureServer/0",
		Name: named("El Camino Real de Tierra Adentro National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS El Camino Real de los Tejas NHT", URL: nps + "ELTE_NHT/FeatureServer/0",
		Name: named("El Camino Real de los Tejas National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS Juan Bautista de Anza NHT", URL: nps + "JUBA_NHT_/FeatureServer/0",
		Name: named("Juan Bautista de Anza National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS Overmountain Victory NHT", URL: nps + "Overmountain_Victory_National_Historic_Trail_Official_Temp/FeatureServer/0",
		Name: named("Overmountain Victory National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "BLM Iditarod NHT", URL: nps + "Iditarod_National_Historic_Trail_BLM_Official_Temp/FeatureServer/0",
		Name: named("Iditarod National Historic Trail"), Manager: "BLM", Lines: true},
	{Label: "NPS Lewis and Clark NHT", URL: nps + "Lewis_and_Clark_National_Historic_Trail_Congressionally_Designated_Route/FeatureServer/0",
		Name: named("Lewis and Clark National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS Washington-Rochambeau NHT", URL: nps + "20200622_2016_URI_WARO_NST_trail_centerline_states_combined_web_mercator/FeatureServer/0",
		Name: named("Washington-Rochambeau Revolutionary Route National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS Butterfield Overland NHT", URL: nps + "NTIR_OTHER_ButterfieldOverlandTrailSRS_ln/FeatureServer/0",
		Name: named("Butterfield Overland National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "NPS Civil War Defenses of Washington trail", URL: nps + "CWDW_Circuit_Trails_2023_NPS/FeatureServer/19",
		Name: named("Civil War Defenses of Washington National Historic Trail"), Manager: "NPS", Lines: true},
	{Label: "BLM Nez Perce NHT", URL: blmHub + "Nez_Perce_National_Historic_Trail_2/FeatureServer/0",
		Name: named("Nez Perce National Historic Trail"), Manager: "BLM", Lines: true},
}
