# Park Map

> This software was **developed with AI** (Claude, by Anthropic). **Use it at your own risk.** It comes with no warranty of any kind. Licensed under GPL-3.0.

A map of US [Parks on the Air](https://pota.app) parks with their real boundaries, plus [Summits on the Air](https://www.sota.org.uk) summits and their activation zones, served as a web page on your network.

- **Park outlines.** About 10,000 of the 13,000 active US parks are drawn with their actual boundary. The rest show as a dot at the POTA location.
- **N-fer filter.** Slide to 2, 3 or more to show only places where you can stand in that many parks at once. Colors show the count: green 1, blue 2, purple 3, orange 4 or more.
- **SOTA summits.** Turn summits and their activation zones on or off, and choose whether a summit counts toward the n-fer.
- **What's here.** Click anywhere to list every park and summit at that exact spot, plus nearby parks that have no outline on file. The box at the bottom shows the short text reply a satellite messenger would get (see below).
- **Search** by reference (`US-1234`, `W7W/KG-001`) or by name.

## Run it with Docker

```
docker run -d --name park-map --restart unless-stopped -p 8075:8075 ghcr.io/jsttylr/park-map:latest
```

Then open `http://<server address>:8075/`. It takes a minute or two after starting to work out all the overlaps before the page answers. There's also a `docker-compose.yml`.

On **Unraid**, add the template from `https://raw.githubusercontent.com/jsttylr/park-map/main/unraid/park-map.xml`.

The map data is built into the image, so nothing needs to be downloaded at startup. The image is rebuilt once a month with the newest park list, summits and activation zones. `latest` follows the `main` branch, and every other branch gets an image tagged with its name.

## Settings

| Setting | Environment variable | Default |
|---|---|---|
| Data folder | `PARK_MAP_DATA` | `data` (`/data` in Docker) |
| Address and port | `PARK_MAP_LISTEN` | `0.0.0.0:8075` |

## Point lookup for a satellite messenger

`http://<server>:8075/api/at?lat=46.8529&lon=-121.7603&format=text` answers in 160 characters or less. It looks like this (the references here are made up):

```
3-fer: US-1111 Big Forest, US-2222 Big Wilderness, US-3333 Long Trail. Summit W7W/XX-001 Some Peak. Near: US-4444 0.8km
```

Leave off `format=text` to get the same answer as JSON. This is the piece an inReach email gateway will call.

## Where the data comes from

| Data | Source | Terms |
|---|---|---|
| Park list and locations | [POTA](https://pota.app/all_parks_ext.csv) | Public list |
| Park boundaries | [USGS PAD-US 4.1](https://www.usgs.gov/programs/gap-analysis-project/science/pad-us-data-overview) | Public domain |
| Summit list | [SOTA summits list](https://storage.sota.org.uk/summitslist.csv) | Public list |
| Activation zones | [sotl.as](https://sotl.as), published by [OffARO/sota-az](https://github.com/OffARO/sota-az) | Activation zones © sotl.as contributors, free non-commercial use |

Boundaries are matched to POTA parks by name and location, so a few will be wrong or missing. Most missing ones are small BLM sites in the west that PAD-US doesn't map as their own parcel. The `report.csv` from each build lists every park with how it was matched.

The n-fer counts come from testing a grid of points where outlines overlap. Two parks that only share a boundary line don't count as overlapping (you need to be about 25 m inside both), and very thin slivers of overlap can be missed.

## How the data is built

The `build` workflow on GitHub does it all:

1. Reads PAD-US from the `padus-4.1` release (the USGS download sits behind a captcha, so the file was uploaded there by hand). If that file is missing it pages the USGS web service instead, which takes about half an hour.
2. `cmd/builddata` matches every active US POTA park to PAD-US boundaries and writes `us-parks.geojson.gz`.
3. `cmd/buildsota` joins the SOTA summits list with the sotl.as activation zones for the US associations and writes `sota.geojson.gz`.
4. `cmd/parkmap -selftest` loads both, works out the overlaps and prints sample lookups.
5. The Docker image is built with the data inside and pushed to `ghcr.io/jsttylr/park-map`.
