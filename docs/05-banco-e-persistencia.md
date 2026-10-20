# 05 — Banco de Dados e Resiliência a Quedas de Energia

## SQLite em WAL com synchronous=FULL
- O modo WAL padrão com `synchronous=NORMAL` pode perder transações já confirmadas se houver corte de energia imediato.
- Adotamos `synchronous=FULL` agrupado em lotes de ~1 s, gerando apenas 1 `fsync` por segundo.
- ACK via MQTT só é publicado após o `tx.Commit()` ser completado no disco.

## Rotinas de Integridade e Backup
- `e2fsck -p` executado no boot pelo `S02fsck-data` antes da montagem de `/data`.
- Backup diário automático via `VACUUM INTO` mantendo as 7 cópias mais recentes.
- Parada limpa capturando `SIGTERM` e executando `wal_checkpoint(TRUNCATE)`.
