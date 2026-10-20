#!/bin/bash
echo "=========================================================="
echo "  Web-Server Cold Chain — QEMU AArch64 (virt)"
echo "  Portas mapeadas no Host:"
echo "    - HTTP Web:    http://localhost:5000"
echo "    - MQTT Broker: localhost:1883"
echo "    - SSH:         localhost:2222 (coldadmin)"
echo "  Pressione 'Ctrl+A' e depois 'X' para sair do QEMU"
echo "=========================================================="

QEMU_BIN=$(which qemu-system-aarch64)
if [ -z "$QEMU_BIN" ]; then
  echo "[-] qemu-system-aarch64 não encontrado!"
  exit 1
fi

IMAGE_KERNEL="output/images/Image"
IMAGE_ROOTFS="output/images/rootfs.ext4"
IMAGE_DATA="output/images/data.ext4"

if [ ! -f "$IMAGE_KERNEL" ] || [ ! -f "$IMAGE_ROOTFS" ]; then
  echo "[!] Imagens não encontradas em output/images/. Rode 'make build-qemu' primeiro."
  exit 1
fi

if [ ! -f "$IMAGE_DATA" ]; then
  echo "[+] Gerando partição /data ext4 (data.ext4)..."
  mkfs.ext4 -L data -F "$IMAGE_DATA" 512M >/dev/null 2>&1
fi

$QEMU_BIN   -M virt   -cpu cortex-a72   -smp 4   -m 2048   -kernel "$IMAGE_KERNEL"   -append "root=/dev/vda rootwait ro console=ttyAMA0 quiet cgroup_enable=memory cgroup_memory=1"   -drive file="$IMAGE_ROOTFS",format=raw,if=virtio   -drive file="$IMAGE_DATA",format=raw,if=virtio   -netdev user,id=net0,hostfwd=tcp::5000-:5000,hostfwd=tcp::1883-:1883,hostfwd=tcp::2222-:22   -device virtio-net-pci,netdev=net0   -nographic
