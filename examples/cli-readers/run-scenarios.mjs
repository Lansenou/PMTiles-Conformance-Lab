// Runs any command-line PMTiles reader against every pmtiles-lab scenario and
// prints one JSON row per (scenario, archive), like ../pmtiles-js.
//
//   pmtiles-lab serve --dir fixtures --delay 2s
//   node run-scenarios.mjs --base http://127.0.0.1:PORT --manifest ../../fixtures/manifest.json \
//       --reader go-pmtiles --cmd 'go-pmtiles tile {url} {z} {x} {y}'
//
// Reader contract: the command prints the stored tile bytes to stdout and
// exits 0; exit 0 with no output means absent; a non-zero exit is an error.
// Each tile is a separate process, so the reader re-reads the header and
// directories for every tile. A run stops at the first error, as the
// pmtiles.js harness does.
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";

function arg(name, dflt) {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 ? process.argv[i + 1] : dflt;
}
const base = arg("base");
const manifestPath = arg("manifest");
const cmd = arg("cmd");
const reader = arg("reader", "cli");
const timeoutMs = Number(arg("timeout-ms", "1000"));
const only = arg("archive");
if (!base || !manifestPath || !cmd) {
  console.error("usage: node run-scenarios.mjs --base URL --manifest FILE --cmd 'reader {url} {z} {x} {y}' [--reader NAME] [--archive NAME] [--timeout-ms 1000]");
  process.exit(2);
}
const MAX_OUT = 2 << 20;
const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
const archives = manifest.archives.filter((a) => a.kind === "valid" && (!only || a.name === only));
const scenarios = await (await fetch(`${base}/__lab/scenarios`)).json();
const sha256 = (b) => createHash("sha256").update(b).digest("hex");

// runTile resolves to {bytes} or {error}.
function runTile(url, t) {
  const argv = cmd.split(/\s+/).map((s) =>
    s.replace("{url}", url).replace("{z}", t.z).replace("{x}", t.x).replace("{y}", t.y)
  );
  return new Promise((resolve) => {
    const p = spawn(argv[0], argv.slice(1), { stdio: ["ignore", "pipe", "pipe"] });
    const out = [];
    let size = 0;
    let err = "";
    let done = false;
    const finish = (r) => {
      if (!done) {
        done = true;
        clearTimeout(timer);
        resolve(r);
      }
    };
    const timer = setTimeout(() => {
      p.kill("SIGKILL");
      finish({ error: `timeout after ${timeoutMs} ms` });
    }, timeoutMs);
    p.stdout.on("data", (d) => {
      size += d.length;
      if (size > MAX_OUT) {
        p.kill("SIGKILL");
        finish({ error: "output too large" });
      } else out.push(d);
    });
    p.stderr.on("data", (d) => {
      if (err.length < 4096) err += d;
    });
    p.on("error", (e) => finish({ error: String(e) }));
    p.on("close", (code) => {
      if (code === 0) finish({ bytes: Buffer.concat(out) });
      else finish({ error: `exit ${code}: ${err.trim().split("\n")[0] || ""}` });
    });
  });
}

for (const sc of scenarios) {
  for (const a of archives) {
    await fetch(`${base}/__lab/reset`, { method: "POST" });
    const url = `${base}/scenarios/${sc.name}/${a.file}`;
    const row = { reader, scenario: sc.name, archive: a.name, tiles_ok: 0, tiles_wrong: 0, error: null };
    for (const t of a.tiles) {
      const r = await runTile(url, t);
      if (r.error) {
        row.error = r.error.slice(0, 160);
        break;
      }
      const got = r.bytes.length ? sha256(r.bytes) : null;
      const want = t.status === "present" ? t.sha256 : null;
      if (got === want) row.tiles_ok++;
      else row.tiles_wrong++;
    }
    // Requests abandoned by a timeout are traced when the server finishes
    // them (bounded by --delay), so wait for them to land.
    let trace;
    for (let i = 0; i < 40; i++) {
      trace = await (await fetch(`${base}/__lab/trace`)).json();
      if (!row.error || trace.entries.length > 0) break;
      await new Promise((r) => setTimeout(r, 250));
    }
    await new Promise((r) => setTimeout(r, row.error ? 2500 : 0));
    trace = await (await fetch(`${base}/__lab/trace`)).json();
    row.requests = trace.entries.length;
    row.statuses = {};
    for (const e of trace.entries) {
      const k = e.status + (e.error ? `(${e.error})` : "");
      row.statuses[k] = (row.statuses[k] || 0) + 1;
    }
    row.outcome = row.error ? "error" : row.tiles_wrong ? "wrong-bytes" : "ok";
    console.log(JSON.stringify(row));
  }
}
