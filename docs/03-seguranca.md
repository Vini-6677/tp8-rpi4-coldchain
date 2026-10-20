# 03 — Modelo de Segurança

## Host e Acesso
- **Root Bloqueado (`!`):** Sem senha definida, impedindo login direto como root.
- **Usuário coldadmin:** Pertence ao grupo `sudo`, mas **não** ao grupo `docker` (para evitar privilégio root irrestrito).
- **Dropbear SSH:** Parâmetros restritivos `-s -w -j -k` (sem root, sem port forwarding). Chaves do host geradas na partição persistente `/data/dropbear/`.

## Firewall e Docker
- O Docker ignora a tabela `INPUT` para portas publicadas (`-p`). Por isso, as regras são criadas na chain `DOCKER-USER` no script `S15firewall` **antes** do dockerd subir.
- Apenas conexões da sub-rede local recebida do DHCP são aceitas nas portas 5000 e 1883.

## Containers
- Execução como `--read-only`, `--cap-drop ALL`, `--security-opt no-new-privileges`.
- UIDs explícitos não-root: `1883:1883` (Mosquitto) e `10001:10001` (App Go).
