import test from 'node:test';
import assert from 'node:assert/strict';
import { buildPayload, initialValues, FormValidationError } from '../assets/form-model.js';

function automation(overrides = {}) {
  return {
    id: 'existing-id', name: 'Integração', type: 'http_request', enabled: true,
    timeout_seconds: 15, max_attempts: 3, overlap_policy: 'allow',
    config: { method: 'POST', url: 'https://example.test/hook?a=1&b=2', headers: { Authorization: 'secret value', Accept: 'application/json' }, body: 'first\r\nsecond\n  ' },
    schedule: { kind: 'interval', interval_seconds: 3600, paused: false, misfire_policy: 'run_once', misfire_grace_seconds: 60 },
    retry_policy: { base_delay_seconds: 5, max_delay_seconds: 300 },
    next_run_at: '2026-10-20T15:00:00Z', created_at: '2026-10-19T15:00:00Z', updated_at: '2026-10-19T15:00:00Z',
    ...overrides,
  };
}

function fresh(overrides = {}) { return { ...initialValues(), name: 'Nova tarefa', url: 'http://127.0.0.1:9090/health', ...overrides }; }
function expectInvalid(values, field) {
  assert.throws(() => buildPayload(values), error => error instanceof FormValidationError && error.field === field);
}

test('new payload contains only supported input fields and no default schedule or retry override', () => {
  const payload = buildPayload(fresh());
  assert.deepEqual(payload, { name: 'Nova tarefa', type: 'http_request', config: { method: 'GET', url: 'http://127.0.0.1:9090/health', headers: {}, body: '' }, enabled: true, timeout_seconds: 15, max_attempts: 3, overlap_policy: 'allow' });
});

test('untouched edit sends an empty PATCH without changing schedule phase', () => {
  const original = automation();
  const before = JSON.stringify(original);
  assert.deepEqual(buildPayload(initialValues(original), original), {});
  assert.equal(JSON.stringify(original), before);
});

test('name-only edit preserves HTTP body, headers, schedule and retry by omission', () => {
  const original = automation();
  const values = initialValues(original);
  values.name = '  Outro nome  ';
  assert.deepEqual(buildPayload(values, original), { name: 'Outro nome' });
  assert.equal(values.body, original.config.body);
});

test('config edit submits the complete config preserving secrets and exact body text', () => {
  const original = automation();
  const values = initialValues(original);
  values.url = 'https://example.test/new';
  const payload = buildPayload(values, original);
  assert.deepEqual(payload, { config: { ...original.config, url: values.url } });
  assert.equal(payload.config.body, 'first\r\nsecond\n  ');
});

test('headers object key order and JSON whitespace are not a dirty config change', () => {
  const original = automation();
  const values = initialValues(original);
  values.headers = '{"Accept":"application/json", "Authorization":"secret value"}';
  assert.deepEqual(buildPayload(values, original), {});
});

test('missing optional config fields and policy defaults are preserved by omission', () => {
  const original = automation({ config: { method: 'GET', url: 'https://example.test' }, schedule: { kind: 'interval', interval_seconds: 60 }, retry_policy: null, overlap_policy: '' });
  assert.deepEqual(buildPayload(initialValues(original), original), {});
});

test('primitive edits include false and numbers without unrelated fields', () => {
  const original = automation();
  assert.deepEqual(buildPayload({ ...initialValues(original), enabled: false, timeout_seconds: '30', max_attempts: '1', overlap_policy: 'forbid' }, original), { enabled: false, timeout_seconds: 30, max_attempts: 1, overlap_policy: 'forbid' });
});

test('interval units produce seconds and equivalent representations do not reset an existing interval', () => {
  const payload = buildPayload(fresh({ schedule_kind: 'interval', interval_value: '2', interval_unit: 'hours', timezone: '' }));
  assert.deepEqual(payload.schedule, { kind: 'interval', paused: false, misfire_policy: 'run_once', misfire_grace_seconds: 60, interval_seconds: 7200 });
  const original = automation();
  const equivalent = { ...initialValues(original), interval_value: '60', interval_unit: 'minutes' };
  assert.deepEqual(buildPayload(equivalent, original), {});
});

test('interval editing preserves stored timezone and changing/removing schedule is explicit', () => {
  const original = automation({ schedule: { kind: 'interval', interval_seconds: 3600, timezone: 'America/Sao_Paulo', paused: true, misfire_policy: 'skip', misfire_grace_seconds: 90 } });
  assert.deepEqual(buildPayload({ ...initialValues(original), interval_value: '2' }, original).schedule, { kind: 'interval', paused: true, misfire_policy: 'skip', misfire_grace_seconds: 90, interval_seconds: 7200, timezone: 'America/Sao_Paulo' });
  assert.deepEqual(buildPayload({ ...initialValues(original), schedule_kind: 'none' }, original), { remove_schedule: true });
});

test('cron fields are submitted with explicit timezone and API remains final expression validator', () => {
  const payload = buildPayload(fresh({ schedule_kind: 'cron', cron: '0 8 * * *', timezone: 'America/Sao_Paulo', schedule_paused: true, misfire_policy: 'skip', misfire_grace_seconds: '120' }));
  assert.deepEqual(payload.schedule, { kind: 'cron', paused: true, misfire_policy: 'skip', misfire_grace_seconds: 120, cron: '0 8 * * *', timezone: 'America/Sao_Paulo' });
  assert.equal(buildPayload(fresh({ schedule_kind: 'cron', cron: '0 0 31 2 *', timezone: 'UTC' })).schedule.cron, '0 0 31 2 *');
});

test('retry policy customization, editing, and inherited reset use distinct payloads', () => {
  const create = buildPayload(fresh({ retry_custom: true, base_delay_seconds: '2', max_delay_seconds: '60' }));
  assert.deepEqual(create.retry_policy, { base_delay_seconds: 2, max_delay_seconds: 60 });
  const original = automation();
  assert.deepEqual(buildPayload({ ...initialValues(original), retry_custom: false }, original), { remove_retry_policy: true });
  assert.deepEqual(buildPayload({ ...initialValues(original), base_delay_seconds: '10' }, original), { retry_policy: { base_delay_seconds: 10, max_delay_seconds: 300 } });
  const inherited = automation({ retry_policy: null });
  assert.deepEqual(buildPayload(initialValues(inherited), inherited), {});
});

test('adversarial names, header strings and bodies are preserved as data', () => {
  const name = '<img src=x onerror="alert(1)">';
  const body = '</textarea><script>globalThis.pwned=true</script>\n \' " & ç';
  const values = fresh({ name, body, headers: '{"X-Text":"<svg onload=alert(1)>","__proto__":"still text"}' });
  const payload = buildPayload(values);
  assert.equal(payload.name, name);
  assert.equal(payload.config.body, body);
  assert.equal(payload.config.headers['X-Text'], '<svg onload=alert(1)>');
  assert.equal(Object.prototype.hasOwnProperty.call(payload.config.headers, '__proto__'), true);
  assert.equal(payload.config.headers.__proto__, 'still text');
  assert.equal({}.pwned, undefined);
});

test('name and body limits count UTF-8 bytes rather than JavaScript character count', () => {
  assert.equal(buildPayload(fresh({ name: 'á'.repeat(80) })).name.length, 80);
  expectInvalid(fresh({ name: 'á'.repeat(81) }), 'name');
  expectInvalid(fresh({ name: ' ' }), 'name');
  expectInvalid(fresh({ name: 'a\0b' }), 'name');
  assert.equal(buildPayload(fresh({ body: 'á'.repeat(32768) })).config.body.length, 32768);
  expectInvalid(fresh({ body: 'á'.repeat(32769) }), 'body');
  expectInvalid(fresh({ body: 'a\0b' }), 'body');
});

test('invalid JSON, non-object headers, non-string values and invalid HTTP headers are rejected', () => {
  for (const headers of ['{', 'null', '[]', '"x"', '{"X-Count":3}', '{"bad name":"x"}', '{"Host":"example.test"}', '{"X-Test":"line\\r\\nbreak"}', '{"X-Test":"\\u0000"}']) expectInvalid(fresh({ headers }), 'headers');
});

test('invalid URLs, unsupported methods and numeric limits are rejected', () => {
  for (const url of ['', '/relative', 'ftp://example.test', 'https://user:pass@example.test', 'https://example.test/#fragment']) expectInvalid(fresh({ url }), 'url');
  expectInvalid(fresh({ method: 'get' }), 'method');
  for (const timeout_seconds of ['0', '301', '1.5', 'NaN', '']) expectInvalid(fresh({ timeout_seconds }), 'timeout_seconds');
  for (const max_attempts of ['0', '11', '-1', '3.5']) expectInvalid(fresh({ max_attempts }), 'max_attempts');
  expectInvalid(fresh({ overlap_policy: 'invalid' }), 'overlap_policy');
});

test('invalid schedule and retry controls are rejected before network request', () => {
  const interval = fresh({ schedule_kind: 'interval', timezone: '' });
  expectInvalid({ ...interval, interval_value: '0' }, 'interval_value');
  expectInvalid({ ...interval, interval_value: '366', interval_unit: 'days' }, 'interval_value');
  expectInvalid({ ...interval, interval_unit: 'years' }, 'interval_unit');
  expectInvalid({ ...interval, misfire_policy: 'all' }, 'misfire_policy');
  expectInvalid({ ...interval, misfire_grace_seconds: '0' }, 'misfire_grace_seconds');
  const cron = fresh({ schedule_kind: 'cron' });
  expectInvalid({ ...cron, cron: '@hourly' }, 'cron');
  expectInvalid({ ...cron, timezone: 'Invalid/Zone' }, 'timezone');
  expectInvalid({ ...cron, timezone: '' }, 'timezone');
  expectInvalid({ ...cron, timezone: 'Local' }, 'timezone');
  expectInvalid(fresh({ retry_custom: true, base_delay_seconds: '0' }), 'base_delay_seconds');
  expectInvalid(fresh({ retry_custom: true, base_delay_seconds: '3601' }), 'base_delay_seconds');
  expectInvalid(fresh({ retry_custom: true, base_delay_seconds: '10', max_delay_seconds: '5' }), 'max_delay_seconds');
  expectInvalid(fresh({ retry_custom: true, max_delay_seconds: '86401' }), 'max_delay_seconds');
});
