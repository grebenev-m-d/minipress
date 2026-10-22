(function () {
  'use strict';

  var form = document.getElementById('editor');
  var area = document.getElementById('content');
  var preview = document.getElementById('preview');
  var stateEl = document.getElementById('save-state');
  var toast = document.getElementById('toast');
  var workspace = document.getElementById('workspace');
  var linksDialog = document.getElementById('links-dialog');
  var titleEl = form.elements.title;

  var dirty = false;
  var failed = false;
  var uploadSeq = 0;
  var toastTimer = null;

  function setState(state, text) {
    stateEl.dataset.state = state;
    stateEl.textContent = text;
    if (state === 'error') {
      stateEl.appendChild(document.createTextNode(' '));
      var retry = document.createElement('button');
      retry.type = 'button';
      retry.textContent = 'Retry';
      retry.addEventListener('click', function () { htmx.trigger(form, 'retry'); });
      stateEl.appendChild(retry);
    }
  }

  function clock() {
    return new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }

  function showToast(message) {
    toast.textContent = message;
    toast.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { toast.hidden = true; }, 4000);
  }

  form.addEventListener('input', function () { dirty = true; });
  document.querySelectorAll('input[name="is_published"]').forEach(function (el) {
    el.addEventListener('change', function () { dirty = true; });
  });

  form.addEventListener('htmx:beforeRequest', function () {
    dirty = false;
    setState('saving', 'Saving...');
  });

  form.addEventListener('htmx:afterRequest', function (e) {
    if (e.detail.successful) {
      failed = false;
      setState('saved', 'Сохранено в ' + clock());
      return;
    }
    failed = true;
    dirty = true;
    var xhr = e.detail.xhr;
    var msg = xhr && xhr.status ? (xhr.responseText || 'Error ' + xhr.status).trim() : 'No connection, changes are not saved';
    setState('error', msg + '.');
  });

  document.body.addEventListener('htmx:afterSwap', function (e) {
    if (e.target === preview) window.highlightCode(preview);
  });

  window.addEventListener('beforeunload', function (e) {
    if (dirty || failed) {
      e.preventDefault();
      e.returnValue = '';
    }
  });

  function fit(el) {
    el.style.height = 'auto';
    el.style.height = el.scrollHeight + 'px';
  }

  function fitAll() {
    fit(titleEl);
    fit(area);
  }

  titleEl.addEventListener('keydown', function (e) {
    if (e.key === 'Enter') {
      e.preventDefault();
      form.elements.author.focus();
    }
  });
  form.addEventListener('input', function (e) {
    if (e.target === titleEl || e.target === area) fit(e.target);
  });
  window.addEventListener('resize', fitAll);
  fitAll();

  function touch() {
    area.dispatchEvent(new Event('input', { bubbles: true }));
  }

  function uploadImage(file) {
    if (!file || file.type.indexOf('image/') !== 0) return;
    var marker = '![Uploading...](#upload-' + (++uploadSeq) + ')';
    area.setRangeText('\n' + marker + '\n', area.selectionStart, area.selectionEnd, 'end');
    fit(area);

    var body = new FormData();
    body.append('image', file);

    fetch(form.dataset.uploadUrl, { method: 'POST', body: body })
      .then(function (res) {
        return res.text().then(function (text) {
          var data = {};
          try { data = JSON.parse(text); } catch (_) { data = { error: text.trim() }; }
          if (!res.ok) throw new Error(data.error || 'Error ' + res.status);
          return data;
        });
      })
      .then(function (data) {
        area.value = area.value.replace(marker, '![image](' + data.url + ')');
        fit(area);
        touch();
      })
      .catch(function (err) {
        area.value = area.value.replace('\n' + marker + '\n', '\n');
        fit(area);
        touch();
        showToast('Image upload failed: ' + (err instanceof TypeError ? 'no connection' : err.message));
      });
  }

  area.addEventListener('paste', function (e) {
    var files = e.clipboardData && e.clipboardData.files;
    if (files && files.length && files[0].type.indexOf('image/') === 0) {
      e.preventDefault();
      uploadImage(files[0]);
    }
  });

  area.addEventListener('dragover', function (e) {
    e.preventDefault();
    area.classList.add('is-dragging');
  });
  area.addEventListener('dragleave', function () { area.classList.remove('is-dragging'); });
  area.addEventListener('drop', function (e) {
    area.classList.remove('is-dragging');
    if (e.dataTransfer && e.dataTransfer.files.length) {
      e.preventDefault();
      uploadImage(e.dataTransfer.files[0]);
    }
  });

  document.querySelectorAll('.tabs [data-pane]').forEach(function (tab) {
    tab.addEventListener('click', function () {
      workspace.dataset.pane = tab.dataset.pane;
      document.querySelectorAll('.tabs [data-pane]').forEach(function (t) {
        t.setAttribute('aria-selected', String(t === tab));
      });
    });
  });

  document.getElementById('open-links').addEventListener('click', function () { linksDialog.showModal(); });
  linksDialog.querySelector('[data-close]').addEventListener('click', function () { linksDialog.close(); });
  linksDialog.addEventListener('click', function (e) {
    if (e.target === linksDialog) linksDialog.close();
  });

  linksDialog.querySelectorAll('[data-copy]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var text = btn.dataset.copy;
      var done = function () {
        var old = btn.textContent;
        btn.textContent = 'Copied';
        setTimeout(function () { btn.textContent = old; }, 1500);
      };
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(text).then(done, function () { showToast('Could not copy'); });
      } else {
        var tmp = document.createElement('textarea');
        tmp.value = text;
        document.body.appendChild(tmp);
        tmp.select();
        try { document.execCommand('copy'); done(); } catch (_) { showToast('Could not copy'); }
        tmp.remove();
      }
    });
  });

  document.getElementById('delete-form').addEventListener('submit', function (e) {
    if (!window.confirm('Delete this article permanently? This cannot be undone.')) {
      e.preventDefault();
      return;
    }
    dirty = false;
    failed = false;
  });

  window.highlightCode(preview);
})();
