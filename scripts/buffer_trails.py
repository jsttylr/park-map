"""Widen trail routes into the area POTA counts as the trail.

Reads the lines cmd/fetchtrails wrote and writes, for each trail park, the
area within 100 ft of its route, cut into pieces on a 0.1 degree grid so the
map server's overlap checks only handle the pieces near another park.

    python3 scripts/buffer_trails.py trails.geojsonl.gz trail-areas.geojsonl.gz
"""
import gzip
import json
import math
import sys

import numpy
import shapely
from pyproj import CRS, Transformer
from shapely.geometry import box, mapping, shape

HALF_WIDTH_M = 30.48  # 100 ft either side of the route
SIMPLIFY_M = 2
CELL_DEG = 0.1


def polygons(g):
    if g.is_empty:
        return []
    if g.geom_type == "Polygon":
        return [g]
    if g.geom_type in ("MultiPolygon", "GeometryCollection"):
        return [p for part in g.geoms for p in polygons(part)]
    return []


def rounded(coords):
    if isinstance(coords[0], (int, float)):
        return [round(coords[0], 6), round(coords[1], 6)]
    return [rounded(c) for c in coords]


def project(g, t):
    return shapely.transform(g, lambda xy: numpy.column_stack(t.transform(xy[:, 0], xy[:, 1])))


def widen(lines):
    # Measure in meters around the trail's own middle; the error stays under
    # a meter even for a trail across the country.
    c = lines.centroid
    aeqd = CRS.from_proj4(f"+proj=aeqd +lat_0={c.y} +lon_0={c.x} +datum=WGS84 +units=m +no_defs")
    to_m = Transformer.from_crs("EPSG:4326", aeqd, always_xy=True)
    to_deg = Transformer.from_crs(aeqd, "EPSG:4326", always_xy=True)
    area = project(lines, to_m).simplify(SIMPLIFY_M).buffer(HALF_WIDTH_M, quad_segs=4)
    area = shapely.make_valid(project(area.simplify(SIMPLIFY_M / 2), to_deg))
    shapely.prepare(area)
    x0, y0, x1, y1 = area.bounds
    pieces = []
    for i in range(math.floor(x0 / CELL_DEG), math.floor(x1 / CELL_DEG) + 1):
        for j in range(math.floor(y0 / CELL_DEG), math.floor(y1 / CELL_DEG) + 1):
            cell = box(i * CELL_DEG, j * CELL_DEG, (i + 1) * CELL_DEG, (j + 1) * CELL_DEG)
            if area.intersects(cell):
                pieces += polygons(area.intersection(cell))
    return pieces


def main(src, dst):
    n = 0
    with gzip.open(src, "rt") as f, gzip.open(dst, "wt") as out:
        for line in f:
            ft = json.loads(line)
            if not ft.get("geometry"):
                continue  # a trail park with no route found
            lines = shape(ft["geometry"])
            if lines.is_empty:
                continue
            pieces = widen(lines)
            if not pieces:
                continue
            ft["geometry"] = {
                "type": "MultiPolygon",
                "coordinates": [rounded(mapping(p)["coordinates"]) for p in pieces],
            }
            out.write(json.dumps(ft, separators=(",", ":")) + "\n")
            n += 1
    print(f"{n} trail areas written to {dst}")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
