// The theme before the first paint: the saved choice, else the system's.
// This is a file of its own, and not a script inside the page, so the
// content security policy can forbid inline scripts.
(function () {
  try {
    var t = localStorage.getItem("bt-theme");
    if (!t) t = matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
    document.documentElement.dataset.theme = t;
  } catch (e) {
    document.documentElement.dataset.theme = "dark";
  }
})();
