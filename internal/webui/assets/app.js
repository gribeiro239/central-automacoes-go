import {
  api,
  esc,
  icon,
  RequestGate,
  badge,
  automationBadge,
  automationStatus,
  formatNumber as num,
  formatDate as date,
  duration,
  scheduleLabel,
  canExecute,
  canRetry,
  canCancel,
  originNames,
  statusNames,
  localInputToISO
} from './ui.js';
import {
  openAutomationForm
} from './form.js';

const $ = s => document.querySelector(s);
const view = $('#view'),
  gate = new RequestGate(),
  timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
let controller,
  route,
  data,
  loading = false,
  search = '',
  autoState = 'all',
  includeArchived = false,
  page = 0,
  cutoff = new Date().toISOString();
const busy = new Set();
let filters = {
  automation_id: '',
  status: '',
  from: '',
  to: ''
};
let historyOpened = false,
  filterDraft = null;

function persistRuns() {
  const q = new URLSearchParams({
    ...filters,
    page: String(page),
    cutoff
  });
  history.replaceState(null, '', `#/runs?${q}`);
}

function editing() {
  return Boolean(document.querySelector('dialog[open]') || view.contains(document.activeElement) && document.activeElement.matches('input,select,textarea'));
}

function render() {
  const focused = document.activeElement;
  const actionKey = view.contains(focused) && focused.dataset.action;
  const targetId = focused?.dataset.id;
  const href = view.contains(focused) && focused.getAttribute('href');
  const focusedId = view.contains(focused) && focused.id;
  const expanded = [...view.querySelectorAll('details')].map(el => el.open);
  renderContent();
  view.querySelectorAll('details').forEach((el, i) => {
    if (expanded[i]) el.open = true;
  });
  if (actionKey) view.querySelector(`[data-action="${CSS.escape(actionKey)}"][data-id="${CSS.escape(targetId||'')}"]`)?.focus({
    preventScroll: true
  });
  else if (focusedId) view.querySelector(`#${CSS.escape(focusedId)}`)?.focus({
    preventScroll: true
  });
  else if (href) view.querySelector(`a[href="${CSS.escape(href)}"]`)?.focus({
    preventScroll: true
  });
}

const idPath = id => encodeURIComponent(id);
const button = (action, id, label, kind = 'secondary', disabled = false) => `<button type="button" class="btn btn-${kind} btn-small" data-action="${action}" data-id="${esc(id)}" ${disabled?'disabled':''}>${esc(label)}</button>`;
const empty = (title, text, action = '') => `<div class="empty-state"><span class="empty-icon">${icon('layers',26)}</span><h3>${esc(title)}</h3><p>${esc(text)}</p>${action}</div>`;
const panel = (title, body, extra = '') => `<section class="panel"><div class="panel-header"><h2>${title}</h2>${extra}</div>${body}</section>`;
const json = value => esc(JSON.stringify(value ?? {}, null, 2));
const active = a => data?.dashboard.active_by_automation[a.id] || {};
const findAuto = id => data?.automations.find(a => a.id === id);

function toast(message, error = false) {
  const el = document.createElement('div');
  el.className = `toast${error?' error':''}`;
  el.innerHTML = `${icon(error?'alert':'checkCircle')}<span>${esc(message)}</span><button class="btn btn-icon" aria-label="Fechar aviso">${icon('close')}</button>`;
  el.querySelector('button').onclick = () => el.remove();
  $('#toasts').append(el);
  setTimeout(() => el.remove(), 7000);
}

function connection(online, storage = '') {
  $('#connection-status').innerHTML = `<span class="connection-dot ${online?'online':'offline'}"></span>${online?'Sistema online':'Sem conexão'}`;
  $('#sidebar-dot').className = `connection-dot ${online?'online':'offline'}`;
  $('#sidebar-storage').innerHTML = `${online?(storage==='postgres'?'PostgreSQL conectado':'Memória local'):'Servidor indisponível'}<small>Execução local</small>`;
}

function parseRoute() {
  const [path, query = ''] = (location.hash.slice(1) || '/').split('?');
  const parts = path.split('/').filter(Boolean);
  return {
    page: parts[0] === 'automations' ? 'automations' : parts[0] === 'runs' ? 'runs' : 'overview',
    id: parts[1] || '',
    query: new URLSearchParams(query)
  };
}

function heading() {
  const names = {
    overview: ['SEU CENTRO DE OPERAÇÕES', 'Visão geral', 'Uma visão clara de tudo o que acontece nas suas automações.'],
    automations: ['ROTINAS QUE TRABALHAM POR VOCÊ', route.id ? 'Detalhes da automação' : 'Automações', route.id ? 'Configuração, agenda e histórico em um só lugar.' : 'Organize suas rotinas e deixe o trabalho repetitivo com a central.'],
    runs: ['ACOMPANHE CADA ETAPA', route.id ? 'Detalhes da execução' : 'Execuções', route.id ? 'Veja o resultado e o histórico de cada tentativa.' : 'Da fila ao resultado: acompanhe o que aconteceu em cada execução.']
  };
  const [eyebrow, title, description] = names[route.page];
  $('#page-eyebrow').textContent = eyebrow;
  $('#page-title').textContent = title;
  $('#page-description').textContent = description;
  $('#breadcrumb-current').textContent = title;
  document.title = `${title} · Central`;
  document.querySelectorAll('#main-nav a').forEach(a => {
    const selected = a.dataset.page === route.page;
    a.classList.toggle('active', selected);
    if (selected) a.setAttribute('aria-current', 'page');
    else a.removeAttribute('aria-current');
  });
}

function rows(runs) {
  if (!runs.length) return empty('Nenhuma execução por aqui', 'Execute uma automação para acompanhar o primeiro resultado.');
  return `<div class="table-scroll"><table><thead><tr><th>Automação</th><th>Status</th><th>Origem</th><th>Criada em</th><th>Tentativas</th><th></th></tr></thead><tbody>${runs.map(r=>`<tr><td><a class="table-name-link" href="#/runs/${idPath(r.id)}">${esc(findAuto(r.automation_id)?.name||r.snapshot?.name||'Automação')}</a><div class="row-subtitle mono">${esc(r.id.slice(0,12))}</div></td><td>${badge(r)}</td><td>${esc(originNames[r.origin]||r.origin)}</td><td class="nowrap">${date(r.created_at,{short:true})}</td><td>${num(r.attempt_count)}</td><td><a class="link-button" href="#/runs/${idPath(r.id)}" aria-label="Ver execução ${esc(r.id)}">${icon('arrow',17)}</a></td></tr>`).join('')}</tbody></table></div>`;
}

function chart(d) {
  const max = Math.max(1, ...d.days.map(day => day.total));
  const bars = d.days.map((day, i) => {
    const x = 58 + i * 76;
    const h = day.total / max * 130;
    const failed = day.failed / max * 130;
    const other = (day.total - day.succeeded - day.failed) / max * 130;
    return `<g><title>${esc(day.date)}: ${day.total} execuções, ${day.succeeded} concluídas, ${day.failed} falhas</title><rect class="chart-bar" x="${x}" y="${160-h}" width="25" height="${h}" rx="4"/><rect class="chart-bar-error" x="${x}" y="${160-failed}" width="25" height="${failed}"/><rect class="chart-bar-other" x="${x}" y="${160-failed-other}" width="25" height="${other}"/><text class="chart-label" x="${x+12}" y="184" text-anchor="middle">${esc(day.date.slice(8)+'/'+day.date.slice(5,7))}</text><text class="chart-label" x="${x+12}" y="${150-h}" text-anchor="middle">${day.total||''}</text></g>`;
  }).join('');
  return `<div class="chart-wrap"><svg class="activity-chart" viewBox="0 0 600 200" role="img" aria-label="Execuções criadas nos últimos sete dias; total ${num(d.stats.total_runs)}"><path class="chart-grid" d="M35 30H580M35 95H580M35 160H580"/><text class="chart-axis" x="8" y="34">${max}</text><text class="chart-axis" x="8" y="164">0</text>${bars}</svg></div>`;
}

function overview() {
  const d = data.dashboard, s = d.stats;
  const metrics = [['Automações ativas', num(s.active_automations), `${num(s.automations)} automações no workspace`, 'layers', ''], ['Execuções no período', num(s.total_runs), 'Criadas nos últimos 7 dias', 'activity', ''], ['Taxa de sucesso', s.success_rate === null ? '—' : `${s.success_rate.toFixed(1).replace('.',',')}%`, 'Concluídas ÷ (concluídas + falhas)', 'checkCircle', ''], ['Falhas no período', num(s.failed_runs), 'Execuções que precisam de atenção', 'alert', 'amber']];
  return `<div class="metrics-grid">${metrics.map(([title,value,caption,i,c])=>`<article class="metric-card"><div class="metric-label">${title}<span class="metric-icon ${c}">${icon(i,18)}</span></div><div class="metric-value">${value}</div><p class="metric-caption">${caption}</p></article>`).join('')}</div><div class="overview-grid">${panel('Atividade das execuções',chart(d)+`<div class="panel-foot">7 dias · ${esc(timezone)} · ${date(d.range.from,{short:true})} a ${date(d.range.to,{short:true})}</div>`,`<div class="legend"><span><i class="legend-dot"></i>Concluídas</span><span><i class="legend-dot red"></i>Falhas</span><span><i class="legend-dot gray"></i>Outras</span></div>`)}${panel('Próximas execuções',d.upcoming.length?`<div class="upcoming-list">${d.upcoming.map(a=>`<a class="upcoming-row" href="#/automations/${idPath(a.id)}"><span class="upcoming-icon">${icon('clock',18)}</span><span class="grow"><strong>${esc(a.name)}</strong><small>${esc(scheduleLabel(a.schedule))}</small></span><span class="upcoming-time">${date(a.next_run_at,{timeOnly:true})}<small>${esc(new Intl.DateTimeFormat('pt-BR',{day:'2-digit',month:'2-digit'}).format(new Date(a.next_run_at)))}</small></span></a>`).join('')}</div>`:empty('Agenda livre','As próximas rotinas agendadas aparecerão aqui.'),`<span class="tag">${num(s.scheduled_automations)} agendas ativas</span>`)}</div><div class="notice">${icon('activity',18)}<span><strong>${num(s.running_runs)}</strong> em execução e <strong>${num(s.pending_runs)}</strong> na fila, considerando todo o histórico.</span><a class="link-button" href="#/runs">Acompanhar ${icon('arrow',15)}</a></div>${panel('Execuções recentes',rows(d.recent_runs),'<a class="link-button" href="#/runs">Ver histórico '+icon('arrow',15)+'</a>')}`;
}

function automationActions(a) {
  const disabled = a.archived_at;
  return `<div class="table-actions">${button('execute',a.id,'Executar','primary',!canExecute(a,active(a)))}${button('edit',a.id,'Editar','secondary',!!disabled)}${a.schedule&&!disabled?button(a.schedule.paused?'resume':'pause',a.id,a.schedule.paused?'Retomar agenda':'Pausar agenda'):''}${!disabled?button('toggle',a.id,a.enabled?'Desabilitar':'Habilitar'):''}${!disabled?button('archive',a.id,'Arquivar','ghost'):''}</div>`;
}

function automationList() {
  const items = data.automations.filter(a => (includeArchived || !a.archived_at) && (autoState === 'all' || automationStatus(a).key === autoState) && `${a.name} ${a.config.url}`.toLocaleLowerCase().includes(search.toLocaleLowerCase()));
  return `<section class="panel"><div class="toolbar"><label class="search-field">${icon('search',17)}<input id="automation-search" aria-label="Buscar automações" placeholder="Buscar nome ou endereço…" value="${esc(search)}"></label><select id="automation-state" aria-label="Filtrar estado">${[['all','Todos os estados'],['enabled','Ativas'],['disabled','Desabilitadas'],['paused','Agenda pausada'],['archived','Arquivadas']].map(([v,l])=>`<option value="${v}" ${autoState===v?'selected':''}>${l}</option>`).join('')}</select><label class="checkbox-label"><input id="include-archived" type="checkbox" ${includeArchived?'checked':''}> Incluir arquivadas</label></div>${items.length?`<div class="table-scroll"><table><thead><tr><th>Automação</th><th>Estado</th><th>Agenda</th><th>Próxima execução</th><th>Ações</th></tr></thead><tbody>${items.map(a=>`<tr><td><a class="table-name-link" href="#/automations/${idPath(a.id)}">${esc(a.name)}</a><div class="row-subtitle"><span class="method">${esc(a.config.method)}</span> ${esc(a.config.url)}</div></td><td>${automationBadge(a)}</td><td>${esc(scheduleLabel(a.schedule))}</td><td class="nowrap">${date(a.next_run_at,{short:true})}</td><td>${automationActions(a)}</td></tr>`).join('')}</tbody></table></div>`:empty('Nenhuma automação encontrada','Crie sua primeira rotina ou ajuste os filtros.',button('new','','Nova automação','primary'))}<div class="panel-foot">${num(items.length)} automações encontradas</div></section>`;
}
const definition = (pairs) => `<dl class="definition-list">${pairs.map(([k,v])=>`<div class="definition-row"><dt>${esc(k)}</dt><dd>${v}</dd></div>`).join('')}</dl>`;

function automationDetail() {
  const a = data.item;
  return `<a class="detail-breadcrumb" href="#/automations">${icon('chevronLeft',15)} Todas as automações</a><section class="panel detail-banner"><div><h2>${esc(a.name)}</h2><p class="mono row-subtitle">${esc(a.id)}</p></div>${automationBadge(a)}${automationActions(a)}</section><div class="detail-grid"><div class="stack">${panel('Requisição HTTP',definition([['Método',esc(a.config.method)],['URL',`<span class="mono">${esc(a.config.url)}</span>`],['Timeout',`${a.timeout_seconds} segundos`],['Tentativas máximas',num(a.max_attempts)],['Concorrência',a.overlap_policy==='forbid'?'Uma execução por vez':'Permitir simultâneas']])+`<details class="snapshot-details"><summary>Headers e corpo da requisição</summary><pre class="code-block">${json(a.config)}</pre></details>`)}${panel('Histórico da automação',rows(data.runs),`<a class="link-button" href="#/runs?automation_id=${idPath(a.id)}">Ver todas ${icon('arrow',15)}</a>`)}</div><div class="stack">${panel('Agendamento',definition([['Frequência',esc(scheduleLabel(a.schedule))],['Fuso horário',esc(a.schedule?.timezone||'—')],['Próxima execução',date(a.next_run_at)],['Estado',a.schedule?(a.schedule.paused?'Pausada':'Ativa'):'Somente manual'],['Execuções atrasadas',esc(a.schedule?.misfire_policy||'—')]]))}${panel('Política de tentativas',definition([['Política',a.retry_policy?'Personalizada':'Padrão da central'],['Espera inicial',a.retry_policy?`${a.retry_policy.base_delay_seconds} segundos`:'Padrão'],['Espera máxima',a.retry_policy?`${a.retry_policy.max_delay_seconds} segundos`:'Padrão'],['Criada em',date(a.created_at)],['Arquivada em',date(a.archived_at)]]))}</div></div>`;
}

function runList() {
  const draft = filterDraft || filters;
  return `<section class="panel"><form id="run-filters" class="filters-grid"><div class="field"><label for="filter-automation">Automação</label><select id="filter-automation" name="automation_id"><option value="">Todas as automações</option>${data.automations.map(a=>`<option value="${esc(a.id)}" ${draft.automation_id===a.id?'selected':''}>${esc(a.name)}${a.archived_at?' (arquivada)':''}</option>`).join('')}</select></div><div class="field"><label for="filter-status">Status</label><select id="filter-status" name="status"><option value="">Todos os status</option>${Object.entries(statusNames).map(([v,l])=>`<option value="${v}" ${draft.status===v?'selected':''}>${l}</option>`).join('')}</select></div><div class="field"><label for="filter-from">Criadas a partir de</label><input id="filter-from" name="from" type="datetime-local" value="${esc(draft.from)}"></div><div class="field"><label for="filter-to">Criadas até</label><input id="filter-to" name="to" type="datetime-local" value="${esc(draft.to)}"></div><button class="btn btn-primary" id="apply-filters" type="submit">Aplicar filtros</button></form>${rows(data.runs.slice(0,25))}<div class="pagination"><span>Página ${page+1} · ${data.runs.slice(0,25).length} resultados<br><small>Novas execuções entram ao atualizar. Status continuam sendo acompanhados.</small></span><div class="pagination-actions">${button('previous','','Anterior','secondary',page===0)}${button('next','','Próxima','secondary',data.runs.length<26)}</div></div></section>`;
}

function resultBlock(result) {
  if (!result) return '<p class="muted">Aguardando resultado.</p>';
  return `${definition([['Resposta HTTP',esc(result.status_code||'—')],['Nova tentativa',result.retryable?'Permitida pelo resultado':'Não indicada pelo resultado']])}${result.error?`<div class="attempt-error">${esc(result.error)}</div>`:''}${result.truncated?'<div class="notice">Resposta limitada a uma prévia de 4 KiB.</div>':''}<pre class="code-block">${esc(result.body_preview||'(sem corpo de resposta)')}</pre>`;
}

function runDetail() {
  const r = data.item, a = findAuto(r.automation_id);
  return `<a class="detail-breadcrumb" href="#/runs">${icon('chevronLeft',15)} Todas as execuções</a><section class="panel detail-banner"><div><h2>${esc(r.snapshot?.name||a?.name||'Execução')}</h2><p class="run-id">${esc(r.id)}</p></div>${badge(r)}<div class="detail-actions">${button('cancel',r.id,r.cancel_requested_at?'Cancelamento solicitado':'Cancelar execução','danger',!canCancel(r))}${button('retry',r.id,'Reexecutar','primary',!canRetry(r,a,a?active(a):{}))}</div></section><div class="detail-grid"><div class="stack">${panel('Resultado',`<div class="panel-body">${resultBlock(r.result)}</div>`)}${panel(`Tentativas (${data.attempts.length})`,data.attempts.length?`<div class="attempt-list">${data.attempts.map(t=>`<article class="attempt-card"><div class="attempt-meta"><strong>Tentativa ${t.number}</strong><span>${duration(t.started_at,t.finished_at)}</span></div><p class="attempt-times">${date(t.started_at)} → ${date(t.finished_at)}</p><p class="attempt-worker">Worker: ${esc(t.worker_id)}</p>${resultBlock(t.result)}</article>`).join('')}</div>`:empty('Aguardando o worker','A primeira tentativa aparecerá quando a execução começar.'))}</div><div class="stack">${panel('Sobre esta execução',definition([['Automação',`<a class="link-button" href="#/automations/${idPath(r.automation_id)}">Ver automação ${icon('arrow',14)}</a>`],['Origem',esc(originNames[r.origin]||r.origin)],['Criada em',date(r.created_at)],['Disponível em',date(r.available_at)],['Finalizada em',date(r.finished_at)],['Tempo total na central',duration(r.created_at,r.finished_at)],['Agendada para',date(r.scheduled_for)],['Execução original',r.parent_run_id?`<a class="link-button mono" href="#/runs/${idPath(r.parent_run_id)}">${esc(r.parent_run_id)}</a>`:'—']]))}${panel('Configuração preservada',`<div class="panel-body"><p class="help">Cada execução usa a configuração registrada ao entrar na fila. Reexecutar mantém essa configuração original.</p><pre class="code-block">${json(r.snapshot)}</pre></div>`)}</div></div>`;
}

function renderContent() {
  view.innerHTML = route.page === 'overview' ? overview() : route.id ? (route.page === 'automations' ? automationDetail() : runDetail()) : route.page === 'automations' ? automationList() : runList();
  view.setAttribute('aria-busy', 'false');
  $('#nav-count').textContent = data.dashboard.stats.automations;
  $('#updated-at').textContent = `Atualizado às ${date(data.dashboard.generated_at,{timeOnly:true})}`;
  for (const el of view.querySelectorAll('[data-action]')) if (busy.has(`${el.dataset.action}:${el.dataset.id}`)) el.disabled = true;
}

function queryRuns() {
  const q = new URLSearchParams({
    limit: '26',
    offset: String(page * 25),
    to: filters.to ? new Date(Math.min(new Date(localInputToISO(filters.to)), new Date(cutoff))).toISOString() : cutoff
  });
  if (filters.from) q.set('from', localInputToISO(filters.from));
  if (filters.status) q.set('status', filters.status);
  if (filters.automation_id) q.set('automation_id', filters.automation_id);
  return q;
}

async function load(silent = false, force = false) {
  if (loading && silent && !force) return;
  const version = gate.begin();
  controller?.abort();
  controller = new AbortController();
  const signal = controller.signal;
  loading = true;
  if (!silent) {
    view.setAttribute('aria-busy', 'true');
    view.innerHTML = '<div class="loading-state"><span class="spinner"></span><p>Carregando dados da central…</p></div>';
  }
  try {
    const [health, automations, dashboard] = await Promise.all([api('/health', {
      signal
    }), api('/automations?include_archived=true', {
      signal
    }), api(`/dashboard?timezone=${encodeURIComponent(timezone)}`, {
      signal
    })]);
    let item, runs = [], attempts = [];
    if (route.id) {
      item = await api(`/${route.page}/${idPath(route.id)}`, {
        signal
      });
      if (route.page === 'automations') runs = await api(`/runs?automation_id=${idPath(route.id)}&limit=8`, {
        signal
      });
      else attempts = await api(`/runs/${idPath(route.id)}/attempts`, {
        signal
      });
    } else if (route.page === 'runs') runs = await api(`/runs?${queryRuns()}`, {
      signal
    });
    if (!gate.current(version)) return;
    if (silent && editing()) return;
    data = {
      health,
      automations,
      dashboard,
      item,
      runs,
      attempts
    };
    connection(true, health.storage);
    $('#page-error').innerHTML = '';
    render();
  } catch (error) {
    if (!gate.current(version) || signal.aborted) return;
    connection(false);
    $('#page-error').innerHTML = `<div class="error-banner" role="alert">${icon('alert')}<span>${esc(error.message)}${silent?' Os dados abaixo podem estar desatualizados.':''}</span><button class="btn btn-small btn-secondary" data-action="refresh">Tentar novamente</button></div>`;
    if (!silent) view.innerHTML = empty('Não foi possível carregar esta página', 'Tente atualizar para buscar os dados novamente.');
    view.setAttribute('aria-busy', 'false');
  } finally {
    if (gate.current(version)) loading = false;
  }
}

function navigate() {
  route = parseRoute();
  gate.invalidate();
  controller?.abort();
  data = null;
  loading = false;
  heading();
  if (route.page === 'runs' && !route.id) {
    if (route.query.size) {
      const next = {
        automation_id: route.query.get('automation_id') || '',
        status: route.query.get('status') || '',
        from: route.query.get('from') || '',
        to: route.query.get('to') || ''
      };
      if (Object.keys(filters).some(key => filters[key] !== next[key])) filterDraft = null;
      filters = next;
      page = Math.max(0, Number.parseInt(route.query.get('page'), 10) || 0);
      const savedCutoff = route.query.get('cutoff');
      cutoff = savedCutoff && !Number.isNaN(Date.parse(savedCutoff)) ? savedCutoff : new Date().toISOString();
    } else if (!historyOpened) {
      cutoff = new Date().toISOString();
    }
    historyOpened = true;
    persistRuns();
  }
  load();
}

function openForm(a = null) {
  openAutomationForm({
    automation: a,
    onSaved: async (saved) => {
      toast(a ? 'Alterações salvas.' : 'Automação criada.');
      if (a) await load(true, true);
      else location.hash = `/automations/${idPath(saved.id)}`;
    }
  });
}

async function confirmCancel() {
  return new Promise(resolve => {
    const previous = document.activeElement;
    const dialog = document.createElement('dialog');
    dialog.className = 'dialog confirm-dialog';
    dialog.setAttribute('aria-labelledby', 'cancel-title');
    dialog.innerHTML = '<div class="dialog-header"><h2 id="cancel-title">Cancelar esta execução?</h2></div><div class="dialog-scroll">O worker interromperá a tentativa em andamento. Efeitos já realizados no destino não podem ser desfeitos. O histórico será preservado.</div><div class="dialog-footer"><button class="btn btn-secondary" data-answer="no">Voltar</button><button class="btn btn-danger" data-answer="yes">Confirmar cancelamento</button></div>';
    document.body.append(dialog);
    dialog.addEventListener('click', e => {
      if (e.target.dataset.answer) dialog.close(e.target.dataset.answer);
    });
    dialog.addEventListener('close', () => {
      const yes = dialog.returnValue === 'yes';
      dialog.remove();
      previous?.focus();
      resolve(yes);
    }, {
      once: true
    });
    dialog.showModal();
    dialog.querySelector('button').focus();
  });
}

async function confirmArchive(a) {
  return new Promise(resolve => {
    const previous = document.activeElement;
    const dialog = document.createElement('dialog');
    dialog.className = 'dialog confirm-dialog';
    dialog.setAttribute('aria-labelledby', 'confirm-title');
    dialog.innerHTML = `<div class="dialog-header"><h2 id="confirm-title">Arquivar automação?</h2></div><div class="dialog-scroll">${esc(a.name)} será desabilitada. O histórico será mantido e execuções já na fila continuarão.</div><div class="dialog-footer"><button class="btn btn-secondary" data-answer="no">Voltar</button><button class="btn btn-danger" data-answer="yes">Arquivar</button></div>`;
    document.body.append(dialog);
    dialog.addEventListener('click', e => {
      if (e.target.dataset.answer) dialog.close(e.target.dataset.answer);
    });
    dialog.addEventListener('close', () => {
      const yes = dialog.returnValue === 'yes';
      dialog.remove();
      previous?.focus();
      resolve(yes);
    }, {
      once: true
    });
    dialog.showModal();
    dialog.querySelector('button').focus();
  });
}

async function action(name, id) {
  if (name === 'new') {
    openForm();
    return;
  }
  if (name === 'refresh') {
    page = 0;
    cutoff = new Date().toISOString();
    if (route.page === 'runs' && !route.id) persistRuns();
    await load();
    return;
  }
  if (name === 'next' || name === 'previous') {
    page += name === 'next' ? 1 : -1;
    persistRuns();
    await load();
    return;
  }
  const a = findAuto(id);
  if (name === 'edit') {
    if (a) openForm(a);
    return;
  }
  const key = `${name}:${id}`;
  if (busy.has(key)) return;
  busy.add(key);
  const locationAtStart = location.hash;
  try {
    if (name === 'archive' && !await confirmArchive(a)) return;
    if (name === 'cancel' && !await confirmCancel()) return;
    render();
    let path, method = 'POST', body;
    if (['retry', 'cancel'].includes(name)) path = `/runs/${idPath(id)}/${name}`;
    else if (name === 'toggle') {
      path = `/automations/${idPath(id)}`;
      method = 'PATCH';
      body = {
        enabled: !a.enabled
      };
    } else path = `/automations/${idPath(id)}/${name==='execute'?'runs':name==='pause'||name==='resume'?`schedule/${name}`:name}`;
    const result = await api(path, {
      method,
      body
    });
    toast(name === 'cancel' ? 'Solicitação de cancelamento recebida.' : name === 'execute' ? 'Execução adicionada à fila.' : name === 'retry' ? 'Nova execução criada com a configuração original.' : 'Automação atualizada.');
    if (location.hash === locationAtStart) {
      if (name === 'execute' || name === 'retry') location.hash = `/runs/${idPath(result.id)}`;
      else await load(true, true);
    }
  } catch (error) {
    toast(error.message, true);
    if (location.hash === locationAtStart) await load(true, true);
  } finally {
    busy.delete(key);
    if (data && location.hash === locationAtStart) render();
  }
}
document.querySelectorAll('[data-icon]').forEach(el => el.innerHTML = icon(el.dataset.icon));
$('#new-automation').onclick = () => openForm();
$('#refresh-button').onclick = () => action('refresh', '');
document.querySelector('.skip-link').onclick = e => {
  e.preventDefault();
  $('#main-content').focus();
};
document.addEventListener('click', e => {
  const el = e.target.closest('[data-action]');
  if (el && !el.disabled) action(el.dataset.action, el.dataset.id || '');
});
for (const event of ['input', 'change']) view.addEventListener(event, e => {
  if (e.target.closest('#run-filters')) filterDraft = Object.fromEntries(new FormData($('#run-filters')));
});
view.addEventListener('input', e => {
  if (e.target.id === 'automation-search') {
    search = e.target.value;
    const start = e.target.selectionStart;
    render();
    const input = $('#automation-search');
    input.focus();
    input.setSelectionRange(start, start);
  }
});
view.addEventListener('change', e => {
  if (e.target.id === 'automation-state') {
    autoState = e.target.value;
    render();
    $('#automation-state').focus();
  }
  if (e.target.id === 'include-archived') {
    includeArchived = e.target.checked;
    render();
    $('#include-archived').focus();
  }
});
view.addEventListener('submit', e => {
  if (e.target.id !== 'run-filters') return;
  e.preventDefault();
  const values = Object.fromEntries(new FormData(e.target));
  try {
    const from = localInputToISO(values.from), to = localInputToISO(values.to);
    if (from && to && from > to) throw new Error('A data inicial deve ser anterior à data final.');
    filters = values;
    filterDraft = null;
    page = 0;
    cutoff = new Date().toISOString();
    persistRuns();
    load();
  } catch (error) {
    toast(error.message, true);
  }
});
window.addEventListener('hashchange', navigate);
navigate();
setInterval(() => {
  if (document.visibilityState === 'hidden' || document.querySelector('dialog[open]') || editing()) return;
  load(true);
}, 3000);
