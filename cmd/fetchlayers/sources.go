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

// spelled reads a name that ends with a short form of its kind of land
// ("Piney Creeks WMA") and spells the kind out, in capitals if the name is.
func spelled(k string, kinds map[string]string) func(attrs) string {
	return func(a attrs) string {
		n := a.s(k)
		i := strings.LastIndex(n, " ")
		if i < 0 {
			return n
		}
		w, ok := kinds[strings.ToUpper(strings.TrimSuffix(n[i+1:], "."))]
		if !ok {
			return n
		}
		if n == strings.ToUpper(n) {
			w = strings.ToUpper(w)
		}
		return n[:i] + " " + w
	}
}

const wma = "Wildlife Management Area"

// mdName spells out the kind of land at the end of a Maryland DNR name
// ("SASSAFRAS NRMA").
var mdName = spelled("DNRName", map[string]string{
	"SP": "State Park", "SF": "State Forest", "WMA": wma, "NRMA": "Natural Resources Management Area",
	"NA": "Natural Area", "NEA": "Natural Environment Area", "FMA": "Fish Management Area", "SB": "State Battlefield",
})

// kyName spells out "WMA" in Kentucky's hunting area names ("Redbird WMA").
var kyName = spelled("AREANAME", map[string]string{"WMA": wma})

// okName names an Oklahoma fishing lake the way POTA does ("Lake Vanderwork
// State Fishing Lake"); the wildlife management areas carry their full name.
func okName(a attrs) string {
	n := a.s("WMANAME")
	if a.s("WMATYPE") == "PFA" && !strings.Contains(n, "Fishing") {
		return n + " State Fishing Lake"
	}
	return n
}

// ilName adds the kind of land to an Illinois DNR site's name ("Devil's
// Island" is a state fish and wildlife area).
func ilName(a attrs) string {
	n := a.s("Name")
	kinds := map[string]string{
		"SP": "State Park", "SFWA": "State Fish and Wildlife Area", "SRA": "State Recreation Area", "SHS": "State Historic Site",
		"SF": "State Forest", "SNA": "State Natural Area", "SWA": "State Wildlife Area", "CA": "Conservation Area",
	}
	k := kinds[a.s("CLASSIFICA")]
	if n == "" || k == "" || strings.Contains(strings.ToLower(n), strings.ToLower(k)) {
		return n
	}
	return n + " " + k
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

	// The next ring of states. Arkansas: Game and Fish wildlife management
	// areas, Natural Heritage Commission natural areas and state parks from
	// the state's GIS office and highway department, whose public lands map
	// also has the lake recreation and use areas.
	{Label: "Arkansas wildlife management areas", URL: "https://gis.arkansas.gov/arcgis/rest/services/FEATURESERVICES/Boundaries/FeatureServer/37",
		Name: spelled("wma", map[string]string{"WMA": wma}), State: "AR", Manager: "AGFC", Designation: "STATE", Class: "AR WMAs"},
	{Label: "Arkansas natural areas", URL: "https://gis.arkansas.gov/arcgis/rest/services/FEATURESERVICES/Boundaries/FeatureServer/21",
		Name: field("maname", ""), State: "AR", Manager: "ANHC", Designation: "STATE", Class: "AR Natural Areas"},
	{Label: "Arkansas state parks", URL: "https://gis.ardot.gov/hosting/rest/services/Hosted/StateParks/FeatureServer/0",
		Name: field("name", ""), State: "AR", Manager: "AR Parks", Designation: "STATE", Class: "AR State Parks"},
	{Label: "Arkansas public lands", URL: "https://gis.ardot.gov/referenced/rest/services/TPP_GISMapping/CORE_BASE_LAYERS/FeatureServer/0",
		Where: "Type IN ('Wildlife Management Area', 'Wildlife Managment Area', 'State Game Management Area', 'Wildlife Demonstration Area', 'Natural Area', 'State Park', 'Recreation Area', 'Public Use Area', 'Park / Public Use Area')",
		Name:  field("Boundary_N", ""), State: "AR", Manager: "ARDOT", Designation: "STATE", Class: "AR Public Lands"},

	// Georgia DNR's managed lands (state parks, historic sites, wildlife
	// management areas, fishing areas).
	{Label: "Georgia DNR lands", URL: "https://services6.arcgis.com/9QlSLDqa0P1cHLhu/arcgis/rest/services/GADNR_Managed_Lands/FeatureServer/0",
		Where: "NAME IS NOT NULL AND NAME <> ' '", Name: spelled("NAME", map[string]string{
			"WMA": wma, "SP": "State Park", "SHS": "State Historic Site", "HS": "Historic Site", "PFA": "Public Fishing Area", "NA": "Natural Area", "FH": "Fish Hatchery",
		}), State: "GA", Manager: "GA DNR", Designation: "STATE", Class: "GA DNR Lands"},

	// South Carolina DNR's public lands (wildlife management areas and
	// heritage preserves), one outline each.
	{Label: "South Carolina DNR lands", URL: "https://services.arcgis.com/acgZYxoN5Oj8pDLa/arcgis/rest/services/DEV_-_SCDNR_Public_Lands_Viewer/FeatureServer/4",
		Name: spelled("PropertyName", map[string]string{"WMA": wma, "HP": "Heritage Preserve"}), State: "SC", Manager: "SCDNR", Designation: "STATE", Class: "SC DNR Lands"},

	// Ohio DNR's lands: parks, forests, wildlife areas and nature preserves.
	{Label: "Ohio DNR lands", URL: "https://services5.arcgis.com/ajRlmtxbNBjZggOT/arcgis/rest/services/ODNR_Lands/FeatureServer/0",
		Name: spelled("Name_Label", map[string]string{
			"SP": "State Park", "SF": "State Forest", "WA": "Wildlife Area", "DNP": "State Nature Preserve", "NP": "State Nature Preserve", "SNP": "State Nature Preserve",
		}), State: "OH", Manager: "ODNR", Designation: "STATE", Class: "OH DNR Lands"},

	// Delaware's state lands open to the public (DNREC parks, wildlife and
	// forest land and others).
	{Label: "Delaware state lands", URL: "https://enterprise.firstmap.delaware.gov/arcgis/rest/services/Society/DE_State_Lands/FeatureServer/0",
		Where: "PUBLIC_ACESS = 'Yes'", Name: field("NAME", ""), State: "DE", Manager: "DNREC", Designation: "STATE", Class: "DE State Lands"},

	// Indiana DNR's managed lands (state parks, recreation areas, forests,
	// fish and wildlife areas and nature preserves).
	{Label: "Indiana DNR lands", URL: "https://gisdata.in.gov/server/rest/services/Hosted/ManagedLands_DNR_Open/FeatureServer/0",
		Where: "manager LIKE 'DNR%'", Name: field("unitname", ""), Loc: field("subunit", ""), State: "IN", Manager: "IN DNR", Designation: "STATE", Class: "IN DNR Lands"},
	{Label: "Indiana nature preserves", URL: "https://gisdata.in.gov/server/rest/services/Hosted/NaturePreservesCoastalArea/FeatureServer/0",
		Where: "manager LIKE 'DNR%'", Name: field("unitname", ""), Loc: field("subunit", ""), State: "IN", Manager: "IN DNR", Designation: "STATE", Class: "IN Nature Preserves"},

	// Missouri: Department of Conservation areas and the state parks and
	// historic sites.
	{Label: "Missouri conservation areas", URL: "https://gisblue.mdc.mo.gov/arcgis/rest/services/Discover_Nature/MDC_Administrative_Areas/MapServer/5",
		Where: "Public_Site = 'Y'", Name: field("OFF_Name", ""), State: "MO", Manager: "MDC", Designation: "STATE", Class: "MO Conservation Areas"},
	{Label: "Missouri state parks", URL: "https://gis.dnr.mo.gov/server/rest/services/parks/Missouri_State_Parks_Boundaries/FeatureServer/4",
		Name: field("LOC_NAME", ""), State: "MO", Manager: "MO DNR", Designation: "STATE", Class: "MO State Parks"},

	// Illinois DNR's sites, named with their kind of land, and the Nature
	// Preserves Commission's preserves and land and water reserves.
	{Label: "Illinois DNR sites", URL: "https://services.arcgis.com/b9DHj1BjfdLLFv11/arcgis/rest/services/IDNR_Site_Boundaries_10_7_25/FeatureServer/0",
		Name: ilName, State: "IL", Manager: "IL DNR", Designation: "STATE", Class: "IL DNR Sites"},
	{Label: "Illinois nature preserves", URL: "https://services.arcgis.com/b9DHj1BjfdLLFv11/arcgis/rest/services/INPC_Update_Holman_10_9_20/FeatureServer/0",
		Where: "Type IN ('Nature Preserve', 'Land and Water Reserve')", Name: field("Site_Name", ""), State: "IL", Manager: "INPC", Designation: "STATE", Class: "IL Nature Preserves"},

	// Pennsylvania's state game lands, named the way POTA does ("PA 294
	// State Game Land").
	{Label: "Pennsylvania state game lands", URL: "https://services1.arcgis.com/k8yxvICm95iIFicb/arcgis/rest/services/State_Game_Lands_Boundaries/FeatureServer/300",
		Name: func(a attrs) string { return "PA " + a.s("sgl") + " State Game Land" }, State: "PA", Manager: "PGC", Designation: "STATE", Class: "PA State Game Lands"},

	// The third ring. Florida: the Natural Areas Inventory's map of state
	// conservation lands (parks, forests, wildlife management areas and
	// water management district lands) and the Suwannee River district's
	// tracts, which POTA names "... State Conservation Area".
	{Label: "Florida state conservation lands", URL: "https://services.arcgis.com/9Jk4Zl9KofTtvg3x/arcgis/rest/services/FL_Conservation_Lands_web/FeatureServer/9",
		Where: "MATYPE2 = 'State'", Name: field("MANAME", ""), State: "FL", Manager: "FNAI", Designation: "STATE", Class: "FL Conservation Lands"},
	{Label: "Suwannee River district tracts", URL: "https://services1.arcgis.com/59dBAbzYnRk8johU/arcgis/rest/services/SRWMD_LM_Ownership/FeatureServer/0",
		Where: "INTEREST LIKE 'FEE%' AND TRACT_NAME NOT LIKE '%STATE PARK%'", Name: field("TRACT_NAME", "STATE CONSERVATION AREA"),
		State: "FL", Manager: "SRWMD", Designation: "STATE", Class: "FL SRWMD Tracts"},

	// Oklahoma: Wildlife Department management areas and fishing lakes.
	{Label: "Oklahoma wildlife management areas", URL: "https://services1.arcgis.com/jRf8jjFwxedITdFe/arcgis/rest/services/Public_WMA_Boundaries/FeatureServer/1",
		Name: okName, State: "OK", Manager: "ODWC", Designation: "STATE", Class: "OK WMAs"},

	// Michigan DNR's project boundaries for parks, game and wildlife areas
	// and hatcheries (the forest units are whole districts, so they're left
	// out).
	{Label: "Michigan DNR areas", URL: "https://services3.arcgis.com/Jdnp1TjADvSDxMAX/arcgis/rest/services/DNRLOTSParcelsOPENDATA/FeatureServer/13",
		Where: "Division IN ('Wildlife', 'Parks', 'Fish')", Name: field("UnitName", ""), State: "MI", Manager: "MI DNR", Designation: "STATE", Class: "MI DNR Areas"},

	// Wisconsin DNR's managed lands, one outline per property.
	{Label: "Wisconsin DNR lands", URL: "https://dnrmaps.wi.gov/arcgis/rest/services/LF_DML/LF_AGOL_STAGING_WTM_Ext/MapServer/0",
		Name: field("PROP_NAME", ""), State: "WI", Manager: "WI DNR", Designation: "STATE", Class: "WI DNR Lands"},

	// Kansas: wildlife areas and fishing lakes, and the state parks and
	// wildlife areas on KDWP's public land map.
	{Label: "Kansas wildlife areas", URL: "https://services1.arcgis.com/q2CglofYX6ACNEeu/arcgis/rest/services/KDWPT_Wildlife_Areas/FeatureServer/0",
		Name: field("NAME", ""), State: "KS", Manager: "KDWP", Designation: "STATE", Class: "KS Wildlife Areas"},
	{Label: "Kansas public lands", URL: "https://services1.arcgis.com/q2CglofYX6ACNEeu/arcgis/rest/services/KDWP_Spatial_Directory_WFL1/FeatureServer/4",
		Where: "MGT_UNIT LIKE 'State%' OR MGT_UNIT LIKE 'SFL%'", Name: field("NAME", ""), State: "KS", Manager: "KDWP", Designation: "STATE", Class: "KS Public Lands"},

	// Louisiana Wildlife and Fisheries management areas and refuges.
	{Label: "Louisiana WMAs and refuges", URL: "https://services1.arcgis.com/6euNCaGPCgCzgAVF/arcgis/rest/services/LDWF_WMA_Refuge/FeatureServer/0",
		Name: spelled("NAME", map[string]string{"WMA": wma}), State: "LA", Manager: "LDWF", Designation: "STATE", Class: "LA WMAs"},

	// New York DEC lands (state forests, forest preserve, wildlife
	// management and unique areas), without offices and access sites.
	{Label: "New York DEC lands", URL: "https://services6.arcgis.com/DZHaqZm9cxOD4CWM/arcgis/rest/services/NYS_DEC_Lands/FeatureServer/0",
		Where: "CATEGORY NOT IN ('ADMINISTRATIVE', 'FISHING ACCESS', 'WATERWAY ACCESS', 'LEASED ACCESS')",
		Name:  spelled("FACILITY", map[string]string{"WMA": wma}), State: "NY", Manager: "NYSDEC", Designation: "STATE", Class: "NY DEC Lands"},

	// Texas Parks and Wildlife management areas, and its parks, natural
	// areas and historic sites as the highway department publishes them.
	{Label: "Texas wildlife management areas", URL: "https://services1.arcgis.com/1mtXwieMId59thmg/arcgis/rest/services/WMA_Boundaries_4PublicDistribution/FeatureServer/0",
		Name: field("LoName", ""), State: "TX", Manager: "TPWD", Designation: "STATE", Class: "TX WMAs"},
	{Label: "Texas state parks", URL: "https://services.arcgis.com/KTcxiTD9dsQw4r7Z/arcgis/rest/services/TPWD_WMAs_and_State_Parks_Boundaries/FeatureServer/1",
		Name:  spelled("LoName", map[string]string{"SP": "State Park", "SNA": "State Natural Area", "SHS": "State Historic Site"}),
		State: "TX", Manager: "TPWD", Designation: "STATE", Class: "TX State Parks"},

	// Nebraska Game and Parks properties: parks, recreation areas,
	// historical parks and wildlife management areas.
	{Label: "Nebraska Game and Parks properties", URL: "https://services5.arcgis.com/IOshH1zLrIieqrNk/arcgis/rest/services/NPGCProperties2023_2024/FeatureServer/268",
		Where: "Status = 'Active'", Name: spelled("AreaName", map[string]string{
			"WMA": wma, "SRA": "State Recreation Area", "SP": "State Park", "SHP": "State Historical Park", "SHS": "State Historic Site",
		}), State: "NE", Manager: "NGPC", Designation: "STATE", Class: "NE NGPC Properties"},

	// New Jersey's state-owned open space (parks, forests, wildlife
	// management areas, Natural Lands Trust preserves).
	{Label: "New Jersey state open space", URL: "https://services1.arcgis.com/QWdNfRs7lkPq4g4Q/arcgis/rest/services/Open_Space__State_Owned__Generalized_h/FeatureServer/67",
		Name: field("FEATURE_NAME", ""), State: "NJ", Manager: "NJDEP", Designation: "STATE", Class: "NJ State Open Space"},

	// The fourth ring. Colorado Parks and Wildlife properties open to the
	// public (state parks, wildlife areas, fish units, watchable wildlife
	// areas).
	{Label: "Colorado Parks and Wildlife properties", URL: "https://services5.arcgis.com/ttNGmDvKQA7oeDQ3/arcgis/rest/services/CPWAdminData/FeatureServer/5",
		Where: "PropType IN ('SWA', 'SP', 'SFU', 'WWA', 'SAA', 'Recreation Area')", Name: spelled("PropName", map[string]string{
			"SWA": "State Wildlife Area", "SP": "State Park", "SFU": "State Fish Unit", "WWA": "Watchable Wildlife Area", "SAA": "State Access Area",
		}), State: "CO", Manager: "CPW", Designation: "STATE", Class: "CO CPW Properties"},

	// Wyoming Game and Fish habitat management areas and public access areas.
	{Label: "Wyoming habitat management areas", URL: "https://services6.arcgis.com/cWzdqIyxbijuhPLw/arcgis/rest/services/WildlifeHabitatManagementAreaBoundaries/FeatureServer/0",
		Name: field("WHMA_Name", "Wildlife Habitat Management Area"), State: "WY", Manager: "WGFD", Designation: "STATE", Class: "WY WHMAs"},
	{Label: "Wyoming public access areas", URL: "https://services6.arcgis.com/cWzdqIyxbijuhPLw/arcgis/rest/services/PublicAccessAreaBoundaries/FeatureServer/0",
		Name: field("PAA_Name", "Public Access Area"), State: "WY", Manager: "WGFD", Designation: "STATE", Class: "WY Public Access Areas"},

	// Minnesota DNR wildlife management areas open to the public.
	{Label: "Minnesota wildlife management areas", URL: "https://enterprise.gisdata.mn.gov/aghost/rest/services/us_mn_state_dnr/bdry_dnr_wildlife_mgmt_areas_pub/FeatureServer/0",
		Name: field("name", ""), State: "MN", Manager: "MN DNR", Designation: "STATE", Class: "MN WMAs"},

	// Massachusetts: state-owned land on MassGIS's open space map (DCR
	// parks and forests, MassWildlife WMAs).
	{Label: "Massachusetts state open space", URL: "https://arcgisserver.digital.mass.gov/arcgisserver/rest/services/AGOL/openspace/FeatureServer/0",
		Where: "OWNER_TYPE = 'S'", Name: field("SITE_NAME", ""), State: "MA", Manager: "MA EEA", Designation: "STATE", Class: "MA State Open Space"},

	// Vermont Agency of Natural Resources land units (state parks, forests,
	// wildlife management areas).
	{Label: "Vermont ANR lands", URL: "https://anrmaps.vermont.gov/arcgis/rest/services/Open_Data/OPENDATA_ANR_CADASTRAL_SP_NOCACHE_v2/MapServer/38",
		Name: field("NAME", ""), State: "VT", Manager: "VT ANR", Designation: "STATE", Class: "VT ANR Lands"},

	// Connecticut DEEP property (parks, forests, wildlife areas).
	{Label: "Connecticut DEEP property", URL: "https://services1.arcgis.com/FjPcSmEFuDYlIdKC/arcgis/rest/services/Connecticut_DEEP_Property/FeatureServer/0",
		Name: field("PROPERTY", ""), State: "CT", Manager: "CT DEEP", Designation: "STATE", Class: "CT DEEP Property"},

	// The fifth ring. North Dakota state parks and recreation areas, and
	// Game and Fish wildlife management areas (named without "WMA").
	{Label: "North Dakota state parks", URL: "https://services1.arcgis.com/GOcSXpzwBHyk2nog/ArcGIS/rest/services/NDGISHUB_State_Parks/FeatureServer/0",
		Name: field("UNIT_NAME", ""), State: "ND", Manager: "ND Parks", Designation: "STATE", Class: "ND State Parks"},
	{Label: "North Dakota wildlife management areas", URL: "https://services1.arcgis.com/GOcSXpzwBHyk2nog/ArcGIS/rest/services/NDGISHUB_Wildlife_Management_Areas/FeatureServer/0",
		Name: field("Unit_Name", wma), State: "ND", Manager: "NDGF", Designation: "STATE", Class: "ND WMAs"},

	// Montana Fish, Wildlife and Parks: fishing access sites, state parks and
	// wildlife management areas, all named without their kind.
	{Label: "Montana fishing access sites", URL: "https://fwp-gis.mt.gov/arcgis/rest/services/fwplnd/fwpLands/MapServer/2",
		Name: field("NAME", "Fishing Access Site"), State: "MT", Manager: "MT FWP", Designation: "STATE", Class: "MT Fishing Access Sites"},
	{Label: "Montana state parks", URL: "https://fwp-gis.mt.gov/arcgis/rest/services/fwplnd/fwpLands/MapServer/5",
		Name: field("NAME", "State Park"), State: "MT", Manager: "MT FWP", Designation: "STATE", Class: "MT State Parks"},
	{Label: "Montana wildlife management areas", URL: "https://fwp-gis.mt.gov/arcgis/rest/services/fwplnd/fwpLands/MapServer/8",
		Name: field("NAME", wma), State: "MT", Manager: "MT FWP", Designation: "STATE", Class: "MT WMAs"},

	// Idaho Fish and Game wildlife management areas ("RED RIVER WMA").
	{Label: "Idaho wildlife management areas", URL: "https://services.arcgis.com/FjJI5xHF2dUPVrgK/arcgis/rest/services/WildlifeManagementAreas/FeatureServer/0",
		Name: spelled("Name", map[string]string{"WMA": wma, "WMU": "Wildlife Management Unit"}), State: "ID", Manager: "IDFG", Designation: "STATE", Class: "ID WMAs"},

	// Utah: Wildlife Resources and State Parks land on the trust lands
	// office's ownership map.
	{Label: "Utah wildlife and state park lands", URL: "https://gis.trustlands.utah.gov/mapping/rest/services/Land_Ownership/FeatureServer/0",
		Where: "admin IN ('UDWR', 'USP')", Name: field("label_state", ""), State: "UT", Manager: "UT DNR", Designation: "STATE", Class: "UT DWR and State Parks"},

	// New Hampshire: land the state owns outright on GRANIT's conservation
	// lands map (state parks, forests, WMAs, natural areas).
	{Label: "New Hampshire state lands", URL: "https://nhgeodata.unh.edu/hosting/rest/services/Hosted/EC_Conservation/FeatureServer/0",
		Where: "ppagentype = 3 AND pptype = 'FO'", Name: spelled("name", map[string]string{"WMA": wma}), State: "NH", Manager: "NH DNCR", Designation: "STATE", Class: "NH State Lands"},

	// Maine state conservation lands (Bureau of Parks and Lands, Inland
	// Fisheries and Wildlife), without the lakes themselves.
	{Label: "Maine state conservation lands", URL: "https://gis.maine.gov/mapservices/rest/services/dep/Maine_DEP_Outstanding_National_Resource_Waters/MapServer/5",
		Where: "HOLD1_TYPE = 'State' AND DESIGNATION <> 'WATERBODY'", Name: spelled("PROJECT", map[string]string{"WMA": wma}), State: "ME", Manager: "ME DACF", Designation: "STATE", Class: "ME State Conservation Lands"},

	// Rhode Island DEM land; single parcels are named after their former
	// owners ("Barber, Arthur") and are left out.
	{Label: "Rhode Island DEM lands", URL: "https://risegis.ri.gov/hosting/rest/services/RIDEM/Outdoor_Recreation_Map_v4/FeatureServer/21",
		Where: "NAME NOT LIKE '%,%'", Name: field("NAME", ""), State: "RI", Manager: "RI DEM", Designation: "STATE", Class: "RI DEM Lands"},

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
