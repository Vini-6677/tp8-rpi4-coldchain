# 04 — Orçamento de Boot e SLA <= 15s

## Orçamento Detalhado
1. **Firmware RPi4 (~2,8 s):** `boot_delay=0`, `disable_splash=1`, `initial_turbo=30`, `dtoverlay=disable-bt`.
2. **Kernel Linux (~1,4 s):** `Image` sem compressão, sem initramfs, drivers Docker em `=y`, `quiet loglevel=3`.
3. **Init e Montagens (~0,7 s):** `mdev`, `e2fsck -p` apenas em `/data`, DHCP em segundo plano (`udhcpc -b`).
4. **Runtime Docker (~2,3 s):** `containerd` isolado sem o plugin CRI (`io.containerd.grpc.v1.cri`).
5. **Containers (~1,3 s):** Mosquitto e App Go sobem em paralelo pelo restart policy.

**Total Estimado:** ~8,5 s (com folga de ~6,5 s para o teto de 15 s).
