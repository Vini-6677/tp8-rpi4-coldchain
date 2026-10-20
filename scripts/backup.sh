#!/bin/sh
BACKUP_DIR="/data/coldchain/backup"
mkdir -p "$BACKUP_DIR"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="${BACKUP_DIR}/coldchain_${TIMESTAMP}.db"

echo "[+] Executando VACUUM INTO diário em $BACKUP_FILE..."
sqlite3 /data/coldchain/coldchain.db "VACUUM INTO '${BACKUP_FILE}';" 2>/dev/null

# Mantém apenas as 7 cópias mais recentes
ls -1t ${BACKUP_DIR}/coldchain_*.db | tail -n +8 | xargs rm -f 2>/dev/null
echo "[✓] Backup concluído com rotação de 7 cópias."
