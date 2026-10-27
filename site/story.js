// The one drawing, moving through time. The graph is a cluster of chuggy's
// own tickets, and the page's sections are its scenes: entering a scene sets
// the graph's state, and reading forward plays the scene's steps. Scrolling
// back shows where a scene ended, without replaying it.
(function () {
  "use strict";

  // ------------------------------------------------------------ the data --

  // One cluster: the Linux runner on rootless Podman. x, y are in the
  // drawing's units; `waits` are the tickets this one waits on.
  var TICKETS = [
    { n: 402, title: "Detect Podman on the host", x: 60, y: 90, waits: [], runner: "kay-thinkpad", mins: 7, cost: 0.74, commit: "a71e0c" },
    { n: 403, title: "Rootless socket path", x: 60, y: 200, waits: [], runner: "kay-thinkpad", mins: 12, cost: 1.31, commit: "3c9d12" },
    { n: 405, title: "Image pull through Podman", x: 60, y: 310, waits: [], runner: "build-a", mins: 18, cost: 2.06, commit: "e02b77" },
    { n: 407, title: "Run a job as uid 1000", x: 280, y: 90, waits: [402, 403], runner: "geoff-mbp", mins: 9, cost: 0.98, commit: "58f1a4" },
    { n: 409, title: "Drop every capability", x: 280, y: 200, waits: [403, 405], runner: "geoff-mbp", mins: 11, cost: 1.84, commit: "a3f91c" },
    { n: 411, title: "Mount the credential read-only", x: 280, y: 310, waits: [403], runner: "build-a", mins: 14, cost: 1.52, commit: "90be3d" },
    { n: 412, title: "Scrub secrets from the log", x: 500, y: 60, waits: [409, 411], runner: "geoff-mbp", mins: 6, cost: 0.93, commit: "c4d0e8" },
    { n: 414, title: "Save the job's log on exit", x: 500, y: 150, waits: [407, 409], runner: "build-a", mins: 8, cost: 0.88, commit: "1b7f55" },
    { n: 415, title: "systemd user service", x: 500, y: 250, waits: [407], runner: "kay-thinkpad", mins: 23, cost: 2.71, commit: "7e2a90" },
    { n: 416, title: "Build cache volume", x: 500, y: 340, waits: [411], runner: "build-a", mins: 15, cost: 1.66, commit: "d8c413" },
    { n: 418, title: "Runner README: Podman", x: 720, y: 150, waits: [415, 416], runner: "build-a", mins: 5, cost: 0.41, commit: "6f0a2b" },
    { n: 419, title: "Trace replay for Podman jobs", x: 720, y: 270, waits: [412, 414], runner: "us-east", mins: 31, cost: 3.9, commit: "b5e7c1" },
    { n: 421, title: "Release chuggy-linux 0.9", x: 940, y: 210, waits: [415, 416, 418, 419], runner: "build-a", mins: 4, cost: 0.37, commit: "" }
  ];

  // The machines under the graph: a member's own, the project's pool, and
  // the cloud. A pool also runs the project's lead and its threads.
  var byN = {};
  TICKETS.forEach(function (t) { byN[t.n] = t; });

  var MACHINES = [
    { id: "mbp", name: "geoff-mbp", kind: "A member's laptop", x: 60, slots: 1 },
    { id: "kay", name: "kay-thinkpad", kind: "A member's laptop", x: 200, slots: 1 },
    { id: "pool", name: "build-a", kind: "The project's pool", x: 360, slots: 4, fixed: { 0: "lead", 1: "thread" } },
    { id: "cloud", name: "us-east", kind: "Cloud, on demand", x: 650, slots: 5, dotted: true }
  ];

  var BASE = {
    t: {
      402: { s: "landed" }, 403: { s: "landed" }, 405: { s: "landed" },
      407: { s: "landed" }, 409: { s: "working" }, 411: { s: "landed" },
      412: { s: "waiting" }, 414: { s: "waiting" }, 415: { s: "working" }, 416: { s: "waiting" },
      418: { s: "absent" }, 419: { s: "waiting" }, 421: { s: "waiting" }
    },
    caption: "chuggy's own graph: the Linux runner on rootless Podman, the week of October 20."
  };

  // A scene is what a section of the page sets. `set` applies on entering;
  // each step applies `at` milliseconds later when the reader arrived from
  // above. The scene's end state is all of them together.
  var SCENES = [
    { id: "hero", set: {} },
    { id: "why", set: { zoom: true, caption: null } },
    {
      id: "graph",
      set: { zoom: false, t: { 418: { s: "draft" } }, caption: "#418 is drafted in a thread. It waits on #415 and #416, so it is placed by them." },
      steps: [{ at: 1500, set: { t: { 418: { s: "waiting" } }, caption: "#418, released on a word. Nothing starts before what it waits on has landed." } }]
    },
    {
      id: "lead",
      set: { t: { 416: { s: "held" } }, note: true, caption: "The lead holds #416, and the reason is written down." }
    },
    {
      id: "verify",
      set: { note: false, t: { 409: { s: "working", stage: "work · geoff-mbp" } }, caption: "#409 through its work, its checks, an independent review, and a person's yes." },
      steps: [
        { at: 1000, set: { t: { 409: { stage: "checks · go test failed" } } } },
        { at: 2000, set: { t: { 409: { stage: "checks · retried, passed" } } } },
        { at: 2900, set: { t: { 409: { stage: "review · approved" } } } },
        { at: 3700, set: { t: { 409: { stage: "approval · Geoff" } } } },
        { at: 4500, set: { t: { 409: { s: "landed", stage: "" }, 412: { s: "working" } }, caption: "#409 landed. #412 waited on it, and now it may start." } }
      ]
    },
    {
      id: "fleet",
      set: { plane: true, t: { 412: { slot: "mbp:0" }, 415: { slot: "kay:0" } }, caption: "Work runs on machines you control, under your own model accounts." },
      steps: [
        { at: 1200, set: { t: { 414: { s: "working", slot: "pool:2" } }, caption: "A laptop is full, so #414 goes to the pool. The pool also runs the lead and the threads." } },
        { at: 2600, set: { t: { 415: { s: "checking", slot: "" }, 415.1: { slot: "cloud:0", label: "#415 checks" }, 415.2: { slot: "cloud:1", label: "#415 review" } }, caption: "#415's work is done. Its checks and its review are jobs too, and the pool is busy, so they run in the cloud." } }
      ]
    },
    {
      id: "path",
      set: { plane: false, dim: true, record: 1, lit: 1, caption: "One ticket's record. Every ticket keeps one: what ran, where, what it cost, and who said yes." },
      steps: [
        { at: 600, set: { record: 2, lit: 2 } },
        { at: 1200, set: { record: 3, lit: 3 } },
        { at: 1800, set: { record: 4, lit: 4 } },
        { at: 2400, set: { record: 5, lit: 4 } },
        { at: 3000, set: { record: 6, lit: 5 } },
        { at: 3600, set: { record: 7, lit: 6 } }
      ]
    },
    {
      id: "proof",
      set: {
        dim: false, record: 0, lit: 0,
        t: { 412: { s: "landed", slot: "" }, 414: { s: "landed", slot: "" }, 415: { s: "landed" }, 416: { s: "landed" }, 418: { s: "landed" }, 419: { s: "landed" }, 421: { s: "working" }, 415.1: { slot: "" }, 415.2: { slot: "" } },
        caption: "Friday. The release is the last ticket in the cluster, and it is running. This graph is ours."
      }
    }
  ];

  var RECORD = [
    ["Draft", "Tue 10:02", "written in a thread, from chat"],
    ["Released", "Tue 10:05", "by Geoff"],
    ["Work", "Tue 10:06", "geoff-mbp · 11 min · $1.84"],
    ["Checks", "Tue 10:18", "go test: one failure, retried, passed"],
    ["Review", "Tue 10:31", "reviewing agent: approved"],
    ["Approval", "Tue 11:40", "Geoff"],
    ["Landed", "Tue 11:41", "a3f91c on main"]
  ];

  // ----------------------------------------------------------- drawing --

  function esc(s) { return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/"/g, "&quot;"); }

  function edgePath(a, b) {
    var mx = (a.x + 9 + b.x - 9) / 2;
    return "M" + (a.x + 9) + "," + a.y + " C" + mx + "," + a.y + " " + mx + "," + b.y + " " + (b.x - 9) + "," + b.y;
  }

  // The rest of the project, around the cluster, for the pulled-back view:
  // clusters of tickets, each with its own goal and its own frontier, with
  // a few edges crossing between them and into ours.
  var GOALS = [
    "macOS runner under launchd", "Windows runner", "The lead's inbox", "The decision log",
    "Trace replay", "Build caches", "Docker in a job", "Threads: drafts from chat", "Rulesets on GitHub",
    "Approval by a person", "Credential scrubbing", "The invite flow", "Sizing a stage", "llms.txt and the docs",
    "Personal runners", "Shared pools", "Bedrock and Vertex", "Codex login", "Landing as a pull request",
    "Parked tickets", "The Runners page", "Job logs", "The project's image", "Workspace administration",
    "Retired bindings", "Evaluator tasks", "chug.gy, the site", "The fabric", "The formal model",
    "The ticket's brief", "Refusals", "Cost per attempt"
  ];
  var HERO_SCALE = 0.34, HERO_ORIGIN = { x: 500, y: 205 };
  function heroPoint(t) { return { x: HERO_ORIGIN.x + (t.x - HERO_ORIGIN.x) * HERO_SCALE, y: HERO_ORIGIN.y + (t.y - HERO_ORIGIN.y) * HERO_SCALE }; }

  var FIELD = (function () {
    var seed = 11;
    function rnd() { seed = (seed * 1664525 + 1013904223) >>> 0; return seed / 4294967296; }
    function pick(list) { return list[Math.floor(rnd() * list.length)]; }
    var COLS = 6, ROWS = 5, CW = 1000 / COLS, CH = 540 / ROWS;
    var nodes = [], edges = [], clusters = [], cells = {};
    var goal = 0;
    for (var r = 0; r < ROWS; r++) {
      for (var c = 0; c < COLS; c++) {
        if ((c === 2 || c === 3) && (r === 1 || r === 2)) continue; // where our cluster sits
        var x0 = c * CW + 16, y0 = r * CH + 10, k = 3 + Math.floor(rnd() * 3), gap = (CW - 36) / (k - 1);
        var cluster = { name: GOALS[goal++ % GOALS.length], cols: [], x: x0, y: y0 + CH - 16, cell: [c, r] };
        var progress = rnd() < 0.15 ? 1 : rnd() < 0.1 ? 0 : 0.2 + rnd() * 0.75;
        for (var i = 0; i < k; i++) {
          var n = 1 + Math.floor(rnd() * 3), col = [];
          for (var j = 0; j < n; j++) {
            var node = { x: x0 + i * gap, y: y0 + 14 + (CH - 52) / 2 + (j - (n - 1) / 2) * 24, waits: [], cluster: cluster };
            var frontier = Math.floor(progress * k);
            node.s = i < frontier ? "landed" : i === frontier && progress < 1 ? (rnd() < 0.55 ? "working" : "waiting") : "waiting";
            if (progress >= 1) node.s = "landed";
            if (i > 0) {
              var prev = cluster.cols[i - 1], m = 1 + (rnd() < 0.4 ? 1 : 0);
              for (var q = 0; q < m; q++) { var w = pick(prev); if (node.waits.indexOf(w) < 0) node.waits.push(w); }
            }
            col.push(node); nodes.push(node);
          }
          cluster.cols.push(col);
        }
        clusters.push(cluster); cells[c + "," + r] = cluster;
      }
    }
    nodes.forEach(function (node) { node.waits.forEach(function (w) { edges.push({ from: w, to: node }); }); });
    // Between clusters: a few edges from one goal's end to the next one's start, and into ours.
    var heroRoots = [402, 403, 405].map(function (n) { return heroPoint(byN[n]); });
    var heroEnd = heroPoint(byN[421]);
    clusters.forEach(function (a) {
      if (rnd() > 0.5) return;
      var from = pick(a.cols[a.cols.length - 1]), c = a.cell[0] + 1, r = a.cell[1];
      if (c >= COLS) return;
      var b = cells[c + "," + r];
      if (!b) { var root = pick(heroRoots); edges.push({ from: from, to: { x: root.x, y: root.y, s: "waiting", hero: true }, cross: true }); return; }
      edges.push({ from: from, to: pick(b.cols[0]), cross: true });
    });
    var after = cells["4,2"] || cells["4,1"];
    if (after) edges.push({ from: { x: heroEnd.x, y: heroEnd.y, s: "waiting", hero: true }, to: pick(after.cols[0]), cross: true });
    return { nodes: nodes, edges: edges, clusters: clusters };
  })();

  function fieldMarkup() {
    var out = "";
    FIELD.edges.forEach(function (e, i) {
      e.i = i;
      out += '<path class="fedge' + (e.cross ? " fedge-cross" : "") + (e.from.s === "landed" ? " fedge-done" : "") + '" data-fe="' + i + '" d="' + edgePath({ x: e.from.x - 7, y: e.from.y }, { x: e.to.x + 7, y: e.to.y }) + '"/>';
    });
    FIELD.nodes.forEach(function (n, i) {
      n.i = i;
      out += '<circle class="fnode fnode-' + n.s + '" data-fn="' + i + '" cx="' + n.x.toFixed(1) + '" cy="' + n.y.toFixed(1) + '" r="3"/>';
    });
    FIELD.clusters.forEach(function (c) {
      out += '<text class="fgoal" x="' + c.x + '" y="' + c.y + '">' + esc(c.name) + "</text>";
    });
    var hp = heroPoint({ x: 60, y: 340 });
    out += '<text class="fgoal fgoal-ours" x="' + hp.x.toFixed(1) + '" y="' + (hp.y + 30).toFixed(1) + '">Linux runner on rootless Podman</text>';
    return '<g class="field">' + out + "</g>";
  }
  function field() { return fieldMarkup(); }

  // While the reader looks at the whole, the fleet keeps working: a working
  // ticket lands, and a ticket whose waits have all landed starts.
  var fieldTimer = null;
  function fieldTick() {
    var working = FIELD.nodes.filter(function (n) { return n.s === "working"; });
    var landing = working.slice().sort(function () { return Math.random() - 0.5; }).slice(0, 1 + Math.floor(Math.random() * 2));
    landing.forEach(function (n) { n.s = "landed"; paintField(n); });
    var ready = FIELD.nodes.filter(function (n) {
      return n.s === "waiting" && n.waits.every(function (w) { return w.s === "landed"; });
    }).sort(function () { return Math.random() - 0.5; }).slice(0, 1 + Math.floor(Math.random() * 2));
    ready.forEach(function (n) { n.s = "working"; paintField(n); });
  }
  function paintField(n) {
    var el = svg.querySelector('[data-fn="' + n.i + '"]');
    if (el) el.setAttribute("class", "fnode fnode-" + n.s);
    FIELD.edges.forEach(function (e) {
      if (e.from !== n) return;
      var p = svg.querySelector('[data-fe="' + e.i + '"]');
      if (p) p.setAttribute("class", "fedge" + (e.cross ? " fedge-cross" : "") + (n.s === "landed" ? " fedge-done" : ""));
    });
  }
  function fieldLife(on) {
    if (fieldTimer) { window.clearInterval(fieldTimer); fieldTimer = null; }
    if (on && !reduced) fieldTimer = window.setInterval(fieldTick, 900);
  }

  // The pulled-back scene is captioned by what the drawing counts.
  (function () {
    var working = FIELD.nodes.filter(function (n) { return n.s === "working"; }).length + 2;
    SCENES[1].set.caption = "The same cluster in the whole of the project: " + (FIELD.clusters.length + 1) + " goals, " +
      (FIELD.nodes.length + TICKETS.length) + " tickets, " + working + " of them in progress. One graph, and every ticket in it says what it waits on.";
  })();


  // A slot's label: one line, or two when it names a stage as well.
  function slotText(text, label) {
    var parts = label.split(" ");
    if (parts.length < 2) { text.textContent = label; text.setAttribute("y", "19"); return; }
    text.textContent = "";
    text.setAttribute("y", "13");
    parts.forEach(function (part, i) {
      var span = document.createElementNS("http://www.w3.org/2000/svg", "tspan");
      span.setAttribute("x", "28");
      if (i) span.setAttribute("dy", "11");
      span.textContent = part;
      text.appendChild(span);
    });
  }

  function slotPos(ref) {
    var parts = ref.split(":"), m = null;
    MACHINES.forEach(function (x) { if (x.id === parts[0]) m = x; });
    if (!m) return null;
    return { x: m.x + 28 + parseInt(parts[1], 10) * 62, y: 476 };
  }

  function plane() {
    var out = '<g class="plane"><line class="plane-rule" x1="60" y1="418" x2="940" y2="418"/><text class="plane-title" x="60" y="410">Runners</text>';
    MACHINES.forEach(function (m) {
      out += '<g class="machine"><text class="machine-name" x="' + m.x + '" y="448">' + esc(m.name) + '</text>';
      out += '<text class="machine-kind" x="' + m.x + '" y="520">' + esc(m.kind) + "</text>";
      for (var i = 0; i < m.slots; i++) {
        var fixed = m.fixed && m.fixed[i];
        out += '<g class="slot' + (fixed ? " slot-fixed" : "") + (m.dotted ? " slot-dotted" : "") + '" data-slot="' + m.id + ":" + i + '" transform="translate(' + (m.x + i * 62) + ',462)">' +
          '<rect width="56" height="30" rx="3"/><text x="28" y="19">' + (fixed || "") + "</text></g>";
      }
      out += "</g>";
    });
    return out + "</g>";
  }

  function record() {
    var out = '<g class="record"><rect class="record-card" x="150" y="30" width="700" height="330" rx="6"/>' +
      '<text class="record-title" x="180" y="68"><tspan class="mono">#409</tspan> Drop every capability</text>';
    RECORD.forEach(function (row, i) {
      var y = 108 + i * 34;
      out += '<g class="record-row" data-row="' + (i + 1) + '"><circle cx="186" cy="' + (y - 5) + '" r="4"/>' +
        '<text class="record-stage" x="204" y="' + y + '">' + esc(row[0]) + '</text>' +
        '<text class="record-when mono" x="330" y="' + y + '">' + esc(row[1]) + '</text>' +
        '<text class="record-what" x="440" y="' + y + '">' + esc(row[2]) + "</text></g>";
    });
    return out + "</g>";
  }

  function drawing() {
    var edges = "", nodes = "";
    TICKETS.forEach(function (t, i) {
      t.waits.forEach(function (w) {
        edges += '<path class="edge" data-from="' + w + '" data-to="' + t.n + '" d="' + edgePath(byN[w], t) + '"/>';
      });
      nodes += '<g class="node" data-n="' + t.n + '" data-i="' + i + '" tabindex="0" transform="translate(' + t.x + "," + t.y + ')">' +
        '<circle r="8"/><text class="num" x="0" y="-16">#' + t.n + '</text>' +
        '<text class="title" x="0" y="26">' + esc(t.title) + '</text>' +
        '<text class="stage" x="0" y="42"></text></g>';
    });
    var note = '<g class="note"><rect x="536" y="322" width="330" height="66" rx="4"/>' +
      '<text x="552" y="344" class="note-head">Held by the lead</text>' +
      '<text x="552" y="364">#416 changes the runner’s config while #415, in flight,</text>' +
      '<text x="552" y="380">has it open. It runs when #415 lands.</text></g>';
    return '<svg class="graph-svg intro" viewBox="0 0 1000 540" role="img" aria-labelledby="graph-title graph-desc">' +
      '<title id="graph-title">A cluster of chuggy’s own work graph</title>' +
      '<desc id="graph-desc">Thirteen tickets for the Linux runner, arranged left to right by what they wait on, and the machines their jobs run on.</desc>' +
      field() + '<g class="cluster"><g class="jobs"></g><g class="graph-edges">' + edges + '</g><g class="graph-nodes">' + nodes + "</g>" + note + "</g>" +
      plane() + record() + "</svg>";
  }

  // ------------------------------------------------------------- states --

  function merge(state, patch) {
    var out = {};
    Object.keys(state).forEach(function (k) { out[k] = state[k]; });
    Object.keys(patch || {}).forEach(function (k) {
      if (k === "t") {
        var t = {};
        Object.keys(state.t || {}).forEach(function (n) { t[n] = state.t[n]; });
        Object.keys(patch.t).forEach(function (n) {
          var cur = t[n] || {}, next = {};
          Object.keys(cur).forEach(function (f) { next[f] = cur[f]; });
          Object.keys(patch.t[n]).forEach(function (f) { next[f] = patch.t[n][f]; });
          t[n] = next;
        });
        out.t = t;
      } else {
        out[k] = patch[k];
      }
    });
    return out;
  }

  // The state each scene ends in, folded from the base.
  var ENDS = [];
  SCENES.reduce(function (prev, scene) {
    var end = merge(prev, scene.set);
    (scene.steps || []).forEach(function (step) { end = merge(end, step.set); });
    ENDS.push(end);
    return end;
  }, BASE);

  function startOf(i) { return i === 0 ? merge(BASE, SCENES[0].set) : merge(ENDS[i - 1], SCENES[i].set); }

  // ------------------------------------------------------------ render --

  var figure = document.querySelector(".graph");
  if (!figure || !("IntersectionObserver" in window)) return;
  var reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  figure.querySelector(".graph-svg").outerHTML = drawing();
  var svg = figure.querySelector(".graph-svg");
  var captionEl = figure.querySelector(".graph-caption");
  var stations = document.querySelectorAll(".stations li");
  var jobsEl = svg.querySelector(".jobs");
  var nodeEls = {}, edgeEls = [], slotEls = {};
  svg.querySelectorAll(".node").forEach(function (el) { nodeEls[el.getAttribute("data-n")] = el; });
  svg.querySelectorAll(".edge").forEach(function (el) { edgeEls.push(el); });
  svg.querySelectorAll(".slot").forEach(function (el) { slotEls[el.getAttribute("data-slot")] = el; });
  window.setTimeout(function () { svg.classList.remove("intro"); }, 2400);

  var current = BASE;

  function render(state) {
    current = state;
    var t = state.t;
    Object.keys(nodeEls).forEach(function (n) {
      var el = nodeEls[n], s = t[n] ? t[n].s : "waiting";
      el.setAttribute("class", "node node-" + s);
      el.querySelector(".stage").textContent = (t[n] && t[n].stage) || "";
    });
    edgeEls.forEach(function (el) {
      var from = t[el.getAttribute("data-from")], to = t[el.getAttribute("data-to")];
      var hidden = (from && from.s === "absent") || (to && to.s === "absent");
      el.setAttribute("class", "edge" + (from && from.s === "landed" ? " edge-done" : "") + (hidden ? " edge-hidden" : ""));
    });
    // Jobs: a line from a working ticket to the slot it runs in.
    var taken = {};
    Object.keys(t).forEach(function (n) {
      var slot = t[n].slot;
      if (!slot) return;
      taken[slot] = t[n].label || "#" + Math.floor(n);
      var ticket = byN[Math.floor(n)], p = slotPos(slot), line = jobsEl.querySelector('[data-job="' + n + '"]');
      if (!line) {
        line = document.createElementNS("http://www.w3.org/2000/svg", "path");
        line.setAttribute("data-job", n);
        jobsEl.appendChild(line);
      }
      var mx = (ticket.x + p.x) / 2;
      line.setAttribute("d", "M" + ticket.x + "," + (ticket.y + 9) + " C" + ticket.x + "," + (ticket.y + 110) + " " + p.x + "," + (p.y - 120) + " " + p.x + "," + (p.y - 16));
      line.setAttribute("class", "job" + (state.plane ? " job-on" : ""));
    });
    jobsEl.querySelectorAll(".job").forEach(function (line) {
      var n = line.getAttribute("data-job");
      if (!t[n] || !t[n].slot) line.setAttribute("class", "job");
    });
    Object.keys(slotEls).forEach(function (ref) {
      var el = slotEls[ref], label = taken[ref];
      el.classList.toggle("slot-on", !!label);
      if (!el.classList.contains("slot-fixed")) slotText(el.querySelector("text"), label || "");
    });
    svg.classList.toggle("zoomed", !!state.zoom);
    fieldLife(!!state.zoom);
    figure.classList.toggle("compact", !state.zoom && !state.plane);
    svg.classList.toggle("plane-on", !!state.plane);
    svg.classList.toggle("note-on", !!state.note);
    svg.classList.toggle("dim", !!state.dim);
    svg.classList.toggle("record-on", !!state.record);
    svg.querySelectorAll(".record-row").forEach(function (row) {
      row.classList.toggle("row-on", parseInt(row.getAttribute("data-row"), 10) <= (state.record || 0));
    });
    stations.forEach(function (li, i) { li.classList.toggle("lit", i < (state.lit || 0)); });
    if (captionEl && captionEl.textContent !== state.caption) {
      captionEl.classList.add("caption-out");
      window.setTimeout(function () { captionEl.textContent = state.caption; captionEl.classList.remove("caption-out"); }, reduced ? 0 : 180);
    }
  }

  // ------------------------------------------------------------ scenes --

  var timers = [], active = -1;

  function enter(i, forward) {
    timers.forEach(window.clearTimeout);
    timers = [];
    active = i;
    var scene = SCENES[i];
    if (!forward || reduced || !scene.steps) { render(ENDS[i]); return; }
    render(startOf(i));
    scene.steps.forEach(function (step) {
      timers.push(window.setTimeout(function () { render(merge(current, step.set)); }, step.at));
    });
  }

  var sceneEls = {};
  document.querySelectorAll("[data-scene]").forEach(function (el) { sceneEls[el.getAttribute("data-scene")] = el; });
  var observer = new IntersectionObserver(function (entries) {
    entries.forEach(function (entry) {
      if (!entry.isIntersecting) return;
      var id = entry.target.getAttribute("data-scene"), i = -1;
      SCENES.forEach(function (s, k) { if (s.id === id) i = k; });
      if (i >= 0 && i !== active) enter(i, i > active);
    });
  }, { rootMargin: "-40% 0px -55% 0px", threshold: 0 });
  SCENES.forEach(function (s) { if (sceneEls[s.id]) observer.observe(sceneEls[s.id]); });
  render(ENDS[0]);
  active = 0;

  // The same scenes on a clock, for a recording: story.play() in the console.
  window.story = {
    go: function (i) { enter(i, true); },
    play: function (hold) {
      var i = 0, step = function () {
        if (i >= SCENES.length) return;
        enter(i, true);
        var steps = SCENES[i].steps, last = steps ? steps[steps.length - 1].at : 0;
        i++;
        window.setTimeout(step, last + (hold || 2600));
      };
      step();
    }
  };

  // --------------------------------------------------------- the card --

  var card = document.createElement("div");
  card.className = "ticket-card";
  card.setAttribute("aria-hidden", "true");
  figure.appendChild(card);

  function describe(t) {
    var st = current.t[t.n] || {}, s = st.s;
    var waits = t.waits.map(function (w) { return "#" + w; }).join(", ");
    if (s === "landed") return "Landed " + t.commit + " · " + t.runner + " · " + t.mins + " min · $" + t.cost.toFixed(2);
    if (s === "working") return "Working on " + (st.slot ? slotName(st.slot) : t.runner) + " · " + Math.max(1, Math.round(t.mins / 2)) + " min so far";
    if (s === "checking") return "Checks and review running · work took " + t.mins + " min";
    if (s === "held") return "Held by the lead · would conflict with #415";
    if (s === "draft") return "Draft · not yet released";
    return waits ? "Waits on " + waits : "Ready to run";
  }
  function slotName(ref) {
    var id = ref.split(":")[0], name = ref;
    MACHINES.forEach(function (m) { if (m.id === id) name = m.name; });
    return name;
  }
  function show(el) {
    var t = byN[el.getAttribute("data-n")];
    card.innerHTML = '<b class="mono">#' + t.n + "</b> " + esc(t.title) + '<span>' + esc(describe(t)) + "</span>";
    var r = el.querySelector("circle").getBoundingClientRect(), f = figure.getBoundingClientRect();
    card.style.left = (r.left + r.width / 2 - f.left) + "px";
    card.style.top = (r.top - f.top - 10) + "px";
    card.classList.add("card-on");
  }
  function hide() { card.classList.remove("card-on"); }
  Object.keys(nodeEls).forEach(function (n) {
    var el = nodeEls[n];
    el.addEventListener("mouseenter", function () { show(el); });
    el.addEventListener("focus", function () { show(el); });
    el.addEventListener("mouseleave", hide);
    el.addEventListener("blur", hide);
  });
})();
