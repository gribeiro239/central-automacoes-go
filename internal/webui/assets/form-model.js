const encoder = new TextEncoder();
const units = { seconds: 1, minutes: 60, hours: 3600, days: 86400 };
const methods = new Set(['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS']);

export class FormValidationError extends Error {
  constructor(message, field) {
    super(message);
    this.name = 'FormValidationError';
    this.field = field;
  }
}

function invalid(message, field) { throw new FormValidationError(message, field); }
function bytes(value) { return encoder.encode(value).length; }
function text(value) { return value == null ? '' : String(value); }
function integer(value, min, max, field, label) {
  const input = text(value).trim();
  const number = Number(input);
  if (!/^\d+$/.test(input) || !Number.isSafeInteger(number) || number < min || number > max) {
    invalid(`${label} deve ser um número inteiro entre ${min} e ${max}.`, field);
  }
  return number;
}

function same(left, right) {
  if (left === right) return true;
  if (!left || !right || typeof left !== 'object' || typeof right !== 'object') return false;
  const a = Object.keys(left).sort();
  const b = Object.keys(right).sort();
  return a.length === b.length && a.every((key, i) => key === b[i] && same(left[key], right[key]));
}

function localTimezone() {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'; }
  catch { return 'UTC'; }
}

function displayInterval(seconds) {
  const value = Number(seconds) || 300;
  for (const unit of ['days', 'hours', 'minutes']) {
    if (value % units[unit] === 0) return { value: String(value / units[unit]), unit };
  }
  return { value: String(value), unit: 'seconds' };
}

export function initialValues(automation = null) {
  const schedule = automation?.schedule;
  const interval = displayInterval(schedule?.interval_seconds);
  return {
    name: automation?.name ?? '',
    method: automation?.config?.method ?? 'GET',
    url: automation?.config?.url ?? '',
    enabled: automation?.enabled ?? true,
    timeout_seconds: String(automation?.timeout_seconds ?? 15),
    max_attempts: String(automation?.max_attempts ?? 3),
    overlap_policy: automation?.overlap_policy || 'allow',
    schedule_kind: schedule?.kind ?? 'none',
    interval_value: interval.value,
    interval_unit: interval.unit,
    cron: schedule?.cron ?? '0 8 * * *',
    timezone: schedule ? (schedule.timezone ?? '') : localTimezone(),
    schedule_paused: schedule?.paused ?? false,
    misfire_policy: schedule?.misfire_policy || 'run_once',
    misfire_grace_seconds: String(schedule?.misfire_grace_seconds || 60),
    retry_custom: Boolean(automation?.retry_policy),
    base_delay_seconds: String(automation?.retry_policy?.base_delay_seconds ?? 5),
    max_delay_seconds: String(automation?.retry_policy?.max_delay_seconds ?? 300),
    headers: JSON.stringify(automation?.config?.headers ?? {}, null, 2),
    body: automation?.config?.body ?? '',
  };
}

function parseHeaders(value) {
  let headers;
  try { headers = JSON.parse(text(value).trim() || '{}'); }
  catch { invalid('Os cabeçalhos devem ser um objeto JSON válido, como {"Accept":"application/json"}.', 'headers'); }
  if (!headers || Array.isArray(headers) || typeof headers !== 'object') invalid('Os cabeçalhos devem ser um objeto JSON.', 'headers');
  const entries = Object.entries(headers);
  if (entries.length > 32) invalid('Use no máximo 32 cabeçalhos.', 'headers');
  for (const [name, value] of entries) {
    if (typeof value !== 'string') invalid('Todos os valores dos cabeçalhos devem ser texto.', 'headers');
    if (!/^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/.test(name) || bytes(name) > 128) invalid('Há um nome de cabeçalho HTTP inválido.', 'headers');
    if (/^(host|content-length|transfer-encoding)$/i.test(name)) invalid(`O cabeçalho ${name} é definido automaticamente e não pode ser informado.`, 'headers');
    if (bytes(value) > 8192 || /[\x00-\x08\x0a-\x1f\x7f]/.test(value)) invalid('Há um valor de cabeçalho inválido ou acima de 8192 bytes.', 'headers');
  }
  return headers;
}

function schedulePayload(values) {
  const kind = values.schedule_kind;
  if (kind === 'none') return null;
  if (kind !== 'interval' && kind !== 'cron') invalid('Escolha um tipo de agendamento válido.', 'schedule_kind');
  const schedule = {
    kind,
    paused: Boolean(values.schedule_paused),
    misfire_policy: values.misfire_policy,
    misfire_grace_seconds: integer(values.misfire_grace_seconds, 1, 3600, 'misfire_grace_seconds', 'A tolerância de atraso'),
  };
  if (!['run_once', 'skip'].includes(schedule.misfire_policy)) invalid('Escolha uma política de atraso válida.', 'misfire_policy');
  if (kind === 'interval') {
    const multiplier = units[values.interval_unit];
    if (!multiplier) invalid('Escolha uma unidade de intervalo válida.', 'interval_unit');
    schedule.interval_seconds = integer(values.interval_value, 1, 31536000, 'interval_value', 'O intervalo') * multiplier;
    if (schedule.interval_seconds > 31536000) invalid('O intervalo não pode ultrapassar 365 dias.', 'interval_value');
  } else {
    schedule.cron = text(values.cron).trim();
    if (!schedule.cron || schedule.cron.split(/\s+/).length !== 5) invalid('Use uma expressão cron de cinco campos: minuto hora dia mês semana.', 'cron');
  }
  const timezone = text(values.timezone).trim();
  if (kind === 'cron' || timezone) {
    if (!timezone || timezone === 'Local') invalid('Informe um fuso IANA explícito, como America/Sao_Paulo.', 'timezone');
    try { new Intl.DateTimeFormat('pt-BR', { timeZone: timezone }); }
    catch { invalid('O fuso horário é inválido. Use um nome IANA, como America/Sao_Paulo.', 'timezone'); }
    schedule.timezone = timezone;
  }
  return schedule;
}

function retryPayload(values) {
  if (!values.retry_custom) return null;
  const policy = {
    base_delay_seconds: integer(values.base_delay_seconds, 1, 3600, 'base_delay_seconds', 'A espera inicial'),
    max_delay_seconds: integer(values.max_delay_seconds, 1, 86400, 'max_delay_seconds', 'A espera máxima'),
  };
  if (policy.max_delay_seconds < policy.base_delay_seconds) invalid('A espera máxima deve ser maior ou igual à espera inicial.', 'max_delay_seconds');
  return policy;
}

function comparableConfig(config) {
  return { method: config?.method ?? 'GET', url: config?.url ?? '', headers: config?.headers ?? {}, body: config?.body ?? '' };
}

export function buildPayload(values, original = null) {
  const name = text(values.name).trim();
  if (!name || bytes(name) > 160 || name.includes('\0')) invalid('O nome deve conter de 1 a 160 bytes, sem caractere NUL.', 'name');
  const method = text(values.method);
  if (!methods.has(method)) invalid('Escolha um método HTTP válido.', 'method');
  const url = text(values.url).trim();
  let parsed;
  try { parsed = new URL(url); }
  catch { invalid('Informe uma URL HTTP ou HTTPS completa.', 'url'); }
  if (!['http:', 'https:'].includes(parsed.protocol) || !parsed.hostname || parsed.username || parsed.password || parsed.hash) invalid('Use uma URL HTTP(S) sem credenciais ou fragmento.', 'url');
  const body = text(values.body);
  if (bytes(body) > 65536 || body.includes('\0')) invalid('O corpo deve ter até 64 KiB e não pode conter NUL.', 'body');
  const config = { method, url, headers: parseHeaders(values.headers), body };
  const current = {
    name,
    enabled: Boolean(values.enabled),
    timeout_seconds: integer(values.timeout_seconds, 1, 300, 'timeout_seconds', 'O tempo limite'),
    max_attempts: integer(values.max_attempts, 1, 10, 'max_attempts', 'O máximo de tentativas'),
    overlap_policy: values.overlap_policy,
  };
  if (!['allow', 'forbid'].includes(current.overlap_policy)) invalid('Escolha uma política de sobreposição válida.', 'overlap_policy');
  const schedule = schedulePayload(values);
  const retry = retryPayload(values);
  let payload;
  if (!original) {
    payload = { name, type: 'http_request', config, enabled: current.enabled, timeout_seconds: current.timeout_seconds, max_attempts: current.max_attempts, overlap_policy: current.overlap_policy };
    if (schedule) payload.schedule = schedule;
    if (retry) payload.retry_policy = retry;
  } else {
    payload = {};
    const before = initialValues(original);
    for (const key of Object.keys(current)) {
      const previous = ['timeout_seconds', 'max_attempts'].includes(key) ? Number(before[key]) : before[key];
      if (current[key] !== previous) payload[key] = current[key];
    }
    if (!same(config, comparableConfig(original.config))) payload.config = config;
    // Compare effective form values, not server-only fields or object ordering.
    // This is what prevents a name-only PATCH from resetting an interval's phase.
    if (!same(schedule, schedulePayload(before))) {
      if (schedule) payload.schedule = schedule;
      else if (original.schedule) payload.remove_schedule = true;
    }
    if (!same(retry, retryPayload(before))) {
      if (retry) payload.retry_policy = retry;
      else if (original.retry_policy) payload.remove_retry_policy = true;
    }
  }
  if (bytes(JSON.stringify(payload)) > 128 * 1024) invalid('A configuração completa ultrapassa o limite de 128 KiB da API.', 'headers');
  return payload;
}
