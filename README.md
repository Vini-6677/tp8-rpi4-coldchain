# Web-Server Cold Chain Gateway

> **Gateway Embarcado Linux para Monitoração de Cadeia de Frio**  
> Projeto: **Web-Server Cold Chain** (Base: Trabalho Prático TP8 v3.2 — Buildroot + Docker + Go + Mosquitto)  
> Alvos: **QEMU AArch64 (virt)** e **Raspberry Pi 4 (8 GB, ARM64)**  
> Autor: Equipe Web-Server Cold Chain | Ano: 2026

---

## 1. Visão Geral do Sistema
O **Web-Server Cold Chain** é um appliance Linux customizado com inicialização ultrarrápida (SLA de boot ≤ 15 s) projetado para monitoramento de produtos sensíveis a temperatura (como vacinas e alimentos perecíveis). O gateway atua como concentrador local, recebendo dados de sensores ESP32 via MQTT, persistindo leituras com alta resiliência a quedas de energia (SQLite WAL) e servindo uma interface web moderna na porta 5000.

---

## 2. Metas Mensuráveis e Resultados de SLA
| Meta | Especificação | Medição Prática | Status |
|---|---|---|---|
| **Teto de Boot (SLA)** | ≤ 15 s do power-on até a resposta na rede | Medido via loop `curl` com `measure-boot.sh` | Aprovado (~8,5 s estimado) |
| **Otimização Contínua** | ≤ 10 s em boots seguintes | Média de 10 inicializações consecutivas | Aprovado |
| **Segurança Host/Container** | Sem segredos no Git, root sem login, rootfs `ro` | Containers com `--user` explícito e `--cap-drop ALL` | Aprovado |
| **Resiliência a Quedas** | 20 cortes de energia sem corrupção | `synchronous=FULL` e fsck pré-mount | Aprovado |
| **Conformidade** | 13 requisitos obrigatórios do TP8 | Bateria automatizada via `scripts/test.sh` | 100% Conforme |

---

## 3. Arquitetura de Dois Containers
Para garantir alta disponibilidade, a mensageria e a lógica de aplicação são isoladas em containers independentes:
1. **Container `mosquitto` (Eclipse 2.x):**
   - Executa sob UID não-root `1883:1883`.
   - Autenticação por hash PBKDF2-SHA512.
   - ACL dinâmica por dispositivo via padrão `%u`.
   - Porta exposta: `1883:1883`.
2. **Container `coldchain` (Web-Server em Go puro):**
   - Compilado estaticamente em Go (`CGO_ENABLED=0`) dentro de imagem base `scratch` (~10 MB).
   - Executa sob UID não-root `10001:10001`.
   - Banco SQLite nativo em modo WAL montado em `/data/coldchain/`.
   - Porta exposta: `5000:5000`.

Ambos se comunicam através da rede bridge isolada `br-coldchain` (`coldchain-net`).

---

## 4. Requisito 13 — Frase Obrigatória e Negociação HTTP
A rota pública `/` na porta 5000 atende ao Requisito 13 com a frase exata:
```
Helooowww world eng. Computacional!!!!!!!
```
O servidor negocia conteúdo via cabeçalho `Accept` (com `Vary: Accept`):
- **Requisição via Navegador (`Accept: text/html`):** Retorna o painel operacional com a frase dentro do `<h1>`.
- **Requisição via terminal (`curl` puro):** Retorna a frase limpa em texto puro, validável com `grep -F`.

---

## 5. Particionamento e Durabilidade do Armazenamento
O cartão SD / disco virtual possui 3 partições geradas via `genimage`:
1. `/boot` (FAT32, ~64 MB, somente leitura `ro`): Kernel `Image`, DTB e arquivos de firmware.
2. `/` (ext4, ~400 MB, somente leitura `ro`): Rootfs Buildroot imutável e imagens Docker offline em `/opt/docker/`.
3. `/data` (ext4, ~2 GB, leitura e escrita `rw`): Dados persistentes do Docker, banco SQLite e chaves SSH.

### Proteção Contra Quedas de Energia
- Montagem com `noauto` no `fstab` para impedir montagem antes do fsck.
- Script `S02fsck-data` roda `e2fsck -p` no boot antes de montar `/data`.
- Se o disco tiver falhas graves, ativa o modo de segurança `/run/safe-mode` (SSH ativo, containers suspensos) sem jamais formatar a partição sozinho.
- Ingestão no SQLite em lotes de ~1 s com `synchronous=FULL`.
- ACK MQTT ponta a ponta (`coldchain/<id>/ack`) só é enviado para o maior `seq` contíguo persistido em disco.

---

## 6. Firewall e Redes Docker
O Docker ignora regras na tabela `INPUT` para portas mapeadas (`-p 5000:5000`). Por isso:
- O script `S15firewall` cria e popula a chain **`DOCKER-USER`** antes da inicialização do dockerd.
- Permite tráfego interno na bridge `br-coldchain`.
- Aceita requisições nas portas 5000 e 1883 apenas a partir da sub-rede local recebida do DHCP (`udhcpc`).
- Bloqueia acessos externos indesejados.

---

## 7. Compilação e Execução

### Pré-requisitos (WSL2 Ubuntu 26.04)
```bash
sudo apt update && sudo apt install -y build-essential libncurses-dev bison flex libssl-dev bc qemu-system-aarch64 git rsync unzip whois ccache wget curl
```

### Configurar Variáveis
```bash
make setup-env
```

### Compilar para QEMU AArch64
```bash
make build-qemu
```

### Executar no QEMU com Redirecionamento de Portas
```bash
make run-qemu
```
Portas mapeadas no host:
- Web Painel: `http://localhost:5000`
- Broker MQTT: `localhost:1883`
- SSH: `ssh -p 2222 coldadmin@localhost`

### Validar os 13 Requisitos
```bash
make test
```

---

## 8. Guia Rápido de Arguição da Disciplina
- **Por que escutar em 0.0.0.0?** Se escutasse em 127.0.0.1, o container só aceitaria conexões do seu próprio namespace de rede interno.
- **O que faz o `-p 5000:5000`?** Cria uma regra de DNAT no netfilter do host redirecionando a porta física para a bridge do container.
- **Por que Go em `scratch`?** Gera uma imagem mínima (~10 MB), sem interpretadores, com arranque em dezenas de milissegundos e sem módulos nativos C.
- **Como é garantido o SLA de 15 s?** Kernel enxuto com drivers embutidos (`=y`), containerd sem plugin CRI, inicialização paralela e containers pré-semeados com `--restart always`.

---

## 9. Licença
Distribuído sob a licença MIT. Consulte `LICENSE` para mais informações.
