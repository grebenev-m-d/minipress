(function () {
  'use strict';

  window.highlightCode = function (root) {
    if (!window.hljs) return;
    (root || document).querySelectorAll('pre code').forEach(function (el) {
      if (!el.dataset.highlighted) window.hljs.highlightElement(el);
    });
  };

  document.addEventListener('DOMContentLoaded', function () {
    window.highlightCode(document);
  });
})();
