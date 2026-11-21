(async () => {
  const sessionRoot = new URL(location.href);
  sessionRoot.search = ''; sessionRoot.hash = '';
  const base = sessionRoot.href;
  const catalog = paxBrowserCatalog;
  const resolve = values => {
    for (const value of values) {
      const base = String(value).toLowerCase().split(/[-_]/)[0];
      if (base === 'zh') return 'zh-Hans';
      if (base === 'en') return 'en';
    }
    return 'en';
  };
  let locale = resolve([document.documentElement.dataset.paxLocale, ...(navigator.languages || [])]), words, customUnavailable = false;
  try {
    const response = await fetch(new URL('presentation', sessionRoot), { credentials: 'omit', cache: 'no-store' });
    if (!response.ok) throw Error();
    const presentation = await response.json();
    if (presentation && catalog[presentation.locale]) {
      locale = presentation.locale;
      words = { ...catalog[locale], ...presentation.messages };
    }
  } catch { customUnavailable = true; }
  words ||= catalog[locale];
  const text = (key, params = {}) => (words[key] || catalog[locale][key] || '').replace(
    /\{\{|\}\}|\{([A-Za-z0-9_]+)\}/g,
    (match, name) => match === '{{' ? '{' : match === '}}' ? '}' : String(params[name] ?? '—'));
  document.documentElement.lang = locale;
  document.title = text('browser.title');
  for (const element of document.querySelectorAll('[data-copy]')) {
    element.textContent = text(element.dataset.copy);
  }
  if (customUnavailable) {
    const note = document.createElement('p');
    note.textContent = text('browser.custom_unavailable');
    document.querySelector('main').append(note);
  }
  const status = document.getElementById('state');
  const card = document.querySelector('main');
  const hint = document.getElementById('hint');
  const icon = document.getElementById('icon');
  const countdown = document.getElementById('countdown');
  const actions = document.getElementById('actions');
  const details = document.getElementById('details');
  const keep = document.getElementById('keep');
  let closeTimer, closeDeadline = 0, remainingMS = 5000;
  let submitted = false, retained = false, closing = false;
  function stage(value, title, description) {
    card.dataset.stage = value;
    status.textContent = title;
    hint.textContent = description;
    icon.textContent = value === 'complete' ? '✓' : value === 'partial' || value === 'failed' ? '!' : '';
  }
  function stopCountdown() {
    if (closeTimer !== undefined) {
      remainingMS = Math.max(0, closeDeadline - Date.now());
      clearTimeout(closeTimer);
      closeTimer = undefined;
    }
  }
  function pauseCountdown() {
    stopCountdown();
    countdown.textContent = text('browser.paused', { seconds: Math.ceil(remainingMS / 1000) });
  }
  function closePage() {
    if (closing) return;
    stopCountdown();
    closing = true;
    countdown.textContent = text('browser.closing');
    try { window.close(); }
    catch { /* Browser policy may reject closing a tab opened by the App. */ }
    setTimeout(() => {
      hint.textContent = text('browser.manual_close');
      countdown.textContent = text('browser.shortcut');
      actions.hidden = true;
    }, 350);
  }
  function startCountdown() {
    stopCountdown();
    closeDeadline = Date.now() + remainingMS;
    const tick = () => {
      remainingMS = Math.max(0, closeDeadline - Date.now());
      if (remainingMS === 0) { closePage(); return; }
      countdown.textContent = text('browser.countdown', { seconds: Math.ceil(remainingMS / 1000) });
      closeTimer = setTimeout(tick, 200);
    };
    tick();
  }
  document.getElementById('close').addEventListener('click', closePage);
  keep.addEventListener('click', () => {
    retained = true;
    stopCountdown();
    countdown.textContent = text('browser.kept');
    keep.hidden = true;
  });
  details.addEventListener('toggle', () => {
    if (!submitted || retained || closing) return;
    if (details.open) {
      pauseCountdown();
    } else {
      startCountdown();
    }
  });
  const result = {
    languages: Array.from(navigator.languages || []).slice(0, 16),
    region: Intl.DateTimeFormat().resolvedOptions().locale,
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    browser: /Edg\//.test(navigator.userAgent) ? 'Microsoft Edge' : /Firefox\//.test(navigator.userAgent) ? 'Firefox' : /Chrome\//.test(navigator.userAgent) ? 'Chrome' : /Safari\//.test(navigator.userAgent) ? 'Safari' : 'Browser',
    ipv4: '', ipv6: '', rtcAddresses: [], rtcState: 'empty', checks: []
  };
  const check = (kind, target) => ({ kind, target, state: 'failed', value: '', errorCode: '', durationMS: 0, httpStatus: 0, at: new Date().toISOString() });
  const timed = (url, options = {}, ms = 6000) => fetch(url, { credentials: 'omit', cache: 'no-store', ...options, signal: AbortSignal.timeout(ms) });
  function failure(c, e) {
    c.errorCode = e.name === 'TimeoutError' || e.name === 'AbortError' ? 'probe_timeout' : 'browser_request_failed';
    c.state = c.errorCode === 'probe_timeout' ? 'timeout' : 'failed';
  }
  async function request(c, url, parse) {
    const start = performance.now();
    try {
      const response = await timed(url);
      c.httpStatus = response.status;
      if (!response.ok) { c.errorCode = 'probe_http_' + response.status; return; }
      const reader = response.body.getReader();
      let chunks = [], size = 0;
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        size += value.length;
        if (size > 65536) { await reader.cancel(); c.errorCode = 'probe_response_invalid'; return; }
        chunks.push(value);
      }
      const data = new Uint8Array(size);
      let offset = 0;
      for (const chunk of chunks) { data.set(chunk, offset); offset += chunk.length; }
      const value = parse(new TextDecoder().decode(data));
      if (!value) { c.errorCode = 'probe_response_invalid'; return; }
      c.value = value.slice(0, 240); c.state = 'observed';
    } catch (e) { failure(c, e); }
    finally { c.durationMS = Math.min(45000, Math.round(performance.now() - start)); result.checks.push(c); }
  }
  const v4 = text => text.trim().match(/^(?:\d{1,3}\.){3}\d{1,3}$/)?.[0] || '';
  const v6 = text => /^[0-9a-f:]+$/i.test(text.trim()) && text.includes(':') ? text.trim() : '';
  async function ip(isV6) {
    const c = check(isV6 ? 'ipv6' : 'ipv4', (isV6 ? 'ipv6' : 'ipv4') + '.icanhazip.com');
    await request(c, 'https://' + c.target, isV6 ? v6 : v4);
    if (c.state === 'observed') result[isV6 ? 'ipv6' : 'ipv4'] = c.value;
  }
  async function dns() {
    const c = check('dns_session', 'bash.ws'), start = performance.now();
    try {
      const response = await timed(base + 'dns/start', { method: 'POST' });
      c.httpStatus = response.status;
      if (!response.ok) { try { c.errorCode = (await response.json()).errorCode || 'probe_http_' + response.status; } catch { c.errorCode = 'probe_http_' + response.status; } return; }
      const { id, names } = await response.json();
      if (!/^[a-zA-Z0-9]{8,64}$/.test(id) || !Array.isArray(names) || names.length !== 12 || !names.every(name => typeof name === 'string' && /^[a-zA-Z0-9]{8,64}\.[a-zA-Z0-9]{8,64}\.bash\.ws$/.test(name) && name.endsWith('.' + id + '.bash.ws'))) { c.errorCode = 'dns_session_invalid'; return; }
      c.state = 'observed';
      const q = check('dns_query', 'bash.ws'), begin = performance.now();
      let resolved = 0;
      await Promise.all(names.map(async name => {
        try {
          await timed('https://' + name + '/css/z.css', { mode: 'no-cors' }, 3000);
          resolved++;
        } catch { /* Opaque triggers are counted separately from authority results. */ }
      }));
      q.state = 'attempted'; q.value = '12 triggers; ' + resolved + ' fetches completed';
      q.durationMS = Math.round(performance.now() - begin); result.checks.push(q);
      await new Promise(resolve => setTimeout(resolve, 1200));
    } catch (e) { failure(c, e); }
    finally { c.durationMS = Math.min(45000, Math.round(performance.now() - start)); result.checks.push(c); }
  }
  async function rtc(kind, target) {
    const c = check(kind, target), start = performance.now();
    if (!globalThis.RTCPeerConnection) {
      c.state = 'unsupported'; c.errorCode = 'rtc_api_unavailable'; result.checks.push(c); return [];
    }
    let pc, addresses = new Set(), candidates = [], seenCandidates = new Set(), errors = [], serverCodes = [], srflx = false, grace;
    const isPublic = a => {
      if (!a || a.endsWith('.local')) return false;
      if (a.includes(':')) return !/^(::1?$|f[cd]|fe[89ab])/i.test(a);
      const p = a.split('.').map(Number);
      return p.length === 4 && !(p[0] === 10 || p[0] === 127 || p[0] === 0 || (p[0] === 169 && p[1] === 254) || (p[0] === 172 && p[1] >= 16 && p[1] <= 31) || (p[0] === 192 && p[1] === 168) || (p[0] === 100 && p[1] >= 64 && p[1] <= 127));
    };
    try {
      pc = new RTCPeerConnection({ iceServers: [{ urls: 'stun:' + target }] });
      pc.createDataChannel('pax-environment');
      await new Promise(resolve => {
        const timer = setTimeout(() => { c.state = 'timeout'; resolve(); }, 6500);
        const done = () => { clearTimeout(timer); clearTimeout(grace); if (c.state !== 'timeout') c.state = addresses.size ? 'observed' : 'empty'; resolve(); };
        pc.onicecandidate = e => {
          if (!e.candidate) { done(); return; }
          const a = e.candidate.address || e.candidate.candidate.split(' ')[4];
          if (!a) return;
          const key = [e.candidate.type, e.candidate.protocol, a, e.candidate.port].join(' ');
          if (!seenCandidates.has(key) && candidates.length < 16) {
            seenCandidates.add(key);
            addresses.add(a);
            candidates.push([e.candidate.type, e.candidate.protocol, a, e.candidate.port].filter(Boolean).join(' '));
          }
          if (e.candidate.type === 'srflx' && isPublic(a) && !srflx) { srflx = true; grace = setTimeout(done, 1500); }
        };
        pc.onicegatheringstatechange = () => { if (pc.iceGatheringState === 'complete') done(); };
        pc.onicecandidateerror = e => {
          if (!(e.errorCode >= 300 && e.errorCode <= 799)) return;
          const code = 'rtc_server_' + e.errorCode;
          const where = e.url || (e.address ? (e.address.includes(':') ? 'ipv6' : 'ipv4') : 'unknown');
          const entry = code + '@' + where;
          if (!errors.includes(entry) && errors.length < 8) errors.push(entry);
          if (!serverCodes.length) serverCodes.push(code);
        };
        (async () => {
          try { await pc.setLocalDescription(await pc.createOffer()); }
          catch { clearTimeout(timer); c.state = 'failed'; c.errorCode = 'rtc_operation_failed'; resolve(); }
        })();
      });
      if (!c.errorCode && !srflx) c.errorCode = serverCodes[0] || (c.state === 'timeout' ? 'rtc_gathering_timeout' : '');
    } catch { c.state = 'failed'; c.errorCode = 'rtc_operation_failed'; }
    finally {
      const errorText = errors.length ? '; errors: ' + errors.join(', ') : '';
      c.value = (candidates.join('; ').slice(0, 4096 - errorText.length) + errorText).slice(0, 4096);
      c.durationMS = Math.min(45000, Math.round(performance.now() - start));
      result.checks.push(c); pc?.close();
    }
    return [...addresses];
  }
  async function echoes() {
    await Promise.all([
      request(check('echo_cf', 'www.cloudflare.com'), 'https://www.cloudflare.com/cdn-cgi/trace', text => {
        const ip = text.match(/^ip=(.+)$/m)?.[1]?.trim(), colo = text.match(/^colo=([A-Z]{3})$/m)?.[1];
        return ip && (v4(ip) || v6(ip)) ? ip + (colo ? ' · ' + colo : '') : '';
      }),
      request(check('echo_ipinfo', 'ipinfo.io'), 'https://ipinfo.io/json', text => {
        const ip = JSON.parse(text).ip || ''; return v4(ip) || v6(ip);
      }),
      request(check('echo_ipip', 'myip.ipip.net'), 'https://myip.ipip.net/', text => v4(text.match(/(?:\d{1,3}\.){3}\d{1,3}/)?.[0] || ''))
    ]);
  }
  try {
    const [,, , cf, google] = await Promise.all([ip(false), ip(true), dns(), rtc('rtc_cf', 'stun.cloudflare.com:3478'), rtc('rtc_google', 'stun.l.google.com:19302'), echoes()]);
    result.rtcAddresses = [...new Set([...cf, ...google])].slice(0, 32);
    const rtcChecks = result.checks.filter(c => c.kind.startsWith('rtc_'));
    result.rtcState = result.rtcAddresses.length ? 'complete' : rtcChecks.every(c => c.state === 'unsupported') ? 'unsupported' : rtcChecks.every(c => c.state === 'failed') ? 'failed' : 'empty';
    stage('submitting', text('browser.submitting'), text('browser.submitting_hint'));
    const response = await timed(base + 'result', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(result) }, 25000);
    if (!response.ok) throw Error();
    const noIPv6 = response.headers.get('X-Pax-IPv6-Route') === 'absent';
    const partial = result.checks.some(c => ['failed', 'timeout', 'unsupported'].includes(c.state) && !(noIPv6 && c.kind === 'ipv6'));
    stage(partial ? 'partial' : 'complete', partial ? text('browser.partial') : text('browser.complete'), text('browser.submitted'));
    submitted = true;
    countdown.hidden = false;
    actions.hidden = false;
    if (details.open) pauseCountdown();
    else startCountdown();
  } catch {
    stage('failed', text('browser.failed'), text('browser.failed_hint'));
  }
})();
