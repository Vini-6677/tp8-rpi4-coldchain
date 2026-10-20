# 02 — Matriz de Conformidade dos 13 Requisitos

| # | Requisito | Status | Implementação |
|---|---|---|---|
| 1 | Imagem Buildroot para o alvo | Concluído | `tp8_qemu_defconfig` e `tp8_raspberrypi4_defconfig` |
| 2 | Hostname | Concluído | `coldchain-rpi4` + mDNS avahi |
| 3 | Usuário e Senha na Compilação | Concluído | `coldadmin` com hash SHA-512 e root bloqueado (`!`) |
| 4 | Mensagem de Boas-Vindas | Concluído | Banner `/etc/issue` renderizado no build |
| 5 | Docker Engine na Imagem | Concluído | Pacotes Docker, containerd, runc, tini e iptables |
| 6 | Docker no Boot | Concluído | Script `S40docker` paralelo |
| 7 | Imagens Offline | Concluído | Tarballs em `/opt/docker/` |
| 8 | Container Automático | Concluído | `S99coldchain` + `--restart always` |
| 9-10 | App Web em Container | Concluído | Go estático em imagem `scratch` |
| 11-12 | Porta 5000 em 0.0.0.0 | Concluído | `-p 5000:5000` ouvindo em 0.0.0.0 |
| 13 | Frase Obrigatória | Concluído | `Helooowww world eng. Computacional!!!!!!!` |
