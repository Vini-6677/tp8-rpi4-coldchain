# Firmware ESP32-S3 — ColdChain Pharma RT (Telemetria Real MQTT)

Firmware de produção para **ESP32-S3** integrado ao gateway Raspberry Pi 4 (`coldchain-rpi4.local`).

## O que este firmware faz

1. Conecta automaticamente na rede Wi-Fi **`S23`** (senha **`Vini1234`**).
2. Localiza automaticamente o **Raspberry Pi 4** na rede do hotspot `S23` em 4 níveis (sem precisar editar IP):
   - **1º** IP manual (caso você queira apontar para um IP específico no teste);
   - **2º** mDNS (`coldchain-rpi4.local` via Avahi da Raspberry Pi);
   - **3º** Último IP válido salvo em NVS (reconexão imediata após reinício);
   - **4º** Varredura automática da sub-rede `/24` validando `GET /healthz` na porta `5000` + broker MQTT na porta `1883`.
3. Autentica no Eclipse Mosquitto da Raspberry Pi 4 com as credenciais já provisionadas na imagem:
   - **Usuário / Client ID:** `esp32-sensor-01`
   - **Senha MQTT:** `SensorSecret2026!`
   - **Tópico de Telemetria:** `coldchain/esp32-sensor-01/telemetry`
   - **Tópico de Status (LWT):** `coldchain/esp32-sensor-01/status` (`online` / `offline`)
   - **Tópico de ACK:** `coldchain/esp32-sensor-01/ack`
4. Envia leituras reais a cada **3 segundos** com **buffer circular offline** de 50 amostras (se o Wi-Fi oscilar, nenhuma leitura é perdida).
5. No servidor da Raspberry Pi 4, a chegada da primeira leitura real cria/atualiza automaticamente o sensor **`ESP32-SENSOR-01`** com o selo **`HARDWARE REAL • MQTT`** (protegido contra sobrescrita pelo simulador de mocks) e registra cada leitura no SQLite + **Ledger Criptográfico ECDSA-P256**.

---

## Como selecionar o seu modelo de sensor de temperatura

Abra `esp32s3_coldchain.ino` e ajuste **apenas a linha `#define SENSOR_MODEL`** no topo do arquivo:

| Valor de `SENSOR_MODEL` | Sensor físico | Pinagem padrão no ESP32-S3 | Bibliotecas extras no Arduino IDE |
|---|---|---|---|
| `SENSOR_TYPE_DS18B20` *(padrão)* | Sonda DS18B20 (OneWire) | `DATA` -> **GPIO 4** (com resistor pull-up 4.7kΩ para 3.3V), `VCC` -> 3.3V, `GND` -> GND | `OneWire` + `DallasTemperature` |
| `SENSOR_TYPE_DHT22` | DHT22 (Temp + Umidade) | `DATA` -> **GPIO 4**, `VCC` -> 3.3V, `GND` -> GND | `DHT sensor library` + `Adafruit Unified Sensor` |
| `SENSOR_TYPE_DHT11` | DHT11 (Temp + Umidade) | `DATA` -> **GPIO 4**, `VCC` -> 3.3V, `GND` -> GND | `DHT sensor library` + `Adafruit Unified Sensor` |
| `SENSOR_TYPE_BME280` | BME280 (I2C Temp + Umidade) | `SDA` -> **GPIO 8**, `SCL` -> **GPIO 9**, `VCC` -> 3.3V, `GND` -> GND | `Adafruit BME280 Library` + `Adafruit Unified Sensor` |
| `SENSOR_TYPE_NTC_ANALOG` | Termistor NTC 10kΩ (B=3950) | Divisor 10kΩ -> **GPIO 4** (ADC) | *(Nenhuma)* |
| `SENSOR_TYPE_S3_INTERNAL` | Sensor interno do próprio ESP32-S3 | *(Nenhum fio externo — teste imediato!)* | *(Nenhuma)* |

> **Biblioteca obrigatória em todos os modos:** instale **`PubSubClient`** (por Nick O'Leary) no Gerenciador de Bibliotecas do Arduino IDE.

---

## Configuração na Arduino IDE 2.x

1. **Placa:** `ESP32S3 Dev Module` (pacote *esp32 by Espressif Systems*).
2. **USB CDC On Boot:** `Enabled` (para ver os logs na porta Serial USB nativa do ESP32-S3 a `115200` baud).
3. Instale **`PubSubClient`** + a biblioteca do seu sensor (tabela acima).
4. Clique em **Upload** e abra o **Monitor Serial (`115200`)** para acompanhar a conexão no Wi-Fi `S23`, a descoberta automática da Raspberry Pi 4 e o recebimento dos `ACK` do Ledger.

## Dica para a apresentação no Hotspot do S23

- No Samsung S23, em **Roteador Wi-Fi**, use a banda **2,4 GHz** com segurança **WPA2-Personal** (o ESP32-S3 e o Wi-Fi da Raspberry Pi 4 conectam de forma mais rápida e estável em 2,4 GHz WPA2).
- Assim que a Raspberry Pi 4 e o ESP32-S3 estiverem na rede `S23`, qualquer celular ou notebook conectado ao `S23` pode abrir:
  - `http://coldchain-rpi4.local:5000` (ou `http://<IP_DA_RASP>:5000`).
