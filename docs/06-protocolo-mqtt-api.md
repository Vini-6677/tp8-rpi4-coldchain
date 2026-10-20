# 06 — Protocolo MQTT e API HTTP

## Tópicos MQTT (QoS 1)
- `coldchain/<id>/telemetry`: Publicação de telemetria pelo ESP32.
- `coldchain/<id>/status`: Mensagem retain de status ("online" / "offline" via Last Will).
- `coldchain/<id>/ack`: ACK publicado pelo gateway contendo o maior `seq` contíguo salvo no banco.

## Rotas HTTP (:5000)
- `/`: Frase obrigatória com negociação por cabeçalho `Accept` (`text/html` devolve painel com `<h1>`, texto simples devolve a frase limpa).
- `/healthz`: Diagnóstico JSON para healthcheck e watchdog.
- `/api/status`: Métricas de uptime, leituras salvas e dispositivos ativos.
- `/api/export.csv`: Exportação CSV sanitizada contra injeção de fórmulas.
- `/events`: Atualização contínua via Server-Sent Events (SSE).
