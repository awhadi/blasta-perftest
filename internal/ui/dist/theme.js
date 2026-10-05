// Every address in the app is relative, so it works at the root of a host or under a
// path such as /blasta, with or without a proxy that strips that path. That needs the
// trailing slash: /blasta becomes /blasta/.
(function () {
  var p = location.pathname;
  // A page's own address (/templates/auth0) needs nothing: the server has already said where
  // the files are. Only the bare root of a mount point needs its slash.
  if (/\/(test|templates|history|login|admin|account|reset|confirm|confirm-email)(\/|$)/.test(p)) return;
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
  document.documentElement.classList.add('js');   // the server-rendered text of a page is for readers without scripts
  // Safari and Android tint the browser bar with theme-color: use the header's own colour for
  // the theme in effect (which may differ from the device's), now and whenever it changes.
  var COLORS = { light: '#ffffff', dark: '#161b22' };
  function tint() {
    var metas = document.querySelectorAll('meta[name="theme-color"]');
    if (!metas.length) return;
    var m = metas[0];
    for (var i = 1; i < metas.length; i++) metas[i].parentNode.removeChild(metas[i]);
    m.removeAttribute('media');
    m.setAttribute('content', COLORS[document.documentElement.getAttribute('data-theme')] || COLORS.dark);
  }
  tint();
  if (window.MutationObserver) new MutationObserver(tint).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
})();
