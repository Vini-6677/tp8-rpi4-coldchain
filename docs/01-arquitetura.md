# 01 — Arquitetura do Sistema

## Visão Geral
O **Web-Server Cold Chain Gateway** adota uma arquitetura em camadas executando sobre um Linux embarcado mínimo construído via Buildroot com `br2-external`. O sistema isola o broker MQTT e a aplicação web em containers Docker com mínimo privilégio na rede bridge `coldchain-net` (`br-coldchain`).

## Decisões Arquiteturais Chave
1. **Dois Containers:** O broker Mosquitto (infraestrutura de mensageria em C) é separado da aplicação web/banco (Go estático). Falhas na aplicação não derrubam os sensores ESP32.
2. **Go em scratch:** Binário estático único (~10 MB) que inicia em dezenas de milissegundos e elimina vulnerabilidades de sistemas de arquivos base.
3. **BusyBox Init:** Inicialização determinística e ultrarrápida, eliminando a sobrecarga do systemd.
4. **Partição /data única com escrita:** O rootfs permanece montado como somente leitura (`ro`), protegendo o cartão SD contra corrupção em cortes de energia.
