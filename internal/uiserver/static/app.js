"use strict";

// Ant colony harness front-end.
//
// Two jobs: draw the live colony from the SSE snapshot stream, and drive the
// headless engine comparison. It deliberately holds no simulation logic — the
// Go engine is the only source of truth, and the canvas only ever renders what
// arrived in the last snapshot.

const $ = (id) => document.getElementById(id);

const canvas = $("grid");
const ctx = canvas.getContext("2d", { alpha: false });

let currentRun = null;   // { id }
let eventSource = null;
let cachedWalls = null;  // walls ship on frame 0 only, so we keep them
let frameTimes = [];

// ---------------------------------------------------------------------------
// bootstrap
// ---------------------------------------------------------------------------

async function init() {
  const meta = await fetch("/api/meta").then((r) => r.json());

  $("env").textContent =
    `${meta.go_version} · GOMAXPROCS=${meta.gomaxprocs} · ${meta.num_cpu} logical CPUs`;

  fillSelect($("scenario"), meta.scenarios, "medium");
  fillSelect($("benchScenario"), meta.scenarios, "small");
  fillSelect($("engine"), meta.engines, meta.engines[0]);

  const picks = $("benchEngines");
  picks.innerHTML = "";
  for (const name of meta.engines) {
    const label = document.createElement("label");
    const box = document.createElement("input");
    box.type = "checkbox";
    box.value = name;
    box.checked = true;
    label.append(box, document.createTextNode(name));
    picks.append(label);
  }

  $("scenario").addEventListener("change", syncScenarioDefaults);
  await syncScenarioDefaults();

  $("start").addEventListener("click", startRun);
  $("stop").addEventListener("click", stopRun);
  $("runBench").addEventListener("click", runBench);
}

function fillSelect(sel, values, preferred) {
  sel.innerHTML = "";
  for (const v of values) {
    const opt = document.createElement("option");
    opt.value = opt.textContent = v;
    sel.append(opt);
  }
  if (values.includes(preferred)) sel.value = preferred;
}

// Pull the scenario's own seed/ticks/ants into the form, so the defaults shown
// are the ones a headless `antsim -config ...` run would use.
async function syncScenarioDefaults() {
  const name = $("scenario").value;
  if (!name) return;
  try {
    const cfg = await fetch(`/api/scenario?name=${encodeURIComponent(name)}`)
      .then((r) => r.json());
    $("seed").value = cfg.seed;
    $("ticks").value = cfg.ticks;
    $("ants").value = cfg.ant_count;
  } catch (_) { /* leave the form as-is */ }
}

// ---------------------------------------------------------------------------
// live run
// ---------------------------------------------------------------------------

async function startRun() {
  stopRun();
  setStatus("runstatus", "starting…");

  const body = {
    scenario: $("scenario").value,
    engine: $("engine").value,
    interval: Math.max(1, +$("interval").value || 1),
    seed: Number($("seed").value),
    ticks: Math.max(1, +$("ticks").value || 1),
    ants: Math.max(1, +$("ants").value || 1),
  };

  let info;
  try {
    const res = await fetch("/api/run", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    info = await res.json();
    if (!res.ok) throw new Error(info.error || res.statusText);
  } catch (err) {
    setStatus("runstatus", err.message, true);
    return;
  }

  currentRun = info;
  cachedWalls = null;
  frameTimes = [];
  $("start").disabled = true;
  setStatus("runstatus", `running ${info.engine} · seed ${info.seed}`);

  eventSource = new EventSource(`/api/stream?id=${encodeURIComponent(info.id)}`);

  eventSource.onmessage = (ev) => {
    const snap = JSON.parse(ev.data);
    if (snap.walls && snap.walls.length) cachedWalls = snap.walls;
    draw(snap);
    trackFps();
    $("s-tick").textContent = snap.tick;
    $("s-collected").textContent = snap.collected;
  };

  eventSource.addEventListener("end", (ev) => {
    const payload = JSON.parse(ev.data);
    $("s-dropped").textContent = payload.dropped ?? 0;
    const r = payload.result || {};
    setStatus(
      "runstatus",
      payload.error
        ? payload.error
        : `done · ${r.food_collected} collected in ${r.ticks_run} ticks · ` +
          `${(r.wall_ns / 1e6).toFixed(1)} ms · checksum ${hex(r.state_checksum)}`,
      !!payload.error
    );
    closeStream();
  });

  eventSource.onerror = () => {
    // Fires on normal close too, so only surface it if the run is still live.
    if (eventSource && eventSource.readyState === EventSource.CLOSED) closeStream();
  };
}

function stopRun() {
  if (currentRun) {
    fetch(`/api/stop?id=${encodeURIComponent(currentRun.id)}`, { method: "POST" })
      .catch(() => {});
  }
  closeStream();
}

function closeStream() {
  if (eventSource) { eventSource.close(); eventSource = null; }
  currentRun = null;
  $("start").disabled = false;
}

function trackFps() {
  const now = performance.now();
  frameTimes.push(now);
  while (frameTimes.length && now - frameTimes[0] > 1000) frameTimes.shift();
  $("s-fps").textContent = frameTimes.length;
}

function hex(n) {
  // Checksums arrive as JSON numbers and exceed 2^53, so the low bits are
  // already lost by the time we see them. Show a short form and point at the
  // CLI for the authoritative value.
  return "0x" + (n >>> 0).toString(16).padStart(8, "0") + "…";
}

// ---------------------------------------------------------------------------
// rendering
// ---------------------------------------------------------------------------

// Go's encoding/json marshals []uint8 as a base64 STRING, not as an array.
// `af`, `pf` and `ph` therefore arrive as text: indexing one yields a character
// ("A"), which is truthy — so every ant read as "carrying" — and arithmetic on
// it yields NaN, which a Uint8ClampedArray stores as 0 — so both pheromone
// layers rendered pure black. Decode once, here, and the rest of draw() sees
// the byte arrays it was written against.
function bytes(v) {
  if (typeof v !== "string") return v || [];
  const bin = atob(v);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function draw(s) {
  const w = s.w, h = s.h;
  const af = bytes(s.af), pf = bytes(s.pf), ph = bytes(s.ph);
  const px = Math.max(1, Math.floor(canvas.width / Math.max(w, h)));
  const size = px * Math.max(w, h);
  if (canvas.width !== size) { canvas.width = canvas.height = size; }

  ctx.fillStyle = "#07080c";
  ctx.fillRect(0, 0, canvas.width, canvas.height);

  const showFood = $("showFood").checked;
  const showHome = $("showHome").checked;

  // Pheromone layers. Food trail is warm, home trail is cool, so an overlap
  // reads as a corridor the colony uses in both directions.
  if (showFood || showHome) {
    const img = ctx.createImageData(w, h);
    const d = img.data;
    for (let i = 0; i < w * h; i++) {
      const f = showFood ? (pf[i] || 0) : 0;
      const hm = showHome ? (ph[i] || 0) : 0;
      const o = i * 4;
      d[o]     = 7 + f * 0.85;
      d[o + 1] = 8 + f * 0.45 + hm * 0.30;
      d[o + 2] = 12 + hm * 0.85;
      d[o + 3] = 255;
    }
    // Draw at 1:1 into an offscreen canvas, then scale up with smoothing off,
    // which is far cheaper than filling w*h rects every frame.
    const off = new OffscreenCanvas(w, h);
    off.getContext("2d").putImageData(img, 0, 0);
    ctx.imageSmoothingEnabled = false;
    ctx.drawImage(off, 0, 0, w * px, h * px);
  }

  if (cachedWalls) {
    ctx.fillStyle = "#454a5e";
    for (let i = 0; i < cachedWalls.length; i += 2) {
      ctx.fillRect(cachedWalls[i] * px, cachedWalls[i + 1] * px, px, px);
    }
  }

  if (s.food) {
    ctx.fillStyle = "#5fd08a";
    for (let i = 0; i < s.food.length; i += 3) {
      ctx.fillRect(s.food[i] * px, s.food[i + 1] * px, px, px);
    }
  }

  ctx.fillStyle = "#5ac8fa";
  ctx.fillRect((s.nest[0] - 1) * px, (s.nest[1] - 1) * px, px * 3, px * 3);

  const ap = Math.max(1, px - 1);
  for (let i = 0; i < s.ax.length; i++) {
    ctx.fillStyle = af[i] ? "#ffb347" : "#e6e7ee";
    ctx.fillRect(s.ax[i] * px, s.ay[i] * px, ap, ap);
  }
}

// ---------------------------------------------------------------------------
// engine comparison
// ---------------------------------------------------------------------------

async function runBench() {
  const engines = [...document.querySelectorAll("#benchEngines input:checked")]
    .map((b) => b.value);
  if (!engines.length) {
    setStatus("benchStatus", "pick at least one engine", true);
    return;
  }

  $("runBench").disabled = true;
  setStatus("benchStatus", `running ${engines.length} engine(s)…`);
  $("benchTable").querySelector("tbody").innerHTML = "";

  try {
    const res = await fetch("/api/bench", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        scenario: $("benchScenario").value,
        engines,
        repeat: Math.max(1, +$("benchRepeat").value || 1),
      }),
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || res.statusText);
    renderBench(data);
  } catch (err) {
    setStatus("benchStatus", err.message, true);
  } finally {
    $("runBench").disabled = false;
  }
}

function renderBench(data) {
  const tbody = $("benchTable").querySelector("tbody");
  tbody.innerHTML = "";

  for (const r of data.rows) {
    const tr = document.createElement("tr");
    if (r.mismatched) tr.className = "mismatch";
    if (r.error) {
      tr.innerHTML = `<td>${r.engine}</td><td colspan="6">${r.error}</td>`;
      tbody.append(tr);
      continue;
    }
    tr.append(
      cell(r.engine),
      cell(r.wall_ms.toFixed(1)),
      cell(Math.round(r.ticks_per_s).toLocaleString()),
      cell(r.speedup_x >= 1 ? r.speedup_x.toFixed(2) + "x" : r.speedup_x.toFixed(2) + "x",
           r.speedup_x > 1.05 ? "speedup" : ""),
      cell(r.allocs.toLocaleString()),
      cell(r.heap_mb.toFixed(1)),
      cell(r.checksum)
    );
    tbody.append(tr);
  }

  const bad = data.rows.filter((r) => r.mismatched).length;
  setStatus(
    "benchStatus",
    bad
      ? `${bad} engine(s) produced a DIFFERENT simulation — their speed does not count`
      : `${data.scenario} · seed ${data.seed} · ${data.ticks} ticks · ` +
        `${data.ants} ants · best of ${data.repeat} · all checksums agree`,
    bad > 0
  );
}

function cell(text, cls) {
  const td = document.createElement("td");
  td.textContent = text;
  if (cls) td.className = cls;
  return td;
}

function setStatus(id, msg, isErr) {
  const el = $(id);
  el.textContent = msg;
  el.classList.toggle("err", !!isErr);
}

init().catch((err) => setStatus("runstatus", err.message, true));
