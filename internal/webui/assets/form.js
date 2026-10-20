import { api, esc, icon } from './ui.js';
import { buildPayload, initialValues, FormValidationError } from './form-model.js';

export function openAutomationForm({ automation = null, onSaved = async () => {} } = {}) {
  if (document.getElementById('automation-dialog')) return document.getElementById('automation-dialog');
  const previousFocus = document.activeElement;
  const editing = Boolean(automation);
  const values = initialValues(automation);
  const dialog = document.createElement('dialog');
  dialog.id = 'automation-dialog';
  dialog.className = 'dialog';
  dialog.setAttribute('aria-labelledby', 'automation-dialog-title');
  dialog.innerHTML = `
    <form novalidate>
      <header class="dialog-header">
        <div><span class="section-label">CONFIGURAÇÃO HTTP</span><h2 id="automation-dialog-title">${esc(editing ? 'Editar automação' : 'Nova automação')}</h2><p class="help">Defina a chamada e como ela deve ser executada.</p></div>
        <button type="button" class="btn btn-ghost" data-dismiss aria-label="Fechar formulário">${icon('close')}</button>
      </header>
      <div class="dialog-scroll">
        <div class="error-box" role="alert" tabindex="-1" hidden></div>
        <section class="form-section">
          <div class="form-grid">
            <div class="field field-wide"><label for="auto-name">Nome da automação</label><input id="auto-name" name="name" placeholder="Ex.: Verificar integração" autocomplete="off" required><span class="help">Um nome que ajude a reconhecer esta tarefa no histórico.</span></div>
            <div class="field"><label for="auto-method">Método HTTP</label><select id="auto-method" name="method">${['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'].map(method => `<option value="${method}">${method}</option>`).join('')}</select></div>
            <div class="field"><label for="auto-url">URL de destino</label><input id="auto-url" name="url" type="url" placeholder="https://api.exemplo.com/health" autocomplete="off" spellcheck="false" required></div>
          </div>
          <label class="switch-row" for="auto-enabled"><input id="auto-enabled" name="enabled" type="checkbox"><span><strong>Automação habilitada</strong><span class="help">Permite novas execuções manuais e agendadas.</span></span></label>
        </section>
        <section class="form-section">
          <div class="section-label">AGENDAMENTO</div>
          <div class="field"><label for="auto-schedule-kind">Quando executar</label><select id="auto-schedule-kind" name="schedule_kind"><option value="none">Somente manualmente</option><option value="interval">Em intervalos regulares</option><option value="cron">Por expressão cron</option></select></div>
          <div data-schedule-details>
            <div class="form-grid" data-interval-fields>
              <div class="field"><label for="auto-interval-value">Executar a cada</label><input id="auto-interval-value" name="interval_value" type="number" min="1" step="1" inputmode="numeric"></div>
              <div class="field"><label for="auto-interval-unit">Unidade do intervalo</label><select id="auto-interval-unit" name="interval_unit"><option value="seconds">Segundos</option><option value="minutes">Minutos</option><option value="hours">Horas</option><option value="days">Dias</option></select></div>
            </div>
            <div class="form-grid" data-cron-fields>
              <div class="field"><label for="auto-cron">Expressão cron</label><input id="auto-cron" name="cron" placeholder="0 8 * * *" spellcheck="false"><span class="help">Minuto · hora · dia · mês · semana. Ex.: 0 8 * * * para 08h.</span></div>
              <div class="field"><label for="auto-timezone">Fuso horário IANA</label><input id="auto-timezone" name="timezone" placeholder="America/Sao_Paulo" spellcheck="false"><span class="help">O cron é interpretado neste fuso; a API valida a expressão completa.</span></div>
            </div>
            <label class="switch-row" for="auto-schedule-paused"><input id="auto-schedule-paused" name="schedule_paused" type="checkbox"><span><strong>Agendamento pausado</strong><span class="help">A pausa mantém a execução manual disponível.</span></span></label>
            <div class="form-grid">
              <div class="field"><label for="auto-misfire-policy">Se o horário passar</label><select id="auto-misfire-policy" name="misfire_policy"><option value="run_once">Executar uma vez ao recuperar</option><option value="skip">Ignorar após a tolerância</option></select></div>
              <div class="field"><label for="auto-misfire-grace">Tolerância de atraso (segundos)</label><input id="auto-misfire-grace" name="misfire_grace_seconds" type="number" min="1" max="3600" step="1"><span class="help">Usada ao ignorar horários atrasados. Não cria uma fila de horários perdidos.</span></div>
            </div>
          </div>
        </section>
        <details class="advanced">
          <summary>Execução e novas tentativas</summary>
          <div class="form-grid">
            <div class="field"><label for="auto-timeout">Tempo limite (segundos)</label><input id="auto-timeout" name="timeout_seconds" type="number" min="1" max="300" step="1"></div>
            <div class="field"><label for="auto-max-attempts">Máximo de tentativas</label><input id="auto-max-attempts" name="max_attempts" type="number" min="1" max="10" step="1"></div>
            <div class="field field-wide"><label for="auto-overlap">Execuções simultâneas da mesma automação</label><select id="auto-overlap" name="overlap_policy"><option value="allow">Permitir sobreposição</option><option value="forbid">Impedir enquanto houver trabalho pendente ou ativo</option></select></div>
          </div>
          <label class="switch-row" for="auto-retry-custom"><input id="auto-retry-custom" name="retry_custom" type="checkbox"><span><strong>Personalizar espera entre tentativas</strong><span class="help">Desmarque para herdar a configuração padrão do servidor.</span></span></label>
          <div class="form-grid" data-retry-fields>
            <div class="field"><label for="auto-retry-base">Espera inicial (segundos)</label><input id="auto-retry-base" name="base_delay_seconds" type="number" min="1" max="3600" step="1"></div>
            <div class="field"><label for="auto-retry-max">Espera máxima (segundos)</label><input id="auto-retry-max" name="max_delay_seconds" type="number" min="1" max="86400" step="1"></div>
          </div>
        </details>
        <details class="advanced">
          <summary>Cabeçalhos e corpo da requisição</summary>
          <div class="field"><label for="auto-headers">Cabeçalhos HTTP (JSON)</label><textarea id="auto-headers" name="headers" rows="4" spellcheck="false" placeholder='{"Accept":"application/json"}'></textarea><span class="help">Objeto JSON com chaves e valores de texto enviados ao destino.</span></div>
          <div class="field"><label for="auto-body">Corpo da requisição</label><textarea id="auto-body" name="body" rows="5" spellcheck="false" placeholder="Conteúdo enviado ao destino, se necessário"></textarea><span class="help">Texto preservado como digitado, até 64 KiB.</span></div>
        </details>
      </div>
      <footer class="dialog-footer"><button type="button" class="btn btn-ghost" data-dismiss>Cancelar</button><button type="submit" class="btn btn-primary">${editing ? 'Salvar alterações' : 'Criar automação'}</button></footer>
    </form>`;
  document.body.append(dialog);
  const form = dialog.querySelector('form');
  const errorBox = dialog.querySelector('.error-box');
  const submit = form.querySelector('[type="submit"]');
  let pending = false;
  const dirty = new Set();
  const control = name => form.elements.namedItem(name);
  for (const [key, value] of Object.entries(values)) {
    const field = control(key);
    if (field.type === 'checkbox') field.checked = Boolean(value);
    else field.value = value;
  }
  const markDirty = event => { if (event.target.name) dirty.add(event.target.name); };
  form.addEventListener('input', markDirty);
  form.addEventListener('change', markDirty);
  const toggle = (selector, visible) => {
    const element = form.querySelector(selector);
    element.hidden = !visible;
    for (const field of element.querySelectorAll('input, select, textarea')) field.disabled = !visible;
  };
  const refreshSections = () => {
    const kind = control('schedule_kind').value;
    toggle('[data-schedule-details]', kind !== 'none');
    toggle('[data-interval-fields]', kind === 'interval');
    toggle('[data-cron-fields]', kind === 'cron');
    toggle('[data-retry-fields]', control('retry_custom').checked);
  };
  control('schedule_kind').addEventListener('change', () => {
    if (control('schedule_kind').value === 'cron' && !control('timezone').value) {
      control('timezone').value = initialValues().timezone;
      dirty.add('timezone');
    }
    refreshSections();
  });
  control('retry_custom').addEventListener('change', refreshSections);
  refreshSections();
  for (const button of form.querySelectorAll('[data-dismiss]')) button.addEventListener('click', () => { if (!pending) dialog.close(); });
  dialog.addEventListener('cancel', event => { if (pending) event.preventDefault(); });
  dialog.addEventListener('close', () => {
    dialog.remove();
    if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true });
  }, { once: true });
  form.addEventListener('submit', async event => {
    event.preventDefault();
    if (pending) return;
    errorBox.hidden = true;
    for (const field of form.querySelectorAll('[aria-invalid]')) field.removeAttribute('aria-invalid');
    let payload;
    try {
      const draft = {};
      for (const key of Object.keys(values)) {
        const field = control(key);
        // Textareas normalize CRLF to LF. Reuse untouched source values rather
        // than silently rewriting a request body when editing only its name.
        draft[key] = dirty.has(key) ? (field.type === 'checkbox' ? field.checked : field.value) : values[key];
      }
      payload = buildPayload(draft, automation);
    } catch (error) {
      errorBox.textContent = error.message;
      errorBox.hidden = false;
      if (error instanceof FormValidationError && control(error.field)) {
        const field = control(error.field);
        field.setAttribute('aria-invalid', 'true');
        const details = field.closest('details');
        if (details) details.open = true;
        field.focus();
      } else errorBox.focus();
      return;
    }
    pending = true;
    form.setAttribute('aria-busy', 'true');
    const controls = [...form.querySelectorAll('input, select, textarea, button')];
    const previousDisabled = controls.map(field => field.disabled);
    controls.forEach(field => { field.disabled = true; });
    submit.textContent = 'Salvando…';
    let saved;
    try {
      saved = await api(editing ? `/automations/${encodeURIComponent(automation.id)}` : '/automations', { method: editing ? 'PATCH' : 'POST', body: payload });
    } catch (error) {
      errorBox.textContent = error.message || 'Não foi possível salvar a automação. Tente novamente.';
      errorBox.hidden = false;
    } finally {
      pending = false;
      form.removeAttribute('aria-busy');
      controls.forEach((field, index) => { field.disabled = previousDisabled[index]; });
      submit.textContent = editing ? 'Salvar alterações' : 'Criar automação';
    }
    if (saved) {
      dialog.close('saved');
      await onSaved(saved, editing ? 'edit' : 'create');
    } else errorBox.focus();
  });
  dialog.showModal();
  control('name').focus();
  return dialog;
}
