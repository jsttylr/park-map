// Takes README screenshots of a running park map: node screenshots.mjs http://127.0.0.1:8075 outdir
import { chromium } from "playwright";

const [base, out] = process.argv.slice(2);
const shots = [
  { name: "overview", hash: "4.2/39/-97" },
  { name: "nfer", hash: "11.2/36.66/-81.55", nfer: 3 },
  { name: "lookup", hash: "12/35.5628/-83.4985", click: true },
  { name: "summits", hash: "11.5/46.86/-121.74" },
];

const browser = await chromium.launch();
for (const s of shots) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });
  page.on("pageerror", e => console.log(s.name, "page error:", e.message));
  await page.goto(`${base}/#${s.hash}`);
  const settle = async () => {
    await page.waitForFunction(() => window.map && map.loaded() && map.areTilesLoaded(), null, { timeout: 60000 }).catch(() => console.log(s.name, "tiles still loading"));
    await page.waitForTimeout(1500);
  };
  await settle();
  if (s.nfer) {
    await page.evaluate(n => { const el = document.getElementById("nfer"); el.value = n; el.dispatchEvent(new Event("input")); }, s.nfer);
    await settle();
  }
  if (s.click) {
    await page.mouse.click(640, 400);
    await page.waitForSelector(".pop h3", { timeout: 15000 }).catch(() => {});
    await page.waitForTimeout(500);
  }
  await page.screenshot({ path: `${out}/${s.name}.png` });
  console.log("saved", s.name);
  await page.close();
}
await browser.close();
