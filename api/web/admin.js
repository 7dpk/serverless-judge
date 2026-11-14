// Admin page: the analytics dashboard and temporary tokens.
//
// The dashboard is drawn from hourly or daily counters the judge keeps after
// the submissions themselves are deleted. Every label that comes from the
// server is set with textContent.
"use strict";

const $ = (id) => document.getElementById(id);
const KEY = "judge.token";
const token = () => localStorage.getItem(KEY) || "";
const SVG = "http://www.w3.org/2000/svg";

async function api(path, options = {}) {
  const headers = Object.assign({ "X-Auth-Token": token() }, options.headers || {});
  if (options.body) headers["Content-Type"] = "application/json";
  const res = await fetch(path, Object.assign({}, options, { headers }));
  const text = await res.text();
  let body = null;
  try { body = text ? JSON.parse(text) : null; } catch (e) { body = null; }
  return { status: res.status, body };
}

function h(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === "class") e.className = v;
    else if (k === "text") e.textContent = v;
    else e.setAttribute(k, v);
  }
  e.append(...kids);
  return e;
}
function s(tag, attrs = {}, text) {
  const e = document.createElementNS(SVG, tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  if (text != null) e.textContent = text;
  return e;
}

const num = (n) => Math.round(n).toLocaleString();
const pct = (a, b) => (b ? (100 * a / b).toFixed(a === b || a === 0 ? 0 : 1) + "%" : "-");
function dur(ms) {
  if (ms == null || isNaN(ms)) return "-";
  if (ms < 1000) return Math.round(ms) + " ms";
  if (ms < 60000) return (ms / 1000).toFixed(ms < 10000 ? 2 : 1) + " s";
  return (ms / 60000).toFixed(1) + " min";
}
const mem = (kb) => (kb == null || isNaN(kb) ? "-" : kb < 1024 ? Math.round(kb) + " KB" : (kb / 1024).toFixed(1) + " MB");

// ---------- data ----------

let data = null; // last /admin/stats answer

function emptyTally(bounds) {
  return { n: 0, ran: 0, sync: 0, warm: 0, lat: 0, cpu: 0, mem: 0, s: {}, h: new Array(bounds.length + 1).fill(0) };
}
function add(t, c) {
  t.n += c.n || 0; t.ran += c.ran || 0; t.sync += c.sync || 0; t.warm += c.warm || 0;
  t.lat += c.lat_ms || 0; t.cpu += c.cpu_ms || 0; t.mem += c.mem_kb || 0;
  for (const [k, v] of Object.entries(c.s || {})) t.s[k] = (t.s[k] || 0) + v;
  for (const [k, v] of Object.entries(c.h || {})) t.h[Number(k)] += v;
}
// Totals for one bucket, for one language or for all of them.
function tally(bucket, lang) {
  const t = emptyTally(data.latency_bounds_ms);
  for (const [name, c] of Object.entries(bucket.l || {})) if (!lang || lang === name) add(t, c);
  return t;
}
// A percentile read off the latency histogram: the bucket it falls in, then
// a straight line across that bucket. Good to within a bucket.
function percentile(t, p) {
  if (!t.n) return null;
  const b = data.latency_bounds_ms;
  const want = p * t.n;
  let seen = 0;
  for (let i = 0; i < t.h.length; i++) {
    if (seen + t.h[i] >= want && t.h[i] > 0) {
      const lo = i === 0 ? 0 : b[i - 1];
      const hi = i < b.length ? b[i] : b[b.length - 1] * 2;
      return lo + (hi - lo) * ((want - seen) / t.h[i]);
    }
    seen += t.h[i];
  }
  return b[b.length - 1];
}

const hourly = () => data.step_seconds === 3600;
function when(iso, long) {
  const d = new Date(iso);
  const day = d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  const time = d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  if (!hourly()) return day;
  if (long) return day + ", " + time;
  return data.buckets.length > 48 ? day : time;
}

// ---------- charts ----------

const W = 560, H = 220, M = { l: 46, r: 14, t: 10, b: 24 };

function niceMax(v) {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 1.2, 1.6, 2, 2.4, 3, 4, 5, 6, 8, 10]) if (v <= m * p) return m * p;
  return 10 * p;
}
// For counts, the top of the axis is a value whose quarters are whole
// numbers, so the gridlines never sit on half a submission.
function niceCount(v) {
  for (const m of [4, 8]) if (v <= m) return m;
  for (let p = 10; ; p *= 10) for (const m of [1.2, 1.6, 2, 2.4, 4, 6, 8, 10]) if (v <= m * p) return m * p;
}
const compact = (v) => (v >= 10000 ? (v / 1000).toFixed(v % 1000 ? 1 : 0) + "K" : v.toLocaleString());

// Frame draws the plot area, gridlines, y ticks and x labels shared by both
// charts, and returns the scales.
function frame(svg, max, yLabel, right) {
  const x0 = M.l, x1 = W - (right || M.r), y0 = H - M.b, y1 = M.t;
  const n = data.buckets.length;
  const band = (x1 - x0) / n;
  const y = (v) => y0 - (v / max) * (y0 - y1);
  for (let i = 0; i <= 4; i++) {
    const v = (max * i) / 4;
    svg.append(s("line", { class: i ? "grid" : "base", x1: x0, x2: x1, y1: y(v), y2: y(v) }));
    svg.append(s("text", { x: x0 - 6, y: y(v) + 4, "text-anchor": "end" }, yLabel(v)));
  }
  const every = Math.max(1, Math.round(n / 6));
  for (let i = 0; i < n; i += every) {
    if (i + every / 2 > n) break;
    svg.append(s("text", { x: x0 + band * (i + 0.5), y: H - 6, "text-anchor": "middle" }, when(data.buckets[i].t)));
  }
  return { x0, x1, y0, y1, band, y, cx: (i) => x0 + band * (i + 0.5) };
}

// One tooltip per chart, placed in the chart's own box.
function tooltip(host) {
  const tip = h("div", { class: "tip", hidden: "" });
  host.append(tip);
  return {
    show(svg, xView, rows, title) {
      tip.replaceChildren(h("div", { class: "when", text: title }));
      for (const r of rows) {
        const left = h("span");
        if (r.key) left.append(h("i", { class: "key " + r.key }));
        left.append(r.label);
        // The value leads: the reader already knows which series it is.
        tip.append(h("div", { class: "r" }, h("b", { text: r.value }), left));
      }
      tip.hidden = false;
      const box = svg.getBoundingClientRect();
      const px = (xView / W) * box.width;
      const flip = px > box.width * 0.6;
      tip.style.left = (flip ? px - tip.offsetWidth - 12 : px + 12) + "px";
      tip.style.top = "8px";
    },
    hide() { tip.hidden = true; },
  };
}

function hitLayer(svg, f, onMove, onLeave) {
  const hit = s("rect", { class: "hit", x: f.x0, y: f.y1, width: f.x1 - f.x0, height: f.y0 - f.y1 });
  svg.append(hit);
  // The pointer only has to be over the right column of time, never on a
  // 2px line or a thin bar.
  hit.addEventListener("pointermove", (e) => {
    const box = svg.getBoundingClientRect();
    const xView = ((e.clientX - box.left) / box.width) * W;
    const i = Math.max(0, Math.min(data.buckets.length - 1, Math.floor((xView - f.x0) / f.band)));
    onMove(i);
  });
  hit.addEventListener("pointerleave", onLeave);
}

function drawVolume(lang) {
  const host = $("vol");
  host.replaceChildren();
  const counts = data.buckets.map((b) => tally(b, lang).n);
  $("volTitle").textContent = hourly() ? "Submissions per hour" : "Submissions per day";
  if (!counts.some(Boolean)) return host.append(h("p", { class: "empty", text: "No submissions in this period." }));

  const svg = s("svg", { viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": $("volTitle").textContent });
  const max = niceCount(Math.max(...counts));
  const f = frame(svg, max, (v) => compact(Math.round(v)));
  const bw = Math.max(1, Math.min(24, f.band - 2)); // thin bars, a gap between neighbours
  const bars = counts.map((c, i) => {
    if (!c) return null;
    const top = f.y(c), x = f.cx(i) - bw / 2, r = Math.min(4, bw / 2, f.y0 - top);
    // Rounded at the data end, square on the baseline.
    const d = `M${x},${f.y0} V${top + r} Q${x},${top} ${x + r},${top} H${x + bw - r} Q${x + bw},${top} ${x + bw},${top + r} V${f.y0} Z`;
    const p = s("path", { class: "bar", d });
    svg.append(p);
    return p;
  });
  host.append(svg);
  const tip = tooltip(host);
  let on = null;
  hitLayer(svg, f, (i) => {
    if (on) on.classList.remove("on");
    on = bars[i];
    if (on) on.classList.add("on");
    tip.show(svg, f.cx(i), [{ value: num(counts[i]), label: "submissions" }], when(data.buckets[i].t, true));
  }, () => { if (on) on.classList.remove("on"); tip.hide(); });

  $("volTable").replaceChildren(table(["When", "Submissions"],
    data.buckets.map((b, i) => [when(b.t, true), num(counts[i])]).filter((_, i) => counts[i]), [1]));
}

function drawLatency(lang) {
  const host = $("lat");
  host.replaceChildren();
  const tallies = data.buckets.map((b) => tally(b, lang));
  const series = [
    { name: "Median", line: "l1", mark: "m1", key: "k1", v: tallies.map((t) => percentile(t, 0.5)) },
    { name: "90th percentile", line: "l2", mark: "m2", key: "k2", v: tallies.map((t) => percentile(t, 0.9)) },
  ];
  const all = series.flatMap((q) => q.v).filter((v) => v != null);
  if (!all.length) return host.append(h("p", { class: "empty", text: "No submissions in this period." }));

  // One axis, one unit: both series are the same measure.
  const top = Math.max(...all);
  const unit = top >= 2000 ? 1000 : 1;
  const max = niceMax(top / unit);
  const svg = s("svg", { viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": "Median and 90th percentile time from submit to result" });
  const f = frame(svg, max, (v) => (unit === 1000 ? +v.toFixed(2) + " s" : Math.round(v) + " ms"), 70);
  const yOf = (v) => f.y(v / unit);

  const ends = [];
  for (const q of series) {
    let d = "", last = -1;
    q.v.forEach((v, i) => {
      if (v == null) return;
      const alone = (q.v[i - 1] == null) && (q.v[i + 1] == null);
      if (alone) svg.append(s("circle", { class: q.mark, cx: f.cx(i), cy: yOf(v), r: 4 })); // a point with no neighbours still shows
      d += (q.v[i - 1] == null ? "M" : "L") + f.cx(i) + "," + yOf(v) + " ";
      last = i;
    });
    svg.append(s("path", { class: "line " + q.line, d }));
    if (last >= 0) ends.push({ q, i: last, y: yOf(q.v[last]) });
  }
  // Label each line where it ends, unless the two would collide: then the
  // legend above and the tooltip carry it.
  if (ends.length < 2 || Math.abs(ends[0].y - ends[1].y) >= 14) {
    for (const e of ends) {
      svg.append(s("circle", { class: e.q.mark, cx: f.cx(e.i), cy: e.y, r: 4 }));
      svg.append(s("text", { class: "end", x: Math.min(f.cx(e.i) + 8, W - 64), y: e.y + 4 }, dur(e.q.v[e.i])));
    }
  }
  const cross = s("line", { class: "cross", y1: f.y1, y2: f.y0, visibility: "hidden" });
  const dots = series.map((q) => s("circle", { class: q.mark, r: 4, visibility: "hidden" }));
  svg.append(cross, ...dots);
  host.append(svg);
  const tip = tooltip(host);
  hitLayer(svg, f, (i) => {
    cross.setAttribute("x1", f.cx(i)); cross.setAttribute("x2", f.cx(i)); cross.setAttribute("visibility", "visible");
    series.forEach((q, k) => {
      const v = q.v[i];
      dots[k].setAttribute("visibility", v == null ? "hidden" : "visible");
      if (v != null) { dots[k].setAttribute("cx", f.cx(i)); dots[k].setAttribute("cy", yOf(v)); }
    });
    tip.show(svg, f.cx(i), series.map((q) => ({ value: dur(q.v[i]), label: q.name, key: q.key }))
      .concat([{ value: num(tallies[i].n), label: "submissions" }]), when(data.buckets[i].t, true));
  }, () => { cross.setAttribute("visibility", "hidden"); dots.forEach((d) => d.setAttribute("visibility", "hidden")); tip.hide(); });

  $("latTable").replaceChildren(table(["When", "Submissions", "Median", "90th percentile"],
    data.buckets.map((b, i) => [when(b.t, true), num(tallies[i].n), dur(series[0].v[i]), dur(series[1].v[i])]).filter((_, i) => tallies[i].n), [1, 2, 3]));
}

function table(head, rows, numeric) {
  const t = h("table");
  const tr = t.createTHead().insertRow();
  head.forEach((c, i) => tr.append(h("th", { class: numeric.includes(i) ? "num" : "", text: c })));
  const body = t.createTBody();
  for (const r of rows) {
    const row = body.insertRow();
    r.forEach((c, i) => {
      const td = row.insertCell();
      if (numeric.includes(i)) td.className = "num";
      if (c instanceof Node) td.append(c); else td.textContent = c;
    });
  }
  return t;
}

function tile(label, value, hint) {
  return h("div", { class: "tile" }, h("div", { class: "label", text: label }), h("div", { class: "value", text: value }), h("div", { class: "hint", text: hint || "" }));
}

function render() {
  const lang = $("flang").value;
  const total = emptyTally(data.latency_bounds_ms);
  const perLang = {};
  for (const b of data.buckets) {
    for (const [name, c] of Object.entries(b.l || {})) {
      perLang[name] = perLang[name] || emptyTally(data.latency_bounds_ms);
      add(perLang[name], c);
      if (!lang || lang === name) add(total, c);
    }
  }

  // Keep the language filter in step with what the period actually contains.
  const sel = $("flang"), names = Object.keys(perLang).sort();
  const want = ["", ...names].join("|");
  if (sel.dataset.have !== want) {
    sel.replaceChildren(h("option", { value: "", text: "All languages" }), ...names.map((n) => h("option", { value: n, text: n })));
    sel.value = names.includes(lang) ? lang : "";
    sel.dataset.have = want;
  }

  const failed = total.s["13"] || 0;
  $("tiles").replaceChildren(
    tile("Submissions", num(total.n), total.n ? pct(total.sync, total.n) + " waited for the result" : ""),
    tile("Accepted", pct(total.s["3"] || 0, total.n), num(total.s["3"] || 0) + " of " + num(total.n)),
    tile("Median time to result", dur(percentile(total, 0.5)), "from submit to verdict"),
    tile("90th percentile", dur(percentile(total, 0.9)), "9 in 10 were faster"),
    tile("Average run time", total.ran ? dur(total.cpu / total.ran) : "-", total.ran ? "average memory " + mem(total.mem / total.ran) : ""),
    tile("Judge errors", num(failed), total.n ? pct(failed, total.n) + " of submissions" : ""),
  );

  drawVolume(lang);
  drawLatency(lang);

  // Verdicts: parts of one whole that the reader compares, so ranked bars in
  // one hue with the figure at the tip.
  const v = $("verdicts");
  v.replaceChildren();
  const parts = Object.entries(total.s).filter(([, c]) => c > 0).sort((a, b) => b[1] - a[1]);
  if (!parts.length) v.append(h("p", { class: "empty", text: "No submissions in this period." }));
  else {
    const grid = h("div", { class: "ranked" });
    const top = parts[0][1];
    for (const [id, c] of parts) {
      const fill = h("div", { class: "fill" });
      fill.style.width = (100 * c / top) + "%";
      grid.append(h("div", { text: data.statuses[id] || "Status " + id }), h("div", { class: "track" }, fill), h("div", { class: "v", text: num(c) + " · " + pct(c, total.n) }));
    }
    v.append(grid);
  }

  const rows = Object.entries(perLang).sort((a, b) => b[1].n - a[1].n);
  const most = rows.length ? rows[0][1].n : 1;
  $("bylang").replaceChildren(rows.length ? table(
    ["Language", "Submissions", "", "Accepted", "Median", "90th pct", "Avg run", "Avg memory"],
    rows.map(([name, t]) => {
      const bar = h("span", { class: "share" });
      bar.style.width = Math.max(2, 80 * t.n / most) + "px";
      return [name, num(t.n), bar, pct(t.s["3"] || 0, t.n), dur(percentile(t, 0.5)), dur(percentile(t, 0.9)),
        t.ran ? dur(t.cpu / t.ran) : "-", t.ran ? mem(t.mem / t.ran) : "-"];
    }), [1, 3, 4, 5, 6, 7]) : h("p", { class: "empty", text: "No submissions in this period." }));
}

async function load() {
  // Hold the previous render, dimmed, while the new numbers arrive.
  const parts = ["tiles", "charts", "tables"].map($);
  parts.forEach((p) => p.classList.add("stale"));
  const res = await api("/admin/stats?range=" + encodeURIComponent($("range").value));
  parts.forEach((p) => p.classList.remove("stale"));
  if (res.status !== 200) return showGate(res.status === 403 ? "That key is not an admin key." : "Could not load analytics.");
  data = res.body;
  render();
}

// ---------- tokens ----------

async function loadTokens() {
  const res = await api("/admin/tokens");
  const box = $("tlist");
  if (res.status !== 200) return box.replaceChildren(h("p", { class: "error", text: "Could not load tokens." }));
  if (!res.body.length) return box.replaceChildren(h("p", { class: "empty", text: "No temporary tokens yet." }));
  box.replaceChildren(table(["For", "Created", "Expires", "Used", "Allowed", "State", ""],
    res.body.map((t) => {
      const revoke = h("button", { class: "small", text: "Revoke" });
      revoke.addEventListener("click", async () => {
        revoke.disabled = true;
        await api("/admin/tokens/" + encodeURIComponent(t.id), { method: "DELETE" });
        loadTokens();
      });
      const spent = t.used >= t.max_submissions;
      return [t.label || "(no label)", new Date(t.created_at).toLocaleString(), new Date(t.expires_at).toLocaleString(),
        num(t.used), num(t.max_submissions), t.expired ? "Expired" : spent ? "Used up" : "Active", revoke];
    }), [3, 4]));
}

async function createToken() {
  $("terr").textContent = "";
  const res = await api("/admin/tokens", {
    method: "POST",
    body: JSON.stringify({ label: $("tlabel").value.trim(), ttl_hours: Number($("tttl").value), max_submissions: Number($("tmax").value) }),
  });
  if (res.status !== 201) {
    $("terr").textContent = (res.body && res.body.error) || "Could not create the token.";
    return;
  }
  // After the #, so the token never reaches a server log.
  $("tlink").value = location.origin + "/ui/#token=" + encodeURIComponent(res.body.token);
  $("tvalue").value = res.body.token;
  $("tnew").hidden = false;
  $("tlabel").value = "";
  loadTokens();
}

// ---------- page ----------

const SECTIONS = ["dash", "tiles", "charts", "tables", "tokens"];

function showGate(message) {
  SECTIONS.forEach((id) => { $(id).hidden = true; });
  $("gate").hidden = false;
  $("signout").hidden = true;
  $("who").textContent = "";
  $("gateerr").textContent = message;
}

async function start() {
  if (!token()) return showGate("");
  const me = await api("/whoami");
  if (me.status !== 200) return showGate("That key was not accepted.");
  if (me.body.role !== "admin") return showGate("This page needs an admin key. A temporary token only opens the playground.");
  $("gate").hidden = true;
  $("signout").hidden = false;
  $("who").textContent = "Signed in with an admin key";
  SECTIONS.forEach((id) => { $(id).hidden = false; });
  load();
  loadTokens();
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
$("range").addEventListener("change", load);
$("refresh").addEventListener("click", load);
$("flang").addEventListener("change", () => { if (data) render(); });
$("tcreate").addEventListener("click", createToken);
document.addEventListener("click", (e) => {
  const copy = e.target.closest("[data-copy]");
  if (copy) {
    const input = $(copy.dataset.copy);
    input.select();
    navigator.clipboard.writeText(input.value).then(() => { copy.textContent = "Copied"; setTimeout(() => { copy.textContent = "Copy"; }, 1500); });
  }
  const tog = e.target.closest("[data-table]");
  if (tog) {
    const id = tog.dataset.table, showTable = $(id + "Table").hidden;
    $(id + "Table").hidden = !showTable;
    $(id).hidden = showTable;
    tog.textContent = showTable ? "Show as chart" : "Show as table";
  }
});

start();
