(function () {
  'use strict';

  const DEFAULT_API_BASE =
    window.location.protocol === 'http:' || window.location.protocol === 'https:'
      ? window.location.origin
      : 'http://localhost:8081';

  let apiBaseValue = localStorage.getItem('rankings_api_base') || DEFAULT_API_BASE;

  function apiBase() {
    return String(apiBaseValue || DEFAULT_API_BASE).replace(/\/+$/, '');
  }

  function setApiBase(raw) {
    const value = String(raw || '').trim();
    apiBaseValue = value ? value.replace(/\/+$/, '') : DEFAULT_API_BASE;
    if (value) {
      localStorage.setItem('rankings_api_base', apiBaseValue);
    } else {
      localStorage.removeItem('rankings_api_base');
    }
    return apiBase();
  }

  function escapeHtml(value) {
    return String(value ?? '')
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#39;');
  }

  function getQueryParam(name) {
    return new URLSearchParams(window.location.search).get(name);
  }

  function updateQueryParam(name, value) {
    const url = new URL(window.location);
    if (value) url.searchParams.set(name, value);
    else url.searchParams.delete(name);
    window.history.replaceState(null, '', url.toString());
  }

  function fmtNorm(value) {
    return Number.isFinite(value) ? value.toFixed(2) : '—';
  }

  function fmtTime(value) {
    if (!value) return '—';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '—';
    return date.toLocaleString();
  }

  function fmtShortTime(value) {
    if (!value) return '—';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '—';
    const now = new Date();
    const time = date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    if (date.toDateString() === now.toDateString()) {
      return time;
    }
    return date.toLocaleDateString([], { month: 'short', day: 'numeric' }) + ' ' + time;
  }

  function friendlyError(status, bodyText) {
    const trimmed = String(bodyText || '').trim();
    switch (status) {
      case 400: return trimmed || 'That request was missing something required.';
      case 401: return 'You need to log in first.';
      case 403: return trimmed || 'You don\u2019t have permission to do that.';
      case 404: return trimmed || 'Not found.';
      case 409: return trimmed || 'That player is already on this owner\u2019s board.';
      case 500: return 'Something went wrong on the server. Try again.';
      default: return trimmed || ('Request failed (' + status + ').');
    }
  }

  async function api(path, options) {
    const res = await fetch(apiBase() + path, Object.assign({
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
    }, options || {}));

    const text = await res.text();
    let body = null;
    if (text) {
      try {
        body = JSON.parse(text);
      } catch (err) {
        body = text;
      }
    }

    if (!res.ok) {
      if (res.status === 401 && !path.startsWith('/auth/')) {
        window.location.replace('login.html');
        return new Promise(() => {});
      }

      let message;
      if (body && typeof body === 'object' && !Array.isArray(body) && (body.error || body.message)) {
        message = body.error || body.message;
      } else {
        message = friendlyError(res.status, typeof body === 'string' ? body : text);
      }

      const err = new Error(message);
      err.status = res.status;
      throw err;
    }

    return body;
  }

  function toast(message, kind) {
    const stack = document.getElementById('toastStack');
    if (!stack) return;

    const t = document.createElement('div');
    t.className = 'toast' + (kind ? ' ' + kind : '');
    t.textContent = message;
    stack.appendChild(t);

    setTimeout(() => {
      t.style.transition = 'opacity 0.25s ease';
      t.style.opacity = '0';
      setTimeout(() => t.remove(), 260);
    }, 3600);
  }

  async function initAuth(config) {
    const opts = config || {};
    try {
      const user = await api('/auth/me');
      if (opts.userNameEl) {
        opts.userNameEl.textContent = user.display_name || user.username;
      }
      if (opts.statusDotEl) {
        opts.statusDotEl.className = 'status-dot ok';
      }
      if (opts.statusTextEl) {
        opts.statusTextEl.textContent = 'connected';
      }
      return user;
    } catch (err) {
      if (err.status === 401) {
        window.location.replace('login.html');
        return null;
      }
      if (opts.statusDotEl) {
        opts.statusDotEl.className = 'status-dot bad';
      }
      if (opts.statusTextEl) {
        opts.statusTextEl.textContent = 'error';
      }
      if (opts.onError) {
        opts.onError(err);
      }
      return null;
    }
  }

  let drawerEls = null;

  function ordinal(value) {
    const n = Math.round(Number(value) || 0);
    const suffixes = ['th', 'st', 'nd', 'rd'];
    const mod100 = n % 100;
    return n + (suffixes[(mod100 - 20) % 10] || suffixes[mod100] || suffixes[0]);
  }

  function drawerElements() {
    if (drawerEls) return drawerEls;

    const backdrop = document.createElement('div');
    backdrop.className = 'drawer-backdrop';
    backdrop.setAttribute('aria-hidden', 'true');

    const drawer = document.createElement('aside');
    drawer.className = 'player-drawer';
    drawer.setAttribute('role', 'dialog');
    drawer.setAttribute('aria-modal', 'true');
    drawer.setAttribute('aria-hidden', 'true');
    drawer.innerHTML =
      '<div class="drawer-head">' +
      '  <div>' +
      '    <h2 class="drawer-title"></h2>' +
      '    <p class="drawer-sub"></p>' +
      '  </div>' +
      '  <button class="drawer-close" type="button" aria-label="Close player detail">×</button>' +
      '</div>' +
      '<div class="drawer-body"></div>';

    document.body.appendChild(backdrop);
    document.body.appendChild(drawer);

    const closeBtn = drawer.querySelector('.drawer-close');
    closeBtn.addEventListener('click', closePlayerDrawer);
    backdrop.addEventListener('click', closePlayerDrawer);
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') closePlayerDrawer();
    });

    drawerEls = {
      backdrop,
      drawer,
      title: drawer.querySelector('.drawer-title'),
      sub: drawer.querySelector('.drawer-sub'),
      body: drawer.querySelector('.drawer-body'),
      closeBtn,
    };
    return drawerEls;
  }

  function closePlayerDrawer() {
    if (!drawerEls) return;
    drawerEls.backdrop.classList.remove('open');
    drawerEls.drawer.classList.remove('open');
    drawerEls.drawer.setAttribute('aria-hidden', 'true');
  }

  function sparklineSVG(points) {
    const valid = (points || []).filter((p) => Number.isFinite(Number(p.avg_normalized_rank)));
    if (!valid.length) return '';

    const width = 100;
    const height = 48;
    const pad = 4;
    const step = valid.length > 1 ? width / (valid.length - 1) : 0;

    const coords = valid.map((p, i) => {
      const value = Math.max(0, Math.min(1, Number(p.avg_normalized_rank)));
      const x = valid.length === 1 ? width / 2 : i * step;
      const y = pad + value * (height - 2 * pad);
      return [x, y];
    });

    const path = coords
      .map(([x, y], i) => (i === 0 ? 'M' : 'L') + x.toFixed(2) + ' ' + y.toFixed(2))
      .join(' ');

    const circles = coords
      .map(([x, y], i) => {
        const title = escapeHtml(valid[i].taken_at || '');
        return '<circle cx="' + x.toFixed(2) + '" cy="' + y.toFixed(2) + '" r="2.5">' +
          '<title>' + title + ' \u2014 ' + fmtNorm(valid[i].avg_normalized_rank) + '</title>' +
          '</circle>';
      })
      .join('');

    return '<svg class="sparkline" viewBox="0 0 ' + width + ' ' + height + '" preserveAspectRatio="none" role="img" aria-label="Rank trend">' +
      '<path d="' + path + '"/>' +
      circles +
      '</svg>';
  }

  function renderPlayerDrawer(els, data) {
    els.title.textContent = data.player_name || 'Unknown player';
    const sub = [
      data.position,
      data.team,
      data.drafted_by ? 'on ' + data.drafted_by + '\u2019s team' : null,
    ].filter(Boolean).join(' \u00b7 ');
    els.sub.textContent = sub;

    let html = '';
    if (data.consensus) {
      const pct = Math.max(0, Math.min(100, Math.round(Number(data.consensus.percentile) || 0)));
      html +=
        '<section class="drawer-section">' +
        '<h3>League consensus</h3>' +
        '<p class="drawer-consensus-line"><b>' + ordinal(pct) + ' percentile</b> \u00b7 ranked by ' +
        data.consensus.num_owners_ranked + ' owners</p>' +
        '<div class="percentile-bar"><div class="percentile-fill" style="width:' + pct + '%"></div></div>' +
        '<p class="drawer-muted">Average normalized rank ' + fmtNorm(data.consensus.avg_normalized_rank) + '</p>' +
        '</section>';
    } else {
      html +=
        '<section class="drawer-section">' +
        '<h3>League consensus</h3>' +
        '<p class="drawer-muted">Not in the consensus big board yet (ranked by fewer than 3 owners).</p>' +
        '</section>';
    }

    html += '<section class="drawer-section"><h3>Rank trend</h3>';
    if (data.trend && data.trend.length >= 2) {
      html += sparklineSVG(data.trend);
      html += '<p class="drawer-muted">League-average normalized rank across snapshots (lower is better).</p>';
    } else {
      html += '<p class="drawer-muted">Not enough snapshots yet.</p>';
    }
    html += '</section>';

    html += '<section class="drawer-section"><h3>Owner rankings</h3>';
    if (!data.rankings || !data.rankings.length) {
      html += '<p class="drawer-muted">No owners have ranked this player yet.</p>';
    } else {
      html +=
        '<table class="drawer-table"><thead><tr>' +
        '<th>Owner</th><th class="num">Overall</th><th class="num">Positional</th><th class="num">Normalized</th>' +
        '</tr></thead><tbody>';
      data.rankings.forEach((row) => {
        const overall = row.owner_total
          ? row.overall_rank + ' / ' + row.owner_total
          : row.overall_rank;
        html +=
          '<tr>' +
          '<td>' + escapeHtml(row.owner) + '</td>' +
          '<td class="num">' + overall + '</td>' +
          '<td class="num">#' + row.positional_rank + '</td>' +
          '<td class="num">' + fmtNorm(row.normalized_rank) + '</td>' +
          '</tr>';
      });
      html += '</tbody></table>';
    }
    html += '</section>';

    els.body.innerHTML = html;
  }

  async function openPlayerDrawer(playerID) {
    const els = drawerElements();
    els.title.textContent = 'Player';
    els.sub.textContent = '';
    els.body.innerHTML = '<div class="drawer-loading">Loading\u2026</div>';
    els.backdrop.classList.add('open');
    els.drawer.classList.add('open');
    els.drawer.setAttribute('aria-hidden', 'false');
    els.closeBtn.focus();

    try {
      const data = await api('/rankings/player/' + encodeURIComponent(playerID));
      renderPlayerDrawer(els, data);
    } catch (err) {
      els.title.textContent = 'Player';
      els.body.innerHTML =
        '<div class="drawer-error">' +
        escapeHtml(err.message || 'Unable to load player detail.') +
        '</div>';
    }
  }

  function makePlayerClickable(node, playerID) {
    if (!node || !playerID) return node;
    node.classList.add('player-link');
    node.title = 'Open player detail';
    node.tabIndex = 0;
    node.addEventListener('click', (e) => {
      e.stopPropagation();
      openPlayerDrawer(playerID);
    });
    node.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        e.stopPropagation();
        openPlayerDrawer(playerID);
      }
    });
    return node;
  }

  window.App = {
    apiBase,
    setApiBase,
    api,
    escapeHtml,
    getQueryParam,
    updateQueryParam,
    fmtNorm,
    fmtTime,
    fmtShortTime,
    friendlyError,
    toast,
    initAuth,
    openPlayerDrawer,
    closePlayerDrawer,
    makePlayerClickable,
    sparklineSVG,
  };
})();
