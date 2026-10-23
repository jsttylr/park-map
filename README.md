# Park Map

> This software was **developed with AI** (Claude, by Anthropic). **Use it at your own risk**, and read the [disclaimer](#disclaimer) before relying on it in the field. Licensed under GPL-3.0.

Inspired by [FieldQTH](https://fieldqth.app), the portable-operations planning map by **LX1MDA**. FieldQTH showed how useful it is to see real park boundaries and the spots where they overlap. This project is a separate, from-scratch build for US parks and SOTA summits that runs on your own server. It uses no FieldQTH code or data.

A map of US [Parks on the Air](https://pota.app) parks with their real boundaries, plus [Summits on the Air](https://www.sota.org.uk) summits and their activation zones, served as a web page on your network.

- **Park outlines.** About 10,000 of the 13,000 active US parks are drawn with their actual boundary. The rest show as a dot at the POTA location.
- **N-fer areas.** Where park boundaries overlap, just the overlapping area is colored by how many parks meet there: blue for a 2-fer, red for a 3-fer, orange for 4 or more. Parks themselves are light green. Slide the filter to 2, 3 or more to show only those areas.
- **SOTA summits.** Summits and their activation zones are yellow with a dark brown dashed edge. Turn them on or off, and choose whether a summit counts toward the n-fer.
- **What's here.** Click anywhere to list every park and summit at that exact spot, plus nearby parks that have no outline on file. The box at the bottom shows the short text reply a satellite messenger would get (see below).
- **Search** by reference (`US-1234`, `W7W/KG-001`), by name, or by pasting coordinates in almost any format (`35.5628, -83.4985`, `N35 33.768 W083 29.910`, `35°33'46.1"N 83°29'54.6"W`).

![US overview, colored by n-fer](https://github.com/jsttylr/park-map/releases/download/screenshots/overview.png)

![Mount Rogers area filtered to 3-fers and up](https://github.com/jsttylr/park-map/releases/download/screenshots/nfer.png)

![Clicking a spot lists every park and summit there](https://github.com/jsttylr/park-map/releases/download/screenshots/lookup.png)

![SOTA activation zones](https://github.com/jsttylr/park-map/releases/download/screenshots/summits.png)

## Run it with Docker

```
docker run -d --name park-map --restart unless-stopped -p 8075:8075 -v /path/to/park-map-config:/config -e PARK_MAP_ADMIN_PASSWORD=change-me ghcr.io/jsttylr/park-map:latest
```

Then open `http://<server address>:8075/`. It takes about half a minute after starting to load the data and work out the overlaps before the page answers. It needs about 1 GB of memory. There's also a `docker-compose.yml`.

On **Unraid**, add the template from `https://raw.githubusercontent.com/jsttylr/park-map/main/unraid/park-map.xml`.

The map data is built into the image, so nothing needs to be downloaded at startup. The image is rebuilt once a month with the newest park list, summits and activation zones. `latest` follows the `main` branch, and every other branch gets an image tagged with its name.

## Settings

| Setting | Environment variable | Default |
|---|---|---|
| Data folder | `PARK_MAP_DATA` | `data` (`/data` in Docker) |
| Address and port | `PARK_MAP_LISTEN` | `0.0.0.0:8075` |
| Settings and gateway log folder | `PARK_MAP_CONFIG` | `config` (`/config` in Docker) |
| Settings page password | `PARK_MAP_ADMIN_PASSWORD` | none (anyone on your network can open it) |

## Satellite messenger gateway (Garmin inReach)

The container can answer messages from an inReach. It checks a mailbox you set up for it, works out where the message was sent from, and replies with the parks and summits there. It can also post POTA and SOTA spots for you. The idea and the command style come from [SOTAmāt](https://sotamat.com) by AB6D.

Set it up on the settings page (the link at the bottom of the map panel, or `http://<server>:8075/settings`):

1. Make a mailbox just for this (a spare Gmail with 2-step verification and an app password works) and fill in the IMAP settings. **Test the login** checks them.
2. Put your callsign in, add `no.reply.inreach@garmin.com` to **Allowed senders**, and set a **PIN**. Every inReach sends from that one Garmin address, so the PIN is what tells your messages from anyone else's, and messages from Garmin's address are refused without one. Put the PIN in the address you send to (with Gmail, save the inReach contact as `yourbox+7373@gmail.com`) or as a word in the message. Messages from anyone else are ignored and logged.

   If your mail host doesn't deliver `+` addresses (cPanel hosts often don't), make a forwarder instead. In cPanel, open **Email > Forwarders > Add Forwarder**, enter an address with the PIN in it (for example `spots7373@yourdomain.com`) and forward it to the gateway's mailbox. Save that forwarder address as the inReach contact. The gateway sees the PIN in the **To** line of the forwarded message just like a `+` address. A forwarder made in your mail program (a rule that resends the message) usually rewrites the To line, so for that kind put the PIN as a word in the message instead.
3. Leave **Test mode** on at first. Replies and spots are written to the log instead of being sent. **Try a message** on the same page runs a command without sending anything.
4. Turn on POTA and SOTA spotting if you want them. SOTA needs your SOTAwatch login.

Commands (send them from the inReach to the mailbox address). Capitals don't matter. If your PIN isn't in the address you send to, add it anywhere in the message as its own word.

| Message | What it does |
|---|---|
| `?` (or an empty message) | Lists the parks and summits at your position, in 160 characters or less. `where`, `here`, `parks` and `lookup` do the same |
| `? 37.5, -79.5` | Looks up that point instead of your position |
| `spot 14062 cw qrp` | Spots **everything** at your position: every park to POTA and the summit to SOTA. `s` is short for `spot` |
| `pota 14062 cw` | Spots only the **parks** at your position, to POTA. `p` is short for `pota` |
| `sota 7032 cw` | Spots only the **summit** at your position, to SOTA |
| `pota US-1234,US-5678 14062` | Spots the parks you name instead of using your position (commas, no spaces) |
| `sota W4V/BR-001 7032` | Spots the summit you name instead of using your position |
| `help` | Replies with a short command list |

How a spot is read:

- **Frequency is always in kHz** (`14062`, or `14062.5`). Anything outside the US amateur bands is refused, and so is a MHz number like `14.062`, so a typo gets an error back instead of a wrong spot. The bands are 2200 m, 630 m, 160 m through 10 m (60 m as 5330 to 5407 kHz), 6 m, 2 m, 1.25 m and 70 cm.
- **Mode** is optional: `cw`, `ssb`, `usb`, `lsb`, `fm`, `am`, `ft8`, `ft4`, `data`, `rtty` or `psk`. Left out, it's guessed from the US band plan: CW at the bottom of each band, SSB above it, FM on 2 m and up. Type `ft8` or `ft4` for those, since the guess can't know.
- Anything after the mode is the **comment**.
- A reference you name must look like one (`US-1234`, `W4V/BR-001`), so a mistyped one is refused.

What gets sent:

- **POTA multi-fer:** one spot for the first park, with the rest in the comment. `spot 14062 cw qrp` in a 3-fer sends US-1234 with the comment `3fer US-5678 US-9012 qrp`.
- **SOTA:** one spot for the summit, with your comment. A comment that starts with `test` (`sota 7032 cw test`) is sent as a SOTAwatch TEST spot, which chasers know to ignore. POTA has no test spot type, so a POTA spot is always a real spot; use **Try a message** with test mode on to check one without posting.
- If `spot` finds a park or summit whose program is turned off on the settings page, it spots the rest and the reply says what was skipped (`SOTA off: W4V/BR-001 not spotted.`). `pota` or `sota` with its program turned off replies that it's off and sends nothing.
- The reply looks like `Spotted US-1234 US-5678 14062 kHz CW.` Anything POTA or SOTA refused shows as `FAILED: ... See log.`, and the log has their full answer.

Messages sent from ordinary email work too, with coordinates typed in the message (`? 37.5, -79.5`). Those replies go out through the SMTP server you set, which is a free way to test before paying for inReach messages.

Settings and passwords are saved in the `/config` folder, never in the image or this repository. Set `PARK_MAP_ADMIN_PASSWORD` on the container so the settings page asks for a password.

Garmin has no public way to reply to an inReach. Like SOTAmāt, the gateway answers through the reply page Garmin links in each message, so a change on Garmin's side can break replies. The log records every exchange with Garmin, POTA and SOTA in full, so you can see what went wrong. Each reply is one 160-character message.

## Point lookup for a satellite messenger

`http://<server>:8075/api/at?lat=46.8529&lon=-121.7603&format=text` answers in 160 characters or less. It looks like this (the references here are made up):

```
3-fer: US-1111 Big Forest, US-2222 Big Wilderness, US-3333 Long Trail. Summit W7W/XX-001 Some Peak. Near: US-4444 0.8km
```

Leave off `format=text` to get the same answer as JSON. This is the piece an inReach email gateway will call.

## Sources and credits

| What | Source | Terms |
|---|---|---|
| Idea and inspiration | [FieldQTH](https://fieldqth.app) by LX1MDA | No code or data used |
| Park list and locations | [Parks on the Air](https://pota.app) ([all_parks_ext.csv](https://pota.app/all_parks_ext.csv)) | Published by POTA |
| Park boundaries | U.S. Geological Survey (USGS) Gap Analysis Project, [Protected Areas Database of the United States (PAD-US) 4.1](https://www.usgs.gov/programs/gap-analysis-project/science/pad-us-data-overview), U.S. Geological Survey data release ([ScienceBase](https://www.sciencebase.gov/catalog/item/652d4fc5d34e44db0e2ee45e)) | Public domain |
| Trail routes | © [OpenStreetMap](https://www.openstreetmap.org/copyright) contributors, state extracts from [Geofabrik](https://download.geofabrik.de) | [ODbL](https://opendatacommons.org/licenses/odbl/); the trail areas in the image are a derived database under the same license |
| Summit list | [Summits on the Air](https://www.sota.org.uk) ([summitslist.csv](https://storage.sota.org.uk/summitslist.csv)) | Published by SOTA |
| Activation zones | [sotl.as](https://sotl.as) and its contributors, published by [OffARO/sota-az](https://github.com/OffARO/sota-az) | Activation zones © sotl.as contributors, free non-commercial use |
| Map display | [MapLibre GL JS](https://maplibre.org) | BSD-3-Clause |
| Background maps | [OpenTopoMap](https://opentopomap.org), [OpenStreetMap](https://www.openstreetmap.org/copyright), [USGS The National Map](https://www.usgs.gov/programs/national-geospatial-program/national-map), [Esri World Imagery](https://www.arcgis.com/home/item.html?id=10df2279f9684e4a9f6a7f08febac2a9) | Each provider's own terms; attribution is shown on the map |

Parks on the Air and Summits on the Air are trademarks of their owners. This project isn't affiliated with or endorsed by POTA, SOTA, sotl.as, FieldQTH or USGS.

Boundaries are matched to POTA parks by name and location, so a few will be wrong or missing. Most missing ones are small BLM sites in the west that PAD-US doesn't map as their own parcel. The `report.csv` on the [reports branch](https://github.com/jsttylr/park-map/blob/reports/report.csv) lists every park with how it was matched, refreshed by each build, and [parks-missing-boundaries.xlsx](docs/parks-missing-boundaries.xlsx) lists the ones with no boundary (see [Notes](#notes)).

Trail parks are drawn as their route plus 100 ft either side, the way POTA defines a trail (see [Notes](#notes)).

N-fer areas are the exact overlaps of the boundaries, worked out when the data is built. Two parks that only share a boundary line don't count as overlapping, and overlaps thinner than about 15 m or smaller than about 2,000 square meters are left out.

## How the data is built

The `build` workflow on GitHub does it all:

1. Reads PAD-US from the `padus-4.1` release (the USGS download sits behind a captcha, so the file was uploaded there by hand). If that file is missing it pages the USGS web service instead, which takes about half an hour.
2. The OpenStreetMap extract of each state with a trail park is downloaded from Geofabrik and cut down to routes and paths with osmium; `cmd/fetchtrails` matches each trail park to its route (cached for the month), and `scripts/buffer_trails.py` widens each route to 100 ft either side.
3. `cmd/builddata` matches every active US POTA park to PAD-US boundaries, uses the trail areas for trail parks, and writes `us-parks.geojson.gz`.
4. `cmd/buildsota` joins the SOTA summits list with the sotl.as activation zones for the US associations and writes `sota.geojson.gz`.
5. `cmd/parkmap -selftest` loads both, works out the overlaps and prints sample lookups.
6. The Docker image is built with the data inside and pushed to `ghcr.io/jsttylr/park-map`.

## Notes

### How trails are drawn

POTA's [rule 1.4, item 5](https://docs.pota.app/docs/rules.html) says that when a trail or river is a POTA reference by itself, and not part of a land park with a defined boundary, the activator and the station must be on public property within 100 ft (30.5 m) of the edge of the trail. So a trail is drawn as its route plus 100 ft either side, not as the land around it.

1. **Which parks are trails.** Any active US park with "Trail" or "Trails" in its name, about 360 of them.
2. **Getting the routes.** For each state with a trail park, the OpenStreetMap extract is downloaded from Geofabrik and cut down with osmium to route relations (hiking, foot, bicycle, mountain bike, horse, historic, canoe, ski and similar) and named paths, footways, cycleways and bridleways.
3. **Matching a park to its route** (`cmd/fetchtrails`):
   - The route's name has to share a distinctive word with the park's name and score at least 0.5 on name similarity.
   - It's rejected if each name has a word the other lacks ("Moab Rim Trail" is not "Hunter Rim Trail"), or if the route is a street ("Santa Fe Drive").
   - A section of a long trail ("Appalachian Trail - Virginia") is swapped for the whole trail it belongs to.
   - The best-named route that comes within 25 km of the park's POTA location wins, with a small bonus within 5 km.
   - With no route relation, the best-named path within 5 km is used, plus every path of the same name within 40 km.
4. **Drawing the band** (`scripts/buffer_trails.py`). Each route is widened by 100 ft (30.48 m) either side, measured in meters around the trail, and cut into 0.1° pieces so the overlap checks stay quick.
5. **Which shape a trail park gets:**
   - A name that ends in "Trail" is the trail itself and gets the band only, even where PAD-US has a wider corridor. The Appalachian Trail's NPS land is about 132,000 acres, but only the roughly 50,000-acre band counts for the trail reference.
   - A park with a boundary that is also a trail ("Katy Trail State Park", "Wilson Cumberland Trail State Park") follows the normal "inside the boundary" rule. It gets its PAD-US outline plus the band, since PAD-US often has only part of these.
   - Land named after a trail ("Scioto Trail State Forest") keeps its PAD-US outline when that's a confident match.
   - A trail with no matching route falls back to its PAD-US outline if it has one, and otherwise isn't drawn.

Limits: the band doesn't cut out private land the trail crosses, even though the rule only counts public property. An OpenStreetMap route can also be incomplete or differ from the official one. POTA itself treats its maps as guidelines only (rule 1.4, item 1).

### Owned land and authorized boundaries

PAD-US often has a unit twice: once as the land the agency owns, and once as the wider area it is authorized to buy (its proclamation boundary), which can include private land. When both match a park equally well, the owned land wins, because POTA only counts public property.

### Parks with no boundary

[docs/parks-missing-boundaries.xlsx](docs/parks-missing-boundaries.xlsx) is a snapshot of the parks the map can't draw yet, from the build of 23 October 2026. About 2,860 of 13,010 US parks have no boundary, and most of them are BLM sites and wildlife areas. The sheet has these tabs:

- **Summary:** counts by kind of park and by state.
- **No boundary:** every undrawn park with links to its POTA page and a map.
- **Weak matches:** boundaries that were drawn but are worth a check.
- **Trails drawn:** the OpenStreetMap route used for each trail.

If you track down an official boundary for one of them, open an issue with a link to it. For the current state of every park, use `report.csv` on the [reports branch](https://github.com/jsttylr/park-map/blob/reports/report.csv).

## Disclaimer

**Use this map at your own risk.** It is provided "as is", without warranty of any kind, and the author accepts no liability for how it is used or for any loss, injury, damage, fine or other consequence that comes from using it.

- **Not accurate enough to rely on.** Boundaries are matched to parks automatically and simplified for display. They can be wrong, out of date, shifted, incomplete or attached to the wrong park. N-fer counts are estimates. Always check the official POTA and SOTA rules, park maps and signs on the ground before an activation.
- **Not permission to enter.** A shape on this map doesn't mean the land is open to the public. Public land often surrounds or borders private property, and inholdings, easements, closures, seasonal restrictions, permits, fees and hours may apply. Never trespass. Get permission where it's needed, obey posted signs and closures, and follow the rules of the agency that manages the land.
- **Not for navigation.** Don't use this map to find your way in the backcountry. Carry proper maps and navigation tools, and know how to use them.
- **The outdoors is dangerous.** Summits and many multi-park spots are remote, steep or exposed, and reaching them can involve hazardous terrain, weather, wildlife, water crossings, long distances and no phone coverage. You alone decide whether a trip is safe for you, and you **assume all risk** of going there. Plan carefully, tell someone where you're going, and turn back when conditions call for it.
- **Spots are posted under your callsign.** Check that the gateway is spotting what you meant before relying on it, and you remain responsible for following POTA and SOTA rules.
- **Satellite replies are best effort.** A reply from the lookup can be late, wrong or never arrive. Don't depend on it for safety.
