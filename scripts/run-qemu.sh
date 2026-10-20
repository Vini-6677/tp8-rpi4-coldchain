#!/bin/bash
echo "=========================================================="
echo "  Web-Server Cold Chain — QEMU AArch64 (virt)"
echo "  Portas mapeadas no Host:"
echo "    - HTTP Web:    http://localhost:5000"
echo "    - MQTT Broker: localhost:1883"
echo "    - SSH:         localhost:2222 (coldadmin)"
echo "=========================================================="

QEMU_BIN=$(which qemu-system-aarch64)
if [ -z "$QEMU_BIN" ]; then
  echo "[-] qemu-system-aarch64 não encontrado!"
  exit 1
fi

IMAGE_KERNEL=${1:-"output/images/Image"}
IMAGE_DISK=${2:-"output/images/disk.img"}

if [ ! -f "$IMAGE_KERNEL" ]; then
  echo "[!] Imagem de kernel $IMAGE_KERNEL não encontrada. Rode 'make build-qemu' primeiro."
  exit 1
fi

$QEMU_BIN \
  -M virt \
  -cpu cortex-a72 \
  -smp 4 \
  -m 2048 \
  -kernel "$IMAGE_KERNEL" \
  -append "root=/dev/vda1 rootwait rw console=ttyAMA0 quiet cgroup_enable=memory cgroup_memory=1" \
  -drive file="$IMAGE_DISK",format=raw,if=virtio \
  -netdev user,id=net0,hostfwd=tcp::5000-:5000,hostfwd=tcp::1883-:1883,hostfwd=tcp::2222-:22 \
  -device virtio-net-pci,netdev=net0 \
  -nographic
