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

  document.getElementById("section-telemetry").style.display = (tab === "telemetry") ? "block" : "none";
  document.getElementById("section-audit").style.display = (tab === "audit") ? "block" : "none";
  document.getElementById("section-users").style.display = (tab === "users") ? "block" : "none";

  if (tab === "audit") {
    loadAuditLogs();
  } else if (tab === "users") {
    loadUsers();
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
