// Runs the third-party `pmtiles` npm client against every pmtiles-lab
// scenario and prints one JSON row per (scenario, archive).
//
//   pmtiles-lab serve --dir fixtures --delay 2s        # prints the base URL
//   node run-scenarios.mjs --base http://127.0.0.1:PORT --manifest ../../fixtures/manifest.json
//
// The lab trace is reset before each run and read after it, so each row
// shows what the client requested and what it concluded.
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { PMTiles, FetchSource } from "pmtiles";

function arg(name, dflt) {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 ? process.argv[i + 1] : dflt;
}
const base = arg("base");
const manifestPath = arg("manifest");
const timeoutMs = Number(arg("timeout-ms", "1000"));
const only = arg("archive"); // optional manifest archive name
if (!base || !manifestPath) {
  console.error("usage: node run-scenarios.mjs --base URL --manifest FILE [--archive NAME] [--timeout-ms 1000]");
  process.exit(2);
}
const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
const archives = manifest.archives.filter((a) => a.kind === "valid" && (!only || a.name === only));
const scenarios = await (await fetch(`${base}/__lab/scenarios`)).json();

const sha256 = (buf) => createHash("sha256").update(Buffer.from(buf)).digest("hex");
const withTimeout = (p, ms) =>
  Promise.race([p, new Promise((_, rej) => setTimeout(() => rej(new Error(`timeout after ${ms} ms`)), ms))]);

const rows = [];
for (const sc of scenarios) {
  for (const a of archives) {
    await fetch(`${base}/__lab/reset`, { method: "POST" });
    const url = `${base}/scenarios/${sc.name}/${a.file}`;
    const p = new PMTiles(new FetchSource(url));
    const row = { scenario: sc.name, archive: a.name, tiles_ok: 0, tiles_wrong: 0, error: null };
    try {
      await withTimeout(p.getHeader(), timeoutMs);
      for (const t of a.tiles) {
        const r = await withTimeout(p.getZxy(t.z, t.x, t.y, AbortSignal.timeout(timeoutMs)), timeoutMs);
        const got = r ? sha256(r.data) : null;
        const want = t.status === "present" ? t.sha256 : null;
        if (got === want) row.tiles_ok++;
        else row.tiles_wrong++;
      }
    } catch (e) {
      row.error = `${e.name}: ${e.message}`.slice(0, 160);
    }
    // A timed-out request is traced when the server finishes it (bounded by
    // the server's --delay), so wait for in-flight requests to land.
    let trace;
    for (let i = 0; i < 40; i++) {
      trace = await (await fetch(`${base}/__lab/trace`)).json();
      if (!row.error || trace.entries.length > 0) break;
      await new Promise((r) => setTimeout(r, 250));
    }
    row.requests = trace.entries.length;
    row.statuses = trace.entries.map((e) => e.status + (e.error ? `(${e.error})` : ""));
    row.outcome = row.error ? "error" : row.tiles_wrong ? "wrong-bytes" : "ok";
    rows.push(row);
    console.log(JSON.stringify(row));
  }
}
