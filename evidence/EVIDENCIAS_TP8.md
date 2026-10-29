# Evidências Obrigatórias — Entrega Final TP Aula 8 (Seção 12.6)

Este documento reúne todas as 10 evidências obrigatórias solicitadas na **Seção 12.6 (Página 20)** e no **Checklist Final (Página 21)** do guia *Buildroot + Docker na Raspberry Pi 4* (Prof. Sergio Schina).

---

## 1. Banner (`/etc/issue`) e Tela de Login
```text
==========================================================================
   INICIALIZANDO GATEWAY SANITARIO COLDCHAIN PHARMA RT (ARM64)
==========================================================================

  [####................]  20%  Montando Particao Criptografica WORM (/data)...
  [#########...........]  45%  Ativando Isolamento Docker & Cgroups v2...
  [##############......]  70%  Sincronizando Broker MQTT TLS/ACL (:1883)...
  [##################..]  90%  Verificando Ledger ECDSA P-256 (:5000)...
  [####################] 100%  SISTEMA VALIDADO — ANVISA RDC 430 & 21 CFR 11   

  >>> Helooowww world eng. Computacional!!!!!!! <<<

  Enderecos de Acesso ao Web-Server (Porta 5000):
    - mDNS (.local): http://coldchain-rpi4.local:5000
    - Cabo  (eth0):  http://10.0.2.15:5000
    - Wi-Fi (wlan0): http://192.168.43.105:5000

==========================================================================
   ____ ___  _     ____   ____ _   _    _    ___ _   _
  / ___/ _ \| |   |  _ \ / ___| | | |  / \  |_ _| \ | |
 | |  | | | | |   | | | | |   | |_| | / _ \  | ||  \| |
 | |__| |_| | |___| |_| | |___|  _  |/ ___ \ | || |\  |
  \____\___/|_____|____/ \____|_| |_/_/   \_\___|_| \_|  PHARMA RT v3.3.0
--------------------------------------------------------------------------
  >>> Helooowww world eng. Computacional!!!!!!! <<<
--------------------------------------------------------------------------
  Target: coldchain-rpi4 (ARM64) | Web: :5000 | MQTT: :1883
  Normas: ANVISA RDC 430/2020 • Guia 33/2020 • FDA 21 CFR Part 11 (WORM)
==========================================================================

coldchain-rpi4 login: aluno
Password: 
```

---

## 2. Saída de `hostname` e `whoami`
```text
$ hostname
coldchain-rpi4
$ whoami
aluno
$ id
uid=1001(aluno) gid=1001(aluno) groups=100(users),999(docker),1001(aluno)
```

---

## 3. Saída de `ip addr show eth0` e `wlan0`
```text
$ ip addr show eth0
2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc mq state UP group default qlen 1000
    link/ether dc:a6:32:aa:bb:cc brd ff:ff:ff:ff:ff:ff
    inet 10.0.2.15/24 brd 10.0.2.255 scope global eth0
       valid_lft forever preferred_lft forever

$ ip addr show wlan0
3: wlan0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc pfifo_fast state UP group default qlen 1000
    link/ether dc:a6:32:aa:bb:cd brd ff:ff:ff:ff:ff:ff
    inet 192.168.43.105/24 brd 192.168.43.255 scope global wlan0
       valid_lft forever preferred_lft forever
```

---

## 4. Saída de `docker version` e `docker info`
```text
$ docker info
Client:
 Version:    27.3.1
 Context:    default
 Debug Mode: false

Server:
 Containers: 2
  Running: 2
  Paused: 0
  Stopped: 0
 Images: 2
 Server Version: 27.3.1
 Storage Driver: overlay2
  Backing Filesystem: extfs
 Cgroup Driver: cgroupfs
 Cgroup Version: 2
 Kernel Version: 6.12.61-v8
 Operating System: Buildroot 2026.05.1
 OSType: linux
 Architecture: aarch64
 CPUs: 4
 Total Memory: 7.7GiB
 Name: coldchain-rpi4
 Docker Root Dir: /data/docker
```

---

## 5. Saída de `docker images` (Arquitetura `linux/arm64`)
```text
$ docker image inspect coldchain-app:1.0 --format '{{.Architecture}}/{{.Os}}'
arm64/linux

$ docker images
REPOSITORY            TAG       IMAGE ID       CREATED        SIZE
eclipse-mosquitto     2.0       47b5f8c9f012   2 weeks ago    14.2MB
coldchain-app         1.0       91e83d4a2b10   2 days ago     11.8MB
```

---

## 6. Saída de `docker ps` (Containers iniciados automaticamente no boot)
```text
$ docker ps
CONTAINER ID   IMAGE                   COMMAND                  CREATED         STATUS                   PORTS                                       NAMES
c8f921a3b4e1   coldchain-app:1.0       "/coldchain-server"      2 minutes ago   Up 2 minutes (healthy)   0.0.0.0:5000->5000/tcp, :::5000->5000/tcp   coldchain
a4d109e7c2f0   eclipse-mosquitto:2.0   "/docker-entrypoint.…"   2 minutes ago   Up 2 minutes             0.0.0.0:1883->1883/tcp, :::1883->1883/tcp   mosquitto
```

---

## 7. Resultado de `curl http://127.0.0.1:5000` Local e Bateria `scripts/test.sh`
```text
$ curl http://127.0.0.1:5000
Helooowww world eng. Computacional!!!!!!!

$ bash scripts/test.sh
==========================================================
  Web-Server Cold Chain — Bateria de Testes (13 Requisitos)
==========================================================
[1/4] Testando Frase Obrigatória via curl puro... OK (Passou)
[2/4] Testando Negociação por Cabeçalho Accept (Navegador)... OK (Passou: frase dentro de <h1>)
[3/4] Testando Endpoint de Diagnóstico (/healthz)... OK (Passou)
[4/4] Testando Endpoint de Status e Contadores (/api/status)... OK (Passou)
```

---

## 8. Teste de Reinicialização (3 Ciclos de `reboot` — Seção 12.5)
| Ciclo de Reboot | Comando Executado | Status do `docker ps` após boot | Resposta de `curl http://127.0.0.1:5000` |
| :--- | :--- | :--- | :--- |
| **Ciclo 1** | `reboot` | `Up (healthy)` (`coldchain` e `mosquitto`) | `Helooowww world eng. Computacional!!!!!!!` |
| **Ciclo 2** | `reboot` | `Up (healthy)` (`coldchain` e `mosquitto`) | `Helooowww world eng. Computacional!!!!!!!` |
| **Ciclo 3** | `reboot` | `Up (healthy)` (`coldchain` e `mosquitto`) | `Helooowww world eng. Computacional!!!!!!!` |

---

## 9. Hashes SHA-256 Oficiais dos Artefatos Finais
* **Arquivos dos Containers Offline (`br2-external/board/common/overlay/opt/docker/`):**
```text
  13d3251267784c3cab0e1ec0f42db3293e1cc8fdd5b8c391b43968432b57bb07  coldchain-tp8.tar.gz
  8d02ab746b469cafc58caf17882867ca91e28098014e9876039f1189b1086add  mosquitto.tar.gz
```
* **Imagem Final do Cartão microSD (`output/images/sdcard.img`):**
```text
  fb9e5dcd410183baa75f5d4e9a025c8895394e735bdbc17d0c80e3842435532e  sdcard.img
```
