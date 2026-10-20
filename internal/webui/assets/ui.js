export function esc(value) {
  return String(value ?? '').replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}

const icons = {
  grid:'<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  layers:'<path d="m12 3 9 5-9 5-9-5 9-5Z"/><path d="m3 12 9 5 9-5M3 16l9 5 9-5"/>',
  activity:'<path d="M3 12h4l3-8 4 16 3-8h4"/>',
  home:'<path d="m3 10 9-7 9 7v10a1 1 0 0 1-1 1h-5v-7H9v7H4a1 1 0 0 1-1-1V10Z"/>',
  plus:'<path d="M12 5v14M5 12h14"/>',
  refresh:'<path d="M20 7v5h-5M4 17v-5h5"/><path d="M5.2 8a8 8 0 0 1 13-3L20 7M4 17l1.8 2A8 8 0 0 0 19 16"/>',
  arrow:'<path d="M5 12h14m-5-5 5 5-5 5"/>',
  arrowUp:'<path d="M7 17 17 7M7 7h10v10"/>',
  chevron:'<path d="m9 5 7 7-7 7"/>',
  chevronLeft:'<path d="m15 5-7 7 7 7"/>',
  clock:'<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  check:'<path d="m5 12 4 4L19 6"/>',
  checkCircle:'<circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/>',
  alert:'<path d="m12 3 10 18H2L12 3Z"/><path d="M12 9v4m0 4h.01"/>',
  close:'<path d="m6 6 12 12M6 18 18 6"/>',
  play:'<path d="m8 4 12 8-12 8V4Z"/>',
  pause:'<path d="M8 5v14M16 5v14" stroke-width="3"/>',
  stop:'<rect x="6" y="6" width="12" height="12" rx="2"/>',
  edit:'<path d="m16 3 5 5-12 12-6 1 1-6L16 3ZM13 6l5 5"/>',
  archive:'<path d="M4 8v12h16V8M3 3h18v5H3V3Zm6 9h6"/>',
  search:'<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/>',
  filter:'<path d="M3 5h18l-7 8v6l-4 2v-8L3 5Z"/>',
  calendar:'<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M16 3v4M8 3v4M3 11h18M8 15h.01M12 15h.01M16 15h.01"/>',
  bolt:'<path d="m13 2-9 12h7l-1 8 10-12h-7l1-8Z"/>',
  code:'<path d="m8 7-5 5 5 5m8-10 5 5-5 5M14 4l-4 16"/>',
  shield:'<path d="m12 3 8 3v6c0 5-8 9-8 9s-8-4-8-9V6l8-3Z"/><path d="m8 12 3 3 5-6"/>',
  monitor:'<rect x="3" y="3" width="18" height="14" rx="2"/><path d="M8 21h8m-4-4v4"/>',
  sparkles:'<path d="m12 3 2.5 6.5L21 12l-6.5 2.5L12 21l-2.5-6.5L3 12l6.5-2.5L12 3Z"/>',
  info:'<circle cx="12" cy="12" r="9"/><path d="M12 11v6m0-10h.01"/>',
  link:'<path d="m10 13 4-4M8 15l-2 2a3 3 0 0 1-4-4l5-5a3 3 0 0 1 4 0m2 1 2-2a3 3 0 0 1 4 4l-5 5a3 3 0 0 1-4 0"/>',
};
export function icon(name, size=20) {
  return `<svg width="${Number(size)||20}" height="${Number(size)||20}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${icons[name] || icons.activity}</svg>`;
}

export class ApiError extends Error {
  constructor(message, status=0) {super(message);this.name='ApiError';this.status=status;}
}
export async function api(path, {method='GET',body,signal}={}) {
  const controller=new AbortController();
  const abort=()=>controller.abort();
  if(signal?.aborted)controller.abort();
  signal?.addEventListener('abort',abort,{once:true});
  const timer=setTimeout(()=>controller.abort(),15000);
  try {
    const response=await fetch(path,{method,headers:body===undefined?{'Accept':'application/json'}:{'Accept':'application/json','Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body),signal:controller.signal,cache:'no-store'});
    const type=response.headers.get('content-type')||'';
    let data;
    if(type.includes('application/json'))data=await response.json();
    if(!response.ok)throw new ApiError(data?.error || `Não foi possível concluir a operação (HTTP ${response.status}).`,response.status);
    if(data===undefined)throw new ApiError('O servidor respondeu em um formato inesperado.');
    return data;
  } catch(error) {
    if(signal?.aborted)throw error;
    if(error instanceof ApiError)throw error;
    throw new ApiError(controller.signal.aborted?'O servidor demorou para responder. Tente novamente.':'Não foi possível conectar ao servidor. Confira se a central está em execução.');
  } finally {clearTimeout(timer);signal?.removeEventListener('abort',abort);}
}

export class RequestGate {
  #version=0;
  begin(){return ++this.#version;}
  current(version){return version===this.#version;}
  invalidate(){this.#version++;}
}

export const statusNames={pending:'Na fila',running:'Em execução',succeeded:'Concluída',failed:'Falhou',canceled:'Cancelada'};
export const originNames={manual:'Manual',scheduled:'Agendada',retry:'Reexecução'};
export function statusName(run){return run?.status==='running'&&run.cancel_requested_at?'Cancelando':statusNames[typeof run==='string'?run:run?.status]||'Desconhecida';}
export function badge(run){const status=typeof run==='string'?run:run?.status;return `<span class="badge badge-${esc(status||'unknown')}"><span class="badge-dot"></span>${esc(statusName(run))}</span>`;}
export function automationStatus(a){if(a.archived_at)return {key:'archived',label:'Arquivada'};if(!a.enabled)return {key:'disabled',label:'Desabilitada'};if(a.schedule?.paused)return {key:'paused',label:'Agenda pausada'};return {key:'enabled',label:'Ativa'};}
export function automationBadge(a){const s=automationStatus(a);return `<span class="badge badge-${s.key}"><span class="badge-dot"></span>${s.label}</span>`;}
export function formatNumber(value){return new Intl.NumberFormat('pt-BR').format(Number(value)||0);}
export function formatDate(value,{short=false,timeOnly=false}={}) {
  if(!value)return '—';const date=new Date(value);if(Number.isNaN(date.getTime()))return '—';
  return new Intl.DateTimeFormat('pt-BR',timeOnly?{hour:'2-digit',minute:'2-digit'}:short?{day:'2-digit',month:'short',hour:'2-digit',minute:'2-digit'}:{dateStyle:'short',timeStyle:'short'}).format(date);
}
export function duration(start,end){
  if(!start||!end)return '—';const ms=new Date(end)-new Date(start);if(!Number.isFinite(ms)||ms<0)return '—';
  if(ms<1000)return `${Math.round(ms)} ms`;if(ms<60000)return `${(ms/1000).toFixed(1).replace('.',',')} s`;const m=Math.floor(ms/60000);return `${m} min ${Math.floor(ms%60000/1000)} s`;
}
export function scheduleLabel(schedule){
  if(!schedule)return 'Execução manual';
  if(schedule.kind==='cron')return schedule.cron;
  const n=Number(schedule.interval_seconds);let value=n;let unit='s';
  if(n%86400===0){value=n/86400;unit=value===1?'dia':'dias';}else if(n%3600===0){value=n/3600;unit='h';}else if(n%60===0){value=n/60;unit='min';}
  return `A cada ${value} ${unit}`;
}
export function localInputToISO(value){if(!value)return '';const d=new Date(value);if(Number.isNaN(d.getTime()))throw new Error('Informe uma data válida.');return d.toISOString();}
export function isoToLocalInput(value){if(!value)return '';const d=new Date(value);if(Number.isNaN(d.getTime()))return '';const p=n=>String(n).padStart(2,'0');return `${d.getFullYear()}-${p(d.getMonth()+1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;}
export function canExecute(a,active={}){return Boolean(a&&!a.archived_at&&a.enabled&&(a.overlap_policy!=='forbid'||!(active.pending||active.running)));}
export function canRetry(run,a,active={}){return ['failed','canceled'].includes(run?.status)&&canExecute(a,active);}
export function canCancel(run){return ['pending','running'].includes(run?.status)&&!run.cancel_requested_at;}
