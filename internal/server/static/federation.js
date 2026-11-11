// Confirms risky runs, follows a running phase's log, and reloads when the
// run ends so the page shows the result. Without JavaScript the forms still
// post; reload the page to see progress.
(function () {
  "use strict";
  var n = document.body.dataset.n;

  document.querySelectorAll("form.runform").forEach(function (f) {
    f.addEventListener("submit", function (e) {
      var msg = f.dataset.confirm;
      if (msg && !window.confirm(msg)) { e.preventDefault(); return; }
      var b = f.querySelector("button");
      if (b) { b.disabled = true; b.textContent = "Starting…"; }
    });
  });

  function setProgress(row, p) {
    var box = row && row.querySelector("[data-progress]");
    if (!box) return;
    var pct = p ? p.pct : parseInt(box.dataset.pct || "0", 10);
    var fill = box.querySelector(".pfill");
    if (fill) fill.style.width = Math.max(3, pct) + "%";
    if (p) {
      var label = box.querySelector("[data-plabel]");
      if (label) label.textContent = (p.m ? "Step " + p.n + " of " + p.m + ": " + p.label : "Starting") + " · " + p.elapsed;
    }
  }
  document.querySelectorAll(".phase.s-running").forEach(function (row) { setProgress(row, null); });

  // Follow the log of a running phase; reload once nothing is running.
  var running = document.querySelector(".phase.s-running") || document.body.dataset.busy;
  if (n && running && window.fetch) {
    var timer = setInterval(async function () {
      var res;
      try {
        res = await fetch("/api/state?n=" + encodeURIComponent(n), { credentials: "same-origin", cache: "no-store" });
      } catch (e) { return; }
      if (res.status === 401 || res.status === 403) { clearInterval(timer); return; }
      if (!res.ok) return;
      var s = await res.json();
      var still = false;
      s.phases.forEach(function (p) {
        if (p.status !== "running") return;
        still = true;
        setProgress(document.querySelector('[data-phase="' + p.id + '"]'), p.progress);
        var pre = document.querySelector('[data-phase="' + p.id + '"] [data-log]');
        if (pre && p.log !== undefined) {
          var atEnd = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 30;
          pre.textContent = p.log;
          if (atEnd) pre.scrollTop = pre.scrollHeight;
        }
      });
      if (!still && !s.busy) { clearInterval(timer); location.reload(); }
    }, 2000);
  }

  // The Status view: the links are measured every minute, so follow them.
  if (document.querySelector(".link-cards")) {
    setTimeout(function () { if (!document.hidden) location.reload(); }, 60000);
  }

  // Keep an open log scrolled to its end on load.
  document.querySelectorAll("details.log[open] pre").forEach(function (pre) { pre.scrollTop = pre.scrollHeight; });

  var menu = document.getElementById("fd-menu");
  document.addEventListener("click", function (e) {
    if (menu && menu.open && !menu.contains(e.target)) menu.open = false;
  });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && menu && menu.open) { menu.open = false; menu.querySelector("summary").focus(); }
  });
})();
