# 07 — Metodologia de Testes e Aceite

## Checklist de Validação
1. **Frase Obrigatória:** Validada com `grep -F "Helooowww world eng. Computacional!!!!!!!"` via curl e navegador.
2. **Containers:** `docker inspect` comprovando `ReadonlyRootfs=true`, `CapDrop=ALL`, `RestartPolicy=always`.
3. **Resiliência:** Simulação de interrupção de rede e reenvio de pacotes com verificação de não-duplicação de `seq`.
4. **Boot SLA:** Medição com `measure-boot.sh` em loop de 100 ms.
