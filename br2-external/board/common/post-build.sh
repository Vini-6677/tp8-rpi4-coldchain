#!/bin/sh
set -e
TARGET_DIR=$1

# Renderiza /etc/issue a partir do template
if [ -f "${TARGET_DIR}/etc/issue.tmpl" ]; then
  cp "${TARGET_DIR}/etc/issue.tmpl" "${TARGET_DIR}/etc/issue"
fi

# Garante link dinâmico para o resolv.conf em /run
rm -f "${TARGET_DIR}/etc/resolv.conf"
ln -sf /run/resolv.conf "${TARGET_DIR}/etc/resolv.conf"

# Cria ponto de montagem /data
mkdir -p "${TARGET_DIR}/data"
mkdir -p "${TARGET_DIR}/opt/docker"
