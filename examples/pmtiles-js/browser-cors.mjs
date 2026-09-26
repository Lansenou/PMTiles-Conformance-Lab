// Checks real browser CORS behaviour against the lab's CORS scenarios.
// A page is served from a second loopback origin (different port), and
// headless Chromium issues a cross-origin fetch with a Range header.
//
//   pmtiles-lab serve --dir fixtures
//   node browser-cors.mjs --base http://127.0.0.1:PORT [--file valid/root-none.pmtiles]
//
// Exit 0 when every observation matches the expectation table below.
import { createServer } from "node:http";
import { chromium } from "playwright-core";

function arg(name, dflt) {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 ? process.argv[i + 1] : dflt;
}
const base = arg("base");
const file = arg("file", "valid/root-none.pmtiles");
if (!base) {
  console.error("usage: node browser-cors.mjs --base URL [--file PATH]");
  process.exit(2);
}

// Expected browser-visible outcome per scenario.
const expected = {
  normal: { ok: true, status: 206, etagVisible: true, contentRangeVisible: true },
  "cors-missing": { ok: false },
  "cors-wrong-origin": { ok: false },
  "cors-no-expose": { ok: true, status: 206, etagVisible: false, contentRangeVisible: false },
};

const page = createServer((req, res) => {
  res.writeHead(200, { "content-type": "text/html" });
  res.end("<!doctype html><title>pmtiles-lab cors check</title>");
});
await new Promise((r) => page.listen(0, "127.0.0.1", r));
const pageOrigin = `http://127.0.0.1:${page.address().port}`;

const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || undefined });
const tab = await browser.newPage();
await tab.goto(pageOrigin + "/");

let failed = 0;
const rows = [];
for (const [scenario, want] of Object.entries(expected)) {
  // A custom non-safelisted header forces a preflight; a plain Range does not
  // need one in browsers implementing the Fetch "safelisted Range" rule.
  for (const variant of ["range-only", "with-preflight"]) {
    await fetch(`${base}/__lab/reset`, { method: "POST" });
    const url = `${base}/scenarios/${scenario}/${file}`;
    const got = await tab.evaluate(
      async ({ url, variant }) => {
        const headers = { Range: "bytes=0-15" };
        if (variant === "with-preflight") headers["If-Match"] = "*";
        try {
          const r = await fetch(url, { headers, cache: "no-store" });
          const b = await r.arrayBuffer();
          return {
            ok: true,
            status: r.status,
            etagVisible: r.headers.get("ETag") !== null,
            contentRangeVisible: r.headers.get("Content-Range") !== null,
            bytes: b.byteLength,
          };
        } catch (e) {
          return { ok: false, error: String(e) };
        }
      },
      { url, variant }
    );
    const trace = await (await fetch(`${base}/__lab/trace`)).json();
    const methods = trace.entries.map((e) => `${e.method} ${e.status}`);
    const match = Object.entries(want).every(([k, v]) => got[k] === v);
    if (!match) failed++;
    const row = { scenario, variant, match, observed: got, server_saw: methods };
    rows.push(row);
    console.log(JSON.stringify(row));
  }
}
console.log(JSON.stringify({ browser: `chromium ${browser.version()}`, page_origin: pageOrigin, failed }));
await browser.close();
page.close();
process.exit(failed ? 1 : 0);
