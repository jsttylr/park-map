// Reads a pasted coordinate: decimal ("35.5628, -83.4985"), degrees and
// minutes ("N35 33.768 W083 29.910") or degrees, minutes and seconds
// ("35°33'46.1\"N 83°29'54.6\"W"). Returns {lat, lon} or null.
function parseCoord(text) {
  const s = text.trim().toUpperCase().replace(/[°º˚'’′"”″]/g, " ").replace(/\s+/g, " ");
  if (!/\d/.test(s) || /[A-DF-MO-RT-VX-Z]/.test(s)) return null;
  const toks = s.match(/[NSEW]|,|[-+]?\d+(?:\.\d+)?/g) || [];
  const parts = [{ nums: [], hem: null }, { nums: [], hem: null }];
  let cur = 0;
  const hasHem = toks.some(t => /^[NSEW]$/.test(t));
  const hasComma = toks.includes(",");
  const leading = /^[NSEW]$/.test(toks[0] || ""); // "N35 W83" rather than "35N 83W"
  for (const t of toks) {
    if (t === ",") { if (parts[cur].nums.length) cur++; continue; }
    if (cur > 1) return null;
    if (/^[NSEW]$/.test(t)) {
      if (leading && parts[cur].nums.length) cur++;
      if (cur > 1 || parts[cur].hem !== null) return null;
      parts[cur].hem = t;
      if (!leading && !hasComma) cur++;
      continue;
    }
    parts[cur].nums.push(t);
  }
  if (!hasHem && !hasComma) {
    const nums = parts[0].nums;
    if (nums.length % 2 || nums.length < 2 || nums.length > 6) return null;
    parts[0].nums = nums.slice(0, nums.length / 2);
    parts[1].nums = nums.slice(nums.length / 2);
  }
  const value = p => {
    if (p.nums.length < 1 || p.nums.length > 3) return NaN;
    const neg = p.nums[0].startsWith("-") || p.hem === "S" || p.hem === "W";
    const [d, m = 0, sec = 0] = p.nums.map(n => Math.abs(parseFloat(n)));
    if (m >= 60 || sec >= 60) return NaN;
    return (neg ? -1 : 1) * (d + m / 60 + sec / 3600);
  };
  let a = value(parts[0]), b = value(parts[1]);
  if (isNaN(a) || isNaN(b)) return null;
  if (parts[0].hem === "E" || parts[0].hem === "W" || parts[1].hem === "N" || parts[1].hem === "S") [a, b] = [b, a];
  if (Math.abs(a) > 90 || Math.abs(b) > 180) return null;
  return { lat: a, lon: b };
}
