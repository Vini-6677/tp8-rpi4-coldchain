#!/bin/bash
set -e

# Higieniza o PATH para eliminar pastas do Windows com espaços (como /mnt/c/Program Files)
export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

TARGET=${1:-"qemu"}
PROJECT_ROOT=$(pwd)

# Carrega variáveis do .env
if [ -f .env ]; then
  source .env
else
  echo "[!] Arquivo .env não encontrado. Rodando make setup-env..."
  cp .env.example .env
  source .env
fi

BUILDROOT_VERSION=${BUILDROOT_VERSION:-"2024.02.1"}

# Resolução dinâmica e autônoma do diretório do Buildroot (Portabilidade Total)
if [ -n "$BUILDROOT_DIR" ] && [ -d "$BUILDROOT_DIR" ]; then
  echo "[+] Usando Buildroot configurado em: $BUILDROOT_DIR"
elif [ -d "$HOME/buildroot-2026.05.1" ]; then
  BUILDROOT_DIR="$HOME/buildroot-2026.05.1"
  echo "[+] Usando Buildroot do host em: $BUILDROOT_DIR"
elif ls -d "$HOME"/buildroot* 1> /dev/null 2>&1; then
  DETECTED=$(ls -d "$HOME"/buildroot* 2>/dev/null | grep -v '\.tar' | head -n 1)
  if [ -n "$DETECTED" ] && [ -d "$DETECTED" ]; then
    BUILDROOT_DIR="$DETECTED"
    echo "[+] Usando Buildroot detectado no host em: $BUILDROOT_DIR"
  fi
fi

if [ -z "$BUILDROOT_DIR" ] || [ ! -d "$BUILDROOT_DIR" ]; then
  BUILDROOT_DIR="${PROJECT_ROOT}/buildroot"
  if [ ! -d "$BUILDROOT_DIR" ]; then
    echo "[!] Buildroot não encontrado em caminhos locais."
    echo "[+] Baixando automaticamente Buildroot LTS ${BUILDROOT_VERSION} oficial para desvincular de qualquer PC..."
    mkdir -p "${PROJECT_ROOT}/dl"
    TAR_FILE="${PROJECT_ROOT}/dl/buildroot-${BUILDROOT_VERSION}.tar.gz"
    if [ ! -f "$TAR_FILE" ]; then
      wget -c "https://buildroot.org/downloads/buildroot-${BUILDROOT_VERSION}.tar.gz" -O "$TAR_FILE" || \
      wget -c "https://sources.buildroot.net/buildroot/buildroot-${BUILDROOT_VERSION}.tar.gz" -O "$TAR_FILE"
    fi
    echo "[+] Extraindo Buildroot em ${BUILDROOT_DIR}..."
    tar -xzf "$TAR_FILE" -C "${PROJECT_ROOT}"
    mv "${PROJECT_ROOT}/buildroot-${BUILDROOT_VERSION}" "$BUILDROOT_DIR"
  fi
  echo "[+] Buildroot autônomo pronto em: $BUILDROOT_DIR"
fi

OUTPUT_DIR="${PROJECT_ROOT}/output"

echo "=========================================================="
echo "  Web-Server Cold Chain — Compilação ($TARGET)"
echo "=========================================================="

# 1. Gera o hash SHA-512 para o usuário coldadmin (Portável com openssl, mkpasswd ou python)
if command -v openssl >/dev/null 2>&1; then
  HASH=$(openssl passwd -6 "$COLDADMIN_PASSWORD")
elif command -v mkpasswd >/dev/null 2>&1; then
  HASH=$(mkpasswd -m sha-512 "$COLDADMIN_PASSWORD")
else
  HASH=$(python3 -c "import crypt; print(crypt.crypt('$COLDADMIN_PASSWORD', crypt.METHOD_SHA512))")
fi

sed "s|__COLDADMIN_HASH__|$HASH|g" br2-external/users_table.txt.tmpl > br2-external/users_table.txt
echo "[+] Tabela de usuários gerada com hash SHA-512 seguro."

# 2. Compila o binário Go estático para ARM64
echo "[+] Compilando binário estático Go para ARM64..."
mkdir -p coldchain-app/dist
(
  cd coldchain-app
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o dist/coldchain ./cmd/coldchain
)

# 3. Prepara a imagem local coldchain-tp8 em tarball offline
mkdir -p br2-external/board/common/overlay/opt/docker
TEMP_DIR=$(mktemp -d)
cp coldchain-app/dist/coldchain "$TEMP_DIR/"
cat << 'DOCKER' > "$TEMP_DIR/Dockerfile"
FROM scratch
COPY coldchain /app/coldchain
USER 10001:10001
EXPOSE 5000
ENTRYPOINT ["/app/coldchain"]
DOCKER

if which docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  echo "[+] Gerando imagem Docker offline do Coldchain App via Docker daemon..."
  docker build -t coldchain-tp8:1.0.0 "$TEMP_DIR"
  docker save coldchain-tp8:1.0.0 | gzip > br2-external/board/common/overlay/opt/docker/coldchain-tp8.tar.gz
  echo "[+] coldchain-tp8.tar.gz atualizado com sucesso."
elif [ -f "br2-external/board/common/overlay/opt/docker/coldchain-tp8.tar.gz" ]; then
  echo "[+] Mantendo imagem Docker pré-empacotada com Ledger ANVISA em br2-external/board/common/overlay/opt/docker/coldchain-tp8.tar.gz."
else
  echo "[!] Docker daemon do host não disponível. Criando pacote tarball..."
  tar -czf br2-external/board/common/overlay/opt/docker/coldchain-tp8.tar.gz -C "$TEMP_DIR" coldchain
fi
rm -rf "$TEMP_DIR"

# 4. Configuração e Compilação do Buildroot
if [ "$TARGET" = "rpi4" ]; then
  DEFCONFIG="tp8_raspberrypi4_defconfig"
else
  DEFCONFIG="tp8_qemu_defconfig"
fi

echo "[+] Aplicando configuração do Buildroot ($DEFCONFIG)..."
make -C "$BUILDROOT_DIR" BR2_EXTERNAL="${PROJECT_ROOT}/br2-external" O="$OUTPUT_DIR" "$DEFCONFIG"

echo "[+] Iniciando compilação do Buildroot com $(nproc) núcleos..."
make -C "$OUTPUT_DIR" -j$(nproc)

echo "=========================================================="
echo "  Build concluído com sucesso!"
echo "  Imagens geradas em: ${OUTPUT_DIR}/images/"
echo "=========================================================="
