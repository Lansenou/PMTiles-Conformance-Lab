// Runs the pmtiles npm package inside a real browser against every
// pmtiles-lab scenario and prints one JSON row per (scenario, archive),
// counted as run-scenarios.mjs counts them. `net_errors` lists the
// browser's own text for each failed archive request.
//
// The page is served from a second loopback origin (different port), so
// every archive read is a cross-origin fetch. pmtiles and fflate are served
// from node_modules through an import map; there is no bundler.
//
//   pmtiles-lab serve --dir fixtures --delay 2s        # prints the base URL
//   node browser-matrix.mjs --base http://127.0.0.1:PORT --manifest ../../fixtures/manifest.json \
//       --browser chromium|firefox|webkit [--timeout-ms 1000] [--scenario NAME] [--archive NAME]
//
// Other scenarios are observations. The harness checks itself on `normal`:
// it exits 1 unless every `normal` row read every tile with only 206
// responses, since anything else means the harness or the browser setup
// is broken, not the server.
//
//   node browser-matrix.mjs --browser B --accept-encoding
//
// prints the raw request head the browser sends for a cross-origin fetch
// with a Range header, as received by a raw loopback listener (the lab trace
// does not record Accept-Encoding).
//
//   node browser-matrix.mjs --raw-listener PORT
//
// runs only that listener, for other clients: it prints each request head
// and answers a 16-byte 206.
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { createServer as createHttpServer } from "node:http";
import { createServer as createNetServer } from "node:net";
import { dirname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";
import * as playwright from "playwright-core";

function arg(name, dflt) {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 ? process.argv[i + 1] : dflt;
}
const flag = (name) => process.argv.includes(`--${name}`);

// rawListener accepts plain HTTP/1.1 requests, hands each request head
// (bytes up to the blank line, verbatim) to onHead and answers a 16-byte
// 206 with CORS headers, so a browser fetch resolves without a preflight.
function rawListener(port, onHead) {
  const srv = createNetServer((sock) => {
    let buf = Buffer.alloc(0);
    sock.on("data", (d) => {
      buf = Buffer.concat([buf, d]);
      const end = buf.indexOf("\r\n\r\n");
      if (end < 0) return;
      onHead(buf.subarray(0, end).toString("latin1"));
      buf = buf.subarray(end + 4);
      sock.end(
        "HTTP/1.1 206 Partial Content\r\nAccess-Control-Allow-Origin: *\r\n" +
          "Content-Range: bytes 0-15/16\r\nContent-Length: 16\r\nConnection: close\r\n\r\n" +
          "0123456789abcdef"
      );
    });
    sock.on("error", () => {});
  });
  return new Promise((r) => srv.listen(port, "127.0.0.1", () => r(srv)));
}

if (arg("raw-listener")) {
  const srv = await rawListener(Number(arg("raw-listener")), (head) => console.log(head + "\n"));
  console.error(`raw listener on http://127.0.0.1:${srv.address().port}`);
  await new Promise(() => {});
}

const browserName = arg("browser");
const base = arg("base");
const manifestPath = arg("manifest");
const timeoutMs = Number(arg("timeout-ms", "1000"));
const onlyScenario = arg("scenario");
const onlyArchive = arg("archive");
const acceptEncoding = flag("accept-encoding");
if (!["chromium", "firefox", "webkit"].includes(browserName) || (!acceptEncoding && (!base || !manifestPath))) {
  console.error(
    "usage: node browser-matrix.mjs --base URL --manifest FILE --browser chromium|firefox|webkit [--timeout-ms 1000] [--scenario NAME] [--archive NAME]\n" +
      "       node browser-matrix.mjs --browser chromium|firefox|webkit --accept-encoding\n" +
      "       node browser-matrix.mjs --raw-listener PORT"
  );
  process.exit(2);
}

// The page origin serves the HTML page and, read-only, node_modules.
const here = dirname(fileURLToPath(import.meta.url));
const nodeModules = join(here, "node_modules");
const html = `<!doctype html>
<meta charset="utf-8">
<title>pmtiles-lab browser matrix</title>
<script type="importmap">
{"imports": {"pmtiles": "/node_modules/pmtiles/dist/esm/index.js", "fflate": "/node_modules/fflate/esm/browser.js"}}
</script>
<script type="module">
import { PMTiles, FetchSource } from "pmtiles";
const withTimeout = (p, ms) =>
  Promise.race([p, new Promise((_, rej) => setTimeout(() => rej(new Error("timeout after " + ms + " ms")), ms))]);
const b64 = (u8) => {
  let s = "";
  for (let i = 0; i < u8.length; i += 0x8000) s += String.fromCharCode(...u8.subarray(i, i + 0x8000));
  return btoa(s);
};
// One PMTiles instance per run. Tile bytes go back to Node, which hashes
// them, so the page needs no crypto.subtle.
window.labRun = async ({ url, tiles, timeoutMs }) => {
  const p = new PMTiles(new FetchSource(url));
  const out = { tiles: [], error: null };
  try {
    await withTimeout(p.getHeader(), timeoutMs);
    for (const t of tiles) {
      const r = await withTimeout(p.getZxy(t.z, t.x, t.y, AbortSignal.timeout(timeoutMs)), timeoutMs);
      out.tiles.push(r ? b64(new Uint8Array(r.data)) : null);
    }
  } catch (e) {
    out.error = (e.name + ": " + e.message).slice(0, 160);
  }
  return out;
};
window.labRawFetch = async (url) => {
  const r = await fetch(url, { headers: { Range: "bytes=0-15" }, cache: "no-store" });
  return r.status + " " + (await r.arrayBuffer()).byteLength;
};
window.labReady = true;
</script>`;
const page = createHttpServer((req, res) => {
  const path = decodeURIComponent(new URL(req.url, "http://x").pathname);
  if (path === "/") {
    res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    return res.end(html);
  }
  const file = normalize(join(here, path));
  if (path.startsWith("/node_modules/") && file.startsWith(nodeModules + "/") && file.endsWith(".js")) {
    try {
      const body = readFileSync(file);
      res.writeHead(200, { "content-type": "text/javascript; charset=utf-8" });
      return res.end(body);
    } catch {}
  }
  res.writeHead(404);
  res.end();
});
await new Promise((r) => page.listen(0, "127.0.0.1", r));
const pageOrigin = `http://127.0.0.1:${page.address().port}`;

const browser = await playwright[browserName].launch();
const version = `${browserName} ${browser.version()}`;
let tab;
// Network errors as the browser reports them (for example net::ERR_...),
// which the page itself only sees as a TypeError. Only requests for the
// current run's URL count: a failure event can arrive after its run ended.
let netErrors = [];
let runUrl = null;
async function openTab() {
  const old = tab;
  tab = null;
  if (old) await old.close().catch(() => {});
  const t = await browser.newPage();
  t.on("requestfailed", (r) => {
    if (tab === t && r.url() === runUrl) netErrors.push(r.failure()?.errorText ?? "unknown");
  });
  tab = t;
  await tab.goto(pageOrigin + "/");
  await tab.waitForFunction(() => window.labReady === true, null, { timeout: 10000 });
}
await openTab();

if (acceptEncoding) {
  const heads = [];
  const srv = await rawListener(0, (h) => heads.push(h));
  const got = await tab.evaluate((url) => window.labRawFetch(url), `http://127.0.0.1:${srv.address().port}/archive.pmtiles`);
  console.log(`# ${version}, page origin ${pageOrigin}, cross-origin fetch with Range: bytes=0-15 -> ${got}`);
  for (const h of heads) console.log(h + "\n");
  srv.close();
  await browser.close();
  page.close();
  process.exit(heads.length ? 0 : 1);
}

const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
const archives = manifest.archives.filter((a) => a.kind === "valid" && (!onlyArchive || a.name === onlyArchive));
const scenarios = (await (await fetch(`${base}/__lab/scenarios`)).json()).filter((s) => !onlyScenario || s.name === onlyScenario);
const sha256 = (buf) => createHash("sha256").update(buf).digest("hex");

let selfCheckFailed = 0;
for (const sc of scenarios) {
  for (const a of archives) {
    await fetch(`${base}/__lab/reset`, { method: "POST" });
    const url = `${base}/scenarios/${sc.name}/${a.file}`;
    const row = { browser: version, scenario: sc.name, archive: a.name, tiles_ok: 0, tiles_wrong: 0, error: null };
    let crashed = false;
    netErrors = [];
    runUrl = url;
    try {
      const tiles = a.tiles.map((t) => ({ z: t.z, x: t.x, y: t.y }));
      // The page bounds every step with timeoutMs; this outer bound only
      // catches a hung or crashed page.
      const got = await Promise.race([
        tab.evaluate((args) => window.labRun(args), { url, tiles, timeoutMs }),
        new Promise((_, rej) => setTimeout(() => rej(new Error("page did not answer")), timeoutMs * (tiles.length + 2) + 5000)),
      ]);
      got.tiles.forEach((b, i) => {
        const t = a.tiles[i];
        const have = b === null ? null : sha256(Buffer.from(b, "base64"));
        const want = t.status === "present" ? t.sha256 : null;
        if (have === want) row.tiles_ok++;
        else row.tiles_wrong++;
      });
      row.error = got.error;
    } catch (e) {
      crashed = true;
      row.error = `crash: ${e.message}`.slice(0, 160);
    }
    // As run-scenarios.mjs: a timed-out request is traced when the server
    // finishes it (bounded by --delay), so wait for in-flight requests.
    let trace;
    for (let i = 0; i < 40; i++) {
      trace = await (await fetch(`${base}/__lab/trace`)).json();
      if (!row.error || trace.entries.length > 0) break;
      await new Promise((r) => setTimeout(r, 250));
    }
    row.requests = trace.entries.length;
    row.statuses = trace.entries.map((e) => e.status + (e.error ? `(${e.error})` : ""));
    row.net_errors = netErrors;
    row.outcome = crashed ? "crash" : row.error ? "error" : row.tiles_wrong ? "wrong-bytes" : "ok";
    console.log(JSON.stringify(row));
    if (sc.name === "normal") {
      const readAll = row.outcome === "ok" && row.tiles_ok === a.tiles.length;
      const only206 = row.statuses.length > 0 && row.statuses.every((s) => s === "206");
      if (!readAll || !only206) {
        selfCheckFailed++;
        console.error(`self-check: normal/${a.name} did not read every tile with 206 responses only`);
      }
    }
    // A fresh page after any error, so no request of this run can leak
    // into the next one.
    if (row.error) await openTab();
  }
}
console.error(JSON.stringify({ browser: version, page_origin: pageOrigin, self_check_failed: selfCheckFailed }));
await browser.close();
page.close();
process.exit(selfCheckFailed ? 1 : 0);
