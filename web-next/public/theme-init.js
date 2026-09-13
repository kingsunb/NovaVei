(function () {
  try {
    var t = localStorage.getItem("nv-theme") || "system";
    if (t === "system") {
      t = matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
    }
    document.documentElement.dataset.theme = t;
  } catch { /* localStorage / matchMedia unavailable — no-op */ }
})();
