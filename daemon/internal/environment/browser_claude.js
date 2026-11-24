async function sampleClaude({ check, request, progress, showCheck, record, v4, v6 }) {
  const webExit = check('echo_claude', 'claude.ai');
  const apiExit = check('echo_claude_api', 'api.anthropic.com');
  const edge = check('claude_edge', 'claude.ai');
  const protocol = check('claude_protocol', 'claude.ai');
  const webStatus = check('claude_status', 'status.claude.com');
  const apiStatus = check('claude_api_status', 'status.claude.com');

  function traceFields(body) {
    return Object.fromEntries(body.trim().split(/\r?\n/).map(line => line.split('=', 2)));
  }
  function exitValue(fields) {
    const ip = fields.ip?.trim();
    const country = /^[A-Z]{2}$/.test(fields.loc) ? fields.loc : '';
    return ip && (v4(ip) || v6(ip)) ? ip + (country ? ' · ' + country : '') : '';
  }
  function derived(source, target, value) {
    target.at = source.at;
    target.durationMS = source.durationMS;
    target.httpStatus = source.httpStatus;
    target.state = value ? 'observed' : source.state === 'timeout' ? 'timeout' : 'failed';
    target.value = value;
    target.errorCode = value ? '' : source.errorCode || 'probe_response_invalid';
    record(target);
  }
  async function webTrace() {
    let fields = {};
    await request(webExit, 'https://claude.ai/cdn-cgi/trace', body => {
      fields = traceFields(body);
      return exitValue(fields);
    });
    showCheck('claude_exit', webExit);
    const colo = /^[A-Z]{3}$/.test(fields.colo) ? fields.colo : '';
    const http = /^http\/(1\.0|1\.1|2|3)$/i.exec(fields.http);
    const tls = /^TLSv(1\.[0-3])$/.exec(fields.tls);
    const connection = http && tls ? 'HTTP/' + http[1] + ' · TLS ' + tls[1] : '';
    derived(webExit, edge, colo);
    derived(webExit, protocol, connection);
    progressTrace('claude_edge', edge);
    progressTrace('claude_protocol', protocol);
  }
  function progressTrace(row, item) {
    if (item.state === 'observed') progress(row, 'check_result_value', 'observed', { value: item.value });
    else showCheck(row, item);
  }
  async function apiTrace() {
    await request(apiExit, 'https://api.anthropic.com/cdn-cgi/trace', body => exitValue(traceFields(body)));
    showCheck('claude_api_exit', apiExit);
  }
  async function officialStatus() {
    const allowed = ['operational', 'degraded_performance', 'partial_outage', 'major_outage', 'under_maintenance'];
    let components = [];
    const value = name => {
      const status = components.find(component => component?.name === name)?.status;
      return allowed.includes(status) ? status : '';
    };
    await request(webStatus, 'https://status.claude.com/api/v2/summary.json', body => {
      const parsed = JSON.parse(body).components;
      if (Array.isArray(parsed)) components = parsed;
      return value('claude.ai');
    });
    derived(webStatus, apiStatus, value('Claude API (api.anthropic.com)'));
    for (const item of [webStatus, apiStatus]) {
      if (item.state === 'observed') {
        progress(item.kind, 'claude_status_' + item.value, item.value === 'operational' ? 'observed' : 'failed');
      } else showCheck(item.kind, item);
    }
  }
  await Promise.all([webTrace(), apiTrace(), officialStatus()]);
}
