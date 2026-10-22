SHELL := /bin/bash
export PATH := /usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
PROJECT_NAME := web-server-coldchain
BR2_EXTERNAL_DIR := $(CURDIR)/br2-external

.PHONY: help setup-env build-qemu build-rpi4 run-qemu test clean distclean docker-builder docker-build-qemu docker-build-rpi4

help:
	@echo "=========================================================="
	@echo "  Web-Server Cold Chain - Comandos de Automação"
	@echo "=========================================================="
	@echo "  make setup-env         - Cria o arquivo .env a partir do .env.example"
	@echo "  make build-qemu        - Compila a imagem para QEMU AArch64"
	@echo "  make run-qemu          - Inicia a emulação no QEMU com portas mapeadas"
	@echo "  make build-rpi4        - Compila a imagem final para Raspberry Pi 4"
	@echo "  make docker-build-qemu - Compila tudo dentro de container Docker isolado"
	@echo "  make docker-build-rpi4 - Compila tudo para RPi4 dentro de container Docker"
	@echo "  make test              - Roda os testes de conformidade dos 13 requisitos"
	@echo "  make clean             - Limpa os artefatos de compilação intermediários"
	@echo "  make distclean         - Remove todas as saídas e builds do Buildroot"

setup-env:
	@if [ ! -f .env ]; then \
		cp .env.example .env; \
		echo "[+] Arquivo .env criado a partir de .env.example"; \
	else \
		echo "[!] Arquivo .env já existe, mantido intacto."; \
	fi

build-qemu: setup-env
	@echo "[+] Iniciando build para QEMU AArch64..."
	@bash scripts/build.sh qemu

build-rpi4: setup-env
	@echo "[+] Iniciando build para Raspberry Pi 4..."
	@bash scripts/build.sh rpi4

docker-builder:
	@echo "[+] Construindo imagem de compilação isolada Docker..."
	@docker build -f Dockerfile.build -t webserver-builder .

docker-build-qemu: docker-builder setup-env
	@echo "[+] Compilando para QEMU em ambiente Docker 100% isolado..."
	@docker run --rm -it -v $(CURDIR):/workspace -v $(CURDIR)/dl:/dl -v $(CURDIR)/output:/workspace/output webserver-builder bash scripts/build.sh qemu

docker-build-rpi4: docker-builder setup-env
	@echo "[+] Compilando para Raspberry Pi 4 em ambiente Docker 100% isolado..."
	@docker run --rm -it -v $(CURDIR):/workspace -v $(CURDIR)/dl:/dl -v $(CURDIR)/output:/workspace/output webserver-builder bash scripts/build.sh rpi4

run-qemu:
	@echo "[+] Executando QEMU AArch64..."
	@bash scripts/run-qemu.sh

test:
	@echo "[+] Executando bateria de validação..."
	@bash scripts/test.sh

clean:
	@echo "[+] Limpando arquivos temporários..."
	@rm -rf output/build

distclean:
	@echo "[+] Limpeza profunda..."
	@rm -rf output buildroot
