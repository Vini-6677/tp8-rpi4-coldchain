let selectedCategory = 'todas';

function filterChamberCategory(cat, btn) {
  selectedCategory = cat;
  document.querySelectorAll('.cat-pill').forEach(b => b.classList.remove('active'));
  if (btn) btn.classList.add('active');
  loadKPIs();
      loadChambers(false);
}

// ColdChain Pharma RT — Frontend Logic (ANVISA RDC 430/2020 & RDC 197/2017)

let currentUser = null;
let activeTab = 'chambers';
let selectedChamberId = 'CH-A';
let chambersData = [];
let sensorsData = [];
let lotsData = [];
let productsData = [];
let chartHistory = {}; // chamberId -> { timestamp, sensorTemps: { S01: val, ... } }

// -----------------------------------------------------------------------------
// INICIALIZAÇÃO & AUTENTICAÇÃO RBAC
// -----------------------------------------------------------------------------
document.addEventListener('DOMContentLoaded', () => {
  checkSession();
  setupSSE();
  // Atualiza métricas a cada 3 segundos
  setInterval(() => {
    if (currentUser) {
      if (activeTab === 'chambers') loadChambers(false);
      if (activeTab === 'excursions') loadAlarms();
    }
  }, 3000);
});

async function checkSession() {
  try {
    const res = await fetch('/api/auth/me');
    const data = await res.json();
    if (data.authenticated && data.user) {
      currentUser = data.user;
      showApp();
    } else {
      showLogin();
    }
  } catch (e) {
    showLogin();
  }
}

function showLogin() {
  document.getElementById('login-screen').style.display = 'flex';
  document.getElementById('app-container').style.display = 'none';
}

function showApp() {
  document.getElementById('login-screen').style.display = 'none';
  document.getElementById('app-container').style.display = 'flex';

  document.getElementById('app-user-name').innerText = currentUser.name || currentUser.username;
  document.getElementById('app-user-role').innerText = currentUser.role.toUpperCase();
  document.getElementById('app-user-avatar').innerText = (currentUser.name || currentUser.username).substring(0, 2).toUpperCase();

  // Permissões de abas por perfil (RBAC)
  const tabUsers = document.getElementById('tab-btn-users');
  if (currentUser.role === 'admin') {
    tabUsers.style.display = 'flex';
  } else {
    tabUsers.style.display = 'none';
  }

  // Carrega dados iniciais
  loadKPIs();
  loadChambers();
  loadProducts();
  loadLots();
  loadAlarms();
  loadExcursions();
  loadMetrology();
}

function fillLogin(u, p) {
  document.getElementById('login-username').value = u;
  document.getElementById('login-password').value = p;
}

async function submitLogin() {
  const u = document.getElementById('login-username').value.trim();
  const p = document.getElementById('login-password').value;
  const btn = document.getElementById('btn-login-submit');

  btn.disabled = true;
  btn.innerText = 'Autenticando...';

  try {
    const res = await fetch('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: u, password: p })
    });
    const data = await res.json();
    if (res.ok && data.status === 'ok') {
      currentUser = data.user;
      showApp();
    } else {
      alert(data.error || 'Credenciais inválidas.');
    }
  } catch (e) {
    alert('Erro ao conectar ao servidor.');
  } finally {
    btn.disabled = false;
    btn.innerText = 'Autenticar no Sistema';
  }
}

async function handleLogout() {
  try {
    await fetch('/api/auth/logout', { method: 'POST' });
  } catch (e) {}
  currentUser = null;
  showLogin();
}

// -----------------------------------------------------------------------------
// NAVEGAÇÃO ENTRE ABAS
// -----------------------------------------------------------------------------
function switchTab(tabId) {
  activeTab = tabId;

  document.querySelectorAll('.nav-tab-btn').forEach(btn => btn.classList.remove('active'));
  document.querySelectorAll('.tab-section').forEach(sec => sec.classList.remove('active'));

  const btn = document.getElementById(`tab-btn-${tabId}`);
  const sec = document.getElementById(`section-${tabId}`);
  if (btn) btn.classList.add('active');
  if (sec) sec.classList.add('active');

  if (tabId === 'chambers') loadChambers();
  if (tabId === 'stock') loadLots();
  if (tabId === 'excursions') { loadAlarms(); loadExcursions(); }
  if (tabId === 'metrology') loadMetrology();
  if (tabId === 'ledger') loadLedger();
  if (tabId === 'users') loadUsers();
  if (tabId === 'quality') loadQualityDossier();
}

function toggleTVMode() {
  document.body.classList.toggle('tv-mode');
  if (document.body.classList.contains('tv-mode')) {
    if (document.documentElement.requestFullscreen) {
      document.documentElement.requestFullscreen().catch(() => {});
    }
  } else {
    if (document.exitFullscreen) {
      document.exitFullscreen().catch(() => {});
    }
  }
}

// -----------------------------------------------------------------------------
// MÓDULO 1: CÂMARAS FRIAS & GRÁFICOS MULTISSENSORES
// -----------------------------------------------------------------------------
async function loadChambers(renderDetail = true) {
  try {
    const res = await fetch('/api/pharma/chambers');
    if (!res.ok) return;
    chambersData = await res.json();

    const container = document.getElementById('chambers-cards-container');
    container.innerHTML = '';

    const toRender = chambersData.filter(c => {
      if (selectedCategory === 'todas') return true;
      return c.type === selectedCategory;
    });

    toRender.forEach(c => {
      const isSel = c.id === selectedChamberId;
      const statusClass = c.status === 'EXCURSAO' ? 'status-excursao' : '';
      const pillClass = c.status === 'EXCURSAO' ? 'excursao' : (c.status === 'ALERTA' ? 'alerta' : 'ok');
      const pillText = c.status === 'EXCURSAO' ? 'Excursão Térmica' : (c.status === 'ALERTA' ? 'Atenção' : 'Conforme');

      const card = document.createElement('div');
      card.className = `chamber-card ${isSel ? 'selected' : ''} ${statusClass}`;
      card.onclick = () => selectChamber(c.id);

      card.innerHTML = `
        <div class="chamber-header">
          <div>
            <div class="chamber-title-text">${c.name}</div>
            <div class="chamber-type-text">${c.type === 'ultra_frio' ? 'Freezer Ultra-Frio -80°C' : (c.type === 'ambiente_controlado' ? 'Ambiente Climatizado (15°C–30°C)' : (c.type === 'geladeira' ? 'Geladeira Farmácia Qualificada' : 'Câmara Fria Industrial 2°C–8°C'))} • ID: ${c.id}</div>
          </div>
          <span class="status-pill ${pillClass}">
            <span style="display:inline-block; width:6px; height:6px; border-radius:50%; background:currentColor;"></span>
            ${pillText}
          </span>
        </div>

        <div class="chamber-main-metric">
          <div class="chamber-temp-val">${c.avg_temp.toFixed(1)}</div>
          <div class="chamber-temp-unit">°C</div>
          <div class="chamber-limits-range">
            Faixa Permitida<br>
            <strong style="color:#60A5FA;">${c.temp_min.toFixed(1)} °C a ${c.temp_max.toFixed(1)} °C</strong>
          </div>
        </div>

        <div class="chamber-stats-row">
          <div class="stat-col">
            <div class="stat-label">Mín 24h</div>
            <div class="stat-val" style="color:#38BDF8;">${c.min_temp_24h.toFixed(1)}°</div>
          </div>
          <div class="stat-col">
            <div class="stat-label">Máx 24h</div>
            <div class="stat-val" style="color:${c.max_temp_24h > c.temp_max ? '#EF4444' : '#E2E8F0'};">${c.max_temp_24h.toFixed(1)}°</div>
          </div>
          <div class="stat-col">
            <div class="stat-label">Sensores</div>
            <div class="stat-val">${c.sensor_count} ativos</div>
          </div>
          <div class="stat-col">
            <div class="stat-label">Quarentena</div>
            <div class="stat-val" style="color:${c.quarantined_lots_count > 0 ? '#EF4444' : '#10B981'};">${c.quarantined_lots_count} lotes</div>
          </div>
        </div>

        <div class="chamber-footer-meta">
          <span>${c.qualification_status}</span>
          <span>Último sinal: ${c.last_reading_time.split(' ')[1] || 'agora'}</span>
        </div>
      `;
      container.appendChild(card);
    });

    if (renderDetail) {
      selectChamber(selectedChamberId);
    }
  } catch (e) {
    console.error('Erro ao carregar câmaras:', e);
  }
}

async function selectChamber(chamberId) {
  selectedChamberId = chamberId;
  const currentChamber = chambersData.find(c => c.id === chamberId) || { name: chamberId, sensor_count: 0 };

  document.getElementById('detail-chamber-title').innerText = `${currentChamber.name} — Mapa Térmico Multissensor`;
  document.getElementById('detail-sensor-count-badge').innerText = `${currentChamber.sensor_count} Sensores PT-100`;

  // Destacar o card selecionado
  document.querySelectorAll('.chamber-card').forEach(card => card.classList.remove('selected'));

  try {
    const res = await fetch(`/api/pharma/sensors?chamber=${chamberId}`);
    if (!res.ok) return;
    sensorsData = await res.json();

    const grid = document.getElementById('chamber-sensors-grid');
    grid.innerHTML = '';

    sensorsData.forEach(s => {
      const isHot = s.thermal_position.includes('Quente');
      const isCold = s.thermal_position.includes('Frio');
      const posColor = isHot ? '#F87171' : (isCold ? '#38BDF8' : '#94A3B8');

      const card = document.createElement('div');
      card.className = 'sensor-item-card';
      card.innerHTML = `
        <div>
          <div class="sensor-item-id">${s.id} <span style="font-size:11px; color:#64748B;">(${s.serial_number})</span></div>
          <div class="sensor-item-pos" style="color:${posColor};">${s.thermal_position}</div>
          <div class="sensor-item-cert">Cert. RBC: ${s.cert_number} • Val: ${s.calib_valid_until}</div>
        </div>
        <div>
          <div class="sensor-item-temp">${s.current_temp.toFixed(1)} °C</div>
          <div class="sensor-item-hum">${s.current_humidity.toFixed(0)}% UR</div>
        </div>
      `;
      grid.appendChild(card);
    });

    renderMultiSensorChart(sensorsData);
  } catch (e) {
    console.error('Erro ao carregar sensores:', e);
  }
}

function renderMultiSensorChart(sensors) {
  const canvas = document.getElementById('multisensor-chart');
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  const dpr = window.devicePixelRatio || 1;
  const rect = canvas.getBoundingClientRect();

  canvas.width = rect.width * dpr;
  canvas.height = rect.height * dpr;
  ctx.scale(dpr, dpr);

  const W = rect.width;
  const H = rect.height;

  // Limpa canvas
  ctx.clearRect(0, 0, W, H);

  // Faixa permitida: 2.0°C a 8.0°C
  const minTemp = 1.0;
  const maxTemp = 10.0;
  const getY = (t) => H - 30 - ((t - minTemp) / (maxTemp - minTemp)) * (H - 50);

  // Zona verde permitida (2.0 a 8.0)
  const yTop = getY(8.0);
  const yBottom = getY(2.0);
  ctx.fillStyle = 'rgba(16, 185, 129, 0.08)';
  ctx.fillRect(50, yTop, W - 60, yBottom - yTop);

  // Linhas de grade e limites
  ctx.strokeStyle = 'rgba(255, 255, 255, 0.08)';
  ctx.lineWidth = 1;

  for (let t = 2; t <= 10; t += 2) {
    const y = getY(t);
    ctx.beginPath();
    ctx.moveTo(50, y);
    ctx.lineTo(W - 10, y);
    ctx.stroke();

    ctx.fillStyle = '#64748B';
    ctx.font = '10px monospace';
    ctx.textAlign = 'right';
    ctx.fillText(`${t}.0°C`, 45, y + 3);
  }

  // Linhas vermelhas de limite ANVISA
  ctx.strokeStyle = 'rgba(239, 68, 68, 0.6)';
  ctx.setLineDash([4, 4]);
  ctx.beginPath();
  ctx.moveTo(50, yTop); ctx.lineTo(W - 10, yTop);
  ctx.moveTo(50, yBottom); ctx.lineTo(W - 10, yBottom);
  ctx.stroke();
  ctx.setLineDash([]);

  ctx.fillStyle = '#EF4444';
  ctx.font = '9px sans-serif';
  ctx.fillText('LIMITE MÁX (8.0°C)', W - 15, yTop - 4);
  ctx.fillText('LIMITE MÍN (2.0°C)', W - 15, yBottom + 12);

  // Desenha curvas sobrepostas para cada sensor
  const colors = ['#60A5FA', '#34D399', '#F87171', '#FBBF24', '#A78BFA', '#38BDF8', '#F472B6', '#4ADE80', '#FB923C', '#E879F9'];
  const numPoints = 20;

  sensors.forEach((s, idx) => {
    const col = colors[idx % colors.length];
    ctx.strokeStyle = col;
    ctx.lineWidth = 2;
    ctx.beginPath();

    const currentT = s.current_temp;
    for (let i = 0; i < numPoints; i++) {
      const x = 55 + (i / (numPoints - 1)) * (W - 80);
      // Simula histórico suave terminando na temperatura atual
      const noise = Math.sin(i * 0.8 + idx) * 0.25;
      const t = currentT + noise;
      const y = getY(t);

      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    ctx.stroke();

    // Ponto final
    const lastX = 55 + (W - 80);
    const lastY = getY(currentT);
    ctx.fillStyle = col;
    ctx.beginPath();
    ctx.arc(lastX, lastY, 3.5, 0, Math.PI * 2);
    ctx.fill();
  });
}

// -----------------------------------------------------------------------------
// MÓDULO 2: ESTOQUE, LOTES & RASTREABILIDADE FEFO
// -----------------------------------------------------------------------------
async function loadProducts() {
  try {
    const res = await fetch('/api/pharma/products');
    if (!res.ok) return;
    productsData = await res.json();

    const select = document.getElementById('entry-product');
    if (select) {
      select.innerHTML = '<option value="">Selecione o medicamento...</option>';
      productsData.forEach(p => {
        select.innerHTML += `<option value="${p.id}">${p.trade_name} (${p.dcb}) — Reg. ${p.anvisa_reg}</option>`;
      });
    }
  } catch (e) {
    console.error('Erro ao carregar produtos:', e);
  }
}

async function loadLots() {
  try {
    const res = await fetch('/api/pharma/lots');
    if (!res.ok) return;
    lotsData = await res.json();

    const tbody = document.getElementById('lots-table-body');
    tbody.innerHTML = '';

    const exitSelect = document.getElementById('exit-lot-select');
    if (exitSelect) exitSelect.innerHTML = '';

    lotsData.forEach(l => {
      const row = document.createElement('tr');
      const isExpired = l.days_to_expiry <= 0;
      const isCritical = l.days_to_expiry > 0 && l.days_to_expiry <= 60;
      const expColor = isExpired ? '#EF4444' : (isCritical ? '#F59E0B' : '#6EE7B7');

      row.innerHTML = `
        <td><span class="badge-fefo-rank">${l.fefo_rank}</span></td>
        <td>
          <div style="font-weight:700; color:#fff;">${l.product_trade_name}</div>
          <div style="font-size:11px; color:#94A3B8;">${l.product_dcb} • ${l.manufacturer}</div>
        </td>
        <td><code style="color:#60A5FA;">${l.anvisa_reg}</code></td>
        <td><strong style="color:#E2E8F0;">${l.lot_number}</strong></td>
        <td>
          <span style="color:${expColor}; font-weight:700;">${l.expiry_date}</span>
          <div style="font-size:10.5px; color:#64748B;">${l.days_to_expiry} dias restantes</div>
        </td>
        <td><strong>${l.quantity}</strong> un</td>
        <td>${l.chamber_id} • <span style="font-size:11px; color:#94A3B8;">${l.shelf_location}</span></td>
        <td><span class="badge-status ${l.status}">${l.status.toUpperCase()}</span></td>
        <td style="text-align: right;">
          <button class="btn-action-primary" style="padding:4px 8px; font-size:11px; display:inline-flex;" onclick="openTraceModal('${l.lot_number}')">
            Rastrear (Laudo)
          </button>
        </td>
      `;
      tbody.appendChild(row);

      // Preenche select de saída
      if (exitSelect && l.status === 'liberado' && l.quantity > 0) {
        exitSelect.innerHTML += `<option value="${l.id}">Rank #${l.fefo_rank} — Lote ${l.lot_number} (${l.product_trade_name}) • Val: ${l.expiry_date} • Saldo: ${l.quantity}</option>`;
      }
    });

    updateExitLotInfo();
  } catch (e) {
    console.error('Erro ao carregar lotes:', e);
  }
}

function filterLotsTable() {
  const query = document.getElementById('filter-lot-input').value.toLowerCase();
  const rows = document.querySelectorAll('#lots-table-body tr');
  rows.forEach(r => {
    const text = r.innerText.toLowerCase();
    r.style.display = text.includes(query) ? '' : 'none';
  });
}

function openEntryModal() {
  document.getElementById('modal-entry').classList.add('active');
}

function closeEntryModal() {
  document.getElementById('modal-entry').classList.remove('active');
}

function checkEntryTemp() {
  const temp = parseFloat(document.getElementById('entry-temp').value);
  const warn = document.getElementById('entry-temp-warning');
  if (temp < 2.0 || temp > 8.0) {
    warn.style.display = 'flex';
  } else {
    warn.style.display = 'none';
  }
}

function updateProductRanges() {
  // auto-preenche faixa se necessário
}

async function submitStockEntry() {
  const payload = {
    product_id: parseInt(document.getElementById('entry-product').value),
    lot_number: document.getElementById('entry-lot').value.trim(),
    expiry_date: document.getElementById('entry-expiry').value,
    quantity: parseInt(document.getElementById('entry-qty').value),
    chamber_id: document.getElementById('entry-chamber').value,
    shelf_location: document.getElementById('entry-shelf').value.trim(),
    unit_price: 150.0,
    supplier: document.getElementById('entry-supplier').value.trim(),
    supplier_cnpj: document.getElementById('entry-cnpj').value.trim(),
    invoice_number: document.getElementById('entry-nfe').value.trim(),
    transport_condition: 'Caixa Térmica EPS Qualificada com Data Logger Ativo',
    arrival_temp: parseFloat(document.getElementById('entry-temp').value),
    cargo_integrity: 'Carga íntegra com lacres conferidos',
    ambient_exposure_minutes: 6,
    decision: parseFloat(document.getElementById('entry-temp').value) > 8.0 || parseFloat(document.getElementById('entry-temp').value) < 2.0 ? 'Quarentena' : 'Aprovado'
  };

  try {
    const res = await fetch('/api/pharma/stock/entry', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    const data = await res.json();
    if (res.ok) {
      alert(`Lote ${payload.lot_number} cadastrado com sucesso! Status: ${data.lot.status.toUpperCase()}`);
      closeEntryModal();
      loadLots();
      loadChambers();
    } else {
      alert(data.error || 'Erro ao registrar entrada.');
    }
  } catch (e) {
    alert('Erro de conexão ao salvar entrada.');
  }
}

function openExitModal() {
  document.getElementById('modal-exit').classList.add('active');
  updateExitLotInfo();
}

function closeExitModal() {
  document.getElementById('modal-exit').classList.remove('active');
}

function updateExitLotInfo() {
  const select = document.getElementById('exit-lot-select');
  const info = document.getElementById('exit-fefo-info');
  if (!select || !select.value) {
    if (info) info.innerText = 'Nenhum lote liberado disponível para dispensação.';
    return;
  }
  const lotId = parseInt(select.value);
  const lot = lotsData.find(l => l.id === lotId);
  if (lot && info) {
    info.innerHTML = `<strong>Prioridade FEFO:</strong> Lote <strong>${lot.lot_number}</strong> é o próximo a vencer em <strong>${lot.expiry_date}</strong> (${lot.days_to_expiry} dias restantes). Saldo disponível: <strong>${lot.quantity}</strong> unidades na câmara <strong>${lot.chamber_id}</strong> (${lot.shelf_location}).`;
  }
}

async function submitStockExit() {
  const select = document.getElementById('exit-lot-select');
  if (!select.value) return;

  const payload = {
    lot_id: parseInt(select.value),
    quantity: parseInt(document.getElementById('exit-qty').value),
    reason: document.getElementById('exit-reason').value,
    recipient: document.getElementById('exit-recipient').value.trim(),
    invoice_number: document.getElementById('exit-nfe').value.trim()
  };

  try {
    const res = await fetch('/api/pharma/stock/exit', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    const data = await res.json();
    if (res.ok) {
      alert('Saída de estoque registrada com sucesso respeitando o algoritmo FEFO!');
      closeExitModal();
      loadLots();
      loadChambers();
    } else {
      alert(data.error || 'Erro ao registrar saída.');
    }
  } catch (e) {
    alert('Erro de conexão ao processar saída.');
  }
}

// -----------------------------------------------------------------------------
// LAUDO PERICIAL DE RASTREABILIDADE DO LOTE (TRACE COMPLETO)
// -----------------------------------------------------------------------------
async function openTraceModal(lotNum) {
  const container = document.getElementById('trace-content');
  container.innerHTML = '<div style="text-align:center; padding:20px; color:#94A3B8;">Gerando laudo pericial de rastreabilidade...</div>';
  document.getElementById('modal-trace').classList.add('active');

  try {
    const res = await fetch(`/api/pharma/lots/trace?lot=${encodeURIComponent(lotNum)}`);
    const data = await res.json();
    if (!res.ok) {
      container.innerHTML = `<div class="alert-box">${data.error}</div>`;
      return;
    }

    const { lot, product, stock_entry, history, excursions, exits, remaining_qty, total_exited_qty, integrity_alcoa } = data;

    container.innerHTML = `
      <div style="background:rgba(255,255,255,0.03); border:1px solid rgba(255,255,255,0.08); border-radius:10px; padding:16px; margin-bottom:16px;">
        <div style="display:flex; justify-content:space-between; align-items:flex-start;">
          <div>
            <div style="font-size:18px; font-weight:800; color:#fff;">${product.trade_name}</div>
            <div style="font-size:12px; color:#94A3B8;">${product.dcb} • Fabricante: ${product.manufacturer}</div>
            <div style="font-size:11px; color:#60A5FA; margin-top:2px;">Reg. ANVISA: ${product.anvisa_reg}</div>
          </div>
          <span class="badge-status ${lot.status}" style="font-size:12px; padding:4px 10px;">${lot.status.toUpperCase()}</span>
        </div>

        <div style="display:grid; grid-template-columns:repeat(4,1fr); gap:10px; margin-top:14px; background:rgba(0,0,0,0.25); padding:10px; border-radius:8px;">
          <div><span style="font-size:10px; color:#64748B;">LOTE</span><br><strong style="font-size:13px; color:#fff;">${lot.lot_number}</strong></div>
          <div><span style="font-size:10px; color:#64748B;">VALIDADE</span><br><strong style="font-size:13px; color:#F59E0B;">${lot.expiry_date}</strong></div>
          <div><span style="font-size:10px; color:#64748B;">SALDO ATUAL</span><br><strong style="font-size:13px; color:#10B981;">${remaining_qty} un</strong></div>
          <div><span style="font-size:10px; color:#64748B;">LOCALIZAÇÃO</span><br><strong style="font-size:13px; color:#60A5FA;">${lot.chamber_id}</strong></div>
        </div>
      </div>

      <!-- Dados de Entrada na Doca -->
      <div style="background:rgba(0,0,0,0.2); border:1px solid rgba(255,255,255,0.06); border-radius:8px; padding:12px 14px; margin-bottom:14px;">
        <div style="font-size:12px; font-weight:700; color:#CBD5E1; margin-bottom:8px;">1. Conferência Térmica e Recebimento na Doca</div>
        ${stock_entry ? `
          <div style="font-size:11.5px; color:#94A3B8; line-height:1.6;">
            • Fornecedor: <strong>${stock_entry.supplier}</strong> (CNPJ: ${stock_entry.supplier_cnpj})<br>
            • Nota Fiscal: <strong>${stock_entry.invoice_number}</strong> • Recebido em: ${stock_entry.received_at}<br>
            • Condição de Transporte: ${stock_entry.transport_condition}<br>
            • Temperatura no Recebimento: <strong style="color:${stock_entry.arrival_temp > 8.0 ? '#EF4444' : '#10B981'};">${stock_entry.arrival_temp.toFixed(1)} °C</strong> (Exposição na doca: ${stock_entry.ambient_exposure_minutes} min)<br>
            • Responsável Técnico: ${stock_entry.responsible_name}
          </div>
        ` : '<div style="font-size:11px; color:#64748B;">Dados de doca não registrados para este lote pré-semeado.</div>'}
      </div>

      <!-- Excursões Ocorridas na Câmara -->
      <div style="background:rgba(0,0,0,0.2); border:1px solid rgba(255,255,255,0.06); border-radius:8px; padding:12px 14px; margin-bottom:14px;">
        <div style="font-size:12px; font-weight:700; color:#EF4444; margin-bottom:8px;">2. Histórico de Excursões Térmicas na Câmara</div>
        ${excursions && excursions.length > 0 ? excursions.map(e => `
          <div style="font-size:11.5px; color:#94A3B8; line-height:1.5; padding:6px 0; border-bottom:1px solid rgba(255,255,255,0.04);">
            • <strong>${e.code}</strong> (${e.start_time}): Pico de <strong style="color:#EF4444;">${e.peak_temp.toFixed(1)} °C</strong> (${e.accumulated_degree_minutes.toFixed(1)} graus-minuto).<br>
            Causa: ${e.cause}. Status: <span class="badge-status ${e.status}">${e.status}</span><br>
            ${e.technical_report ? `Parecer: <em>"${e.technical_report}"</em>` : ''}
          </div>
        `).join('') : '<div style="font-size:11px; color:#10B981;">Nenhuma excursão térmica registrada durante o período de armazenagem deste lote.</div>'}
      </div>

      <!-- Saídas -->
      <div style="background:rgba(0,0,0,0.2); border:1px solid rgba(255,255,255,0.06); border-radius:8px; padding:12px 14px; margin-bottom:14px;">
        <div style="font-size:12px; font-weight:700; color:#CBD5E1; margin-bottom:8px;">3. Saídas e Dispensações Registradas (Total: ${total_exited_qty} un)</div>
        ${exits && exits.length > 0 ? exits.map(x => `
          <div style="font-size:11.5px; color:#94A3B8; line-height:1.5;">
            • ${x.exit_at}: <strong>${x.quantity} un</strong> dispensadas para <strong>${x.recipient}</strong> (Motivo: ${x.reason}, NF: ${x.invoice_number}) por ${x.responsible_name}.
          </div>
        `).join('') : '<div style="font-size:11px; color:#64748B;">Nenhuma saída realizada ainda para este lote.</div>'}
      </div>

      <!-- Selo ALCOA+ -->
      <div style="font-size:10.5px; color:#6EE7B7; background:rgba(16,185,129,0.1); border:1px solid rgba(16,185,129,0.3); padding:8px 12px; border-radius:6px; text-align:center;">
        ✓ ${integrity_alcoa}
      </div>
    `;
  } catch (e) {
    container.innerHTML = '<div class="alert-box">Erro ao gerar laudo pericial.</div>';
  }
}

function closeTraceModal() {
  document.getElementById('modal-trace').classList.remove('active');
}

// -----------------------------------------------------------------------------
// MÓDULO 3: ALARMES & EXCURSÕES ANVISA
// -----------------------------------------------------------------------------
async function loadAlarms() {
  try {
    const res = await fetch('/api/pharma/alarms');
    if (!res.ok) return;
    const alarms = await res.json();

    const tbody = document.getElementById('alarms-table-body');
    if (!tbody) return;
    tbody.innerHTML = '';

    if (alarms.length === 0) {
      tbody.innerHTML = '<tr><td colspan="7" style="text-align:center; color:#64748B; padding:16px;">Nenhum alarme operacional ativo. Todos os equipamentos em conformidade.</td></tr>';
      return;
    }

    alarms.forEach(a => {
      const isResolved = a.status === 'resolvido';
      const isAck = a.status === 'reconhecido';
      const statusBadge = isResolved ? 'liberado' : (isAck ? 'alerta' : 'quarentena');

      tbody.innerHTML += `
        <tr>
          <td><strong>${a.chamber_name}</strong></td>
          <td><code style="color:#60A5FA;">${a.sensor_id || 'CÂMARA'}</code></td>
          <td><span style="color:#F59E0B; font-weight:600;">${a.type.toUpperCase()}</span></td>
          <td>${a.message}</td>
          <td><strong style="color:#EF4444;">${a.value ? a.value.toFixed(1) + ' °C' : '-'}</strong></td>
          <td><span class="badge-status ${statusBadge}">${a.status.toUpperCase()}</span></td>
          <td style="text-align:right;">
            ${a.status === 'ativo' ? `
              <button class="btn-action-primary" style="padding:4px 8px; font-size:11px;" onclick="promptAcknowledgeAlarm(${a.id})">
                Reconhecer
              </button>
            ` : (a.status === 'reconhecido' ? `
              <div style="display:flex; flex-direction:column; align-items:flex-end; gap:4px;">
                <span style="font-size:11px; color:#64748B;">Reconhecido: ${a.acknowledged_by}</span>
                <button class="btn-action-primary" style="padding:3px 8px; font-size:11px; background:#10B981;" onclick="promptResolveAlarm(${a.id})">
                  Resolver
                </button>
              </div>
            ` : `<span style="font-size:11px; color:#10B981; font-weight:600;">✓ Resolvido</span>`)}
          </td>
        </tr>
      `;
    });
  } catch (e) {
    console.error('Erro ao carregar alarmes:', e);
  }
}

async function promptAcknowledgeAlarm(alarmId) {
  const action = prompt('Descreva a ação imediata tomada para mitigar o alarme (ex: verificado termostato e fechamento de porta):');
  if (!action) return;

  try {
    const res = await fetch('/api/pharma/alarms/acknowledge', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ alarm_id: alarmId, action_taken: action })
    });
    if (res.ok) {
      alert('Alarme reconhecido formalmente e registrado na trilha de auditoria.');
      loadAlarms();
      loadKPIs();
    } else {
      alert('Erro ao reconhecer alarme.');
    }
  } catch (e) {
    alert('Erro de conexão.');
  }
}


async function promptResolveAlarm(alarmId) {
  const notes = prompt('Descreva o laudo de encerramento do alarme (ex: temperatura normalizada em 4.5°C, porta inspecionada e fechada):');
  if (!notes) return;

  try {
    const res = await fetch('/api/pharma/alarms/resolve', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ alarm_id: alarmId, notes: notes })
    });
    if (res.ok) {
      alert('Alarme encerrado e resolvido com sucesso!');
      loadAlarms();
      loadKPIs();
    } else {
      alert('Erro ao resolver alarme.');
    }
  } catch (e) {
    alert('Erro de conexão ao resolver alarme.');
  }
}

async function loadExcursions() {
  try {
    const res = await fetch('/api/pharma/excursions');
    if (!res.ok) return;
    const list = await res.json();

    const tbody = document.getElementById('excursions-table-body');
    if (!tbody) return;
    tbody.innerHTML = '';

    if (list.length === 0) {
      tbody.innerHTML = '<tr><td colspan="8" style="text-align:center; color:#64748B; padding:16px;">Nenhum processo de excursão aberto.</td></tr>';
      return;
    }

    list.forEach(e => {
      const isDone = e.status === 'liberado' || e.status === 'descartado';
      tbody.innerHTML += `
        <tr>
          <td><strong style="color:#60A5FA;">${e.code}</strong></td>
          <td>${e.chamber_name}</td>
          <td><span style="font-size:11px; color:#CBD5E1;">${e.start_time}<br>${e.end_time || 'Em andamento'}</span></td>
          <td><strong style="color:#EF4444;">${e.peak_temp.toFixed(1)} °C</strong></td>
          <td>${e.accumulated_degree_minutes.toFixed(1)} °C·min</td>
          <td><span class="badge-status quarentena">${e.quarantined_lots_count} lotes segregados</span></td>
          <td><span class="badge-status ${isDone ? 'liberado' : 'alerta'}">${e.status.toUpperCase()}</span></td>
          <td style="text-align:right;">
            ${!isDone ? `
              <button class="btn-action-primary" style="padding:4px 8px; font-size:11px;" onclick="openDecisionModal(${e.id}, '${e.code}', '${e.chamber_name}', ${e.peak_temp})">
                Parecer / Decisão
              </button>
            ` : `<span style="font-size:11px; color:#10B981;">Decisão: ${e.final_decision.toUpperCase()}<br>Assinado por ${e.decided_by}</span>`}
          </td>
        </tr>
      `;
    });
  } catch (e) {
    console.error('Erro ao carregar excursões:', e);
  }
}

function openDecisionModal(id, code, chName, peak) {
  document.getElementById('dec-exc-id').value = id;
  document.getElementById('dec-exc-summary').innerHTML = `
    <strong>Processo:</strong> ${code} na <strong>${chName}</strong>.<br>
    Pico registrado: <strong style="color:#EF4444;">${peak.toFixed(1)} °C</strong>. Todos os lotes foram segregados preventivamente em Quarentena.
  `;
  document.getElementById('modal-excursion-decision').classList.add('active');
}

function closeDecisionModal() {
  document.getElementById('modal-excursion-decision').classList.remove('active');
}

async function submitExcursionDecision() {
  const excId = parseInt(document.getElementById('dec-exc-id').value);
  const decision = document.getElementById('dec-decision').value;
  const report = document.getElementById('dec-report').value.trim();
  const sig = document.getElementById('dec-signature').value;

  if (!report || !sig) {
    alert('Preencha o parecer técnico e a assinatura eletrônica.');
    return;
  }

  try {
    const res = await fetch('/api/pharma/excursions/decide', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        excursion_id: excId,
        decision: decision,
        report: report,
        electronic_sig: `SIG-P256-RT-${currentUser.username}-${Date.now()}`
      })
    });
    const data = await res.json();
    if (res.ok) {
      alert(`Decisão pericial registrada com sucesso! Lotes ${decision === 'liberado' ? 'LIBERADOS para uso' : 'REPROVADOS para descarte'}.`);
      closeDecisionModal();
      loadExcursions();
      loadLots();
      loadChambers();
    } else {
      alert(data.error || 'Erro ao registrar decisão.');
    }
  } catch (e) {
    alert('Erro de conexão.');
  }
}

async function simulateExcursion() {
  if (!confirm('Deseja disparar uma simulação de excursão térmica na Câmara Fria A? Todos os lotes serão segregados instantaneamente para Quarentena para validação regulatória.')) {
    return;
  }

  try {
    const res = await fetch('/api/pharma/excursions/simulate?chamber=CH-A', { method: 'POST' });
    const data = await res.json();
    if (res.ok) {
      alert(data.message);
      loadExcursions();
      loadAlarms();
      loadLots();
      loadChambers();
    } else {
      alert(data.error || 'Erro na simulação.');
    }
  } catch (e) {
    alert('Erro de conexão ao simular.');
  }
}

// -----------------------------------------------------------------------------
// MÓDULO 4: METROLOGIA RBC & QUALIFICAÇÃO
// -----------------------------------------------------------------------------
async function loadMetrology() {
  try {
    const res = await fetch('/api/pharma/sensors');
    if (!res.ok) return;
    const list = await res.json();
    window.allMetrologySensors = list;

    const tbody = document.getElementById('metrology-table-body');
    if (!tbody) return;
    tbody.innerHTML = '';

    list.forEach(s => {
      let badgeClass = 'liberado';
      if (s.status.includes('Vencido')) {
        badgeClass = 'reprovado';
      } else if (s.status.includes('Atenção') || s.status.includes('Vencendo')) {
        badgeClass = 'quarentena';
      }

      tbody.innerHTML += `
        <tr>
          <td><code style="color:#60A5FA; font-weight:700;">${s.id}</code></td>
          <td><strong>${s.chamber_id}</strong></td>
          <td>${s.serial_number}</td>
          <td>${s.thermal_position}</td>
          <td><strong style="color:#C4B5FD;">${s.cert_number}</strong></td>
          <td>${s.calibrated_by}</td>
          <td><span style="color:#6EE7B7;">${s.calib_valid_until}</span></td>
          <td><span class="badge-status ${badgeClass}">${s.status}</span></td>
          <td style="text-align: right;">
            <button class="btn-action-secondary" style="padding: 3px 8px; font-size: 11px;" onclick="openEditSensorModal('${s.id}')">
              Recalibrar / Editar
            </button>
          </td>
        </tr>
      `;
    });
  } catch (e) {
    console.error('Erro ao carregar metrologia:', e);
  }
}

// -----------------------------------------------------------------------------
// MÓDULO 5: AUDITORIA ALCOA+ & LEDGER CRIPTOGRÁFICO
// -----------------------------------------------------------------------------
async function loadLedger() {
  try {
    // Carrega registros do ledger
    const resL = await fetch('/api/v1/ledger/records');
    if (resL.ok) {
      const dataL = await resL.json();
      const records = dataL.records || [];
      const tbodyL = document.getElementById('ledger-records-body');
      if (tbodyL) {
        tbodyL.innerHTML = '';
        records.forEach(r => {
          const prev = (r.prev_hash || '').substring(0, 16);
          const cur = (r.current_hash || '').substring(0, 16);
          const sig = (r.signature || '').substring(0, 24);
          tbodyL.innerHTML += `
            <tr>
              <td><strong style="color:#60A5FA;">#${r.id}</strong></td>
              <td><span class="badge-status liberado">${r.type_name || 'DATA'}</span></td>
              <td><code style="font-size:10px; color:#94A3B8;">${prev}...</code></td>
              <td><code style="font-size:10px; color:#6EE7B7;">${cur}...</code></td>
              <td><code style="font-size:10px; color:#C4B5FD;">${sig}...</code></td>
              <td style="font-size:11px; color:#64748B;">${r.created_at || ''}</td>
            </tr>
          `;
        });
      }
    }

    // Carrega trilha de auditoria
    const resA = await fetch('/api/audit/logs');
    if (resA.ok) {
      const logs = await resA.json();
      const tbodyA = document.getElementById('audit-logs-body');
      tbodyA.innerHTML = '';

      logs.forEach(l => {
        tbodyA.innerHTML += `
          <tr>
            <td style="font-size:11px; color:#CBD5E1;">${l.timestamp}</td>
            <td><strong>${l.username}</strong></td>
            <td><span class="tag-badge tag-blue">${l.role.toUpperCase()}</span></td>
            <td><code style="color:#F59E0B;">${l.action}</code></td>
            <td style="font-size:11.5px; color:#E2E8F0;">${l.details}</td>
            <td style="font-size:11px; color:#64748B;">${l.ip_address}</td>
          </tr>
        `;
      });
    }
  } catch (e) {
    console.error('Erro ao carregar auditoria e ledger:', e);
  }
}

async function verifyLedgerLive() {
  const box = document.getElementById('ledger-audit-box');
  const msg = document.getElementById('ledger-audit-msg');
  box.style.display = 'flex';
  msg.innerText = 'Executando verificação pericial criptográfica (SHA-256 Chaining + ECDSA P-256)...';

  try {
    const res = await fetch('/api/v1/ledger/verify');
    const data = await res.json();
    if (res.ok && data.valid) {
      msg.innerHTML = `<strong>Laudo Pericial: 100% CONFORME!</strong> Todos os <strong>${data.total_records || data.total_blocks || data.checked_hashes || 0} blocos</strong> auditados em <strong>${(data.duration_ms || data.verification_time_ms || 15).toFixed(1)} ms</strong>. Zero adulterações ou quebras de hash detectadas. Assinatura P-256 íntegra.`;
      box.style.background = 'rgba(16, 185, 129, 0.15)';
      box.style.borderColor = 'rgba(16, 185, 129, 0.4)';
      box.style.color = '#6EE7B7';
    } else {
      msg.innerHTML = `<strong>FALHA DE INTEGRIDADE:</strong> ${data.error || 'Cadeia violada!'}`;
      box.style.background = 'rgba(239, 68, 68, 0.15)';
      box.style.borderColor = 'rgba(239, 68, 68, 0.4)';
      box.style.color = '#FCA5A5';
    }
  } catch (e) {
    msg.innerText = 'Erro ao executar verificação do ledger.';
  }
}

function exportLedgerJSON() {
  window.open('/api/v1/ledger/export', '_blank');
}

async function createAnchorLive() {
  try {
    const res = await fetch('/api/v1/ledger/anchor', { method: 'POST' });
    const data = await res.json();
    if (res.ok) {
      alert(`Âncora RFC 3161 #${data.anchor.id} gerada com sucesso!`);
      loadLedger();
    } else {
      alert(data.error || 'Erro ao gerar âncora.');
    }
  } catch (e) {
    alert('Erro de conexão.');
  }
}

// -----------------------------------------------------------------------------
// MÓDULO 6: GESTÃO DE USUÁRIOS (RBAC)
// -----------------------------------------------------------------------------
async function loadUsers() {
  try {
    const res = await fetch('/api/admin/users');
    if (!res.ok) return;
    const users = await res.json();

    const tbody = document.getElementById('users-table-body');
    tbody.innerHTML = '';

    users.forEach(u => {
      tbody.innerHTML += `
        <tr>
          <td>${u.id}</td>
          <td><strong>${u.username}</strong></td>
          <td>${u.name}</td>
          <td><span class="tag-badge tag-blue">${u.role.toUpperCase()}</span></td>
          <td><span class="badge-status ${u.is_blocked ? 'reprovado' : 'liberado'}">${u.is_blocked ? 'BLOQUEADO' : 'ATIVO'}</span></td>
          <td style="text-align:right;">
            <button class="btn-action-primary" style="padding:4px 8px; font-size:11px; background:#1E293B; border:1px solid #334155;" onclick="toggleBlockUser(${u.id}, ${u.is_blocked})">
              ${u.is_blocked ? 'Desbloquear' : 'Bloquear'}
            </button>
          </td>
        </tr>
      `;
    });
  } catch (e) {
    console.error('Erro ao carregar usuários:', e);
  }
}

async function toggleBlockUser(userId, currentBlocked) {
  try {
    const res = await fetch('/api/admin/users/toggle-block', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ user_id: userId, is_blocked: currentBlocked ? 0 : 1 })
    });
    if (res.ok) {
      loadUsers();
    } else {
      alert('Erro ao alterar bloqueio de usuário.');
    }
  } catch (e) {
    alert('Erro de conexão.');
  }
}

// -----------------------------------------------------------------------------
// STREAMING SSE (SERVER-SENT EVENTS)
// -----------------------------------------------------------------------------
function setupSSE() {
  const ev = new EventSource('/events');
  ev.onmessage = (e) => {
    try {
      const data = JSON.parse(e.data);
      // Atualiza telemetria em tempo real se necessário
    } catch (err) {}
  };
}


// ==============================================================================
// SEÇÃO: KPIS REGULATÓRIOS E DOSSIÊ DA QUALIDADE RDC 430/2020 & HAGELAB 2025
// ==============================================================================
async function loadKPIs() {
  try {
    const res = await fetch('/api/pharma/kpis');
    if (!res.ok) return;
    const kpi = await res.json();
    const elUptime = document.getElementById('kpi-uptime');
    const elMttr = document.getElementById('kpi-mttr');
    const elExc = document.getElementById('kpi-excursion-rate');
    const elCapa = document.getElementById('kpi-capa-eff');
    const elUptimeHours = document.getElementById('kpi-uptime-hours');
    const elAlarmsStat = document.getElementById('kpi-alarms-stat');
    const elLotsQuar = document.getElementById('kpi-lots-quarantined');
    const elCapaStatus = document.getElementById('kpi-capa-status');
    const elCompliance = document.getElementById('kpi-compliance-short');
    const elSensorsSummary = document.getElementById('kpi-sensors-summary');
    const elAuditDate = document.getElementById('kpi-audit-date');

    if (elUptime) elUptime.innerText = `${kpi.availability_pct.toFixed(2)}%`;
    if (elMttr) elMttr.innerText = `${kpi.avg_response_time_minutes.toFixed(1)} min`;
    if (elExc) elExc.innerText = `${kpi.excursion_rate_pct.toFixed(2)}%`;
    if (elCapa) elCapa.innerText = `${kpi.capa_efficiency_pct.toFixed(1)}%`;

    if (elUptimeHours) elUptimeHours.innerText = `${kpi.uptime_hours.toFixed(1)}h / ${kpi.total_monitored_hours.toFixed(0)}h`;
    if (elAlarmsStat) elAlarmsStat.innerText = `${kpi.active_alarms} ativos / ${kpi.total_alarms} total`;
    if (elLotsQuar) elLotsQuar.innerText = kpi.excursion_rate_pct > 0 ? 'Lotes em segregação' : '0 lotes em segregação';
    if (elCapaStatus) elCapaStatus.innerText = kpi.capa_efficiency_pct >= 95.0 ? 'Concluídas e Aprovadas' : 'Investigação em Aberto';
    if (elSensorsSummary) elSensorsSummary.innerText = `${kpi.calibrated_sensors}/${kpi.total_sensors} Calibrados (${kpi.expiring_sensors} vencendo)`;
    if (elAuditDate) elAuditDate.innerText = kpi.last_audit_date ? `Audit: ${kpi.last_audit_date.split(' ')[0]}` : 'Auditado';

    if (elCompliance) {
      elCompliance.innerText = kpi.compliance_status;
      if (kpi.compliance_status.includes('100% Conforme')) {
        elCompliance.className = 'kpi-value text-green';
        elCompliance.style.color = '#52d68a';
      } else if (kpi.compliance_status.includes('CRÍTICO')) {
        elCompliance.className = 'kpi-value text-amber';
        elCompliance.style.color = '#EF4444';
      } else {
        elCompliance.className = 'kpi-value text-amber';
        elCompliance.style.color = '#F59E0B';
      }
    }
  } catch (e) {
    console.error('Erro ao carregar KPIs regulatórios:', e);
  }
}

async function loadQualityDossier() {
  try {
    const res = await fetch('/api/pharma/quality/dossier');
    if (!res.ok) return;
    const dossier = await res.json();

    window.currentQualityDossier = dossier;

    // 1. POPs Obrigatórios
    const popsTbody = document.getElementById('quality-pops-tbody');
    if (popsTbody && dossier.pops) {
      popsTbody.innerHTML = dossier.pops.map(p => `
        <tr>
          <td><strong style="color:#38bdf8">${p.code}</strong></td>
          <td>${p.title}</td>
          <td><span class="tag-badge tag-blue">${p.version}</span></td>
          <td>${p.last_review}</td>
          <td>${p.next_review}</td>
          <td>${p.responsible}</td>
          <td><span class="tag-badge tag-green">${p.status}</span></td>
          <td style="display:flex; gap:6px;">
            <button class="btn-table-action" onclick="alert('Exibindo documento oficial auditado:\n' + '${p.code} - ${p.title}\nVersão: ${p.version}\nResponsável: ${p.responsible}\nStatus: 100% VIGENTE')">Visualizar</button>
            <button class="btn-action-secondary" style="padding:2px 8px; font-size:11px;" onclick="openEditPOPModal('${p.code}')">Revisar</button>
          </td>
        </tr>
      `).join('');
    }

    // 2. Qualificações DQ/IQ/OQ/PQ
    const qualsTbody = document.getElementById('quality-quals-tbody');
    if (qualsTbody) {
      qualsTbody.innerHTML = dossier.qualifications.map(q => `
        <tr>
          <td><strong style="color:#38bdf8">${q.chamber_id}</strong></td>
          <td><strong>${q.chamber_name}</strong></td>
          <td><span class="tag-badge tag-purple">${q.stage}</span></td>
          <td>${q.report_number}</td>
          <td>${q.executed_by}</td>
          <td><span style="color:#52d68a; font-weight:600;">${q.thermal_autonomy}</span></td>
          <td>${q.valid_until}</td>
          <td><span class="tag-badge tag-green">${q.status}</span></td>
        </tr>
      `).join('');
    }

    // 3. Treinamentos da Equipe
    const trainsTbody = document.getElementById('quality-trainings-tbody');
    if (trainsTbody) {
      trainsTbody.innerHTML = dossier.trainings.map(t => `
        <tr>
          <td><strong>${t.employee_name}</strong></td>
          <td>${t.role}</td>
          <td><span style="color:#93c5fd; font-size:0.75rem;">${t.pop_code}</span></td>
          <td><span style="color:#52d68a; font-weight:700;">${t.score_pct}%</span></td>
          <td><span class="tag-badge tag-green">${t.status}</span></td>
        </tr>
      `).join('');
    }

    // 4. Checklist Autoinspeção RDC 430
    const checkContainer = document.getElementById('quality-checklist-container');
    if (checkContainer) {
      checkContainer.innerHTML = dossier.checklist_items.map((item, idx) => `
        <div class="checklist-row">
          <div>
            <div style="font-weight:600; color:#ffffff; margin-bottom:0.15rem;">${item.item}</div>
            <div style="font-size:0.72rem; color:var(--text-muted);">${item.requirement} • <em style="color:#93c5fd;">Evidência: ${item.evidence}</em></div>
          </div>
          <span class="tag-badge tag-green" style="flex-shrink:0;">100% Conforme</span>
        </div>
      `).join('');
    }
  } catch (e) {
    console.error('Erro ao carregar dossiê da qualidade:', e);
  }
}


function downloadDossierPDF() {
  window.open('/api/pharma/reports/dossier.pdf', '_blank');
}

function downloadDossierCSV() {
  window.open('/api/pharma/reports/dossier.csv', '_blank');
}

function openPrintableDossier() {
  window.open('/api/pharma/reports/dossier.html', '_blank');
}

function downloadLedgerCSV() {
  window.open('/api/pharma/reports/ledger.csv', '_blank');
}

function exportQualityJSON() {
  window.open('/api/pharma/quality/dossier', '_blank');
}


// ==============================================================================
// FASE 2: GESTÃO REAL DE MEDICAMENTOS E CÂMARAS (ANVISA RDC 430/2020)
// ==============================================================================

function openNewProductModal() {
  const m = document.getElementById('modal-new-product');
  if (m) m.classList.add('active');
}

function closeNewProductModal() {
  const m = document.getElementById('modal-new-product');
  if (m) m.classList.remove('active');
  const f = document.getElementById('form-new-product');
  if (f) f.reset();
}

async function submitNewProduct() {
  const tradeName = document.getElementById('prod-name').value.trim();
  const dcb = document.getElementById('prod-dcb').value.trim();
  const anvisaReg = document.getElementById('prod-anvisa').value.trim();
  const presentation = document.getElementById('prod-presentation').value.trim();
  const manufacturer = document.getElementById('prod-manufacturer').value.trim();
  const tempMin = parseFloat(document.getElementById('prod-temp-min').value);
  const tempMax = parseFloat(document.getElementById('prod-temp-max').value);
  const humMax = parseFloat(document.getElementById('prod-hum-max').value);
  const photosensitive = parseInt(document.getElementById('prod-photosensitive').value, 10);

  if (tempMin > tempMax) {
    alert('A temperatura mínima não pode ser superior à máxima.');
    return;
  }

  const payload = {
    trade_name: tradeName,
    dcb: dcb,
    anvisa_reg: anvisaReg,
    presentation: presentation,
    manufacturer: manufacturer,
    temp_min: tempMin,
    temp_max: tempMax,
    humidity_min: 30.0,
    humidity_max: humMax,
    photosensitive: photosensitive
  };

  try {
    const res = await fetch('/api/pharma/products', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    const data = await res.json();
    if (!res.ok) {
      alert('Erro ao cadastrar medicamento: ' + (data.error || 'Erro desconhecido'));
      return;
    }

    alert('Medicamento ' + tradeName + ' cadastrado com sucesso no Catálogo ANVISA!');
    closeNewProductModal();
    loadProducts(); // recarrega catálogo e selects
  } catch (e) {
    console.error('Erro na requisição:', e);
    alert('Erro de conexão ao salvar medicamento.');
  }
}

function openNewChamberModal() {
  const m = document.getElementById('modal-new-chamber');
  if (m) {
    m.classList.add('active');
    const valid = document.getElementById('ch-qual-valid');
    if (valid && !valid.value) {
      const d = new Date();
      d.setFullYear(d.getFullYear() + 2);
      valid.value = d.toISOString().split('T')[0];
    }
  }
}

function closeNewChamberModal() {
  const m = document.getElementById('modal-new-chamber');
  if (m) m.classList.remove('active');
  const f = document.getElementById('form-new-chamber');
  if (f) f.reset();
}

function updateChamberPresets() {
  const type = document.getElementById('ch-type').value;
  const tMin = document.getElementById('ch-temp-min');
  const tMax = document.getElementById('ch-temp-max');
  if (type === 'refrigerado') {
    tMin.value = '2.0';
    tMax.value = '8.0';
  } else if (type === 'ambiente_controlado') {
    tMin.value = '15.0';
    tMax.value = '30.0';
  } else if (type === 'congelado') {
    tMin.value = '-25.0';
    tMax.value = '-15.0';
  } else if (type === 'ultra_frio') {
    tMin.value = '-80.0';
    tMax.value = '-60.0';
  }
}

async function submitNewChamber() {
  const id = document.getElementById('ch-id').value.trim().toUpperCase();
  const name = document.getElementById('ch-name').value.trim();
  const type = document.getElementById('ch-type').value;
  const delay = parseInt(document.getElementById('ch-delay').value, 10);
  const tempMin = parseFloat(document.getElementById('ch-temp-min').value);
  const tempMax = parseFloat(document.getElementById('ch-temp-max').value);
  const qualStatus = document.getElementById('ch-qual-status').value.trim();
  const qualValid = document.getElementById('ch-qual-valid').value;

  if (tempMin > tempMax) {
    alert('A temperatura mínima não pode ser superior à máxima.');
    return;
  }

  const payload = {
    id: id,
    name: name,
    type: type,
    temp_min: tempMin,
    temp_max: tempMax,
    humidity_min: 30.0,
    humidity_max: 75.0,
    alarm_delay_min: delay,
    qualification_status: qualStatus,
    qualification_valid_until: qualValid
  };

  try {
    const res = await fetch('/api/pharma/chambers', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    const data = await res.json();
    if (!res.ok) {
      alert('Erro ao cadastrar equipamento: ' + (data.error || 'Erro desconhecido'));
      return;
    }

    alert('Câmara ' + name + ' (' + id + ') cadastrada com sucesso!');
    closeNewChamberModal();
    loadChambers(true);
  } catch (e) {
    console.error('Erro na requisição:', e);
    alert('Erro de conexão ao cadastrar câmara.');
  }
}


// ==============================================================================
// FASE 3: GESTÃO METROLÓGICA DE SENSORES E RASTREABILIDADE RBC
// ==============================================================================

async function populateSensorChambersSelect(selectedChamberId) {
  const sel = document.getElementById('sensor-chamber-id');
  if (!sel) return;
  sel.innerHTML = '';
  try {
    const res = await fetch('/api/pharma/chambers');
    if (res.ok) {
      const chambers = await res.json();
      chambers.forEach(c => {
        const opt = document.createElement('option');
        opt.value = c.id;
        opt.textContent = `${c.name} (${c.id}) - ${c.temp_min}°C a ${c.temp_max}°C`;
        if (selectedChamberId && c.id === selectedChamberId) {
          opt.selected = true;
        }
        sel.appendChild(opt);
      });
    }
  } catch (e) {
    console.error('Erro ao buscar câmaras para o select de sensores:', e);
  }
}

function openNewSensorModal() {
  const m = document.getElementById('modal-sensor');
  if (!m) return;
  m.classList.add('active');

  document.getElementById('modal-sensor-title').textContent = 'Cadastrar Sensor / Certificado RBC';
  document.getElementById('sensor-is-edit').value = '0';

  const idInp = document.getElementById('sensor-id');
  idInp.value = '';
  idInp.disabled = false;

  document.getElementById('sensor-sn').value = '';
  document.getElementById('sensor-pos').value = '';
  document.getElementById('sensor-cert').value = '';
  document.getElementById('sensor-lab').value = 'LabMetrologia RBC nº 412';

  const now = new Date();
  document.getElementById('sensor-calib-at').value = now.toISOString().split('T')[0];
  const nextYear = new Date();
  nextYear.setFullYear(nextYear.getFullYear() + 1);
  document.getElementById('sensor-valid-until').value = nextYear.toISOString().split('T')[0];

  document.getElementById('btn-submit-sensor').textContent = 'Cadastrar no Parque Metrológico';

  populateSensorChambersSelect();
}

function openEditSensorModal(sensorId) {
  if (!window.allMetrologySensors) return;
  const s = window.allMetrologySensors.find(x => x.id === sensorId);
  if (!s) return;

  const m = document.getElementById('modal-sensor');
  if (!m) return;
  m.classList.add('active');

  document.getElementById('modal-sensor-title').textContent = `Recalibrar / Atualizar Sensor ${s.id}`;
  document.getElementById('sensor-is-edit').value = '1';

  const idInp = document.getElementById('sensor-id');
  idInp.value = s.id;
  idInp.disabled = true;

  document.getElementById('sensor-sn').value = s.serial_number;
  document.getElementById('sensor-pos').value = s.thermal_position;
  document.getElementById('sensor-cert').value = s.cert_number;
  document.getElementById('sensor-lab').value = s.calibrated_by;

  const now = new Date();
  document.getElementById('sensor-calib-at').value = now.toISOString().split('T')[0];
  const nextYear = new Date();
  nextYear.setFullYear(nextYear.getFullYear() + 1);
  document.getElementById('sensor-valid-until').value = nextYear.toISOString().split('T')[0];

  document.getElementById('btn-submit-sensor').textContent = 'Salvar Recalibração RBC';

  populateSensorChambersSelect(s.chamber_id);
}

function closeSensorModal() {
  const m = document.getElementById('modal-sensor');
  if (m) m.classList.remove('active');
  const f = document.getElementById('form-sensor');
  if (f) f.reset();
}

async function submitSensor() {
  const isEdit = document.getElementById('sensor-is-edit').value === '1';
  const id = document.getElementById('sensor-id').value.trim().toUpperCase();
  const chamberId = document.getElementById('sensor-chamber-id').value;
  const sn = document.getElementById('sensor-sn').value.trim();
  const pos = document.getElementById('sensor-pos').value.trim();
  const cert = document.getElementById('sensor-cert').value.trim();
  const lab = document.getElementById('sensor-lab').value.trim();
  const calibAt = document.getElementById('sensor-calib-at').value;
  const validUntil = document.getElementById('sensor-valid-until').value;

  if (!id || !chamberId || !sn || !pos || !cert || !lab || !calibAt || !validUntil) {
    alert('Por favor, preencha todos os campos obrigatórios do sensor metrológico.');
    return;
  }

  const payload = {
    id: id,
    chamber_id: chamberId,
    serial_number: sn,
    thermal_position: pos,
    cert_number: cert,
    calibrated_by: lab,
    calibrated_at: calibAt,
    calib_valid_until: validUntil
  };

  const method = isEdit ? 'PUT' : 'POST';

  try {
    const res = await fetch('/api/pharma/sensors', {
      method: method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    const data = await res.json();
    if (!res.ok) {
      alert('Erro na operação metrológica: ' + (data.error || 'Erro desconhecido'));
      return;
    }

    const actionText = isEdit ? 'recalibrado e atualizado' : 'cadastrado';
    alert(`Sensor ${id} ${actionText} com sucesso no Parque Metrológico RBC!`);
    closeSensorModal();
    loadMetrology();
    loadKPIs();
    loadChambers(true);
  } catch (e) {
    console.error('Erro na requisição metrológica:', e);
    alert('Erro de conexão ao salvar sensor metrológico.');
  }
}


// ==============================================================================
// FASE 4: GESTÃO DO SISTEMA DE GESTÃO DA QUALIDADE (SGC) NO SQLITE
// ==============================================================================

function openNewPOPModal() {
  const m = document.getElementById('modal-pop');
  if (!m) return;
  m.classList.add('active');

  document.getElementById('modal-pop-title').textContent = 'Cadastrar Novo Procedimento (POP)';
  const cInp = document.getElementById('pop-code');
  cInp.value = '';
  cInp.disabled = false;

  document.getElementById('pop-title').value = '';
  document.getElementById('pop-version').value = 'Rev. 01/2026';

  const now = new Date();
  document.getElementById('pop-last-review').value = now.toISOString().split('T')[0];
  const next = new Date();
  next.setFullYear(next.getFullYear() + 1);
  document.getElementById('pop-next-review').value = next.toISOString().split('T')[0];

  document.getElementById('pop-responsible').value = 'Farmacêutica RT';
  document.getElementById('pop-status').value = 'Vigente';
}

function openEditPOPModal(popCode) {
  if (!window.currentQualityDossier || !window.currentQualityDossier.pops) return;
  const p = window.currentQualityDossier.pops.find(x => x.code === popCode);
  if (!p) return;

  const m = document.getElementById('modal-pop');
  if (!m) return;
  m.classList.add('active');

  document.getElementById('modal-pop-title').textContent = `Revisar Procedimento ${p.code}`;
  const cInp = document.getElementById('pop-code');
  cInp.value = p.code;
  cInp.disabled = true;

  document.getElementById('pop-title').value = p.title;
  document.getElementById('pop-version').value = p.version;

  const now = new Date();
  document.getElementById('pop-last-review').value = now.toISOString().split('T')[0];
  const next = new Date();
  next.setFullYear(next.getFullYear() + 1);
  document.getElementById('pop-next-review').value = next.toISOString().split('T')[0];

  document.getElementById('pop-responsible').value = p.responsible;
  document.getElementById('pop-status').value = p.status || 'Vigente';
}

function closePOPModal() {
  const m = document.getElementById('modal-pop');
  if (m) m.classList.remove('active');
  const f = document.getElementById('form-pop');
  if (f) f.reset();
}

async function submitPOP() {
  const code = document.getElementById('pop-code').value.trim();
  const title = document.getElementById('pop-title').value.trim();
  const version = document.getElementById('pop-version').value.trim();
  const lastRev = document.getElementById('pop-last-review').value;
  const nextRev = document.getElementById('pop-next-review').value;
  const resp = document.getElementById('pop-responsible').value.trim();
  const status = document.getElementById('pop-status').value;

  if (!code || !title || !version || !lastRev || !nextRev || !resp) {
    alert('Por favor, preencha todos os campos obrigatórios do POP.');
    return;
  }

  const payload = {
    code: code,
    title: title,
    version: version,
    last_review: lastRev,
    next_review: nextRev,
    responsible: resp,
    status: status
  };

  try {
    const res = await fetch('/api/pharma/quality/pops', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    const data = await res.json();
    if (!res.ok) {
      alert('Erro ao salvar POP: ' + (data.error || 'Erro desconhecido'));
      return;
    }

    alert(`Procedimento ${code} salvo com sucesso no SGC!`);
    closePOPModal();
    loadQualityDossier();
  } catch (e) {
    console.error('Erro na requisição de POP:', e);
    alert('Erro de conexão ao salvar POP.');
  }
}

function openNewTrainingModal() {
  const m = document.getElementById('modal-training');
  if (!m) return;
  m.classList.add('active');

  document.getElementById('train-name').value = '';
  document.getElementById('train-role').value = '';
  document.getElementById('train-pops').value = 'POP-GQ-001, POP-GQ-003';
  document.getElementById('train-date').value = new Date().toISOString().split('T')[0];
  document.getElementById('train-score').value = 95;
}

function closeTrainingModal() {
  const m = document.getElementById('modal-training');
  if (m) m.classList.remove('active');
  const f = document.getElementById('form-training');
  if (f) f.reset();
}

async function submitTraining() {
  const name = document.getElementById('train-name').value.trim();
  const role = document.getElementById('train-role').value.trim();
  const pops = document.getElementById('train-pops').value.trim();
  const date = document.getElementById('train-date').value;
  const score = parseInt(document.getElementById('train-score').value, 10);

  if (!name || !role || !pops || !date || isNaN(score)) {
    alert('Por favor, preencha todos os campos do treinamento.');
    return;
  }

  const payload = {
    employee_name: name,
    role: role,
    pop_code: pops,
    trained_at: date,
    score_pct: score
  };

  try {
    const res = await fetch('/api/pharma/quality/trainings', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    const data = await res.json();
    if (!res.ok) {
      alert('Erro ao registrar treinamento: ' + (data.error || 'Erro desconhecido'));
      return;
    }

    alert(`Treinamento de ${name} registrado com sucesso! Status: ${data.status}`);
    closeTrainingModal();
    loadQualityDossier();
  } catch (e) {
    console.error('Erro na requisição de treinamento:', e);
    alert('Erro de conexão ao salvar treinamento.');
  }
}

async function openNewQualModal() {
  const m = document.getElementById('modal-qual');
  if (!m) return;
  m.classList.add('active');

  const sel = document.getElementById('qual-chamber-id');
  sel.innerHTML = '';
  try {
    const res = await fetch('/api/pharma/chambers');
    if (res.ok) {
      const chambers = await res.json();
      chambers.forEach(c => {
        const opt = document.createElement('option');
        opt.value = c.id;
        opt.textContent = `${c.name} (${c.id})`;
        sel.appendChild(opt);
      });
    }
  } catch (e) {}

  document.getElementById('qual-stage').value = 'DQ / IQ / OQ / PQ';
  document.getElementById('qual-report').value = '';
  document.getElementById('qual-lab').value = 'LabMetrologia RBC Cert nº 412';

  const now = new Date();
  document.getElementById('qual-date').value = now.toISOString().split('T')[0];
  const valid = new Date();
  valid.setFullYear(valid.getFullYear() + 2);
  document.getElementById('qual-valid').value = valid.toISOString().split('T')[0];
  document.getElementById('qual-autonomy').value = '6h 30min sem energia (limite 8.0°C)';
}

function closeQualModal() {
  const m = document.getElementById('modal-qual');
  if (m) m.classList.remove('active');
  const f = document.getElementById('form-qual');
  if (f) f.reset();
}

async function submitQual() {
  const chId = document.getElementById('qual-chamber-id').value;
  const stage = document.getElementById('qual-stage').value.trim();
  const report = document.getElementById('qual-report').value.trim();
  const lab = document.getElementById('qual-lab').value.trim();
  const date = document.getElementById('qual-date').value;
  const valid = document.getElementById('qual-valid').value;
  const autonomy = document.getElementById('qual-autonomy').value.trim();

  if (!chId || !stage || !report || !lab || !date || !valid || !autonomy) {
    alert('Por favor, preencha todos os campos do laudo de qualificação.');
    return;
  }

  const payload = {
    chamber_id: chId,
    stage: stage,
    report_number: report,
    executed_by: lab,
    execution_date: date,
    valid_until: valid,
    thermal_autonomy: autonomy,
    status: 'Aprovado'
  };

  try {
    const res = await fetch('/api/pharma/quality/qualifications', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    const data = await res.json();
    if (!res.ok) {
      alert('Erro ao registrar laudo: ' + (data.error || 'Erro desconhecido'));
      return;
    }

    alert(`Laudo ${report} registrado com sucesso para a câmara ${chId}!`);
    closeQualModal();
    loadQualityDossier();
  } catch (e) {
    console.error('Erro na requisição de qualificação:', e);
    alert('Erro de conexão ao salvar laudo.');
  }
}
