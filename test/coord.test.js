// Checks the pasted-coordinate reader in the map page: node test/coord.test.js
const fs = require("fs"); eval(fs.readFileSync(__dirname + "/../cmd/parkmap/web/coord.js", "utf8"));
const cases = [
  ["35.5628, -83.4985", 35.5628, -83.4985],
  ["35.5628 -83.4985", 35.5628, -83.4985],
  ["35°33'46.1\"N 83°29'54.6\"W", 35.56281, -83.49850],
  ["N35 33.768 W083 29.910", 35.5628, -83.4985],
  ["35.5628N 83.4985W", 35.5628, -83.4985],
  ["N 35° 33.768', W 83° 29.910'", 35.5628, -83.4985],
  ["W83.4985 N35.5628", 35.5628, -83.4985],
  ["35 33 46.1 -83 29 54.6", 35.56281, -83.4985],
  ["35.5628,-83.4985", 35.5628, -83.4985],
];
for (const [q, la, lo] of cases) {
  const r = parseCoord(q);
  const ok = r && Math.abs(r.lat - la) < 1e-4 && Math.abs(r.lon - lo) < 1e-4;
  if (!ok) { console.log("FAIL", q, JSON.stringify(r)); process.exitCode = 1; }
}
for (const q of ["US-1234", "rainier", "W7W/KG-001", "12345", "Mount 5"]) if (parseCoord(q) !== null) { console.log("FAIL", q); process.exitCode = 1; }
if (!process.exitCode) console.log("coordinate tests passed");
