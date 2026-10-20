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

# Bloqueio de senha do root (!) conforme Requisito 3 e Seção 7
if [ -f "${TARGET_DIR}/etc/shadow" ]; then
  sed -i 's|^root:[^:]*:|root:!:|' "${TARGET_DIR}/etc/shadow"
fi

mkdir -p "${TARGET_DIR}/sys/fs/cgroup"

# Garante home dir de coldadmin
mkdir -p "${TARGET_DIR}/home/coldadmin"
chmod 0750 "${TARGET_DIR}/home/coldadmin"

# Garante hash SHA-512 exato do coldadmin em /etc/shadow
if [ -f "${TARGET_DIR}/etc/shadow" ]; then
  sed -i 's|^coldadmin:.*|coldadmin:$6$I07/L5VA68JPcX/m$abdKjucq2wH4ABkAsqvalL7GD8FatlceJvgVLqU3oG/cMX7uMXwz2IOLIAaREsloU3KsaRpsDTC1okM4gJh/10:19000:0:99999:7:::|' "${TARGET_DIR}/etc/shadow"
fi

# Permissões estritas do sudo
if [ -f "${TARGET_DIR}/usr/bin/sudo" ]; then
  chmod 4755 "${TARGET_DIR}/usr/bin/sudo"
fi
if [ -f "${TARGET_DIR}/etc/sudoers" ]; then
  chmod 0440 "${TARGET_DIR}/etc/sudoers"
fi
if [ -d "${TARGET_DIR}/etc/sudoers.d" ]; then
  chmod 0750 "${TARGET_DIR}/etc/sudoers.d"
  chmod 0440 "${TARGET_DIR}/etc/sudoers.d"/* 2>/dev/null || true
fi

# Remove S60dockerd duplicado (usamos S40docker deterministico)
rm -f "${TARGET_DIR}/etc/init.d/S60dockerd"
