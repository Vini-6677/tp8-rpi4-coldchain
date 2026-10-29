# Web-Server Cold Chain Gateway — TP Aula 8 (Buildroot + Docker na Raspberry Pi 4)

> **Gateway Embarcado Linux ARM64 para Monitoramento Farmacêutico de Cadeia de Frio (ANVISA RDC 430/2020 & FDA 21 CFR Part 11)**  
> **Disciplina:** Sistemas Embarcados — Engenharia de Computação (Prof. Sergio Schina)  
> **Versão do Buildroot:** Buildroot `2026.05.1` (compatível com o Guia Técnico TP8 Buildroot `2026.08`)  
> **Alvos Suportados:** **Raspberry Pi 4 Model B (ARM64 / Cortex-A72)** e **QEMU AArch64 (`virt`)**  
> **Tag Oficial de Entrega:** `tp8-final`

---

## 1. Visão Geral e Arquitetura da Solução

Ao ligar a **Raspberry Pi 4**, o sistema operacional Linux gerado pelo **Buildroot** inicia a partir do cartão microSD, monta a partição de dados persistentes `/data` (`ext4`), inicializa o **Docker Engine** automaticamente em segundo plano, carrega localmente as imagens de containers **ARM64** pré-embarcadas em `/opt/docker/` (100% offline, sem depender da Internet) e publica a aplicação Web na porta **`:5000`** e o broker MQTT na porta **`:1883`**.

### Caminho de uma Requisição HTTP (Porta 5000)
```text
Notebook / Celular (Mesma Rede LAN ou Hotspot Wi-Fi S23)
   │  http://IP_DA_RASPBERRY:5000  (ou http://coldchain-rpi4.local:5000)
   ▼
Interface de Rede da Raspberry Pi 4 (eth0 / wlan0 :5000)
   │  Regras Netfilter/DNAT (-p 5000:5000) + Firewall DOCKER-USER (S15firewall)
   ▼
Docker Engine (bridge br-coldchain)
   ├──► Container `coldchain` (Go 1.22 estático em scratch, UID 10001, :5000)
   │       ├── `curl http://IP:5000` ──► "Helooowww world eng. Computacional!!!!!!!"
   │       └── Navegador Web         ──► Painel Sinótico ColdChain Pharma RT + Ledger ECDSA
   └──► Container `mosquitto` (Eclipse Mosquitto 2.0, UID 1883, :1883)
           └── Recebe telemetria MQTT dos sensores ESP32 com autenticação PBKDF2-SHA512
```

---

## 2. Credenciais e Usuários do Sistema

### 2.1. Usuários do Linux Embarcado (SSH / Console HDMI / Serial)
Configurados via `BR2_ROOTFS_USERS_TABLES` no arquivo [`br2-external/users_table.txt`](br2-external/users_table.txt):
| Usuário | Senha | Grupos | Finalidade |
| :--- | :--- | :--- | :--- |
| **`aluno`** | `senha123` | `aluno`, `docker`, `users` | Usuário oficial do guia TP Aula 8 com permissão direta ao Docker CLI. |
| **`coldadmin`** | `ColdChain2026` | `coldadmin`, `docker`, `wheel` | Usuário administrativo de manutenção e auditoria do gateway. |
| **`root`** | *(bloqueado)* | `root` | Login direto de root desabilitado por segurança (`passwd -l root`). |

### 2.2. Perfis de Acesso da Aplicação Web (`http://coldchain-rpi4.local:5000`)
| Usuário Web | Senha Inicial | Papel (RBAC) | Permissões no Sistema |
| :--- | :--- | :--- | :--- |
| **`admin`** | `admin123` | Administrador de TI / Sistema | Gestão de contas de usuários, bloqueio/desbloqueio, reset de chaves criptográficas e visualização de logs. |
| **`rt`** | `rt123` | Farmacêutico Responsável Técnico (RT) | Criação/descomissionamento de câmaras e sensores, julgamento de excursões térmicas, emissão de dossiês PDF/CSV/JSON assinados com chave criptográfica ECDSA P-256. |
| **`enfermagem`** | `enf123` | Operador de Enfermagem | Monitoramento do painel sinótico, cadastro/retirada FEFO e transferência térmica de lotes. |

---

## 3. Estrutura do Repositório Git (`BR2_EXTERNAL`)

```text
Web-Server/
├── Makefile                        # Automação completa de build, testes e execução QEMU
├── README.md                       # Documentação técnica principal e guia de reprodutibilidade
├── app/                            # Aplicação Web e Motor Farmacêutico em Go + Mosquitto
│   ├── Dockerfile                  # Build multi-stage ARM64 (golang:1.22-alpine -> scratch)
│   ├── main.go                     # Servidor HTTP :5000, cliente MQTT, SQLite WAL e gerador PDF
│   ├── pharma.go                   # Regras ANVISA RDC 430/2020, FDA 21 CFR Part 11 e Ledger ECDSA
│   ├── static/                     # Frontend Vanilla JS/CSS de alta performance
│   └── mosquitto/                  # Configuração Mosquitto 2.0, ACL e hashes PBKDF2-SHA512
├── br2-external/                   # Árvore BR2_EXTERNAL isolada do código-fonte do Buildroot
│   ├── external.desc               # Identificador BR2_EXTERNAL (COLDCHAIN)
│   ├── Config.in & external.mk     # Descritores de pacotes customizados
│   ├── users_table.txt             # Definição declarativa dos usuários (aluno e coldadmin)
│   ├── device_table.txt            # Permissões estáticas de dispositivos e diretórios
│   ├── configs/
│   │   ├── rpi4_docker_defconfig   # Defconfig da Raspberry Pi 4 (64 bits + Docker + Wi-Fi)
│   │   ├── tp8_rpi4_defconfig      # Alias oficial exigido pelo Checklist do Guia TP8
│   │   └── qemu_aarch64_docker_defconfig # Defconfig para validação rápida em QEMU ARM64
│   └── board/
│       ├── common/
│       │   ├── linux-docker.fragment # Fragmento do Kernel (Namespaces, Cgroups v2, OverlayFS, Veth, Netfilter)
│       │   ├── post-build.sh       # Script pós-build (fstab, permissões, descompressão de módulos .ko)
│       │   └── overlay/            # Rootfs Overlay copiado sobre o sistema alvo
│       │       ├── etc/issue       # Banner customizado de pré-login
│       │       ├── etc/fstab       # Montagem proc, sysfs, cgroup2 e partições RO/RW
│       │       ├── etc/network/interfaces # Configuração DHCP (lo, eth0, wlan0)
│       │       ├── etc/wpa_supplicant.conf # Conexão automática ao Hotspot Wi-Fi "S23" (Vini1234)
│       │       ├── etc/init.d/     # Scripts SysV init paralelizados (S02fsck, S15firewall, S38wifi, S40network, S99coldchain)
│       │       └── opt/docker/     # Imagens Docker ARM64 exportadas para boot 100% offline
│       └── rpi4/
│           ├── genimage-rpi4.cfg.in # Layout das 3 partições do microSD (boot.vfat, rootfs.ext4, data.ext4)
│           └── post-image.sh       # Geração final do sdcard.img
├── docs/                           # Documentação arquitetural detalhada e PDF de Arquitetura v3.7
├── evidence/
│   └── EVIDENCIAS_TP8.md           # Consolidação das 10 evidências obrigatórias da Seção 12.6
├── esp32-sim/                      # Simulador de sensores de temperatura/porta via MQTT
└── scripts/                        # Scripts de automação (build.sh, run-qemu.sh, test.sh, measure-boot.sh)
```

---

## 4. Hashes SHA-256 dos Artefatos Finais (Seção 12.6)

| Artefato | Caminho | Hash SHA-256 |
| :--- | :--- | :--- |
| **Container ARM64 Offline (`coldchain-tp8.tar.gz`)** | `br2-external/board/common/overlay/opt/docker/coldchain-tp8.tar.gz` | `13d3251267784c3cab0e1ec0f42db3293e1cc8fdd5b8c391b43968432b57bb07` |
| **Container ARM64 Offline (`mosquitto.tar.gz`)** | `br2-external/board/common/overlay/opt/docker/mosquitto.tar.gz` | `8d02ab746b469cafc58caf17882867ca91e28098014e9876039f1189b1086add` |
| **Imagem Final microSD** | `output/images/sdcard.img` (`961 MB`) | `fb9e5dcd410183baa75f5d4e9a025c8895394e735bdbc17d0c80e3842435532e` |

---

## 5. Como Compilar a Imagem do Zero (Reprodutibilidade — Etapa 10)

### 5.1. Instalar dependências no Host Linux (Ubuntu 22.04 / 24.04)
```bash
sudo apt update
sudo apt install -y build-essential git wget cpio unzip rsync file bc bison flex libssl-dev libncurses-dev python3 qemu-system-aarch64 docker.io
```

### 5.2. Carregar a `defconfig` da Raspberry Pi 4 e Compilar
```bash
# Carrega a defconfig usando a árvore BR2_EXTERNAL
make -C buildroot O=$(pwd)/output BR2_EXTERNAL=$(pwd)/br2-external rpi4_docker_defconfig

# Compila o Kernel, rootfs e gera output/images/sdcard.img
make -C output -j$(nproc)
```
Ou utilizando o atalho automatizado do projeto:
```bash
bash scripts/build.sh rpi4
```

---

## 6. Como Gravar o Cartão microSD e Conectar na Raspberry Pi 4 (Etapa 11)

### Opção A — Pelo Raspberry Pi Imager (Windows / Linux / macOS)
1. Abra o **Raspberry Pi Imager**.
2. Em **Sistema Operacional (OS)**, role até o final e clique em **Use custom (Usar imagem personalizada)**.
3. Selecione o arquivo `sdcard.img` (`SHA-256: fb9e5dcd410183baa75f5d4e9a025c8895394e735bdbc17d0c80e3842435532e`).
4. Selecione o cartão microSD e clique em **Write / Gravar**.

### Opção B — Pelo Terminal Linux (`dd`)
```bash
lsblk -o NAME,SIZE,MODEL,TYPE,MOUNTPOINTS
sudo dd if=output/images/sdcard.img of=/dev/sdX bs=4M status=progress conv=fsync
sync
```
*(Substitua `/dev/sdX` pelo dispositivo real do seu cartão microSD identificado no `lsblk`).*

### Como Conectar na Raspberry Pi 4 (Cabo RJ45 ou Wi-Fi Roteado do Celular `S23`)
* **Via Wi-Fi do Celular:** Ligue o Roteador Wi-Fi (Hotspot) do celular na banda **2.4 GHz (WPA2)** com nome **`S23`** e senha **`Vini1234`**. A Raspberry Pi 4 conecta sozinha no boot via [`S38wifi`](br2-external/board/common/overlay/etc/init.d/S38wifi) e mostra o IP na tela HDMI!
* **Acesso no Navegador (PC ou Celular na mesma rede):**
  * Pelo nome mDNS: `http://coldchain-rpi4.local:5000`
  * Pelo endereço IP exibido no monitor HDMI: `http://IP_DA_RASPBERRY:5000`

---

## 7. Como Testar e Validar (Etapa 12)

### 7.1. Teste Local ou Remoto via `curl` (Frase Obrigatória)
```bash
curl http://127.0.0.1:5000
# Saída exata:
# Helooowww world eng. Computacional!!!!!!!
```

### 7.2. Bateria Automatizada dos Requisitos (`scripts/test.sh`)
```bash
bash scripts/test.sh
```
Valida automaticamente:
1. Resposta exata `"Helooowww world eng. Computacional!!!!!!!"` via `curl` puro.
2. Negociação por cabeçalho `Accept: text/html` retornando o painel completo com a frase dentro de `<h1>`.
3. Diagnóstico de saúde `/healthz` (`HTTP 200`).
4. Telemetria e persistência SQLite em `/api/status`.

---

## 8. Problemas Encontrados e Soluções Adotadas (Seção 12.7)

1. **Atraso no boot (`udhcpc: broadcasting discover`) quando ligada sem cabo RJ45:**
   * **Causa:** O script de rede padrão executava o cliente DHCP (`udhcpc`) de forma síncrona na interface `eth0`, bloqueando o boot por ~15 segundos quando apenas o Wi-Fi estava em uso.
   * **Solução:** Criamos os scripts [`S40network`](br2-external/board/common/overlay/etc/init.d/S40network) e [`S38wifi`](br2-external/board/common/overlay/etc/init.d/S38wifi) executando o `udhcpc` em **segundo plano (`&` / assíncrono)**, reduzindo o tempo de bloqueio de rede para `< 0,01s`.
2. **Aviso `Mounting cgroupfs2: already mounted or mount point busy FAIL`:**
   * **Causa:** O `/etc/fstab` montava `cgroup2` sem antes montar `sysfs` em `/sys`, e o script `S30cgroupfs2` tentava montar novamente sem checar `/proc/mounts`.
   * **Solução:** Adicionamos `proc /proc` e `sysfs /sys` no topo do [`/etc/fstab`](br2-external/board/common/overlay/etc/fstab) e tornamos o [`S30cgroupfs2`](br2-external/board/common/overlay/etc/init.d/S30cgroupfs2) idempotente.
3. **Submódulos Wi-Fi Broadcom BCM43455 no Kernel Linux 6.12 (`brcmfmac`):**
   * **Causa:** No Kernel 6.12+, os módulos são gerados como `.ko.xz` e o driver `brcmfmac` foi dividido em submódulos de fornecedor (`brcmfmac-wcc`, `brcmfmac-cyw`, `brcmfmac-bca`).
   * **Solução:** O [`post-build.sh`](br2-external/board/common/post-build.sh) descompacta todos os `.ko.xz` e regenera o `depmod -a`, garantindo carregamento imediato pelo `modprobe` do BusyBox.

---

## 9. Checklist Final de Entrega (Página 21 do Guia TP8)

- [x] Buildroot identificado na documentação (`README.md` e `docs/`).
- [x] `tp8_rpi4_defconfig` e `rpi4_docker_defconfig` versionadas em `br2-external/configs/`.
- [x] `BR2_EXTERNAL` versionado (`br2-external/`).
- [x] `users_table.txt` versionado (`br2-external/users_table.txt`).
- [x] `rootfs-overlay` versionado (`br2-external/board/common/overlay/`).
- [x] `/etc/issue` e `/etc/network/interfaces` presentes no overlay.
- [x] Docker Engine e Docker CLI habilitados.
- [x] Kernel Linux validado para containers (`linux-docker.fragment`).
- [x] Código da aplicação e `Dockerfile` presentes em `app/`.
- [x] Container construído para `linux/arm64`.
- [x] Pacotes `.tar.gz` das imagens Docker incluídos no overlay (`/opt/docker/`).
- [x] Script `S99coldchain` executável (`chmod +x`).
- [x] `sdcard.img` compilado e hash SHA-256 documentado.
- [x] Login (`aluno` / `coldadmin`), hostname (`coldchain-rpi4`) e rede validados.
- [x] Docker e containers iniciam automaticamente no boot (validado com 3 ciclos de `reboot`).
- [x] Página acessível pela rede na porta `5000` exibindo `"Helooowww world eng. Computacional!!!!!!!"`.
- [x] Evidências organizadas em [`evidence/EVIDENCIAS_TP8.md`](evidence/EVIDENCIAS_TP8.md) e [`docs/`](docs/).
- [x] `README.md` completo.
- [x] Tag `tp8-final` criada.
