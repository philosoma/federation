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

  // What we send: Save shows once a box differs from what's sent; sending is a
  // release decision, so it says what changes and asks first.
  var dirty = false;
  document.querySelectorAll("form.send-form").forEach(function (f) {
    var boxes = Array.prototype.slice.call(f.querySelectorAll('input[name="topic"]'));
    var save = f.querySelector("[data-save]");
    var label = function (b) { return b.closest("label").querySelector("span").firstChild.textContent.trim(); };
    var changed = function () { return boxes.filter(function (b) { return b.checked !== b.defaultChecked; }); };
    boxes.forEach(function (b) {
      b.addEventListener("change", function () {
        var c = changed().length > 0;
        if (save) save.hidden = !c;
        dirty = document.querySelectorAll("form.send-form [data-save]:not([hidden])").length > 0;
      });
    });
    f.addEventListener("submit", function (e) {
      var start = changed().filter(function (b) { return b.checked; }).map(label);
      var stop = changed().filter(function (b) { return !b.checked; }).map(label);
      var msg = "Change what we send " + f.dataset.name + "?\n";
      if (start.length) msg += "\nStart sending: " + start.join(", ");
      if (stop.length) msg += "\nStop sending: " + stop.join(", ");
      msg += "\n\nThis is a release decision: it's committed to the config with your name.";
      if (!window.confirm(msg)) { e.preventDefault(); return; }
      if (save) { save.disabled = true; save.textContent = "Saving…"; }
      dirty = true;
    });
  });

  // The Status view: the links are measured every minute, so follow them, but
  // not over boxes someone is changing. While a change applies, check often.
  if (document.querySelector(".link-cards")) {
    var applying = document.querySelector("form.send-form[data-applying]");
    setInterval(function () { if (!document.hidden && !dirty) location.reload(); }, applying ? 3000 : 60000);
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
