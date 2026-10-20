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

BUILDROOT_DIR=${BUILDROOT_DIR:-"/home/vini/buildroot-2026.05.1"}
OUTPUT_DIR="${PROJECT_ROOT}/output"

echo "=========================================================="
echo "  Web-Server Cold Chain — Compilação ($TARGET)"
echo "=========================================================="

# 1. Gera o hash SHA-512 para o usuário coldadmin
HASH=$(mkpasswd -m sha-512 "$COLDADMIN_PASSWORD")
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
echo "[+] Gerando imagem Docker offline do Coldchain App..."
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
  docker build -t coldchain-tp8:1.0.0 "$TEMP_DIR"
  docker save coldchain-tp8:1.0.0 | gzip > br2-external/board/common/overlay/opt/docker/coldchain-tp8.tar.gz
  echo "[+] coldchain-tp8.tar.gz gerado com sucesso."
else
  echo "[!] Docker daemon do host não disponível. Criando pacote tarball para carga..."
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
