// Conexão Server-Sent Events (SSE) para atualização em tempo real
const evtSource = new EventSource("/events");

evtSource.onmessage = function(event) {
  try {
    const data = JSON.parse(event.data);
    if (data.type === "telemetry") {
      updateUI(data.payload);
    }
  } catch (e) {
    console.error("Erro ao decodificar telemetria:", e);
  }
};

function updateUI(t) {
  document.getElementById("temp-val").textContent = t.t.toFixed(1) + " °C";
  const tbody = document.getElementById("telemetry-body");
  const row = document.createElement("tr");
  const dateStr = new Date(t.ts * 1000).toLocaleTimeString();
  row.innerHTML = `
    <td>${dateStr}</td>
    <td><code>${t.device_id}</code></td>
    <td style="color: ${t.t >= 2 && t.t <= 8 ? 'var(--success)' : 'var(--danger)'}; font-weight: 600;">${t.t.toFixed(1)}</td>
    <td>${t.h.toFixed(0)}%</td>
    <td>${t.door === 0 ? 'Fechada' : '<span style="color:var(--danger)">Aberta</span>'}</td>
    <td>#${t.seq}</td>
  `;
  if (tbody.children[0] && tbody.children[0].children.length === 1) {
    tbody.innerHTML = "";
  }
  tbody.insertBefore(row, tbody.firstChild);
  while (tbody.children.length > 10) {
    tbody.removeChild(tbody.lastChild);
  }
}

// Atualização periódica dos contadores
async function fetchStatus() {
  try {
    const res = await fetch("/api/status");
    const st = await res.json();
    document.getElementById("dev-count").textContent = st.devices_count || 0;
    document.getElementById("readings-count").textContent = st.readings_count || 0;
  } catch (err) {}
}
setInterval(fetchStatus, 3000);
fetchStatus();
