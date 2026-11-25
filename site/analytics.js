// The site measures itself: this script sends page events to this site's own
// server (POST /e) and to nobody else. It sets no cookie and stores nothing in
// the browser. A preview, or a browser driven by automation, sends nothing.
(function () {
  var root = document.documentElement;
  var page = root.getAttribute("data-page");
  if (!page || root.hasAttribute("data-preview") || navigator.webdriver) return;

  var test = root.getAttribute("data-test");
  var arm = root.getAttribute("data-arm");

  function send(event) {
    event.page = page;
    event.path = location.pathname;
    if (test && arm) {
      event.test = test;
      event.arm = arm;
    }
    var body = JSON.stringify(event);
    try {
      if (navigator.sendBeacon && navigator.sendBeacon("/e", body)) return;
    } catch (e) {}
    if (window.fetch) {
      fetch("/e", { method: "POST", body: body, keepalive: true }).catch(function () {});
    }
  }

  // view
  var params = new URLSearchParams(location.search);
  send({
    kind: "view",
    referrer: document.referrer,
    utm_source: params.get("utm_source") || "",
    utm_medium: params.get("utm_medium") || "",
    utm_campaign: params.get("utm_campaign") || "",
  });

  // scroll: each of 25, 50, 75 and 100 at most once
  var marks = [25, 50, 75, 100];
  function scrolled() {
    var height = document.documentElement.scrollHeight;
    if (!height) return;
    var percent = ((window.scrollY + window.innerHeight) / height) * 100;
    while (marks.length && percent >= marks[0]) send({ kind: "scroll", value: marks.shift() });
    if (!marks.length) removeEventListener("scroll", scrolled);
  }
  addEventListener("scroll", scrolled, { passive: true });
  scrolled();

  // engaged: whole seconds visible and focused, sent as increments
  var seconds = 0;
  var last = 0;
  function active() {
    return document.visibilityState === "visible" && document.hasFocus();
  }
  function tick() {
    if (last) seconds += (Date.now() - last) / 1000;
    last = active() ? Date.now() : 0;
  }
  function flush() {
    tick();
    var whole = Math.floor(seconds);
    if (whole >= 1) {
      seconds -= whole;
      send({ kind: "engaged", value: whole });
    }
  }
  last = active() ? Date.now() : 0;
  addEventListener("focus", tick);
  addEventListener("blur", tick);
  document.addEventListener("visibilitychange", function () {
    if (document.visibilityState === "hidden") flush();
    else tick();
  });
  addEventListener("pagehide", flush);

  // click: anything marked data-track
  document.addEventListener("click", function (e) {
    var el = e.target.closest && e.target.closest("[data-track]");
    if (el) send({ kind: "click", target: el.getAttribute("data-track") });
  });

  // form_start: the first focus inside the request form
  var form = document.querySelector(".request-form");
  if (form) {
    form.addEventListener("focusin", function () {
      send({ kind: "form_start" });
    }, { once: true });
  }
})();
