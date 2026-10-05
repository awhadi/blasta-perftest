// Every address in the app is relative, so it works at the root of a host or under a
// path such as /blasta, with or without a proxy that strips that path. That needs the
// trailing slash: /blasta becomes /blasta/.
(function () {
  var p = location.pathname;
  if (p.slice(-1) !== '/' && p.split('/').pop().indexOf('.') < 0) location.replace(p + '/' + location.search + location.hash);
})();
// Runs before first paint so the page never flashes the wrong colour scheme.
(function () {
  var t = null;
  try { t = localStorage.getItem('blasta.theme'); } catch (e) {}
  if (t !== 'light' && t !== 'dark') {
    t = window.matchMedia && matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  }
  document.documentElement.setAttribute('data-theme', t);
})();
