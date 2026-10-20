#!/bin/bash
TARGET=${1:-"127.0.0.1"}
PORT=${2:-5000}
PHRASE="Helooowww world eng. Computacional!!!!!!!"

echo "[+] Aguardando o Gateway responder na porta ${PORT}..."
START=$(date +%s%N)
while true; do
  if curl -s --connect-timeout 0.1 "http://${TARGET}:${PORT}/" | grep -F "$PHRASE" >/dev/null 2>&1; then
    END=$(date +%s%N)
    DIFF=$(( (END - START) / 1000000 ))
    SECS=$(echo "scale=2; $DIFF / 1000" | bc)
    echo "[✓] SUCESSO: Gateway respondeu em ${SECS} s (Meta: <= 15s)"
    break
  fi
  sleep 0.1
done
