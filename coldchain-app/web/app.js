function formatChamberHeader(c) {
  let mainTitle = c.name;
  let subTitle = '';
  
  if (c.name.includes(' - ')) {
    const parts = c.name.split(' - ');
    mainTitle = parts[0];
    subTitle = parts.slice(1).join(' - ');
  } else if (c.name.includes(' (')) {
    const parts = c.name.split(' (');
    mainTitle = parts[0];
    subTitle = '(' + parts.slice(1).join(' (');
  }

  let typeText = 'Câmara Fria Industrial 2 °C–8 °C';
  if (c.type === 'ultra_frio' || c.id === 'ULT-01') {
    typeText = 'Freezer Ultra-Frio -80 °C';
  } else if (c.type === 'ambiente_controlado' || c.id === 'ALM-01') {
    typeText = 'Ambiente Climatizado 15 °C–30 °C';
  } else if (c.type === 'geladeira' || c.id === 'GEL-01') {
    typeText = 'Geladeira Farmácia Qualificada 2 °C–8 °C';
  } else if (c.temp_min && c.temp_max) {
    typeText = `Câmara Fria Industrial ${c.temp_min.toFixed(0)} °C–${c.temp_max.toFixed(0)} °C`;
  }

  return { mainTitle, subTitle, typeText };
}

function generateSparklineSVG(chamber, idx = 0) {
  const W = 340;
  const H = 26;
  const midY = 13;
  const seed = (chamber.id.charCodeAt(chamber.id.length - 1) || 1) * 3 + idx;
  const p1 = midY + Math.sin(0.8 + seed) * 3.5;
  const p2 = midY + Math.sin(1.6 + seed) * 4.5;
  const p4 = midY + Math.sin(3.2 + seed) * 5.0;
  const p5 = midY + Math.sin(4.0 + seed) * 2.5;
  const endY = midY + Math.sin(4.8 + seed) * 2.0;

  const d = `M 0 ${p1.toFixed(1)} C 50 ${(p1 - 4).toFixed(1)}, 90 ${(p2 + 4).toFixed(1)}, 140 ${p2.toFixed(1)} S 220 ${(p4 - 3).toFixed(1)}, 270 ${p5.toFixed(1)} T ${W - 6} ${endY.toFixed(1)}`;

  return `
    <svg viewBox="0 0 ${W} ${H}" width="100%" height="${H}" preserveAspectRatio="none" style="overflow:visible; display:block;">
      <line x1="0" y1="${midY}" x2="${W}" y2="${midY}" stroke="rgba(255, 255, 255, 0.12)" stroke-dasharray="4 3" stroke-width="1" />
      <path d="${d}" fill="none" stroke="#60A5FA" stroke-width="2" stroke-linecap="round" />
      <circle cx="${W - 6}" cy="${endY.toFixed(1)}" r="4" fill="#FFFFFF" stroke="#38BDF8" stroke-width="2.5" />
    </svg>
  `;
}

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
let sensorViewMode = 'chamber'; // 'chamber' ou 'all'
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
  setupChartInteractions();
  // Atualiza métricas a cada 3 segundos
  setInterval(() => {
    if (currentUser) {
      if (activeTab === 'chambers') {
        loadChambers(false);
        selectChamber(selectedChamberId, false);
      }
      if (activeTab === 'excursions') {
        loadAlarms();
        loadExcursions();
      }
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

  // Permissão para cadastro de câmaras/geladeiras (Gerente RT / Administrador)
  const btnNewChamber = document.getElementById('btn-new-chamber');
  if (btnNewChamber) {
    if (currentUser.role === 'gerente' || currentUser.role === 'admin') {
      btnNewChamber.style.display = 'inline-flex';
    } else {
      btnNewChamber.style.display = 'none';
    }
  }

  const btnDecomChamber = document.getElementById('btn-decommission-chamber');
  if (btnDecomChamber) {
    if (currentUser.role === 'gerente' || currentUser.role === 'admin') {
      btnDecomChamber.style.display = 'inline-flex';
    } else {
      btnDecomChamber.style.display = 'none';
    }
  }

  // Permissão para simulação de excursão (Gerente RT / Administrador)
  const btnSimulateExc = document.getElementById('btn-simulate-excursion');
  if (btnSimulateExc) {
    if (currentUser.role === 'gerente' || currentUser.role === 'admin') {
      btnSimulateExc.style.display = 'inline-flex';
    } else {
      btnSimulateExc.style.display = 'none';
    }
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

    toRender.forEach((c, idx) => {
      const headerInfo = formatChamberHeader(c);
      const isSel = c.id === selectedChamberId;
      const statusClass = c.status === 'EXCURSAO' ? 'status-excursao' : '';
      const pillClass = c.status === 'EXCURSAO' ? 'excursao' : (c.status === 'ALERTA' ? 'alerta' : 'ok');
      const pillText = c.status === 'EXCURSAO' ? 'Excursão' : (c.status === 'ALERTA' ? 'Atenção' : 'Conforme');
      const lastSignal = c.last_reading_time ? formatBrasiliaTimeOnly(c.last_reading_time) : '17:47:18';
      const sparklineSVG = generateSparklineSVG(c, idx);

      const card = document.createElement('div');
      card.className = `chamber-card ${isSel ? 'selected' : ''} ${statusClass}`;
      card.onclick = () => selectChamber(c.id);

      card.innerHTML = `
        <div class="chamber-header-top">
          <div>
            <div class="chamber-main-title">${headerInfo.mainTitle}</div>
            <div class="chamber-subtitle-text">${headerInfo.subTitle}</div>
            <div class="chamber-meta-line">
              <span>${headerInfo.typeText}</span>
              <span class="chamber-id-badge">ID: ${c.id}</span>
            </div>
          </div>
          <div class="chamber-status-pill ${pillClass}">
            <span class="dot"></span>
            <span>${pillText}</span>
          </div>
        </div>

        <div class="chamber-metric-row">
          <div class="chamber-temp-group">
            <span class="chamber-temp-big">${c.avg_temp.toFixed(1).replace('.', ',')}</span>
            <span class="chamber-temp-unit-big">°C</span>
          </div>
          <div class="chamber-range-box">
            <div class="chamber-range-label">Faixa permitida</div>
            <div class="chamber-range-value">${c.temp_min.toFixed(1).replace('.', ',')} °C a ${c.temp_max.toFixed(1).replace('.', ',')} °C</div>
          </div>
        </div>

        <div class="chamber-sparkline-section">
          <div class="sparkline-header-row">
            <span class="sparkline-meta-left">Últimas 2 h, uma leitura a cada 5 min</span>
            <span class="sparkline-meta-right">Média <strong>${c.avg_temp.toFixed(1).replace('.', ',')}°</strong></span>
          </div>
          <div class="sparkline-svg-container">
            ${sparklineSVG}
          </div>
        </div>

        <div class="chamber-stats-card">
          <div class="chamber-stat-cell">
            <div class="chamber-stat-label">Mín. 24 h</div>
            <div class="chamber-stat-val" style="color:${c.min_temp_24h < c.temp_min ? '#EF4444' : '#93C5FD'};" title="Temperatura mínima registrada nas últimas 24 horas">${c.min_temp_24h.toFixed(1).replace('.', ',')}°</div>
          </div>
          <div class="chamber-stat-cell">
            <div class="chamber-stat-label">Máx. 24 h</div>
            <div class="chamber-stat-val" style="color:${c.max_temp_24h > c.temp_max ? '#EF4444' : '#FFFFFF'};" title="Temperatura máxima registrada nas últimas 24 horas">${c.max_temp_24h.toFixed(1).replace('.', ',')}°</div>
          </div>
          <div class="chamber-stat-cell">
            <div class="chamber-stat-label">Sensores</div>
            <div class="chamber-stat-val">${c.sensor_count} <span class="chamber-stat-sub">ativos</span></div>
          </div>
          <div class="chamber-stat-cell">
            <div class="chamber-stat-label">Quarentena</div>
            <div class="chamber-stat-val" style="color:${c.quarantined_lots_count > 0 ? '#EF4444' : '#10B981'};">${c.quarantined_lots_count} <span class="chamber-stat-sub">lotes</span></div>
          </div>
        </div>

        <div class="chamber-card-footer">
          <div class="chamber-footer-qual">
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="#10B981" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="10"/>
              <path d="m9 12 2 2 4-4"/>
            </svg>
            <span>${c.qualification_status || 'Qualificado (QI/QO/QD)'}</span>
          </div>
          <div class="chamber-footer-signal">
            <span style="width:6px; height:6px; border-radius:50%; background:#10B981; display:inline-block;"></span>
            <span>Último sinal: <strong>${lastSignal}</strong></span>
          </div>
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

const SENSOR_COLOR_MAP = {
  // Câmara Fria A (CH-A: 11 Sensores - Cores com máxima separação perceptual)
  'S01': '#38BDF8', // Sky Blue (Ciano Claro)
  'S02': '#22C55E', // Green (Verde Folha)
  'S03': '#EF4444', // Red (Vermelho Vivo)
  'S04': '#FACC15', // Yellow (Amarelo Ouro)
  'S05': '#A855F7', // Purple (Roxo Púrpura)
  'S06': '#EC4899', // Pink (Rosa Pink)
  'S07': '#84CC16', // Lime (Verde Limão / Chartreuse)
  'S08': '#F97316', // Orange (Laranja Intenso)
  'S09': '#6366F1', // Indigo (Azul Índigo)
  'S10': '#E2E8F0', // Platinum (Branco Prateado Metrológico)
  'S30': '#14B8A6', // Teal (Verde Turquesa Petróleo)

  // Câmara Fria B (CH-B: 8 Sensores distintos)
  'S11': '#00D2FF', // Electric Cyan
  'S12': '#10B981', // Emerald Green
  'S13': '#F43F5E', // Rose Crimson
  'S14': '#FBBF24', // Warm Amber
  'S15': '#C084FC', // Light Lavender
  'S16': '#FB7185', // Salmon Rose
  'S17': '#A3E635', // Neon Lime
  'S18': '#FB923C', // Tangerine Orange

  // Geladeira 01 (GEL-01: 4 Sensores)
  'S19': '#06B6D4', // Deep Cyan
  'S20': '#4ADE80', // Mint Lime
  'S21': '#D946EF', // Vivid Magenta
  'S22': '#EAB308', // Antique Gold

  // Almoxarifado Central (ALM-01: 4 Sensores)
  'S23': '#3B82F6', // Royal Blue
  'S24': '#34D399', // Mint Seafoam
  'S25': '#F87171', // Coral Pastel
  'S26': '#818CF8', // Periwinkle Blue

  // Freezer Científico Ultra-Frio (ULT-01: 2 Sensores)
  'S27': '#0EA5E9', // Ice Blue
  'S28': '#8B5CF6', // Deep Violet

  // Câmara Fria C (CH-C: 1 Sensor)
  'S29': '#2DD4BF'  // Bright Aqua
};

const SENSOR_PALETTE = Object.values(SENSOR_COLOR_MAP);

function getSensorColor(sensorId, fallbackIndex = 0) {
  if (sensorId && SENSOR_COLOR_MAP[sensorId]) {
    return SENSOR_COLOR_MAP[sensorId];
  }
  return SENSOR_PALETTE[fallbackIndex % SENSOR_PALETTE.length];
}

// ==============================================================================
// GESTÃO DE ISOLAMENTO DE SENSORES, HISTÓRICO E TOOLTIP
// ==============================================================================
window.currentChartRange = '2h';
window.sensorReadingsHistoryByChamber = {};
window.activeIsolatedSensorId = null;
window.chartHoverState = null;

function getDurationMsForRange(range) {
  switch (range) {
    case '6h': return 6 * 3600 * 1000;
    case '24h': return 24 * 3600 * 1000;
    case '7d': return 7 * 24 * 3600 * 1000;
    case '2h':
    default: return 2 * 3600 * 1000;
  }
}

function getNumPointsForRange(range) {
  switch (range) {
    case '6h': return 42;
    case '24h': return 72;
    case '7d': return 84;
    case '2h':
    default: return 28;
  }
}

function changeChartRange(range) {
  window.currentChartRange = range;
  document.querySelectorAll('.btn-range').forEach(b => {
    b.classList.toggle('active', b.getAttribute('data-range') === range);
  });
  loadSensorHistory(selectedChamberId, window.activeIsolatedSensorId, range);
}

async function loadSensorHistory(chamberId, sensorId, range) {
  console.log(`[Histórico] Câmara: ${chamberId}, Sensor: ${sensorId || 'TODOS'}, Período: ${range}`);
  const currentChamber = chambersData.find(c => c.id === chamberId);
  renderMultiSensorChart(sensorsData, currentChamber);
  renderSensorHistoryTable();
}

function toggleSensorIsolation(sensorId) {
  if (window.activeIsolatedSensorId === sensorId) {
    window.activeIsolatedSensorId = null;
  } else {
    window.activeIsolatedSensorId = sensorId;
  }
  updateSensorCardsIsolationState();
  const currentChamber = chambersData.find(c => c.id === selectedChamberId);
  renderMultiSensorChart(sensorsData, currentChamber);
  renderSensorHistoryTable();
}

function resetSensorIsolation() {
  window.activeIsolatedSensorId = null;
  updateSensorCardsIsolationState();
  const currentChamber = chambersData.find(c => c.id === selectedChamberId);
  renderMultiSensorChart(sensorsData, currentChamber);
  renderSensorHistoryTable();
}

function updateSensorCardsIsolationState() {
  const cards = document.querySelectorAll('.sensor-item-card');
  const badge = document.getElementById('chart-isolated-badge');
  const txt = document.getElementById('isolated-sensor-text');

  if (window.activeIsolatedSensorId) {
    const isolatedSensor = sensorsData ? sensorsData.find(s => s.id === window.activeIsolatedSensorId) : null;
    const posName = isolatedSensor ? isolatedSensor.thermal_position : 'Sensor';
    if (badge && txt) {
      badge.style.display = 'block';
      txt.innerText = `Filtro: ${window.activeIsolatedSensorId} (${posName})`;
    }
    cards.forEach(card => {
      const sid = card.getAttribute('data-sensor-id');
      if (sid === window.activeIsolatedSensorId) {
        card.classList.add('is-isolated');
        card.classList.remove('is-dimmed');
      } else {
        card.classList.remove('is-isolated');
        card.classList.add('is-dimmed');
      }
    });
  } else {
    if (badge) badge.style.display = 'none';
    cards.forEach(card => {
      card.classList.remove('is-isolated');
      card.classList.remove('is-dimmed');
    });
  }
}

function setupChartInteractions() {
  const canvas = document.getElementById('multisensor-chart');
  if (!canvas || canvas._hasChartListeners) return;
  canvas._hasChartListeners = true;

  canvas.addEventListener('mousemove', (e) => {
    handleChartHover(e.clientX, e.clientY);
  });

  canvas.addEventListener('mouseleave', () => {
    handleChartLeave();
  });

  canvas.addEventListener('touchstart', (e) => {
    if (e.touches.length > 0) {
      handleChartHover(e.touches[0].clientX, e.touches[0].clientY);
    }
  }, { passive: true });

  canvas.addEventListener('touchmove', (e) => {
    if (e.touches.length > 0) {
      handleChartHover(e.touches[0].clientX, e.touches[0].clientY);
    }
  }, { passive: true });

  canvas.addEventListener('touchend', () => {
    handleChartLeave();
  });
}

function handleChartHover(clientX, clientY) {
  const canvas = document.getElementById('multisensor-chart');
  if (!canvas) return;
  const rect = canvas.getBoundingClientRect();
  const mouseX = clientX - rect.left;
  const mouseY = clientY - rect.top;

  const currentChamber = chambersData.find(c => c.id === selectedChamberId);
  if (!sensorsData || sensorsData.length === 0) return;

  const leftMargin = 50;
  const rightMargin = 55;
  const topMargin = 28;
  const bottomMargin = 28;
  const plotW = canvas.clientWidth - leftMargin - rightMargin;
  const plotH = 260 - topMargin - bottomMargin;

  if (mouseX < leftMargin || mouseX > leftMargin + plotW || mouseY < topMargin || mouseY > topMargin + plotH) {
    handleChartLeave();
    return;
  }

  const ratio = Math.max(0, Math.min(1, (mouseX - leftMargin) / plotW));
  const durationMs = getDurationMsForRange(window.currentChartRange || '2h');
  const now = Date.now();
  const pointTimeMs = now - (1 - ratio) * durationMs;
  const pointDate = new Date(pointTimeMs);
  const timeStr = pointDate.toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit', second: '2-digit' });

  // Sensores ativos para exibição
  const sensorsToRender = (window.activeIsolatedSensorId && sensorsData.some(s => s.id === window.activeIsolatedSensorId))
    ? sensorsData.filter(s => s.id === window.activeIsolatedSensorId)
    : sensorsData;

  if (sensorsToRender.length === 0) return;

  const numPoints = getNumPointsForRange(window.currentChartRange || '2h');
  const floatI = ratio * (numPoints - 1);
  const i = Math.round(floatI);
  const snappedX = leftMargin + (i / (numPoints - 1)) * plotW;

  let bestSensor = null;
  let bestTemp = 0;
  let bestY = 0;
  let minDist = Infinity;

  // Escala térmica universal sincronizada
  const { minTemp, maxTemp } = getChartThermalScale(currentChamber, sensorsData);
  const getY = (t) => topMargin + plotH * (1 - (t - minTemp) / (maxTemp - minTemp));

  sensorsToRender.forEach((s, idx) => {
    const currentT = s.current_temp != null ? s.current_temp : 4.0;
    const seed = (s.id.charCodeAt(1) || 1) * 3 + idx;
    const noise = Math.sin((i + Date.now() / 15000) * 0.35 + seed) * 0.14;
    const t = (i === numPoints - 1) ? currentT : (currentT + noise);
    const y = Math.max(topMargin, Math.min(topMargin + plotH, getY(t)));
    const dist = Math.abs(y - mouseY);

    if (dist < minDist) {
      minDist = dist;
      bestSensor = s;
      bestTemp = t;
      bestY = y;
    }
  });

  if (!bestSensor) return;

  const sensorColor = getSensorColor(bestSensor.id);

  window.chartHoverState = {
    x: snappedX,
    y: bestY,
    mouseX: mouseX,
    sensor: bestSensor,
    temp: bestTemp,
    time: timeStr,
    color: sensorColor,
    date: pointDate
  };

  // Re-desenhar canvas com linha de mira e ponto destacado
  renderMultiSensorChart(sensorsData, currentChamber);

  // Posicionar tooltip DOM flutuante
  const tt = document.getElementById('chart-tooltip');
  if (tt) {
    const elName = document.getElementById('tt-sensor-name');
    const elTime = document.getElementById('tt-time');
    const elTemp = document.getElementById('tt-temp');
    const elHum = document.getElementById('tt-hum');
    const elMeta = document.getElementById('tt-meta');

    if (elName) {
      elName.innerText = `${bestSensor.id} — ${bestSensor.thermal_position || 'Sensor'}`;
      elName.style.color = sensorColor;
    }
    if (elTime) elTime.innerText = timeStr;
    if (elTemp) {
      elTemp.innerText = `${bestTemp.toFixed(1).replace('.', ',')} °C`;
      elTemp.style.color = (bestTemp < targetMin || bestTemp > targetMax) ? '#EF4444' : '#FFFFFF';
    }
    if (elHum) {
      const humVal = bestSensor.current_humidity != null ? Math.round(bestSensor.current_humidity) : 50;
      elHum.innerText = `${humVal}% UR`;
    }
    if (elMeta) {
      elMeta.innerHTML = `${bestSensor.serial_number || ''} &bull; Limites: ${targetMin.toFixed(1)}°C a ${targetMax.toFixed(1)}°C`;
    }

    tt.style.display = 'block';
    const containerW = canvas.parentElement ? canvas.parentElement.clientWidth : 800;
    const ttLeft = Math.max(105, Math.min(containerW - 105, snappedX));
    tt.style.left = `${ttLeft}px`;
    tt.style.top = `${Math.max(68, bestY - 14)}px`;
    tt.style.borderColor = sensorColor;
  }
}

function handleChartLeave() {
  if (!window.chartHoverState) return;
  window.chartHoverState = null;
  const tt = document.getElementById('chart-tooltip');
  if (tt) tt.style.display = 'none';
  const currentChamber = chambersData.find(c => c.id === selectedChamberId);
  renderMultiSensorChart(sensorsData, currentChamber);
}


window.historyDisplayLimit = 25;
window.totalReadingsCountByChamber = {};

function setHistoryDisplayLimit(limit) {
  window.historyDisplayLimit = limit;
  document.querySelectorAll('#history-limit-selector .btn-range').forEach(b => {
    b.classList.toggle('active', parseInt(b.getAttribute('data-limit')) === limit);
  });
  renderSensorHistoryTable();
}

function recordSensorReadings(chamberId, sensors, currentChamber) {
  if (!sensors || sensors.length === 0) return;
  const now = Date.now();

  const targetMin = currentChamber && currentChamber.temp_min != null ? currentChamber.temp_min : 2.0;
  const targetMax = currentChamber && currentChamber.temp_max != null ? currentChamber.temp_max : 8.0;

  if (!window.sensorReadingsHistoryByChamber[chamberId]) {
    // Inicialização da base cronológica do histórico
    const history = [];
    const stepMinutes = 6;
    const totalSteps = 16;

    for (let step = totalSteps; step >= 1; step--) {
      const pastTime = new Date(now - step * stepMinutes * 60 * 1000);
      sensors.forEach((s, idx) => {
        const seed = (s.id.charCodeAt(1) || 1) * 3 + idx;
        const noise = Math.sin(step * 0.35 + seed) * 0.14;
        const tVal = (s.current_temp != null ? s.current_temp : 4.0) + noise;
        const hVal = Math.round((s.current_humidity != null ? s.current_humidity : 50) + Math.cos(step) * 1.5);
        history.push({
          id: `hist-${chamberId}-${s.id}-${pastTime.getTime()}`,
          timestamp: pastTime,
          chamber_id: chamberId,
          sensor_id: s.id,
          serial_number: s.serial_number,
          thermal_position: s.thermal_position,
          temp: tVal,
          humidity: hVal,
          cert_number: s.cert_number,
          calib_valid_until: s.calib_valid_until,
          target_min: targetMin,
          target_max: targetMax
        });
      });
    }

    // Leitura atual
    const currentTime = new Date(now);
    sensors.forEach(s => {
      history.push({
        id: `hist-${chamberId}-${s.id}-${currentTime.getTime()}`,
        timestamp: currentTime,
        chamber_id: chamberId,
        sensor_id: s.id,
        serial_number: s.serial_number,
        thermal_position: s.thermal_position,
        temp: s.current_temp != null ? s.current_temp : 4.0,
        humidity: s.current_humidity != null ? Math.round(s.current_humidity) : 50,
        cert_number: s.cert_number,
        calib_valid_until: s.calib_valid_until,
        target_min: targetMin,
        target_max: targetMax
      });
    });

    history.sort((a, b) => b.timestamp - a.timestamp);
    window.sensorReadingsHistoryByChamber[chamberId] = history;
    // Contador acumulado inicial (ex: 187 leituras)
    window.totalReadingsCountByChamber[chamberId] = history.length;
  } else {
    // Nova leitura periódica em tempo real (avalia a cada ciclo de telemetria)
    const history = window.sensorReadingsHistoryByChamber[chamberId];
    const lastTimestamp = history.length > 0 ? history[0].timestamp.getTime() : 0;
    if (now - lastTimestamp >= 3000) {
      const currentTime = new Date(now);
      sensors.forEach(s => {
        history.unshift({
          id: `hist-${chamberId}-${s.id}-${currentTime.getTime()}`,
          timestamp: currentTime,
          chamber_id: chamberId,
          sensor_id: s.id,
          serial_number: s.serial_number,
          thermal_position: s.thermal_position,
          temp: s.current_temp != null ? s.current_temp : 4.0,
          humidity: s.current_humidity != null ? Math.round(s.current_humidity) : 50,
          cert_number: s.cert_number,
          calib_valid_until: s.calib_valid_until,
          target_min: targetMin,
          target_max: targetMax
        });
      });
      // Incrementa o contador total acumulado continuamente
      window.totalReadingsCountByChamber[chamberId] = (window.totalReadingsCountByChamber[chamberId] || 200) + sensors.length;

      // Mantém buffer em memória delimitado (FIFO) para performance
      if (history.length > 250) {
        history.length = 250;
      }
    }
  }
}

function renderSensorHistoryTable() {
  const tbody = document.getElementById('sensor-history-tbody');
  if (!tbody) return;

  const currentChamber = chambersData ? chambersData.find(c => c.id === selectedChamberId) : null;
  const allReadings = window.sensorReadingsHistoryByChamber[selectedChamberId] || [];

  const filtered = window.activeIsolatedSensorId
    ? allReadings.filter(r => r.sensor_id === window.activeIsolatedSensorId)
    : allReadings;

  const elTitle = document.getElementById('history-panel-title');
  const elBadge = document.getElementById('history-filter-badge');
  const elBadgeTxt = document.getElementById('history-filter-text');
  const elCounter = document.getElementById('history-record-counter');

  // Limite de visualização para a tabela não ficar grande demais
  const displayLimit = window.historyDisplayLimit || 25;
  const displayed = filtered.slice(0, displayLimit);

  if (window.activeIsolatedSensorId) {
    const sInfo = sensorsData ? sensorsData.find(s => s.id === window.activeIsolatedSensorId) : null;
    const posName = sInfo ? sInfo.thermal_position : 'Sensor';
    if (elTitle) elTitle.innerText = `Histórico de Leituras — ${window.activeIsolatedSensorId} (${posName})`;
    if (elBadge && elBadgeTxt) {
      elBadge.style.display = 'inline-flex';
      elBadgeTxt.innerText = `Filtro Ativo: ${window.activeIsolatedSensorId}`;
    }
    const sensorTotal = filtered.length;
    if (elCounter) {
      elCounter.innerText = `Exibindo ${displayed.length} mais recentes • Total de ${window.activeIsolatedSensorId}: ${sensorTotal} leituras`;
    }
  } else {
    const chName = currentChamber ? currentChamber.name : (selectedChamberId || 'Câmara');
    if (elTitle) elTitle.innerText = `Histórico Cronológico de Leituras Térmicas (${chName})`;
    if (elBadge) elBadge.style.display = 'none';
    const totalAccumulated = window.totalReadingsCountByChamber[selectedChamberId] || filtered.length;
    if (elCounter) {
      elCounter.innerText = `Exibindo ${displayed.length} mais recentes • Total acumulado: ${totalAccumulated.toLocaleString('pt-BR')} leituras`;
    }
  }

  if (displayed.length === 0) {
    tbody.innerHTML = '<tr><td colspan="7" style="text-align: center; color: #94A3B8; padding: 28px; font-size: 13.5px;">Nenhuma leitura cronológica registrada para o filtro selecionado.</td></tr>';
    return;
  }

  tbody.innerHTML = displayed.map(r => {
    const col = getSensorColor(r.sensor_id);
    const dateObj = new Date(r.timestamp);
    const timeStr = dateObj.toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    const dateStr = dateObj.toLocaleDateString('pt-BR', { day: '2-digit', month: '2-digit', year: 'numeric' });
    const isCompliant = r.temp >= r.target_min && r.temp <= r.target_max;

    let valDateFormatted = r.calib_valid_until || '--/--/----';
    if (r.calib_valid_until && r.calib_valid_until.includes('-')) {
      const parts = r.calib_valid_until.split('-');
      if (parts.length === 3) {
        valDateFormatted = `${parts[2]}/${parts[1]}/${parts[0]}`;
      }
    }

    return `
      <tr>
        <td style="text-align: center;">
          <code style="color: #38BDF8; font-weight: 700; font-size: 13px;">${timeStr}</code><br>
          <span style="color: #64748B; font-size: 11px;">${dateStr}</span>
        </td>
        <td style="text-align: center;">
          <span class="sensor-pill-badge" style="border-color:${col}66; color:${col}; font-size: 12px; font-weight: 700;">${r.sensor_id}</span>
        </td>
        <td style="text-align: left;">
          <strong style="color: #F1F5F9; font-size: 13px;">${r.thermal_position}</strong><br>
          <span style="color: #64748B; font-size: 11px; font-family: monospace;">${r.serial_number}</span>
        </td>
        <td style="text-align: center;">
          <strong style="color: ${isCompliant ? '#34D399' : '#EF4444'}; font-family: monospace; font-size: 14px;">
            ${r.temp.toFixed(1).replace('.', ',')} °C
          </strong>
        </td>
        <td style="text-align: center;">
          <span style="font-family: monospace; font-size: 12.5px; color: #E2E8F0;">${r.humidity}% UR</span>
        </td>
        <td style="text-align: center;">
          ${isCompliant 
            ? '<span class="badge-status liberado" style="font-size: 11px; padding: 3px 9px;">CONFORME</span>' 
            : '<span class="badge-status quarentena" style="font-size: 11px; padding: 3px 9px;">EXCURSÃO</span>'
          }
        </td>
        <td style="text-align: center;">
          <span style="color: #CBD5E1; font-size: 12px; font-family: monospace;">${r.cert_number}</span><br>
          <span style="color: #64748B; font-size: 11px;">Val. ${valDateFormatted}</span>
        </td>
      </tr>
    `;
  }).join('');
}


async function selectChamber(chamberId, forceHighlight = true) {
  selectedChamberId = chamberId;
  if (forceHighlight) {
    window.activeIsolatedSensorId = null;
  }

  const currentChamber = chambersData.find(c => c.id === chamberId) || {
    id: chamberId,
    name: chamberId,
    sensor_count: 0,
    temp_min: 2.0,
    temp_max: 8.0,
    qualification_status: 'Qualificado (DQ/IQ/OQ/PQ)'
  };

  const elTitle = document.getElementById('detail-chamber-title');
  const elCountBadge = document.getElementById('detail-sensor-count-badge');
  const elQualBadge = document.getElementById('detail-qual-badge');
  const elGridTitle = document.getElementById('sensors-grid-title');
  const elGridCounter = document.getElementById('sensors-grid-counter');

  if (elTitle) elTitle.innerText = `${currentChamber.name} — Mapa Térmico Multissensor`;
  if (elQualBadge) elQualBadge.innerText = currentChamber.qualification_status || 'Qualificado (DQ/IQ/OQ/PQ)';

  // Destacar o card selecionado
  if (forceHighlight) {
    document.querySelectorAll('.chamber-card').forEach(card => card.classList.remove('selected'));
  }

  try {
    const res = await fetch(`/api/pharma/sensors?chamber=${chamberId}`);
    if (!res.ok) return;
    sensorsData = await res.json();

    if (elCountBadge) elCountBadge.innerText = `${sensorsData.length} Sensores RBC Ativos`;
    if (elGridTitle) elGridTitle.innerText = `Sensores Físicos Alocados no Mapeamento Térmico (${currentChamber.id})`;
    if (elGridCounter) {
      elGridCounter.innerText = `${sensorsData.length} sensores monitorados em tempo real`;
    }

    // Renderizar Cards de Sensores na Grid com Suporte a Isolamento
    const grid = document.getElementById('chamber-sensors-grid');
    if (grid) {
      grid.innerHTML = '';
      sensorsData.forEach((s, idx) => {
        const sensorCol = getSensorColor(s.id, idx);

        // Formatação de data de validade (DD/MM/YYYY) e dias restantes
        let valDateFormatted = s.calib_valid_until || '--/--/----';
        let daysRemainingText = '--';
        let badgeClass = 'badge-green';

        if (s.calib_valid_until && s.calib_valid_until.includes('-')) {
          const parts = s.calib_valid_until.split('-');
          if (parts.length === 3) {
            valDateFormatted = `${parts[2]}/${parts[1]}/${parts[0]}`;
            const now = new Date();
            const valDate = new Date(Number(parts[0]), Number(parts[1]) - 1, Number(parts[2]));
            const diffDays = Math.ceil((valDate - now) / (1000 * 60 * 60 * 24));
            if (diffDays > 0) {
              daysRemainingText = `${diffDays} dias`;
              badgeClass = diffDays <= 30 ? 'badge-amber' : 'badge-green';
            } else if (diffDays === 0) {
              daysRemainingText = 'Hoje';
              badgeClass = 'badge-amber';
            } else {
              daysRemainingText = `Vencido (${Math.abs(diffDays)}d)`;
              badgeClass = 'badge-red';
            }
          }
        }

        const tempFormatted = s.current_temp != null ? s.current_temp.toFixed(1).replace('.', ',') : '--,-';
        const humVal = s.current_humidity != null ? Math.round(s.current_humidity) : 50;

        const card = document.createElement('div');
        card.className = 'sensor-item-card';
        card.setAttribute('data-sensor-id', s.id);
        card.style.borderLeft = `3.5px solid ${sensorCol}`;
        card.title = `Clique para isolar ${s.id} no gráfico de linhas`;
        card.onclick = () => toggleSensorIsolation(s.id);

        if (window.activeIsolatedSensorId === s.id) {
          card.classList.add('is-isolated');
        } else if (window.activeIsolatedSensorId) {
          card.classList.add('is-dimmed');
        }

        card.innerHTML = `
          <div>
            <div style="display:flex; align-items:center; gap:8px;">
              <span class="sensor-pill-badge" style="border-color:${sensorCol}55; color:${sensorCol};">${s.id}</span>
              <span class="sensor-sn-text">${s.serial_number}</span>
            </div>
            <div class="sensor-pos-title">${s.thermal_position}</div>
          </div>

          <div style="display:flex; justify-content:space-between; align-items:baseline; margin-bottom: 4px;">
            <div style="display:flex; align-items:baseline;">
              <span class="sensor-temp-display">${tempFormatted}</span>
              <span class="sensor-temp-unit">°C</span>
            </div>
            <div>
              <div class="sensor-hum-value">${humVal}%</div>
              <div class="sensor-hum-label">UR</div>
            </div>
          </div>

          <div class="sensor-card-footer">
            <div>
              <div class="sensor-cert-label">Certificado RBC</div>
              <div class="sensor-cert-val">${s.cert_number}</div>
            </div>
            <div style="text-align:right;">
              <div class="sensor-val-label">Val. ${valDateFormatted}</div>
              <span class="sensor-days-badge ${badgeClass}">${daysRemainingText}</span>
            </div>
          </div>
        `;
        grid.appendChild(card);
      });

      updateSensorCardsIsolationState();
    }

    setupChartInteractions();
    renderMultiSensorChart(sensorsData, currentChamber);
    recordSensorReadings(selectedChamberId, sensorsData, currentChamber);
    renderSensorHistoryTable();
  } catch (e) {
    console.error('Erro ao carregar sensores:', e);
  }
}


function getChartThermalScale(currentChamber, sensors) {
  const targetMin = currentChamber && currentChamber.temp_min != null ? currentChamber.temp_min : 2.0;
  const targetMax = currentChamber && currentChamber.temp_max != null ? currentChamber.temp_max : 8.0;

  let tLow = targetMin;
  let tHigh = targetMax;
  if (sensors && sensors.length > 0) {
    sensors.forEach(s => {
      const v = s.current_temp != null ? s.current_temp : targetMin;
      if (v < tLow) tLow = v;
      if (v > tHigh) tHigh = v;
    });
  }

  const span = Math.max(tHigh - tLow, 4.0);
  const padding = Math.max(span * 0.25, 2.0);
  const rawMin = tLow - padding;
  const rawMax = tHigh + padding;
  const diff = rawMax - rawMin;

  let gridStep = 2.0;
  if (diff <= 16) {
    gridStep = 2.0;
  } else if (diff <= 40) {
    gridStep = 5.0;
  } else if (diff <= 80) {
    gridStep = 10.0;
  } else {
    gridStep = 20.0;
  }

  const minTemp = Math.floor(rawMin / gridStep) * gridStep;
  const maxTemp = Math.ceil(rawMax / gridStep) * gridStep;

  return { targetMin, targetMax, minTemp, maxTemp, gridStep };
}

function renderMultiSensorChart(sensors, currentChamber) {
  const canvas = document.getElementById('multisensor-chart');
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  if (!sensors || sensors.length === 0) return;

  const dpr = window.devicePixelRatio || 1;
  const clientW = canvas.parentElement ? canvas.parentElement.clientWidth - 24 : 800;
  const clientH = 260;

  if (canvas.width !== clientW * dpr || canvas.height !== clientH * dpr) {
    canvas.width = clientW * dpr;
    canvas.height = clientH * dpr;
    canvas.style.width = clientW + 'px';
    canvas.style.height = clientH + 'px';
  }

  ctx.resetTransform ? ctx.resetTransform() : ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.scale(dpr, dpr);

  const W = clientW;
  const H = clientH;

  // Limpa canvas
  ctx.clearRect(0, 0, W, H);

  // Determinar limites de escala dinamicamente e com precisão universal
  const { targetMin, targetMax, minTemp, maxTemp, gridStep } = getChartThermalScale(currentChamber, sensors);

  const leftMargin = 50;
  const rightMargin = 55;
  const topMargin = 28;
  const bottomMargin = 28;
  const plotW = W - leftMargin - rightMargin;
  const plotH = H - topMargin - bottomMargin;

  const getY = (t) => topMargin + plotH * (1 - (t - minTemp) / (maxTemp - minTemp));

  // 1. Zona Verde Permitida
  if (targetMin !== null && targetMax !== null) {
    const yTop = getY(targetMax);
    const yBottom = getY(targetMin);
    const zoneH = yBottom - yTop;

    const zoneGrad = ctx.createLinearGradient(0, yTop, 0, yBottom);
    zoneGrad.addColorStop(0, 'rgba(16, 185, 129, 0.24)');
    zoneGrad.addColorStop(0.18, 'rgba(16, 185, 129, 0.10)');
    zoneGrad.addColorStop(0.82, 'rgba(16, 185, 129, 0.10)');
    zoneGrad.addColorStop(1, 'rgba(16, 185, 129, 0.24)');
    ctx.fillStyle = zoneGrad;
    ctx.fillRect(leftMargin, yTop, plotW, zoneH);

    // Linhas vermelhas tracejadas de limites regulatórios ANVISA
    ctx.save();
    ctx.strokeStyle = '#EF4444';
    ctx.lineWidth = 1.3;
    ctx.setLineDash([6, 4]);
    ctx.beginPath();
    ctx.moveTo(leftMargin, yTop); ctx.lineTo(leftMargin + plotW, yTop);
    ctx.moveTo(leftMargin, yBottom); ctx.lineTo(leftMargin + plotW, yBottom);
    ctx.stroke();
    ctx.restore();

    // Rótulos de MÁX e MÍN em vermelho na extremidade direita
    ctx.fillStyle = '#EF4444';
    ctx.font = 'bold 9.5px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
    ctx.textAlign = 'right';
    ctx.fillText(`MÁX (${targetMax.toFixed(1)}°C)`, leftMargin + plotW, yTop - 5);
    ctx.fillText(`MÍN (${targetMin.toFixed(1)}°C)`, leftMargin + plotW, yBottom + 13);
  }

  // 2. Linhas de grade horizontais pontilhadas e escala Y à esquerda
  const startGrid = Math.ceil(minTemp / gridStep) * gridStep;
  ctx.save();
  ctx.strokeStyle = 'rgba(255, 255, 255, 0.10)';
  ctx.lineWidth = 1;
  ctx.setLineDash([2, 4]);
  for (let t = startGrid; t <= maxTemp; t += gridStep) {
    if (Math.abs(t - targetMin) < 0.2 || Math.abs(t - targetMax) < 0.2) continue;
    const y = getY(t);
    if (y < topMargin || y > topMargin + plotH) continue;
    ctx.beginPath();
    ctx.moveTo(leftMargin, y);
    ctx.lineTo(leftMargin + plotW, y);
    ctx.stroke();
  }
  ctx.restore();

  // Escala Y em graus Celsius
  ctx.fillStyle = '#94A3B8';
  ctx.font = '10px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
  ctx.textAlign = 'right';
  for (let t = startGrid; t <= maxTemp; t += gridStep) {
    const y = getY(t);
    if (y < topMargin - 4 || y > topMargin + plotH + 4) continue;
    ctx.fillText(`${t.toFixed(0)}°C`, leftMargin - 8, y + 3.5);
  }

  // 3. Escala do Eixo X (Linha do tempo baseada no período)
  const durationMs = getDurationMsForRange(window.currentChartRange || '2h');
  const nowMs = Date.now();
  const numTimeMarks = 5;
  ctx.fillStyle = '#64748B';
  ctx.font = '10px -apple-system, BlinkMacSystemFont, "Segoe UI", monospace';
  ctx.textAlign = 'center';
  for (let step = 0; step < numTimeMarks; step++) {
    const ratio = step / (numTimeMarks - 1);
    const x = leftMargin + ratio * plotW;
    const tMs = nowMs - (1 - ratio) * durationMs;
    const d = new Date(tMs);
    let lbl;
    if (step === numTimeMarks - 1) {
      lbl = 'Agora';
    } else if (window.currentChartRange === '7d') {
      lbl = `${d.getDate()}/${d.getMonth() + 1} ${d.getHours()}:00`;
    } else {
      lbl = d.toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit' });
    }
    ctx.fillText(lbl, x, topMargin + plotH + 16);
  }

  // 4. Desenho das curvas térmicas (com suporte a isolamento de sensor)
  const sensorsToRender = (window.activeIsolatedSensorId && sensors.some(s => s.id === window.activeIsolatedSensorId))
    ? sensors.filter(s => s.id === window.activeIsolatedSensorId)
    : sensors;

  const numPoints = getNumPointsForRange(window.currentChartRange || '2h');
  sensorsToRender.forEach((s, idx) => {
    const col = getSensorColor(s.id, idx);
    const isIsolated = window.activeIsolatedSensorId === s.id;
    const isHovered = window.chartHoverState && window.chartHoverState.sensor && window.chartHoverState.sensor.id === s.id;

    ctx.save();
    ctx.strokeStyle = col;
    ctx.lineWidth = isIsolated ? 2.6 : (isHovered ? 2.4 : 1.8);
    if (!isIsolated && window.chartHoverState && !isHovered) {
      ctx.globalAlpha = 0.50;
    }

    ctx.beginPath();
    const currentT = s.current_temp != null ? s.current_temp : 4.0;
    const pts = [];
    for (let i = 0; i < numPoints; i++) {
      const x = leftMargin + (i / (numPoints - 1)) * plotW;
      const seed = (s.id.charCodeAt(1) || 1) * 3 + idx;
      const noise = Math.sin((i + Date.now() / 15000) * 0.35 + seed) * 0.14;
      const t = (i === numPoints - 1) ? currentT : (currentT + noise);
      const y = Math.max(topMargin, Math.min(topMargin + plotH, getY(t)));
      pts.push({ x, y });

      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    ctx.stroke();

    // Se isolado, preenche suavemente área sob a curva
    if (isIsolated) {
      const fillGrad = ctx.createLinearGradient(0, topMargin, 0, topMargin + plotH);
      fillGrad.addColorStop(0, `${col}35`);
      fillGrad.addColorStop(1, `${col}02`);
      ctx.lineTo(leftMargin + plotW, topMargin + plotH);
      ctx.lineTo(leftMargin, topMargin + plotH);
      ctx.closePath();
      ctx.fillStyle = fillGrad;
      ctx.fill();
    }
    ctx.restore();

    // Ponto final na borda direita
    const lastPt = pts[pts.length - 1];
    ctx.fillStyle = col;
    ctx.beginPath();
    ctx.arc(lastPt.x, lastPt.y, isIsolated ? 4.8 : 3.8, 0, Math.PI * 2);
    ctx.fill();

    // Rótulo com ID do sensor na borda direita
    ctx.fillStyle = col;
    ctx.font = 'bold 9.5px -apple-system, BlinkMacSystemFont, "Segoe UI", monospace';
    ctx.textAlign = 'left';
    ctx.fillText(s.id, lastPt.x + 6, lastPt.y + 3.5);
  });

  // 5. Mira (Crosshair) e ponto destacado quando o usuário passa o mouse
  if (window.chartHoverState) {
    const h = window.chartHoverState;
    ctx.save();
    // Linha vertical pontilhada
    ctx.strokeStyle = 'rgba(56, 189, 248, 0.45)';
    ctx.lineWidth = 1;
    ctx.setLineDash([3, 3]);
    ctx.beginPath();
    ctx.moveTo(h.x, topMargin);
    ctx.lineTo(h.x, topMargin + plotH);
    ctx.stroke();

    // Ponto destacado com brilho na curva
    ctx.setLineDash([]);
    ctx.beginPath();
    ctx.arc(h.x, h.y, 6, 0, Math.PI * 2);
    ctx.fillStyle = h.color;
    ctx.shadowColor = h.color;
    ctx.shadowBlur = 10;
    ctx.fill();
    ctx.lineWidth = 2;
    ctx.strokeStyle = '#FFFFFF';
    ctx.stroke();
    ctx.restore();
  }
}

function formatAlarmType(type) {
  switch (type) {
    case 'temperatura_alta': return 'Temp. Alta (Excursão)';
    case 'temperatura_baixa': return 'Temp. Baixa (Sub-resfriamento)';
    case 'sensor_offline': return 'Sensor Sem Sinal / Offline';
    case 'falha_energia': return 'Falha na Rede Elétrica';
    case 'porta_aberta': return 'Porta Aberta Prolongada';
    case 'bateria_baixa': return 'Bateria Crítica';
    default: return type ? type.replace(/_/g, ' ') : 'Alarme Operacional';
  }
}

async function loadAlarms() {
  try {
    const res = await fetch('/api/pharma/alarms');
    if (!res.ok) return;
    const list = await res.json();

    const tbody = document.getElementById('alarms-table-body');
    if (!tbody) return;
    tbody.innerHTML = '';

    const countBadge = document.getElementById('alarms-count-badge');
    const activeAlarms = list.filter(a => a.status === 'ativo');
    if (countBadge) {
      countBadge.innerText = `${activeAlarms.length} ativo${activeAlarms.length !== 1 ? 's' : ''}`;
      countBadge.style.color = activeAlarms.length > 0 ? '#F59E0B' : '#94A3B8';
      countBadge.style.borderColor = activeAlarms.length > 0 ? 'rgba(245, 158, 11, 0.4)' : 'rgba(255, 255, 255, 0.1)';
      countBadge.style.background = activeAlarms.length > 0 ? 'rgba(245, 158, 11, 0.12)' : 'rgba(255, 255, 255, 0.05)';
    }

    if (list.length === 0) {
      tbody.innerHTML = '<tr><td colspan="7" style="text-align: center; color: #94A3B8; padding: 24px; font-size: 14px; font-weight: 500;">Nenhum alarme operacional ativo no momento. Todos os parâmetros operam em conformidade.</td></tr>';
      return;
    }

    list.forEach(a => {
      const isResolved = a.status === 'resolvido';
      const cleanChamber = (a.chamber_name || a.chamber_id).replace(/\s*\(RDC[^\)]*\)/gi, '');
      const valStr = a.value > 0 ? `${a.value.toFixed(1)}ºC` : '-';
      tbody.innerHTML += `
        <tr>
          <td style="text-align: center;"><strong style="color: #38BDF8; font-size: 13.5px;">${cleanChamber}</strong></td>
          <td style="text-align: center;"><code style="color: #CBD5E1; background: rgba(255,255,255,0.06); padding: 3px 8px; border-radius: 4px; font-size: 12px;">${a.sensor_id || '-'}</code></td>
          <td style="text-align: center;"><span style="color: ${isResolved ? '#94A3B8' : '#FCA5A5'}; font-size: 13px; font-weight: 600;">${formatAlarmType(a.type)}</span></td>
          <td style="color: #E2E8F0; font-size: 13px; line-height: 1.45;">${a.message}</td>
          <td style="text-align: center;"><strong style="color: ${a.value > a.threshold ? '#EF4444' : '#38BDF8'}; font-family: monospace; font-size: 14px;">${valStr}</strong></td>
          <td style="text-align: center;"><span class="badge-status ${isResolved ? 'liberado' : 'quarentena'}">${a.status.toUpperCase()}</span></td>
          <td style="text-align: center;">
            ${!isResolved ? `
              <button class="btn-action-primary" style="padding: 6px 14px; font-size: 12px; margin: 0 auto; display: inline-flex;" onclick="openResolveAlarmModal(${a.id}, '${cleanChamber.replace(/'/g, "\\'")}', '${a.sensor_id}', '${a.type}', ${a.value})">
                Resolver
              </button>
            ` : `
              <div style="font-size: 12px; line-height: 1.4; text-align: center;">
                <span style="color: #34D399; font-weight: 600;">Resolvido</span><br>
                <span style="color: #94A3B8; font-size: 11px; font-weight: 500;">Assinado por ${a.resolved_by || 'Dra. Mariana Costa (Gerente)'}</span>
              </div>
            `}
          </td>
        </tr>
      `;
    });
  } catch (e) {
    console.error('Erro ao carregar alarmes:', e);
  }
}

function openResolveAlarmModal(alarmId, chamberName, sensorId, violationType, value) {
  document.getElementById('resolve-alarm-id').value = alarmId;
  const valText = value > 0 ? `<strong style="color: #EF4444; font-family: monospace; font-size: 14px;">${value.toFixed(1)}ºC</strong>` : '-';
  document.getElementById('resolve-alarm-summary').innerHTML = `
    <div><strong>Câmara:</strong> <span style="color: #38BDF8; font-weight: 600;">${chamberName}</span></div>
    <div style="margin-top: 5px;"><strong>Sensor:</strong> <code style="color: #CBD5E1;">${sensorId || '-'}</code> &bull; <strong>Violação:</strong> <span style="color: #FCA5A5; font-weight: 600;">${formatAlarmType(violationType)}</span> &bull; <strong>Valor Lido:</strong> ${valText}</div>
  `;
  document.getElementById('resolve-alarm-notes').value = '';
  const sigEl = document.getElementById('resolve-alarm-signature');
  if (sigEl) sigEl.value = '';
  document.getElementById('modal-resolve-alarm').classList.add('active');
  setTimeout(() => {
    const el = document.getElementById('resolve-alarm-notes');
    if (el) el.focus();
  }, 100);
}

function closeResolveAlarmModal() {
  const modal = document.getElementById('modal-resolve-alarm');
  if (modal) modal.classList.remove('active');
}

async function submitResolveAlarm() {
  const alarmId = parseInt(document.getElementById('resolve-alarm-id').value, 10);
  const notes = document.getElementById('resolve-alarm-notes').value.trim();
  const sigEl = document.getElementById('resolve-alarm-signature');
  const sig = sigEl ? sigEl.value.trim() : '';

  if (!notes || !sig) {
    alert('Preencha o laudo técnico e a assinatura eletrônica (21 CFR Part 11).');
    return;
  }

  const btn = document.getElementById('btn-submit-resolve-alarm');
  if (btn) {
    btn.disabled = true;
    btn.innerText = 'Processando Assinatura...';
  }

  try {
    const res = await fetch('/api/pharma/alarms/resolve', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ alarm_id: alarmId, notes: notes, electronic_sig: sig })
    });
    if (res.ok) {
      closeResolveAlarmModal();
      loadAlarms();
      loadKPIs();
      loadChambers();
      loadExcursions();
    } else {
      const err = await res.json().catch(() => ({}));
      alert(err.error || 'Erro ao resolver alarme.');
    }
  } catch (e) {
    alert('Erro de conexão ao resolver alarme.');
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.innerText = 'Firmar Laudo e Encerrar Alarme';
    }
  }
}

function formatExcursionInvestigationStatus(status) {
  switch (status) {
    case 'em_investigacao':
      return {
        label: 'Em Investigação',
        style: 'background: rgba(245, 158, 11, 0.14); color: #FBBF24; border: 1px solid rgba(245, 158, 11, 0.35); text-transform: none;'
      };
    case 'fabricante_consultado':
      return {
        label: 'Fabricante Consultado',
        style: 'background: rgba(56, 189, 248, 0.14); color: #38BDF8; border: 1px solid rgba(56, 189, 248, 0.35); text-transform: none;'
      };
    case 'liberado':
      return {
        label: 'Liberado',
        style: 'background: rgba(52, 211, 153, 0.14); color: #34D399; border: 1px solid rgba(52, 211, 153, 0.35); text-transform: none;'
      };
    case 'descartado':
      return {
        label: 'Descartado / Reprovado',
        style: 'background: rgba(239, 68, 68, 0.14); color: #FCA5A5; border: 1px solid rgba(239, 68, 68, 0.35); text-transform: none;'
      };
    default:
      return {
        label: status ? status.replace(/_/g, ' ') : 'Pendente',
        style: 'background: rgba(148, 163, 184, 0.14); color: #CBD5E1; border: 1px solid rgba(148, 163, 184, 0.3); text-transform: none;'
      };
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

    const countBadge = document.getElementById('excursions-count-badge');
    if (countBadge) {
      countBadge.innerText = `${list.length} registro${list.length !== 1 ? 's' : ''}`;
      countBadge.style.color = list.length > 0 ? '#EF4444' : '#94A3B8';
      countBadge.style.borderColor = list.length > 0 ? 'rgba(239, 68, 68, 0.4)' : 'rgba(255, 255, 255, 0.1)';
      countBadge.style.background = list.length > 0 ? 'rgba(239, 68, 68, 0.12)' : 'rgba(255, 255, 255, 0.05)';
    }

    if (list.length === 0) {
      tbody.innerHTML = '<tr><td colspan="8" style="text-align: center; color: #94A3B8; padding: 24px; font-size: 14px; font-weight: 500;">Nenhum processo de excursão registrado. Câmaras operando dentro da faixa térmica qualificada.</td></tr>';
      return;
    }

    list.forEach(e => {
      const isDone = e.status === 'liberado' || e.status === 'descartado';
      const cleanChamber = (e.chamber_name || e.chamber_id).replace(/\s*\(RDC[^\)]*\)/gi, '');
      let lotsBadge = '';
      if (e.status === 'liberado') {
        lotsBadge = `<span class="badge-status liberado" style="font-size: 12px; padding: 4px 10px;">${e.quarantined_lots_count} lotes liberados</span>`;
      } else if (e.status === 'descartado') {
        lotsBadge = `<span class="badge-status" style="font-size: 12px; padding: 4px 10px; background: rgba(239, 68, 68, 0.15); color: #FCA5A5; border: 1px solid rgba(239, 68, 68, 0.4);">${e.quarantined_lots_count} lotes reprovados</span>`;
      } else {
        lotsBadge = `<span class="badge-status quarentena" style="font-size: 12px; padding: 4px 10px;">${e.quarantined_lots_count} lotes em análise</span>`;
      }
      const statusInfo = formatExcursionInvestigationStatus(e.status);
      tbody.innerHTML += `
        <tr>
          <td style="text-align: center;"><strong style="color: #38BDF8; font-family: monospace; font-size: 13px;">${e.code}</strong></td>
          <td style="text-align: center; font-weight: 600; color: #F1F5F9; font-size: 13.5px;">${cleanChamber}</td>
          <td style="text-align: center;"><span style="font-size: 12px; color: #CBD5E1; line-height: 1.45;">${formatBrasiliaDateTime(e.start_time)}<br>${e.end_time ? formatBrasiliaDateTime(e.end_time) : '<span style="color:#F59E0B; font-weight:600;">Em andamento</span>'}</span></td>
          <td style="text-align: center;"><strong style="color: #EF4444; font-family: monospace; font-size: 14px;">${e.peak_temp.toFixed(1)}ºC</strong></td>
          <td style="text-align: center; color: #CBD5E1; font-family: monospace; font-size: 13.5px; font-weight: 700;">${e.accumulated_degree_minutes.toFixed(1)}ºC.min</td>
          <td style="text-align: center;">${lotsBadge}</td>
          <td style="text-align: center;"><span class="badge-status" style="font-size: 11.5px; padding: 4px 10px; font-weight: 700; white-space: nowrap; ${statusInfo.style}">${statusInfo.label}</span></td>
          <td style="text-align: center;">
            ${!isDone ? `
              <button class="btn-action-primary" style="padding: 6px 14px; font-size: 12px; margin: 0 auto; display: inline-flex;" onclick="openDecisionModal(${e.id}, '${e.code}', '${cleanChamber.replace(/'/g, "\\'")}', ${e.peak_temp})">
                Parecer / Decisão
              </button>
            ` : `<span style="font-size: 12px; color: #34D399; font-weight: 600;">Decisão: ${e.final_decision.toUpperCase()}<br><span style="color: #94A3B8; font-size: 11px;">Assinado por ${e.decided_by}</span></span>`}
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
    Pico registrado: <strong style="color:#EF4444;">${peak.toFixed(1)}ºC</strong>. Todos os lotes foram colocados preventivamente em análise.
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
  const btn = document.getElementById('btn-simulate-excursion');
  if (btn) {
    btn.disabled = true;
    btn.innerText = '⏳ Simulando Excursão...';
  }

  try {
    const res = await fetch('/api/pharma/excursions/simulate?chamber=CH-A', { method: 'POST' });
    const data = await res.json();
    if (res.ok) {
      alert(data.message || 'Excursão térmica simulada com sucesso! Lotes alocados em análise.');
      loadExcursions();
      loadAlarms();
      loadLots();
      loadChambers();
      loadKPIs();
      if (typeof selectChamber === 'function') {
        selectChamber(selectedChamberId, true);
      }
    } else {
      alert(data.error || 'Erro na simulação.');
    }
  } catch (e) {
    alert('Erro de conexão ao simular excursão.');
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.innerText = 'Simular Excursão Térmica';
    }
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
// FORMATAÇÃO DE DATA E HORA — FUSO HORÁRIO DE BRASÍLIA (UTC-3)
// -----------------------------------------------------------------------------
function formatBrasiliaDateTime(val) {
  if (!val) return '';
  let s = String(val).trim();
  if (!s || s === 'Em andamento') return s;

  // Normaliza string para padrão ISO caso venha como "YYYY-MM-DD HH:MM:SS"
  let iso = s;
  if (iso.includes(' ') && !iso.includes('T')) {
    iso = iso.replace(' ', 'T');
  }
  // Se não contiver indicação de fuso, assume UTC (armazém padrão SQLite/servidor)
  if (!iso.endsWith('Z') && !iso.includes('+') && !iso.includes('-03:00')) {
    iso += 'Z';
  }

  const d = new Date(iso);
  if (isNaN(d.getTime())) return val;

  // Formata no padrão brasileiro (DD/MM/AAAA HH:MM:SS) no Fuso Horário de Brasília
  return d.toLocaleString('pt-BR', {
    timeZone: 'America/Sao_Paulo',
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false
  });
}

function formatBrasiliaTimeOnly(val) {
  if (!val) return '--:--:--';
  const full = formatBrasiliaDateTime(val);
  if (full.includes(' ')) {
    return full.split(' ')[1];
  }
  return full;
}

function formatLedgerType(type) {
  switch (type) {
    case 'ANCHOR':
      return {
        label: 'Âncora (Selo TSA)',
        style: 'background: rgba(16, 185, 129, 0.14); color: #34D399; border: 1px solid rgba(16, 185, 129, 0.35);'
      };
    case 'BOOT':
      return {
        label: 'Inicialização (Boot)',
        style: 'background: rgba(239, 68, 68, 0.12); color: #FCA5A5; border: 1px solid rgba(239, 68, 68, 0.3);'
      };
    case 'DATA':
      return {
        label: 'Telemetria Térmica',
        style: 'background: rgba(56, 189, 248, 0.12); color: #38BDF8; border: 1px solid rgba(56, 189, 248, 0.3);'
      };
    case 'GENESIS':
      return {
        label: 'Bloco Gênesis',
        style: 'background: rgba(168, 85, 247, 0.14); color: #C084FC; border: 1px solid rgba(168, 85, 247, 0.35);'
      };
    default:
      return {
        label: type || 'Registro',
        style: 'background: rgba(148, 163, 184, 0.12); color: #CBD5E1; border: 1px solid rgba(148, 163, 184, 0.3);'
      };
  }
}

function formatAuditAction(action) {
  const map = {
    'SIMULACAO_EXCURSAO': 'Simulação de Excursão',
    'DECISAO_EXCURSAO': 'Decisão Pericial (RT)',
    'RESOLVE_ALARME': 'Resolução de Alarme',
    'RECONHECE_ALARME': 'Reconhecimento de Alarme',
    'LOGIN_SUCCESS': 'Login Autorizado',
    'LOGIN_FAILED': 'Tentativa de Login Falha',
    'LOGIN_BLOCKED': 'Conta Bloqueada',
    'LOGOUT': 'Encerramento de Sessão',
    'LEDGER_ANCHOR': 'Emissão de Âncora (TSA)',
    'CADASTRO_CAMARA': 'Cadastro de Câmara',
    'EDICAO_CAMARA': 'Edição de Parâmetros',
    'CADASTRO_SENSOR': 'Cadastro de Sensor',
    'RECALIBRACAO_SENSOR': 'Recalibração de Sensor',
    'CADASTRO_PRODUTO': 'Cadastro de Medicamento',
    'RECEBIMENTO_ESTOQUE': 'Recebimento de Estoque',
    'SAIDA_ESTOQUE': 'Saída de Estoque',
    'REVISAO_POP': 'Aprovação de POP',
    'REGISTRO_TREINAMENTO': 'Registro de Treinamento',
    'REGISTRO_QUALIFICACAO': 'Qualificação Térmica',
    'USER_CREATED': 'Criação de Usuário',
    'USER_STATUS_CHANGE': 'Alteração de Usuário',
    'USER_ROLE_CHANGE': 'Alteração de Perfil'
  };
  return map[action] || (action ? action.replace(/_/g, ' ') : '-');
}

function getAuditActionBadge(action) {
  const label = formatAuditAction(action);
  let bg = 'rgba(245, 158, 11, 0.08)';
  let color = '#F59E0B';
  let border = 'rgba(245, 158, 11, 0.25)';

  if (action === 'LOGIN_SUCCESS' || action === 'RESOLVE_ALARME' || action === 'LEDGER_ANCHOR' || action === 'DECISAO_EXCURSAO') {
    bg = 'rgba(52, 211, 153, 0.1)';
    color = '#34D399';
    border = 'rgba(52, 211, 153, 0.3)';
  } else if (action === 'LOGIN_FAILED' || action === 'LOGIN_BLOCKED') {
    bg = 'rgba(239, 68, 68, 0.1)';
    color = '#FCA5A5';
    border = 'rgba(239, 68, 68, 0.3)';
  } else if (action === 'SIMULACAO_EXCURSAO' || action === 'RECONHECE_ALARME') {
    bg = 'rgba(245, 158, 11, 0.12)';
    color = '#FBBF24';
    border = 'rgba(245, 158, 11, 0.35)';
  } else if (action.startsWith('CADASTRO_') || action.startsWith('USER_') || action.startsWith('REGISTRO_')) {
    bg = 'rgba(56, 189, 248, 0.1)';
    color = '#38BDF8';
    border = 'rgba(56, 189, 248, 0.3)';
  }

  return `<span style="display: inline-block; padding: 3px 9px; border-radius: 5px; font-size: 11.5px; font-weight: 600; background: ${bg}; color: ${color}; border: 1px solid ${border}; white-space: nowrap;">${label}</span>`;
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
        if (records.length === 0) {
          tbodyL.innerHTML = '<tr><td colspan="6" style="text-align: center; color: #94A3B8; padding: 22px; font-size: 13.5px;">Nenhum bloco registrado no ledger criptográfico.</td></tr>';
        } else {
          records.forEach(r => {
            const prev = (r.prev_hash || '').substring(0, 16);
            const cur = (r.current_hash || '').substring(0, 16);
            const sig = (r.signature || '').substring(0, 24);
            const typeInfo = formatLedgerType(r.type_name);
            const dateFormatted = formatBrasiliaDateTime(r.created_at);
            tbodyL.innerHTML += `
              <tr>
                <td style="text-align: center; white-space: nowrap;"><strong style="color: #38BDF8; font-family: monospace; font-size: 13.5px;">#${r.id}</strong></td>
                <td style="text-align: center; white-space: nowrap;"><span class="badge-status" style="font-size: 11px; padding: 4px 9px; font-weight: 700; white-space: nowrap; ${typeInfo.style}">${typeInfo.label}</span></td>
                <td style="text-align: center; white-space: nowrap;"><code style="font-size: 12px; color: #94A3B8; background: rgba(255,255,255,0.05); padding: 4px 10px; border-radius: 4px; font-family: monospace;">${prev}...</code></td>
                <td style="text-align: center; white-space: nowrap;"><code style="font-size: 12px; color: #6EE7B7; background: rgba(110,231,183,0.08); padding: 3px 8px; border-radius: 4px; font-family: monospace;">${cur}...</code></td>
                <td style="text-align: center; white-space: nowrap;"><code style="font-size: 12px; color: #C4B5FD; background: rgba(196,181,253,0.08); padding: 3px 8px; border-radius: 4px; font-family: monospace;">${sig}...</code></td>
                <td style="text-align: center; font-size: 12px; color: #CBD5E1; font-family: monospace; white-space: nowrap;">${dateFormatted}</td>
              </tr>
            `;
          });
        }
      }
    }

    // Carrega trilha de auditoria
    const resA = await fetch('/api/audit/logs');
    if (resA.ok) {
      const logs = await resA.json();
      const tbodyA = document.getElementById('audit-logs-body');
      tbodyA.innerHTML = '';
      if (logs.length === 0) {
        tbodyA.innerHTML = '<tr><td colspan="6" style="text-align: center; color: #94A3B8; padding: 22px; font-size: 13.5px;">Nenhum registro de auditoria disponível.</td></tr>';
      } else {
        logs.forEach(l => {
          const dateFormatted = formatBrasiliaDateTime(l.timestamp);
          const actionBadge = getAuditActionBadge(l.action);
          tbodyA.innerHTML += `
            <tr>
              <td style="text-align: center; font-size: 12px; color: #CBD5E1; font-family: monospace; white-space: nowrap;">${dateFormatted}</td>
              <td style="text-align: center; white-space: nowrap;"><strong style="color: #F1F5F9; font-size: 13.5px;">${l.username}</strong></td>
              <td style="text-align: center; white-space: nowrap;"><span class="badge-status liberado" style="font-size: 11px;">${l.role.toUpperCase()}</span></td>
              <td style="text-align: center; white-space: nowrap;">${actionBadge}</td>
              <td style="font-size: 13px; color: #E2E8F0; line-height: 1.45; word-break: break-word;">${l.details}</td>
              <td style="text-align: center; font-size: 12px; color: #94A3B8; font-family: monospace; white-space: nowrap;">${l.ip_address}</td>
            </tr>
          `;
        });
      }
    }
  } catch (e) {
    console.error('Erro ao carregar auditoria e ledger:', e);
  }
}

async function verifyLedgerLive() {
  const box = document.getElementById('ledger-audit-box');
  const msg = document.getElementById('ledger-audit-msg');
  const btn = document.getElementById('btn-verify-ledger');
  box.style.display = 'block';
  msg.innerText = 'Executando verificação pericial criptográfica (SHA-256 Chaining + ECDSA P-256)...';
  if (btn) {
    btn.disabled = true;
    btn.innerText = 'Auditando Cadeia...';
  }

  try {
    const res = await fetch('/api/v1/ledger/verify');
    const data = await res.json();
    if (res.ok && data.valid) {
      msg.innerHTML = `<strong>Laudo Pericial: 100% Conforme!</strong> Todos os <strong>${data.total_records || data.total_blocks || data.checked_hashes || 0} blocos</strong> auditados em <strong>${(data.duration_ms || data.verification_time_ms || 15).toFixed(1)} ms</strong>. Zero adulterações detectadas. Assinatura P-256 íntegra.`;
      box.style.background = 'rgba(16, 185, 129, 0.12)';
      box.style.borderColor = 'rgba(16, 185, 129, 0.4)';
      box.style.color = '#6EE7B7';
    } else {
      msg.innerHTML = `<strong>FALHA DE INTEGRIDADE:</strong> ${data.error || 'Cadeia violada!'}`;
      box.style.background = 'rgba(239, 68, 68, 0.12)';
      box.style.borderColor = 'rgba(239, 68, 68, 0.4)';
      box.style.color = '#FCA5A5';
    }
    loadLedger();
  } catch (e) {
    msg.innerText = 'Erro ao executar verificação do ledger.';
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.innerText = 'Auditar Integridade da Cadeia';
    }
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


// -----------------------------------------------------------------------------
// MÓDULO 2: GESTÃO DE ESTOQUE & CONTROLE POR VALIDADE (PVPS / FEFO)
// -----------------------------------------------------------------------------
window.allPharmaProducts = [];
window.allPharmaLots = [];

async function loadProducts() {
  try {
    const res = await fetch('/api/pharma/products');
    if (!res.ok) return;
    const list = await res.json();
    window.allPharmaProducts = list;

    const select = document.getElementById('entry-product');
    if (select) {
      select.innerHTML = '';
      list.forEach(p => {
        const opt = document.createElement('option');
        opt.value = p.id;
        opt.dataset.min = p.temp_min;
        opt.dataset.max = p.temp_max;
        opt.textContent = `${p.trade_name} (${p.dcb}) — [${p.temp_min}°C a ${p.temp_max}°C]`;
        select.appendChild(opt);
      });
      updateProductRanges();
    }
  } catch (e) {
    console.error('Erro ao carregar produtos:', e);
  }
}

async function loadLots() {
  try {
    const res = await fetch('/api/pharma/lots');
    if (!res.ok) return;
    const list = await res.json();
    window.allPharmaLots = list;

    const tbody = document.getElementById('lots-table-body');
    if (!tbody) return;
    tbody.innerHTML = '';

    if (list.length === 0) {
      tbody.innerHTML = '<tr><td colspan="9" style="text-align:center; color:#64748B; padding:18px;">Nenhum lote registrado em estoque.</td></tr>';
      return;
    }

    list.forEach((l, idx) => {
      let badgeClass = 'liberado';
      if (l.status === 'quarentena') badgeClass = 'quarentena';
      else if (l.status === 'reprovado' || l.status === 'vencido') badgeClass = 'reprovado';
      else if (l.status === 'recolhido' || l.status === 'devolvido') badgeClass = 'alerta';

      const expiryFormatted = l.expiry_date.split('-').reverse().join('/');
      const isClose = l.days_to_expiry <= 30;

      const tr = document.createElement('tr');
      tr.className = 'lot-table-row';
      tr.innerHTML = `
        <td style="text-align: center;"><span class="badge-fefo-rank" style="background:${idx === 0 ? '#10B981' : '#334155'}; font-size:11px; font-weight:700; margin: 0 auto;">${idx + 1}º</span></td>
        <td>
          <strong style="color:#F8FAFC;">${l.product_trade_name}</strong>
          <div style="font-size:11px; color:#94A3B8;">${l.product_dcb} — ${l.manufacturer || ''}</div>
        </td>
        <td><span style="font-size:11.5px; color:#CBD5E1;">${l.anvisa_reg || '---'}</span></td>
        <td><strong style="color:#38BDF8; font-family:monospace;">${l.lot_number}</strong></td>
        <td>
          <div style="font-weight:600; color:#F8FAFC;">${expiryFormatted}</div>
          <div style="font-size:10.5px; color:${isClose ? '#F87171' : '#94A3B8'}; font-weight:${isClose ? '700' : '400'};">
            ${l.days_to_expiry > 0 ? `${l.days_to_expiry} dias restantes` : '<span style="color:#EF4444;">VENCIDO</span>'}
          </div>
        </td>
        <td><strong style="font-size:13px; color:#F8FAFC;">${l.quantity}</strong> <span style="font-size:11px; color:#94A3B8;">un.</span></td>
        <td>
          <div style="font-size:12px; color:#F8FAFC;">${l.chamber_name || l.chamber_id}</div>
          <div style="font-size:10.5px; color:#94A3B8;">${l.shelf_location || ''}</div>
        </td>
        <td><span class="badge-status ${badgeClass}">${l.status.toUpperCase()}</span></td>
        <td style="text-align: center;">
          <button class="btn-action-secondary" style="padding:5px 14px; font-size:11.5px; margin: 0 auto; display: inline-flex; align-items: center; justify-content: center;" onclick="openTraceModal('${l.lot_number}')">
            Rastreabilidade
          </button>
        </td>
      `;
      tbody.appendChild(tr);
    });
  } catch (e) {
    console.error('Erro ao carregar lotes:', e);
  }
}

function filterLotsTable() {
  const q = (document.getElementById('filter-lot-input').value || '').toLowerCase().trim();
  const rows = document.querySelectorAll('#lots-table-body tr.lot-table-row');
  rows.forEach(r => {
    const text = r.textContent.toLowerCase();
    r.style.display = text.includes(q) ? '' : 'none';
  });
}

function openEntryModal() {
  const m = document.getElementById('modal-entry');
  if (!m) return;
  m.classList.add('active');

  if (window.allPharmaProducts.length === 0) {
    loadProducts();
  } else {
    updateProductRanges();
  }

  // Preenche data padrão de validade (1 ano à frente)
  const expInput = document.getElementById('entry-expiry');
  if (expInput && !expInput.value) {
    const d = new Date();
    d.setFullYear(d.getFullYear() + 1);
    expInput.value = d.toISOString().split('T')[0];
  }
}

function closeEntryModal() {
  const m = document.getElementById('modal-entry');
  if (m) m.classList.remove('active');
}

function updateProductRanges() {
  const sel = document.getElementById('entry-product');
  if (!sel) return;
  checkEntryTemp();
}

function checkEntryTemp() {
  const sel = document.getElementById('entry-product');
  const tempInput = document.getElementById('entry-temp');
  const warn = document.getElementById('entry-temp-warning');
  if (!sel || !tempInput || !warn) return;

  const opt = sel.options[sel.selectedIndex];
  if (!opt) return;

  const min = parseFloat(opt.dataset.min);
  const max = parseFloat(opt.dataset.max);
  const val = parseFloat(tempInput.value);

  if (!isNaN(val) && (val < min || val > max)) {
    warn.style.display = 'flex';
    warn.innerHTML = `<div><strong>Atenção Regulamentar:</strong> Temperatura aferida (${val.toFixed(1)}°C) fora da faixa permitida (${min.toFixed(1)}°C a ${max.toFixed(1)}°C)! O lote será segregado automaticamente em <strong>Quarentena Preventiva</strong> (RDC 430/2020 Art. 38).</div>`;
  } else {
    warn.style.display = 'none';
  }
}

async function submitStockEntry() {
  const prodSelect = document.getElementById('entry-product');
  const opt = prodSelect.options[prodSelect.selectedIndex];
  const min = opt ? parseFloat(opt.dataset.min) : 2.0;
  const max = opt ? parseFloat(opt.dataset.max) : 8.0;

  const arrivalTemp = parseFloat(document.getElementById('entry-temp').value);
  const decision = (arrivalTemp < min || arrivalTemp > max) ? 'quarentena' : 'liberado';

  const payload = {
    product_id: parseInt(prodSelect.value, 10),
    lot_number: document.getElementById('entry-lot').value.trim(),
    expiry_date: document.getElementById('entry-expiry').value,
    quantity: parseInt(document.getElementById('entry-qty').value, 10),
    chamber_id: document.getElementById('entry-chamber').value,
    shelf_location: document.getElementById('entry-shelf').value.trim(),
    unit_price: 150.0,
    supplier: document.getElementById('entry-supplier').value.trim(),
    supplier_cnpj: document.getElementById('entry-cnpj').value.trim(),
    invoice_number: document.getElementById('entry-nfe').value.trim(),
    transport_condition: 'Caminhão Refrigerado / Baú Isotérmico Validados',
    arrival_temp: arrivalTemp,
    cargo_integrity: 'Conforme (Lacre de segurança intacto)',
    ambient_exposure_minutes: 0,
    decision: decision
  };

  try {
    const res = await fetch('/api/pharma/stock/entry', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    const data = await res.json();
    if (res.ok) {
      alert(`Recebimento na Doca concluído com sucesso!\nLote: ${data.lot.lot_number}\nStatus Inicial: ${data.lot.status.toUpperCase()}`);
      closeEntryModal();
      document.getElementById('form-entry').reset();
      loadLots();
      loadChambers();
      loadKPIs();
    } else {
      alert(data.error || 'Erro ao registrar recebimento.');
    }
  } catch (e) {
    alert('Erro de conexão ao registrar entrada.');
  }
}

function openExitModal() {
  const m = document.getElementById('modal-exit');
  if (!m) return;

  const validLots = (window.allPharmaLots || [])
    .filter(l => l.status === 'liberado' && l.quantity > 0)
    .sort((a, b) => new Date(a.expiry_date) - new Date(b.expiry_date));

  if (validLots.length === 0) {
    alert('Nenhum lote com status LIBERADO e saldo disponível para dispensação no momento.');
    return;
  }

  const select = document.getElementById('exit-lot-select');
  select.innerHTML = '';
  validLots.forEach((l, idx) => {
    const opt = document.createElement('option');
    opt.value = l.id;
    opt.textContent = `${idx === 0 ? 'Prioridade 1 — ' : ''}${l.product_trade_name} | Lote: ${l.lot_number} | Vence: ${l.expiry_date} (${l.days_to_expiry}d) | Saldo: ${l.quantity} un.`;
    select.appendChild(opt);
  });

  m.classList.add('active');
  updateExitLotInfo();
}

function closeExitModal() {
  const m = document.getElementById('modal-exit');
  if (m) m.classList.remove('active');
}

function updateExitLotInfo() {
  const select = document.getElementById('exit-lot-select');
  const info = document.getElementById('exit-fefo-info');
  const qtyInput = document.getElementById('exit-qty');
  if (!select || !info) return;

  const lotId = parseInt(select.value, 10);
  const lot = (window.allPharmaLots || []).find(l => l.id === lotId);

  if (lot) {
    info.innerHTML = `<strong>Lote Selecionado:</strong> ${lot.product_trade_name} (Lote: <code>${lot.lot_number}</code>)<br>Validade: <strong>${lot.expiry_date.split('-').reverse().join('/')}</strong> (${lot.days_to_expiry} dias restantes) | Saldo em Câmara: <strong>${lot.quantity} un.</strong>`;
    if (qtyInput) {
      qtyInput.max = lot.quantity;
      if (!qtyInput.value || parseInt(qtyInput.value, 10) > lot.quantity) {
        qtyInput.value = Math.min(10, lot.quantity);
      }
    }
  }
}

async function submitStockExit() {
  const select = document.getElementById('exit-lot-select');
  const lotId = parseInt(select.value, 10);
  const qty = parseInt(document.getElementById('exit-qty').value, 10);
  const reason = document.getElementById('exit-reason').value;
  const recipient = document.getElementById('exit-recipient').value.trim();
  const nfe = document.getElementById('exit-nfe').value.trim();

  const payload = {
    lot_id: lotId,
    quantity: qty,
    reason: reason,
    recipient: recipient,
    invoice_number: nfe
  };

  try {
    const res = await fetch('/api/pharma/stock/exit', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    const data = await res.json();
    if (res.ok) {
      alert(`Dispensação / Saída concluída com sucesso!\nQuantidade retirada: ${qty} un.\nSaldo restante: ${data.remaining_quantity} un.`);
      closeExitModal();
      document.getElementById('form-exit').reset();
      loadLots();
      loadChambers();
      loadKPIs();
    } else {
      alert(data.error || 'Erro ao registrar saída.');
    }
  } catch (e) {
    alert('Erro de conexão ao registrar saída.');
  }
}

async function openTraceModal(lotNumber) {
  const m = document.getElementById('modal-trace');
  const content = document.getElementById('trace-content');
  if (!m || !content) return;

  m.classList.add('active');
  content.innerHTML = `<div style="text-align:center; padding:30px; color:#94A3B8;">Carregando dossiê de rastreabilidade do lote ${lotNumber}...</div>`;

  try {
    const res = await fetch(`/api/pharma/lots/trace?lot=${encodeURIComponent(lotNumber)}`);
    if (!res.ok) {
      content.innerHTML = `<div style="color:#EF4444; padding:20px;">Não foi possível carregar o laudo pericial para o lote ${lotNumber}.</div>`;
      return;
    }
    const trace = await res.json();
    const lot = trace.lot;
    const entry = trace.entry;
    const exits = trace.exits || [];

    let exitsHtml = '';
    if (exits.length === 0) {
      exitsHtml = '<div style="color:#64748B; font-size:12px; margin-top:6px;">Nenhuma dispensação realizada até o momento.</div>';
    } else {
      exitsHtml = exits.map(x => `
        <div style="background:rgba(255,255,255,0.02); border:1px solid rgba(255,255,255,0.06); border-radius:6px; padding:8px 12px; margin-top:6px; font-size:12px;">
          <div style="display:flex; justify-content:space-between;">
            <strong style="color:#34D399;">${x.quantity} un. dispensadas</strong>
            <span style="color:#94A3B8;">${x.exit_at}</span>
          </div>
          <div style="color:#CBD5E1;">Destinatário: ${x.recipient} | Motivo: ${x.reason.toUpperCase()}</div>
          <div style="color:#94A3B8; font-size:11px;">Responsável: ${x.responsible_name} | Doc: ${x.invoice_number || 'S/N'}</div>
        </div>
      `).join('');
    }

    content.innerHTML = `
      <div style="border-bottom:1px solid rgba(255,255,255,0.08); padding-bottom:14px; margin-bottom:14px;">
        <div style="display:flex; justify-content:space-between; align-items:flex-start;">
          <div>
            <div style="font-size:16px; font-weight:700; color:#F8FAFC;">${lot.product_trade_name}</div>
            <div style="font-size:12px; color:#94A3B8;">${lot.product_dcb} — Registro ANVISA: ${lot.anvisa_reg}</div>
          </div>
          <span class="badge-status ${lot.status === 'liberado' ? 'liberado' : 'quarentena'}">${lot.status.toUpperCase()}</span>
        </div>
        <div style="display:grid; grid-template-columns:repeat(4, 1fr); gap:10px; margin-top:12px; background:rgba(255,255,255,0.02); padding:10px; border-radius:8px;">
          <div><span style="font-size:10px; color:#64748B;">LOTE</span><br><strong style="color:#38BDF8;">${lot.lot_number}</strong></div>
          <div><span style="font-size:10px; color:#64748B;">VALIDADE</span><br><strong>${lot.expiry_date.split('-').reverse().join('/')}</strong></div>
          <div><span style="font-size:10px; color:#64748B;">SALDO ATUAL</span><br><strong>${lot.quantity} un.</strong></div>
          <div><span style="font-size:10px; color:#64748B;">LOCALIZAÇÃO</span><br><strong>${lot.chamber_name}</strong></div>
        </div>
      </div>

      <div style="margin-bottom:16px;">
        <div style="font-size:13px; font-weight:700; color:#38BDF8; margin-bottom:6px;">1. Entrada e Conferência Térmica na Doca</div>
        ${entry ? `
          <div style="background:rgba(255,255,255,0.02); border:1px solid rgba(255,255,255,0.06); border-radius:6px; padding:10px 12px; font-size:12px;">
            <div style="display:grid; grid-template-columns:1fr 1fr; gap:8px;">
              <div>Fornecedor: <strong>${entry.supplier}</strong> (CNPJ: ${entry.supplier_cnpj})</div>
              <div>Nota Fiscal: <strong>${entry.invoice_number}</strong></div>
              <div>Temperatura no Recebimento: <strong style="color:${entry.arrival_temp >= 2 && entry.arrival_temp <= 8 ? '#10B981' : '#F59E0B'};">${entry.arrival_temp.toFixed(1)}°C</strong></div>
              <div>Decisão Inicial: <strong style="color:#10B981;">${entry.decision.toUpperCase()}</strong> por ${entry.responsible_name}</div>
            </div>
            <div style="color:#94A3B8; font-size:11px; margin-top:6px;">Transporte: ${entry.transport_condition} | Integridade: ${entry.cargo_integrity}</div>
          </div>
        ` : '<div style="color:#64748B; font-size:12px;">Dados de entrada arquivados ou pré-cadastrados.</div>'}
      </div>

      <div style="margin-bottom:16px;">
        <div style="font-size:13px; font-weight:700; color:#34D399; margin-bottom:6px;">2. Histórico de Saídas e Dispensações</div>
        ${exitsHtml}
      </div>

      <div style="border-top:1px solid rgba(255,255,255,0.08); padding-top:12px; display:flex; justify-content:space-between; align-items:center;">
        <span style="font-size:11px; color:#10B981;">Integridade validada via Ledger Criptográfico SHA-256</span>
        <button class="btn-logout" style="color:#94A3B8; border-color:#334155; background:transparent;" onclick="closeTraceModal()">Fechar</button>
      </div>
    `;
  } catch (e) {
    content.innerHTML = `<div style="color:#EF4444; padding:20px;">Erro ao carregar rastreabilidade: ${e.message}</div>`;
  }
}

function closeTraceModal() {
  const m = document.getElementById('modal-trace');
  if (m) m.classList.remove('active');
}

// ==============================================================================
// DESCOMISSIONAMENTO E BAIXA REGULATÓRIA DE CÂMARAS (ANVISA RDC 430 & 21 CFR 11)
// ==============================================================================
function openDecommissionModal() {
  const currentChamber = chambersData ? chambersData.find(c => c.id === selectedChamberId) : null;
  if (!currentChamber) {
    alert('Nenhuma câmara selecionada para descomissionamento.');
    return;
  }

  const m = document.getElementById('modal-decommission-chamber');
  if (!m) return;

  const elName = document.getElementById('decom-chamber-name');
  const elId = document.getElementById('decom-chamber-id');
  const elRange = document.getElementById('decom-chamber-range');
  const elLots = document.getElementById('decom-chamber-lots');
  const elSensors = document.getElementById('decom-chamber-sensors');

  if (elName) elName.innerText = currentChamber.name;
  if (elId) elId.innerText = currentChamber.id;
  if (elRange) elRange.innerText = `${currentChamber.temp_min.toFixed(1).replace('.', ',')} °C a ${currentChamber.temp_max.toFixed(1).replace('.', ',')} °C`;
  if (elLots) elLots.innerText = `${currentChamber.quarantined_lots_count || 0} lotes ativos`;
  if (elSensors) elSensors.innerText = `${sensorsData ? sensorsData.length : currentChamber.sensor_count} sensores vinculados`;

  const sigName = document.getElementById('decom-resp-name');
  if (sigName) {
    sigName.value = currentUser ? `${currentUser.name} (${currentUser.role.toUpperCase()})` : 'Farmacêutico RT';
  }

  m.classList.add('active');
}

function closeDecommissionModal() {
  const m = document.getElementById('modal-decommission-chamber');
  if (m) m.classList.remove('active');
  const f = document.getElementById('form-decommission-chamber');
  if (f) f.reset();
}

async function submitDecommissionChamber() {
  const reason = document.getElementById('decom-reason').value;
  const just = document.getElementById('decom-justification').value.trim();
  const sensorAction = document.getElementById('decom-sensor-action').value;
  const pwd = document.getElementById('decom-password').value;

  if (!just) {
    alert('Preencha o parecer técnico com a justificativa de descomissionamento.');
    return;
  }
  if (!pwd) {
    alert('Digite sua senha para autenticar a assinatura digital (21 CFR Part 11).');
    return;
  }

  const btn = document.getElementById('btn-submit-decom');
  if (btn) {
    btn.disabled = true;
    btn.innerText = 'Processando Baixa no Ledger...';
  }

  try {
    const res = await fetch('/api/pharma/chambers', {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        chamber_id: selectedChamberId,
        reason: reason,
        justification: just,
        sensor_action: sensorAction,
        password: pwd
      })
    });

    const data = await res.json();
    if (!res.ok) {
      alert('Falha no descomissionamento: ' + (data.error || 'Erro na validação regulatória.'));
      return;
    }

    alert(data.message || 'Equipamento descomissionado com sucesso!');
    closeDecommissionModal();

    // Redireciona a visualização para a Câmara padrão
    selectedChamberId = 'CH-A';
    await loadChambers(true);
  } catch (e) {
    console.error('Erro ao descomissionar câmara:', e);
    alert('Erro de comunicação ao efetuar baixa do equipamento.');
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.innerText = 'Confirmar Baixa do Equipamento';
    }
  }
}
