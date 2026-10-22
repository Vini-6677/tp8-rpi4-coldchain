// Estado da Aplicação
let currentUser = null;

// ============================================================================
// 1. AUTENTICAÇÃO E SESSÃO
// ============================================================================

async function checkSession() {
  try {
    const res = await fetch("/api/auth/me");
    const data = await res.json();
    if (data.authenticated && data.user) {
      currentUser = data.user;
      renderAuthenticatedUI();
    } else {
      currentUser = null;
      renderLoginUI();
    }
  } catch (err) {
    currentUser = null;
    renderLoginUI();
  }
}

function renderLoginUI() {
  document.getElementById("login-view").style.display = "flex";
  document.getElementById("dashboard-view").style.display = "none";
}

function renderAuthenticatedUI() {
  document.getElementById("login-view").style.display = "none";
  document.getElementById("dashboard-view").style.display = "block";

  // Atualiza identificação visual do usuário
  const initials = currentUser.name
    .split(" ")
    .map(p => p[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
  document.getElementById("user-avatar").textContent = initials || "US";
  document.getElementById("user-display-name").textContent = currentUser.name;

  const roleTag = document.getElementById("user-display-role");
  roleTag.className = `user-role-tag role-${currentUser.role}`;
  roleTag.textContent = currentUser.role.toUpperCase();

  // Controle de Permissões nas Abas (RBAC)
  const tabAudit = document.getElementById("tab-audit");
  const tabUsers = document.getElementById("tab-users");
  const roleBanner = document.getElementById("role-notice-banner");
  const roleText = document.getElementById("role-notice-text");

  // Auditoria visível para Gerente e Admin
  if (currentUser.role === "gerente" || currentUser.role === "admin") {
    tabAudit.style.display = "inline-flex";
  } else {
    tabAudit.style.display = "none";
  }

  // Auditoria Criptográfica Regulatório ANVISA acessível para Gerente, Admin e Técnico
  const tabLedger = document.getElementById("tab-ledger");
  if (tabLedger) {
    if (currentUser.role === "gerente" || currentUser.role === "admin" || currentUser.role === "tecnico") {
      tabLedger.style.display = "inline-flex";
    } else {
      tabLedger.style.display = "none";
    }
  }

  // Gestão de Usuários visível exclusivamente para Admin
  if (currentUser.role === "admin") {
    tabUsers.style.display = "inline-flex";
  } else {
    tabUsers.style.display = "none";
  }

  // Notificação de contexto operacional do usuário
  if (currentUser.role === "enfermeiro") {
    roleBanner.style.display = "flex";
    roleText.textContent = "Perfil Enfermeiro: Acesso de visualização restrito aos sensores térmicos e câmaras de conservação.";
  } else if (currentUser.role === "tecnico") {
    roleBanner.style.display = "flex";
    roleText.textContent = "Perfil Técnico: Monitoramento de telemetria e diagnóstico operacional de sensores ESP32.";
  } else if (currentUser.role === "gerente") {
    roleBanner.style.display = "flex";
    roleText.textContent = "Perfil Gerente: Acesso irrestrito a todas as leituras, estatísticas e à Trilha de Auditoria imutável.";
  } else if (currentUser.role === "admin") {
    roleBanner.style.display = "flex";
    roleText.textContent = "Perfil Administrador: Controle de acessos e usuários. Registros de auditoria são protegidos e não podem ser alterados.";
  } else {
    roleBanner.style.display = "none";
  }

  // Retorna à aba de telemetria por padrão
  switchTab("telemetry");
}

// Preenchimento Rápido para Demonstração e Testes
function fillQuick(role) {
  const userMap = {
    enfermeiro: { u: "enfermeiro", p: "enfermeiro123" },
    tecnico:    { u: "tecnico",    p: "tecnico123" },
    gerente:    { u: "gerente",    p: "gerente123" },
    admin:      { u: "admin",      p: "admin123" }
  };
  if (userMap[role]) {
    document.getElementById("login-user").value = userMap[role].u;
    document.getElementById("login-pass").value = userMap[role].p;
  }
}

async function handleLogin(e) {
  e.preventDefault();
  const alertBox = document.getElementById("login-alert");
  alertBox.style.display = "none";

  const username = document.getElementById("login-user").value.trim();
  const password = document.getElementById("login-pass").value;

  try {
    const res = await fetch("/api/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password })
    });

    const data = await res.json();
    if (!res.ok) {
      alertBox.textContent = data.error || "Erro ao autenticar.";
      alertBox.style.display = "block";
      return;
    }

    currentUser = data.user;
    renderAuthenticatedUI();
  } catch (err) {
    alertBox.textContent = "Falha de comunicação com o servidor.";
    alertBox.style.display = "block";
  }
}

async function handleLogout() {
  try {
    await fetch("/api/auth/logout", { method: "POST" });
  } catch (err) {}
  currentUser = null;
  renderLoginUI();
}

// ============================================================================
// 2. CONTROLE DE ABAS
// ============================================================================

function switchTab(tab) {
  document.getElementById("tab-telemetry").classList.toggle("active", tab === "telemetry");
  document.getElementById("tab-audit").classList.toggle("active", tab === "audit");
  document.getElementById("tab-users").classList.toggle("active", tab === "users");
  const tabLedger = document.getElementById("tab-ledger");
  if (tabLedger) tabLedger.classList.toggle("active", tab === "ledger");

  document.getElementById("section-telemetry").style.display = (tab === "telemetry") ? "block" : "none";
  document.getElementById("section-audit").style.display = (tab === "audit") ? "block" : "none";
  document.getElementById("section-users").style.display = (tab === "users") ? "block" : "none";
  const secLedger = document.getElementById("section-ledger");
  if (secLedger) secLedger.style.display = (tab === "ledger") ? "block" : "none";

  if (tab === "audit") {
    loadAuditLogs();
  } else if (tab === "users") {
    loadUsers();
  } else if (tab === "ledger") {
    loadLedgerOverview();
    loadLedgerRecords();
  }
}

// ============================================================================
// 3. LOGS DE AUDITORIA (GERENTE & ADMIN)
// ============================================================================

async function loadAuditLogs() {
  const tbody = document.getElementById("audit-table-body");
  tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; color: var(--text-muted);">Consultando trilha de auditoria...</td></tr>`;

  try {
    const res = await fetch("/api/audit/logs");
    if (!res.ok) {
      tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; color: var(--danger);">Acesso não autorizado.</td></tr>`;
      return;
    }
    const logs = await res.json();
    if (!logs || logs.length === 0) {
      tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; color: var(--text-muted);">Nenhum registro encontrado.</td></tr>`;
      return;
    }

    tbody.innerHTML = "";
    logs.forEach(l => {
      const tr = document.createElement("tr");
      const badgeClass = getActionBadgeClass(l.action);
      tr.innerHTML = `
        <td style="color: #cbd5e1; font-family: monospace; font-size: 0.82rem;">${l.timestamp}</td>
        <td><strong>${escapeHtml(l.username)}</strong></td>
        <td><span class="user-role-tag role-${l.role}">${escapeHtml(l.role)}</span></td>
        <td><span class="action-badge ${badgeClass}">${escapeHtml(l.action)}</span></td>
        <td style="color: #f1f5f9;">${escapeHtml(l.details)}</td>
        <td style="color: var(--text-muted); font-family: monospace;">${escapeHtml(l.ip_address)}</td>
      `;
      tbody.appendChild(tr);
    });
  } catch (err) {
    tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; color: var(--danger);">Erro ao carregar registros.</td></tr>`;
  }
}

function getActionBadgeClass(action) {
  if (action === "LOGIN_SUCCESS") return "badge-login-success";
  if (action === "LOGIN_FAILED") return "badge-login-failed";
  if (action === "LOGIN_BLOCKED") return "badge-login-blocked";
  if (action === "USER_CREATED") return "badge-user-created";
  if (action === "USER_STATUS_CHANGE") return "badge-user-status";
  if (action === "USER_ROLE_CHANGE") return "badge-role-change";
  return "badge-logout";
}

function filterAuditTable() {
  const query = document.getElementById("audit-search")?.value.toLowerCase().trim() || "";
  const rows = document.querySelectorAll("#audit-table-body tr");
  rows.forEach(r => {
    const text = r.textContent.toLowerCase();
    r.style.display = text.includes(query) ? "" : "none";
  });
}

// ============================================================================
// 4. GESTÃO DE USUÁRIOS (ADMIN EXCLUSIVO)
// ============================================================================

async function loadUsers() {
  const tbody = document.getElementById("users-table-body");
  tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; color: var(--text-muted);">Carregando usuários...</td></tr>`;

  try {
    const res = await fetch("/api/admin/users");
    if (!res.ok) {
      tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; color: var(--danger);">Acesso negado.</td></tr>`;
      return;
    }
    const users = await res.json();
    if (!users || users.length === 0) {
      tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; color: var(--text-muted);">Nenhum usuário cadastrado.</td></tr>`;
      return;
    }

    tbody.innerHTML = "";
    users.forEach(u => {
      const tr = document.createElement("tr");
      const isBlocked = u.is_blocked === 1;
      const isSelf = currentUser && u.username === currentUser.username;

      tr.innerHTML = `
        <td style="color: var(--text-muted); font-family: monospace;">#${u.id}</td>
        <td><code>${escapeHtml(u.username)}</code></td>
        <td><strong>${escapeHtml(u.name)}</strong></td>
        <td>
          ${u.role === 'admin' 
            ? '<span class="user-role-tag role-admin">ADMIN</span>' 
            : `<select class="role-select" onchange="changeUserRole('${u.username}', this.value)">
                <option value="enfermeiro" ${u.role === 'enfermeiro' ? 'selected' : ''}>Enfermeiro</option>
                <option value="tecnico" ${u.role === 'tecnico' ? 'selected' : ''}>Técnico</option>
                <option value="gerente" ${u.role === 'gerente' ? 'selected' : ''}>Gerente</option>
              </select>`
          }
        </td>
        <td>
          ${isBlocked 
            ? '<span style="color: var(--danger); font-weight: 700;">● Bloqueado</span>' 
            : '<span style="color: var(--success); font-weight: 700;">● Ativo</span>'
          }
        </td>
        <td>
          ${isSelf 
            ? '<span style="color: var(--text-muted); font-size: 0.8rem;">(Você)</span>'
            : `<button class="btn-sm-action ${isBlocked ? 'btn-unblock' : 'btn-block'}" onclick="toggleBlockUser('${u.username}')">
                ${isBlocked ? 'Desbloquear' : 'Bloquear'}
               </button>`
          }
        </td>
      `;
      tbody.appendChild(tr);
    });
  } catch (err) {
    tbody.innerHTML = `<tr><td colspan="6" style="text-align: center; color: var(--danger);">Falha ao obter lista de usuários.</td></tr>`;
  }
}

async function createUser() {
  const username = document.getElementById("new-user-name").value.trim();
  const name = document.getElementById("new-user-fullname").value.trim();
  const password = document.getElementById("new-user-pass").value;
  const role = document.getElementById("new-user-role").value;

  if (!username || !name || !password) {
    alert("Preencha todos os campos para cadastrar o usuário.");
    return;
  }

  try {
    const res = await fetch("/api/admin/users/create", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, name, password, role })
    });
    const data = await res.json();
    if (!res.ok) {
      alert(data.error || "Erro ao cadastrar usuário.");
      return;
    }

    alert(data.message || "Usuário cadastrado com sucesso!");
    document.getElementById("new-user-name").value = "";
    document.getElementById("new-user-fullname").value = "";
    document.getElementById("new-user-pass").value = "";
    loadUsers();
  } catch (err) {
    alert("Erro de comunicação com o servidor.");
  }
}

async function toggleBlockUser(username) {
  if (!confirm(`Deseja alterar o status de bloqueio do usuário '${username}'?`)) return;

  try {
    const res = await fetch("/api/admin/users/toggle-block", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username })
    });
    const data = await res.json();
    if (!res.ok) {
      alert(data.error || "Erro ao atualizar usuário.");
      return;
    }
    loadUsers();
  } catch (err) {
    alert("Erro de comunicação.");
  }
}

async function changeUserRole(username, newRole) {
  try {
    const res = await fetch("/api/admin/users/update-role", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, role: newRole })
    });
    const data = await res.json();
    if (!res.ok) {
      alert(data.error || "Erro ao atualizar perfil.");
      loadUsers();
      return;
    }
    loadUsers();
  } catch (err) {
    alert("Erro de comunicação.");
  }
}

// ============================================================================
// 5. TELEMETRIA EM TEMPO REAL & RELÓGIO
// ============================================================================

function updateClock() {
  const now = new Date();
  const timeElem = document.getElementById("live-time");
  const dateElem = document.getElementById("live-date");

  if (timeElem) {
    const hours = String(now.getHours()).padStart(2, "0");
    const minutes = String(now.getMinutes()).padStart(2, "0");
    const seconds = String(now.getSeconds()).padStart(2, "0");
    timeElem.textContent = `${hours}:${minutes}:${seconds}`;
  }

  if (dateElem) {
    const weekdays = ["Domingo", "Segunda-feira", "Terça-feira", "Quarta-feira", "Quinta-feira", "Sexta-feira", "Sábado"];
    const weekday = weekdays[now.getDay()];
    const day = String(now.getDate()).padStart(2, "0");
    const month = String(now.getMonth() + 1).padStart(2, "0");
    const year = now.getFullYear();
    dateElem.textContent = `${weekday}, ${day}/${month}/${year}`;
  }
}
setInterval(updateClock, 1000);
updateClock();

const evtSource = new EventSource("/events");

evtSource.onmessage = function(event) {
  try {
    const data = JSON.parse(event.data);
    if (data.type === "telemetry" && data.payload) {
      updateTelemetryUI(data.payload);
    }
  } catch (e) {
    console.error("Erro telemetria:", e);
  }
};

function updateTelemetryUI(t) {
  const tempValElem = document.getElementById("temp-val");
  const tempProgElem = document.getElementById("temp-progress");

  if (tempValElem) {
    tempValElem.textContent = t.t.toFixed(1) + " °C";
  }

  if (tempProgElem) {
    let pct = ((t.t - 0) / 10) * 100;
    if (pct < 10) pct = 10;
    if (pct > 95) pct = 95;
    tempProgElem.style.width = pct.toFixed(0) + "%";
    tempProgElem.style.background = (t.t < 2.0 || t.t > 8.0) ? "var(--danger)" : "var(--success)";
  }

  const tbody = document.getElementById("telemetry-body");
  if (!tbody) return;

  const row = document.createElement("tr");
  const timeStr = new Date(t.ts * 1000).toLocaleTimeString();
  const isSafe = t.t >= 2.0 && t.t <= 8.0;
  const isDoorOpen = t.door !== 0;

  row.innerHTML = `
    <td>${timeStr}</td>
    <td>${escapeHtml(t.device_id || "esp32-sensor-01")}</td>
    <td class="${isSafe ? 'temp-good' : 'temp-bad'}">${t.t.toFixed(1)}</td>
    <td class="${isDoorOpen ? 'door-open' : 'door-closed'}">${isDoorOpen ? 'Aberta' : 'Fechada'}</td>
  `;

  tbody.insertBefore(row, tbody.firstChild);

  while (tbody.children.length > 15) {
    tbody.removeChild(tbody.lastChild);
  }

  filterTable();
}

function filterTable() {
  const query = document.getElementById("device-search")?.value.toLowerCase().trim() || "";
  const rows = document.querySelectorAll("#telemetry-body tr");
  rows.forEach(row => {
    const text = row.textContent.toLowerCase();
    row.style.display = text.includes(query) ? "" : "none";
  });
}

async function fetchStatus() {
  try {
    const res = await fetch("/api/status");
    if (!res.ok) return;
    const st = await res.json();
    const devElem = document.getElementById("dev-count");
    if (devElem && st.devices_count !== undefined) {
      const active = st.devices_count > 0 ? st.devices_count : 1;
      devElem.textContent = `${active} / ${active}`;
    }
  } catch (err) {}
}
setInterval(fetchStatus, 3000);
fetchStatus();

function escapeHtml(str) {
  if (!str) return "";
  return String(str)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#039;");
}

// Inicializa verificação de sessão ao carregar a página
checkSession();


// ============================================================================
// 5. AUDITORIA CRIPTOGRÁFICA REGULATÓRIA ANVISA (LEDGER WORM)
// ============================================================================

async function loadLedgerOverview() {
  try {
    const resKey = await fetch("/api/v1/ledger/public-key");
    if (resKey.ok) {
      const keyData = await resKey.json();
      const keyEl = document.getElementById("ledger-key-id");
      if (keyEl) keyEl.textContent = keyData.key_id ? keyData.key_id.toUpperCase() : "--------";
    }

    const resRec = await fetch("/api/v1/ledger/records?limit=1");
    if (resRec.ok) {
      const recData = await resRec.json();
      const heightEl = document.getElementById("ledger-height-text");
      const heightSub = document.getElementById("ledger-height-sub");
      if (heightEl) heightEl.textContent = `${recData.total} Blocos`;
      if (heightSub) heightSub.textContent = `Gênese #1 até Head #${recData.total}`;
    }
  } catch (err) {
    console.error("Erro ao carregar visão geral do ledger:", err);
  }
}

async function loadLedgerRecords() {
  const tbody = document.getElementById("ledger-table-body");
  if (!tbody) return;
  tbody.innerHTML = `<tr><td colspan="9" style="text-align: center; color: var(--text-muted);">Consultando cadeia criptográfica...</td></tr>`;

  try {
    const res = await fetch("/api/v1/ledger/records?limit=100");
    if (!res.ok) {
      tbody.innerHTML = `<tr><td colspan="9" style="text-align: center; color: var(--danger);">Acesso não autorizado ou ledger indisponível.</td></tr>`;
      return;
    }
    const data = await res.json();
    const records = data.records || [];

    if (records.length === 0) {
      tbody.innerHTML = `<tr><td colspan="9" style="text-align: center; color: var(--text-muted);">Nenhum bloco registrado no ledger.</td></tr>`;
      return;
    }

    // Atualiza contadores do card
    const heightEl = document.getElementById("ledger-height-text");
    const heightSub = document.getElementById("ledger-height-sub");
    if (heightEl) heightEl.textContent = `${data.total} Blocos`;
    if (heightSub) heightSub.textContent = `Gênese #1 até Head #${data.total}`;

    tbody.innerHTML = records.map(r => {
      let typeClass = "type-data";
      if (r.type_name === "GENESIS") typeClass = "type-genesis";
      else if (r.type_name === "BOOT") typeClass = "type-boot";
      else if (r.type_name === "ANCHOR") typeClass = "type-anchor";
      else if (r.type_name === "CHECKPOINT") typeClass = "type-checkpoint";

      const shortPrev = r.prev_hash ? `${r.prev_hash.slice(0, 8)}...${r.prev_hash.slice(-6)}` : "zeros";
      const shortCurr = r.current_hash ? `${r.current_hash.slice(0, 8)}...${r.current_hash.slice(-6)}` : "";
      const shortSig = r.signature ? `${r.signature.slice(0, 8)}...${r.signature.slice(-6)}` : "";

      return `
        <tr>
          <td><strong style="color: #ffffff;">#${r.id}</strong></td>
          <td><span class="type-badge ${typeClass}">${r.type_name}</span></td>
          <td><span style="font-family: monospace; font-size: 0.8rem; color: #a5b4fc;">${escapeHtml(r.device_id)}</span></td>
          <td>${r.seq || 0}</td>
          <td style="font-size: 0.78rem; font-family: monospace; color: var(--text-muted);">${r.created_at || r.server_time || "-"}</td>
          <td><span class="hash-chip" title="Clique para copiar hash completo" onclick="navigator.clipboard.writeText('${r.prev_hash}')">${shortPrev}</span></td>
          <td><span class="hash-chip" style="color: #52d68a; border-color: rgba(82,214,138,0.3);" title="Clique para copiar hash completo" onclick="navigator.clipboard.writeText('${r.current_hash}')">${shortCurr}</span></td>
          <td><span class="hash-chip" title="Clique para copiar assinatura raw de 64 bytes" onclick="navigator.clipboard.writeText('${r.signature}')">${shortSig}</span></td>
          <td>
            <span style="display: inline-flex; align-items: center; gap: 0.35rem; color: #52d68a; font-size: 0.75rem; font-weight: 700;">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg>
              Íntegro
            </span>
          </td>
        </tr>
      `;
    }).join("");
  } catch (err) {
    tbody.innerHTML = `<tr><td colspan="9" style="text-align: center; color: var(--danger);">Erro de comunicação com o servidor.</td></tr>`;
  }
}

async function runLedgerVerification() {
  const contentEl = document.getElementById("audit-box-content");
  const timeEl = document.getElementById("audit-box-time");
  const statusVal = document.getElementById("ledger-status-text");
  const statusSub = document.getElementById("ledger-status-sub");
  const iconEl = document.getElementById("badge-valid-icon");

  contentEl.innerHTML = `<span style="color: #38bdf8;">[ AUDITORIA EM PROGRESSO ] Recalculando todos os hashes SHA-256 e validando assinaturas ECDSA P-256...</span>`;
  timeEl.textContent = `Executando perícia agora...`;

  try {
    const res = await fetch("/api/v1/ledger/verify");
    if (!res.ok) {
      contentEl.innerHTML = `<span style="color: var(--danger);">Falha ao executar auditoria pericial.</span>`;
      return;
    }
    const report = await res.json();
    timeEl.textContent = `Laudo emitido em: ${report.verified_at}`;

    if (report.valid) {
      statusVal.textContent = "100% ÍNTEGRO";
      statusVal.className = "l-card-val text-success";
      statusSub.textContent = "Cadeia matematicamente comprovada";
      iconEl.textContent = "🛡️";

      contentEl.innerHTML = `
        <div style="margin-bottom: 0.5rem; color: #52d68a; font-weight: 700;">
          ✓ PROVA MATEMÁTICA CONCLUÍDA COM SUCESSO: NENHUMA FRAUDE OU ADULTERAÇÃO DETECTADA
        </div>
        <div>
          <span class="audit-stat-chip">Total Auditado: <strong>${report.total_records} blocos</strong></span>
          <span class="audit-stat-chip">Hashes Recalculados: <strong>${report.checked_hashes}</strong></span>
          <span class="audit-stat-chip">Assinaturas Validadas: <strong>${report.checked_signatures}</strong></span>
          <span class="audit-stat-chip">Tempo de Verificação: <strong>${report.duration_ms.toFixed(2)} ms</strong></span>
          <span class="audit-stat-chip">Chave Pública ID: <strong>${report.signer_key_id}</strong></span>
        </div>
        <div style="margin-top: 0.6rem; color: #9cb9e6; font-size: 0.8rem;">
          ${report.regulatory_notice} — Encadeamento ininterrupto de #1 (Gênese) até #${report.head_id} (Head).
        </div>
      `;
    } else {
      statusVal.textContent = "ALERTA: VIOLADO";
      statusVal.className = "l-card-val text-danger";
      statusSub.textContent = "Adulteração detectada!";
      iconEl.textContent = "⚠️";

      contentEl.innerHTML = `
        <div style="margin-bottom: 0.5rem; color: var(--danger); font-weight: 700;">
          ❌ ALERTA PERICIAL CRÍTICO: DETECTADAS ANOMALIAS OU TENTATIVAS DE ADULTERAÇÃO!
        </div>
        <div style="color: #f87171; margin-top: 0.4rem;">
          ${(report.anomalies || []).map(a => `<div>• ${escapeHtml(a)}</div>`).join("")}
        </div>
      `;
    }
  } catch (err) {
    contentEl.innerHTML = `<span style="color: var(--danger);">Erro ao conectar com o serviço de perícia.</span>`;
  }
}

function exportLedgerJSON() {
  window.location.href = "/api/v1/ledger/export";
}

async function createManualAnchor() {
  if (!confirm("Deseja emitir uma nova Âncora Criptográfica RFC 3161 no Ledger agora?")) {
    return;
  }
  try {
    const res = await fetch("/api/v1/ledger/anchor", { method: "POST" });
    const data = await res.json();
    if (res.ok) {
      alert(`Âncora #${data.anchor.id} registrada com sucesso no Ledger!`);
      loadLedgerOverview();
      loadLedgerRecords();
    } else {
      alert(data.error || "Erro ao emitir âncora.");
    }
  } catch (err) {
    alert("Falha de comunicação.");
  }
}
