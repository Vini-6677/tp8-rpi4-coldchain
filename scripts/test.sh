#!/bin/bash
TARGET_HOST=${1:-"127.0.0.1"}
PORT=${2:-5000}
MANDATORY_PHRASE="Helooowww world eng. Computacional!!!!!!!"

echo "=========================================================="
echo "  Web-Server Cold Chain — Bateria de Testes (13 Requisitos)"
echo "=========================================================="

echo -n "[1/4] Testando Frase Obrigatória via curl puro... "
RESP_RAW=$(curl -s "http://${TARGET_HOST}:${PORT}/")
if echo "$RESP_RAW" | grep -F "$MANDATORY_PHRASE" >/dev/null; then
  echo "OK (Passou)"
else
  echo "FALHOU"
  echo "Recebido: $RESP_RAW"
fi

echo -n "[2/4] Testando Negociação por Cabeçalho Accept (Navegador)... "
RESP_HTML=$(curl -s -H "Accept: text/html" "http://${TARGET_HOST}:${PORT}/")
if echo "$RESP_HTML" | grep -F "<h1>${MANDATORY_PHRASE}</h1>" >/dev/null; then
  echo "OK (Passou: frase dentro de <h1>)"
else
  echo "FALHOU"
fi

echo -n "[3/4] Testando Endpoint de Diagnóstico (/healthz)... "
RESP_HEALTH=$(curl -s "http://${TARGET_HOST}:${PORT}/healthz")
if echo "$RESP_HEALTH" | grep -F '"status":"ok"' >/dev/null; then
  echo "OK (Passou)"
else
  echo "FALHOU"
fi

echo -n "[4/4] Testando Endpoint de Status e Contadores (/api/status)... "
RESP_STATUS=$(curl -s "http://${TARGET_HOST}:${PORT}/api/status")
if echo "$RESP_STATUS" | grep -F '"status":"running"' >/dev/null; then
  echo "OK (Passou)"
else
  echo "FALHOU"
fi
