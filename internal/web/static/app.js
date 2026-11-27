/* Progressive enhancement for the server-rendered market (ADR-0034).
   Every page works without this file: links navigate and forms post.
   Handlers are bound by name from data-on<event>="block.handler". */
(function () {
  "use strict";
  var d = document;
  var KEY_SAVED = "saved";

  function store(k, v) {
    try {
      if (v === undefined) return JSON.parse(localStorage.getItem(k) || "null");
      localStorage.setItem(k, JSON.stringify(v));
    } catch (e) { return null; }
  }
  function toast(text) {
    var t = d.querySelector("[data-toast]") || d.createElement("div");
    if (!t.parentNode) {
      t.setAttribute("data-toast", "");
      t.setAttribute("role", "status");
      t.className = "fixed bottom-4 left-1/2 z-50 -translate-x-1/2 rounded-md bg-foreground px-4 py-2 text-sm text-background shadow-lg";
      d.body.appendChild(t);
    }
    t.textContent = text;
    t.hidden = false;
    clearTimeout(t._h);
    t._h = setTimeout(function () { t.hidden = true; }, 2500);
  }
  function submit(el) {
    var f = el.form || el.closest("form");
    if (f) { if (f.requestSubmit) f.requestSubmit(); else f.submit(); }
  }
  function toggleTheme() {
    var root = d.documentElement, dark = !root.classList.contains("dark");
    root.classList.toggle("dark", dark);
    root.style.colorScheme = dark ? "dark" : "light";
    try { localStorage.setItem("theme", dark ? "dark" : "light"); } catch (e) {}
  }
  function popover(btn) {
    var root = btn.closest("[data-popover-root]");
    if (!root) return;
    var panel = root.querySelector("[hidden], [data-popover-open]");
    if (!panel) return;
    var open = panel.hasAttribute("hidden");
    closePopovers(root);
    if (open) { panel.removeAttribute("hidden"); panel.setAttribute("data-popover-open", ""); }
    else { panel.setAttribute("hidden", ""); panel.removeAttribute("data-popover-open"); }
    btn.setAttribute("aria-expanded", open ? "true" : "false");
  }
  function closePopovers(except) {
    d.querySelectorAll("[data-popover-root]").forEach(function (root) {
      if (root === except) return;
      var p = root.querySelector("[data-popover-open]");
      if (p) { p.setAttribute("hidden", ""); p.removeAttribute("data-popover-open"); }
      var b = root.querySelector("[aria-expanded]");
      if (b) b.setAttribute("aria-expanded", "false");
    });
  }
  function collapse(btn, cls) {
    var target = d.getElementById(btn.getAttribute("aria-controls"));
    if (!target) return;
    var open = btn.getAttribute("aria-expanded") !== "true";
    target.classList.toggle(cls, !open);
    btn.setAttribute("aria-expanded", open ? "true" : "false");
  }
  function menu(open) {
    var m = d.getElementById("mobile-menu");
    if (!m) return;
    if (open) { if (m.showModal) m.showModal(); else m.setAttribute("open", ""); }
    else if (m.close) m.close(); else m.removeAttribute("open");
    var t = d.getElementById("menu-trigger");
    if (t) t.setAttribute("aria-expanded", open ? "true" : "false");
  }
  function saved() { return store(KEY_SAVED) || []; }
  function saveToggle(btn) {
    var list = saved(), href = location.pathname, i = -1;
    list.forEach(function (it, n) { if (it.href === href) i = n; });
    if (i >= 0) list.splice(i, 1);
    else {
      var h = d.querySelector("main h1"), img = d.getElementById("gallery-main");
      list.unshift({ href: href, title: h ? h.textContent.trim() : d.title, image: img ? img.getAttribute("src") : "" });
    }
    store(KEY_SAVED, list.slice(0, 100));
    btn.setAttribute("aria-pressed", i >= 0 ? "false" : "true");
  }
  function renderSaved(box) {
    var list = saved();
    box.textContent = "";
    if (!list.length) {
      var p = d.createElement("p");
      p.className = "text-sm text-muted-foreground";
      p.textContent = box.getAttribute("data-empty") || "";
      box.appendChild(p);
      return;
    }
    list.forEach(function (it) {
      var a = d.createElement("a");
      a.href = it.href;
      a.className = "flex items-center gap-3 rounded-lg border border-border bg-card p-3 text-sm font-medium hover:bg-muted";
      if (it.image) {
        var im = d.createElement("img");
        im.src = it.image; im.alt = ""; im.width = 64; im.height = 40;
        im.className = "h-10 w-16 rounded object-cover";
        a.appendChild(im);
      }
      a.appendChild(d.createTextNode(it.title));
      box.appendChild(a);
    });
  }
  function license(input) {
    var v = input.value;
    d.querySelectorAll('a[href^="/checkout/"]').forEach(function (a) {
      var u = new URL(a.href, location.href);
      u.searchParams.set("license", v);
      a.href = u.pathname + u.search;
    });
    d.querySelectorAll("[data-license-price]").forEach(function (el) {
      var p = input.getAttribute("data-price");
      if (p) el.textContent = p;
    });
  }

  var H = {
    "site-header.theme": toggleTheme,
    "mobile-menu.theme": toggleTheme,
    "site-header.menu": function () { menu(true); },
    "mobile-menu.close": function () { menu(false); },
    "main-nav.toggle": popover,
    "account-menu.toggle": popover,
    "catalog-filters.toggle": function (b) { collapse(b, "catalog-filters-stack-is1"); },
    "product-about.toggle": function (b) { collapse(b, "product-about-text-2-is1"); },
    "catalog-filters.editor": submit,
    "catalog-filters.free": submit,
    "catalog-filters.type": submit,
    "catalog-results.sort": submit,
    "license-options.change": license,
    "product-gallery.select": function (b) {
      var main = d.getElementById("gallery-main");
      if (main && b.getAttribute("data-src")) main.setAttribute("src", b.getAttribute("data-src"));
      b.parentNode.querySelectorAll("[aria-pressed]").forEach(function (x) { x.setAttribute("aria-pressed", x === b ? "true" : "false"); });
    },
    "copy-field.copy": function (b) {
      var root = b.closest(".copy-field") || b.parentNode, input = root.querySelector("input");
      if (!input) return;
      var done = function () { toast(b.getAttribute("data-copied") || "\u2713"); };
      if (navigator.clipboard) navigator.clipboard.writeText(input.value).then(done, function () { input.select(); });
      else { input.select(); d.execCommand("copy"); done(); }
    },
    "product-head.save": saveToggle
  };
  var TEXT = { "catalog-filters.min": 1, "catalog-filters.max": 1 };

  function dispatch(ev, name) {
    var el = ev.target.closest("[data-on" + name + "]");
    if (!el) return;
    var key = el.getAttribute("data-on" + name), fn = H[key];
    if (fn) {
      var anchor = el.tagName === "A";
      if (!anchor || key === "site-header.menu") { if (name === "click" && el.type !== "submit") ev.preventDefault(); }
      fn(el, ev);
    }
  }
  d.addEventListener("click", function (ev) {
    if (!ev.target.closest("[data-popover-root]")) closePopovers(null);
    dispatch(ev, "click");
  });
  d.addEventListener("change", function (ev) {
    var el = ev.target.closest("[data-oninput]");
    if (el && TEXT[el.getAttribute("data-oninput")]) { submit(el); return; }
    dispatch(ev, "change");
  });
  d.addEventListener("keydown", function (ev) {
    if (ev.key === "Escape") closePopovers(null);
  });
  var m = d.getElementById("mobile-menu");
  if (m) m.addEventListener("click", function (ev) { if (ev.target === m) menu(false); });

  d.querySelectorAll("[data-saved-list]").forEach(renderSaved);
  var sv = d.querySelector('[data-onclick="product-head.save"]');
  if (sv) {
    var on = saved().some(function (it) { return it.href === location.pathname; });
    sv.setAttribute("aria-pressed", on ? "true" : "false");
  }
  var checked = d.querySelector('input[data-onchange="license-options.change"]:checked');
  if (checked && !checked.form) license(checked);
})();
