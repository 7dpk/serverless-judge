// Playground. Everything it shows from the judge (program output, compiler
// messages, labels) is untrusted and goes in with textContent only.
"use strict";

const $ = (id) => document.getElementById(id);
const KEY = "judge.token";

// A shared link carries the token after the #, which browsers never send to
// the server. Take it, store it, and clean the address bar.
(function takeTokenFromLink() {
  const m = location.hash.match(/token=([^&]+)/);
  if (m) {
    localStorage.setItem(KEY, decodeURIComponent(m[1]));
    history.replaceState(null, "", location.pathname);
  }
})();

const token = () => localStorage.getItem(KEY) || "";

async function api(path, options = {}) {
  const headers = Object.assign({ "X-Auth-Token": token() }, options.headers || {});
  if (options.body) headers["Content-Type"] = "application/json";
  const res = await fetch(path, Object.assign({}, options, { headers }));
  let body = null;
  const text = await res.text();
  try { body = text ? JSON.parse(text) : null; } catch (e) { body = null; }
  return { status: res.status, body, timing: res.headers.get("Server-Timing") || "" };
}

// Judge0 status ids, grouped by how the result should read.
const TONE = { 3: "good", 4: "serious", 5: "warning", 6: "serious", 13: "critical", 15: "warning" };
const SYMBOL = { good: "✓", warning: "!", serious: "×", critical: "×", idle: "" };
const toneOf = (id) => TONE[id] || (id >= 7 && id <= 12 || id === 14 ? "serious" : "idle");

function setVerdict(dot, text, tone, label) {
  dot.className = "dot " + tone;
  dot.textContent = SYMBOL[tone];
  text.textContent = label;
}

const fmtMs = (ms) => (ms < 1000 ? Math.round(ms) + " ms" : (ms / 1000).toFixed(2) + " s");
const fmtRun = (t) => (t == null ? "-" : fmtMs(parseFloat(t) * 1000));
const fmtMem = (kb) => (kb == null ? "-" : kb < 1024 ? kb + " KB" : (kb / 1024).toFixed(1) + " MB");

function serverMs(timing) {
  const m = timing.match(/total;dur=(\d+)/);
  return m ? fmtMs(Number(m[1])) : "-";
}

function explain(res) {
  if (res.status === 401) return (res.body && res.body.error) || "That token was not accepted.";
  if (res.status === 429) return (res.body && res.body.error) || "This token has no submissions left.";
  if (res.status === 503) return "The judge is busy right now. Try again in a moment.";
  if (res.status === 422 && res.body) {
    return Object.entries(res.body).map(([k, v]) => k + " " + [].concat(v).join(", ")).join("; ");
  }
  return (res.body && res.body.error) || "The judge answered with HTTP " + res.status + ".";
}

let languages = []; // one entry per language: {id, key, name}

async function start() {
  if (!token()) return showGate("");
  const me = await api("/whoami");
  if (me.status !== 200) return showGate(explain(me));
  $("gate").hidden = true;
  $("signout").hidden = false;
  if (me.body.role === "admin") {
    $("who").textContent = "Signed in with an admin key";
  } else {
    const left = me.body.max_submissions - me.body.used;
    const until = new Date(me.body.expires_at).toLocaleString();
    $("who").textContent = (me.body.label ? me.body.label + ": " : "") + left + " submissions left, valid until " + until;
  }

  // Shown only to a key that may ask for network access, on a deployment
  // that allows it.
  $("netbox").hidden = !me.body.network;

  const res = await api("/languages");
  if (res.status !== 200) return showGate(explain(res));
  // Several Judge0 ids can point at one language; show each language once.
  const seen = new Map();
  for (const l of res.body) if (!seen.has(l.key)) seen.set(l.key, l);
  languages = [...seen.values()].sort((a, b) => a.name.localeCompare(b.name));
  const sel = $("lang");
  sel.replaceChildren();
  for (const l of languages) {
    const o = document.createElement("option");
    o.value = l.key;
    o.textContent = l.name;
    sel.append(o);
  }
  const last = localStorage.getItem("judge.lang");
  sel.value = languages.some((l) => l.key === last) ? last : (languages.find((l) => l.key === "python") || languages[0]).key;
  loadSample();
  $("app").hidden = false;
  $("allcard").hidden = false;
}

function showGate(message) {
  $("app").hidden = true;
  $("allcard").hidden = true;
  $("gate").hidden = false;
  $("signout").hidden = true;
  $("who").textContent = "";
  $("gateerr").textContent = message;
}

function loadSample() {
  const s = sampleFor($("lang").value);
  if (!s) return;
  $("code").value = s.code;
  $("stdin").value = s.stdin;
  $("expected").value = s.expected;
  $("stdinlabel").textContent = s.sql ? "Setup SQL (tables and rows)" : "Standard input";
  $("codelabel").textContent = s.sql ? "Query" : "Source code";
}

function submission(key, code, stdin, expected) {
  const body = {
    language_id: languages.find((l) => l.key === key).id,
    source_code: code,
    stdin,
    cpu_time_limit: Number($("cpu").value),
    memory_limit: Number($("mem").value),
  };
  if (expected !== "") body.expected_output = expected;
  return body;
}

// Only a single run can ask for network access; "Run all" never does.
function withNetwork(body) {
  if (!$("netbox").hidden && $("net").value === "1") body.enable_network = true;
  return body;
}

async function run() {
  const btn = $("run");
  btn.disabled = true;
  setVerdict($("vdot"), $("vtext"), "idle", "Running...");
  $("vmsg").textContent = "";
  const t0 = performance.now();
  let res;
  try {
    res = await api("/submissions?wait=true&fields=*", {
      method: "POST",
      body: JSON.stringify(withNetwork(submission($("lang").value, $("code").value, $("stdin").value, $("expected").value))),
    });
  } catch (e) {
    res = { status: 0, body: { error: "Could not reach the judge." }, timing: "" };
  }
  const trip = performance.now() - t0;
  btn.disabled = false;

  if (res.status !== 201) {
    setVerdict($("vdot"), $("vtext"), "critical", "Not run");
    $("vmsg").textContent = explain(res);
    for (const id of ["ftime", "fmem", "fserver"]) $(id).textContent = "-";
    $("ftrip").textContent = fmtMs(trip);
    $("stdout").textContent = "";
    $("stderrbox").hidden = $("compilebox").hidden = true;
    if (res.status === 401) showGate(explain(res));
    return;
  }
  const r = res.body;
  setVerdict($("vdot"), $("vtext"), toneOf(r.status.id), r.status.description);
  $("vmsg").textContent = r.message || "";
  $("ftime").textContent = fmtRun(r.time);
  $("fmem").textContent = fmtMem(r.memory);
  $("ftrip").textContent = fmtMs(trip);
  $("fserver").textContent = serverMs(res.timing);
  $("stdout").textContent = r.stdout || "";
  $("stderr").textContent = r.stderr || "";
  $("stderrbox").hidden = !r.stderr;
  $("compile").textContent = r.compile_output || "";
  $("compilebox").hidden = !r.compile_output;
}

async function runAll() {
  const btn = $("runall");
  btn.disabled = true;
  const tbody = $("alltable").tBodies[0];
  tbody.replaceChildren();
  $("alltable").hidden = false;
  const rows = languages.filter((l) => sampleFor(l.key)).map((l) => {
    const tr = tbody.insertRow();
    tr.insertCell().textContent = l.name;
    const result = tr.insertCell();
    const dot = document.createElement("span");
    const text = document.createElement("span");
    const pill = document.createElement("span");
    pill.className = "pill";
    pill.append(dot, text);
    result.append(pill);
    setVerdict(dot, text, "idle", "Waiting");
    const cells = [tr.insertCell(), tr.insertCell(), tr.insertCell()];
    cells.forEach((c) => { c.className = "num"; c.textContent = "-"; });
    return { l, dot, text, cells };
  });
  // Three at a time: enough to be quick, not enough to look like a burst.
  let next = 0;
  async function worker() {
    while (next < rows.length) {
      const row = rows[next++];
      const s = sampleFor(row.l.key);
      setVerdict(row.dot, row.text, "idle", "Running...");
      const t0 = performance.now();
      let res;
      try {
        res = await api("/submissions?wait=true&fields=*", { method: "POST", body: JSON.stringify(submission(row.l.key, s.code, s.stdin, s.expected)) });
      } catch (e) {
        res = { status: 0, body: { error: "Could not reach the judge." } };
      }
      row.cells[2].textContent = fmtMs(performance.now() - t0);
      if (res.status !== 201) {
        setVerdict(row.dot, row.text, "critical", explain(res));
        continue;
      }
      setVerdict(row.dot, row.text, toneOf(res.body.status.id), res.body.status.description);
      row.cells[0].textContent = fmtRun(res.body.time);
      row.cells[1].textContent = fmtMem(res.body.memory);
    }
  }
  await Promise.all([worker(), worker(), worker()]);
  btn.disabled = false;
}

$("savetoken").addEventListener("click", () => {
  const v = $("token").value.trim();
  if (!v) return;
  localStorage.setItem(KEY, v);
  $("token").value = "";
  start();
});
$("token").addEventListener("keydown", (e) => { if (e.key === "Enter") $("savetoken").click(); });
$("signout").addEventListener("click", () => { localStorage.removeItem(KEY); showGate(""); });
$("lang").addEventListener("change", () => { localStorage.setItem("judge.lang", $("lang").value); loadSample(); });
$("sample").addEventListener("click", loadSample);
$("run").addEventListener("click", run);
$("runall").addEventListener("click", runAll);
document.addEventListener("keydown", (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key === "Enter" && !$("app").hidden && !$("run").disabled) run();
});
// Tab indents in the editor instead of leaving it.
$("code").addEventListener("keydown", (e) => {
  if (e.key !== "Tab" || e.shiftKey || e.ctrlKey || e.metaKey || e.altKey) return;
  e.preventDefault();
  const t = e.target, at = t.selectionStart;
  t.setRangeText("    ", at, t.selectionEnd, "end");
});

start();
