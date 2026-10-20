#!/bin/bash
IMAGE=${1:-"output/images/sdcard.img"}
DEVICE=$2

if [ -z "$DEVICE" ] || [ ! -b "$DEVICE" ]; then
  echo "Uso: sudo $0 <imagem.img> <dispositivo_sd (ex: /dev/sdb)>"
  exit 1
fi

echo "ATENÇÃO: Isso apagará todos os dados em $DEVICE!"
read -p "Deseja continuar? [y/N] " CONFIRM
if [ "$CONFIRM" = "y" ] || [ "$CONFIRM" = "Y" ]; then
  dd if="$IMAGE" of="$DEVICE" bs=4M status=progress conv=fsync
  echo "[✓] Cartão SD gravado com sucesso!"
fi
