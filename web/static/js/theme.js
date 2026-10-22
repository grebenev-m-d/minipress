(function () {
  'use strict';

  var KEY = 'theme';
  var root = document.documentElement;

  function stored() {
    try { return localStorage.getItem(KEY); } catch (_) { return null; }
  }

  function systemTheme() {
    return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }

  function apply(theme) {
    root.dataset.theme = theme;
    document.querySelectorAll('[data-theme-toggle]').forEach(function (btn) {
      btn.textContent = theme === 'dark' ? 'Светлая' : 'Темная';
    });
  }

  apply(stored() || systemTheme());

  document.addEventListener('DOMContentLoaded', function () {
    apply(root.dataset.theme);
    document.querySelectorAll('[data-theme-toggle]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var next = root.dataset.theme === 'dark' ? 'light' : 'dark';
        try { localStorage.setItem(KEY, next); } catch (_) { }
        apply(next);
      });
    });
  });
})();
