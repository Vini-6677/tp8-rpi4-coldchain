/*
 * ============================================================================
 *  ColdChain Pharma RT — Firmware ESP32-S3 (Telemetria Real MQTT / ANVISA)
 *  Placa alvo : ESP32-S3 (DevKitC-1 / N16R8 / SuperMini / compatíveis)
 *  Rede Wi-Fi : SSID "S23" | Senha "Vini1234"
 *  Broker MQTT: Descoberta automática do Raspberry Pi 4 na rede S23
 *               (1º IP manual opcional -> 2º mDNS coldchain-rpi4.local ->
 *                3º IP salvo em NVS -> 4º varredura rápida HTTP /healthz)
 *  Credenciais: esp32-sensor-01 / SensorSecret2026! (Mosquitto ACL)
 *  Tópicos    :
 *    PUB  coldchain/esp32-sensor-01/telemetry  (JSON de telemetria)
 *    PUB  coldchain/esp32-sensor-01/status     ("online" / LWT "offline")
 *    SUB  coldchain/esp32-sensor-01/ack        (ACK transacional do gateway)
 *    SUB  coldchain/esp32-sensor-01/cmd        (comandos remotos futuros)
 * ============================================================================
 */

#include <Arduino.h>
#include <WiFi.h>
#include <ESPmDNS.h>
#include <Preferences.h>
#include <PubSubClient.h>
#include <math.h>
#include <time.h>

// ============================================================================
// 1. SELEÇÃO DO SENSOR DE TEMPERATURA (ALTERE APENAS ESTA LINHA SE NECESSÁRIO)
// ============================================================================
#define SENSOR_TYPE_DS18B20      1   // Sonda à prova d'água DS18B20 (OneWire, GPIO 4)
#define SENSOR_TYPE_DHT22        2   // Sensor DHT22 Temp + Umidade (GPIO 4)
#define SENSOR_TYPE_DHT11        3   // Sensor DHT11 Temp + Umidade (GPIO 4)
#define SENSOR_TYPE_BME280       4   // Sensor I2C BME280 (SDA=GPIO 8, SCL=GPIO 9)
#define SENSOR_TYPE_S3_INTERNAL  5   // Sensor interno do chip ESP32-S3 (sem fios externos)
#define SENSOR_TYPE_NTC_ANALOG   6   // Termistor NTC 10k (divisor 10k no GPIO 4 ADC)

// >>> ESCOLHA AQUI O SENSOR LIGADO NO SEU ESP32-S3 <<<
#define SENSOR_MODEL SENSOR_TYPE_DS18B20

// Pinos padrão no ESP32-S3 (altere conforme sua montagem física)
#define SENSOR_DATA_PIN   4    // Usado por DS18B20, DHT22, DHT11 e NTC ADC
#define I2C_SDA_PIN       8    // Usado pelo BME280
#define I2C_SCL_PIN       9    // Usado pelo BME280
#define DOOR_SENSOR_PIN  -1    // Opcional: pino reed-switch de porta (-1 = desabilitado)

// Parâmetros do termistor NTC (usados apenas se SENSOR_MODEL == SENSOR_TYPE_NTC_ANALOG)
#define NTC_SERIES_RESISTOR  10000.0f
#define NTC_NOMINAL_RES      10000.0f
#define NTC_NOMINAL_TEMP     25.0f
#define NTC_BETA_COEFF       3950.0f

// ============================================================================
// 2. REDE WI-FI (HOTSPOT S23) E CREDENCIAIS MQTT DO GATEWAY RASPBERRY PI 4
// ============================================================================
static const char* WIFI_SSID          = "S23";
static const char* WIFI_PASS          = "Vini1234";

// Deixe "" para descoberta 100% automática do Raspberry Pi 4 na rede S23.
// Para teste local no PC com QEMU ou IP fixo, coloque ex: "192.168.43.100".
static const char* BROKER_IP_MANUAL   = "";
static const char* GATEWAY_MDNS_HOST  = "coldchain-rpi4";
static const uint16_t MQTT_PORT       = 1883;
static const uint16_t HTTP_PORT       = 5000;

static const char* DEVICE_ID          = "esp32-sensor-01";
static const char* MQTT_USER          = "esp32-sensor-01";
static const char* MQTT_PASS          = "SensorSecret2026!";
static const char* FW_VERSION         = "esp32s3-1.0.0";

// Intervalo entre leituras e envios (ms)
static const uint32_t SAMPLE_INTERVAL_MS     = 3000;
static const uint32_t WIFI_RETRY_INTERVAL_MS = 5000;
static const uint32_t MQTT_RETRY_INTERVAL_MS = 3000;

// ============================================================================
// 3. BIBLIOTECAS CONDICIONAIS DO SENSOR SELECIONADO
// ============================================================================
#if SENSOR_MODEL == SENSOR_TYPE_DS18B20
  #include <OneWire.h>
  #include <DallasTemperature.h>
  OneWire oneWireBus(SENSOR_DATA_PIN);
  DallasTemperature ds18b20(&oneWireBus);
#elif (SENSOR_MODEL == SENSOR_TYPE_DHT22) || (SENSOR_MODEL == SENSOR_TYPE_DHT11)
  #include <DHT.h>
  #if SENSOR_MODEL == SENSOR_TYPE_DHT22
    DHT dhtSensor(SENSOR_DATA_PIN, DHT22);
  #else
    DHT dhtSensor(SENSOR_DATA_PIN, DHT11);
  #endif
#elif SENSOR_MODEL == SENSOR_TYPE_BME280
  #include <Wire.h>
  #include <Adafruit_Sensor.h>
  #include <Adafruit_BME280.h>
  Adafruit_BME280 bme280;
  bool bme280Ready = false;
#endif

// ============================================================================
// 4. ESTADO GLOBAL, BUFFER OFFLINE (STORE-AND-FORWARD) E CLIENTE MQTT
// ============================================================================
struct TelemetrySample {
  int64_t  ts;
  float    temp;
  float    hum;
  int      door;
  uint32_t seq;
  int      sync;
};

static const size_t OFFLINE_RING_CAP = 50;
TelemetrySample offlineRing[OFFLINE_RING_CAP];
size_t ringHead  = 0;
size_t ringTail  = 0;
size_t ringCount = 0;

WiFiClient   wifiNet;
PubSubClient mqttClient(wifiNet);
Preferences  prefs;

String   discoveredBrokerIP = "";
uint32_t sequenceCounter    = 0;
uint32_t lastAckedSeq       = 0;
uint32_t lastSampleMs       = 0;
uint32_t lastWiFiAttemptMs  = 0;
uint32_t lastMQTTAttemptMs  = 0;
bool     ntpConfigured      = false;
bool     mdnsStarted        = false;

char topicTelemetry[96];
char topicStatus[96];
char topicAck[96];
char topicCmd[96];

// ============================================================================
// 5. BUFFER CIRCULAR OFFLINE (GARANTIA CONTRA PERDA DE DADOS EM QUEDA DE REDE)
// ============================================================================
void ringPush(const TelemetrySample& s) {
  if (ringCount == OFFLINE_RING_CAP) {
    // Descarta a amostra mais antiga se o buffer lotar completamente
    ringHead = (ringHead + 1) % OFFLINE_RING_CAP;
    ringCount--;
  }
  offlineRing[ringTail] = s;
  ringTail = (ringTail + 1) % OFFLINE_RING_CAP;
  ringCount++;
}

bool ringPeek(TelemetrySample& out) {
  if (ringCount == 0) return false;
  out = offlineRing[ringHead];
  return true;
}

void ringPop() {
  if (ringCount == 0) return;
  ringHead = (ringHead + 1) % OFFLINE_RING_CAP;
  ringCount--;
}

// ============================================================================
// 6. RELÓGIO NTP E VALIDAÇÃO DE SINCRONISMO
// ============================================================================
void setupNTP() {
  if (ntpConfigured) return;
  configTime(0, 0, "pool.ntp.org", "a.st1.ntp.br", "time.google.com");
  ntpConfigured = true;
}

bool isTimeSynced(int64_t& epochOut) {
  time_t nowSec = time(nullptr);
  // Qualquer epoch acima de 2024-01-01 (1704067200) indica NTP válido
  if (nowSec > 1704067200) {
    epochOut = (int64_t)nowSec;
    return true;
  }
  epochOut = 0;
  return false;
}

// ============================================================================
// 7. LEITURA FÍSICA DO SENSOR DE TEMPERATURA (COM VALIDAÇÃO METROLÓGICA)
// ============================================================================
void initTemperatureSensor() {
#if DOOR_SENSOR_PIN >= 0
  pinMode(DOOR_SENSOR_PIN, INPUT_PULLUP);
#endif

#if SENSOR_MODEL == SENSOR_TYPE_DS18B20
  ds18b20.begin();
  ds18b20.setResolution(12); // Precisão 0.0625 °C
  ds18b20.setWaitForConversion(true);
  int count = ds18b20.getDeviceCount();
  Serial.printf("[SENSOR] DS18B20 inicializado no GPIO %d (sondas detectadas: %d)\n",
                SENSOR_DATA_PIN, count);
#elif (SENSOR_MODEL == SENSOR_TYPE_DHT22) || (SENSOR_MODEL == SENSOR_TYPE_DHT11)
  dhtSensor.begin();
  Serial.printf("[SENSOR] DHT inicializado no GPIO %d\n", SENSOR_DATA_PIN);
#elif SENSOR_MODEL == SENSOR_TYPE_BME280
  Wire.begin(I2C_SDA_PIN, I2C_SCL_PIN);
  bme280Ready = bme280.begin(0x76);
  if (!bme280Ready) {
    bme280Ready = bme280.begin(0x77);
  }
  Serial.printf("[SENSOR] BME280 I2C (SDA=%d, SCL=%d): %s\n",
                I2C_SDA_PIN, I2C_SCL_PIN, bme280Ready ? "OK" : "NAO ENCONTRADO");
#elif SENSOR_MODEL == SENSOR_TYPE_S3_INTERNAL
  Serial.println("[SENSOR] Usando sensor de temperatura interno do chip ESP32-S3");
#elif SENSOR_MODEL == SENSOR_TYPE_NTC_ANALOG
  analogReadResolution(12);
  analogSetPinAttenuation(SENSOR_DATA_PIN, ADC_11db);
  Serial.printf("[SENSOR] Termistor NTC 10k inicializado no GPIO %d (ADC 12-bit)\n", SENSOR_DATA_PIN);
#endif
}

bool readTemperatureSensor(float& tempC, float& humPct) {
  tempC  = NAN;
  humPct = -1.0f; // -1 indica sonda somente de temperatura (exibido como "--" no painel)

#if SENSOR_MODEL == SENSOR_TYPE_DS18B20
  ds18b20.requestTemperatures();
  float t = ds18b20.getTempCByIndex(0);
  // -127.0 = sensor desconectado; 85.0 na 1ª leitura = reset de energia do DS18B20
  if (t == DEVICE_DISCONNECTED_C || t <= -120.0f) {
    Serial.println("[SENSOR] ERRO: DS18B20 desconectado! Verifique fio DATA (GPIO 4) e resistor pull-up 4.7k.");
    return false;
  }
  if (fabsf(t - 85.0f) < 0.01f && sequenceCounter == 0) {
    delay(150);
    ds18b20.requestTemperatures();
    t = ds18b20.getTempCByIndex(0);
  }
  tempC = t;

#elif (SENSOR_MODEL == SENSOR_TYPE_DHT22) || (SENSOR_MODEL == SENSOR_TYPE_DHT11)
  float t = dhtSensor.readTemperature();
  float h = dhtSensor.readHumidity();
  if (isnan(t)) {
    Serial.println("[SENSOR] ERRO: Falha ao ler DHT! Verifique fiação no GPIO 4.");
    return false;
  }
  tempC = t;
  if (!isnan(h) && h >= 0.0f && h <= 100.0f) {
    humPct = h;
  }

#elif SENSOR_MODEL == SENSOR_TYPE_BME280
  if (!bme280Ready) {
    bme280Ready = bme280.begin(0x76) || bme280.begin(0x77);
    if (!bme280Ready) {
      Serial.println("[SENSOR] ERRO: BME280 não respondeu em 0x76/0x77.");
      return false;
    }
  }
  tempC  = bme280.readTemperature();
  humPct = bme280.readHumidity();

#elif SENSOR_MODEL == SENSOR_TYPE_S3_INTERNAL
  tempC = temperatureRead();

#elif SENSOR_MODEL == SENSOR_TYPE_NTC_ANALOG
  // Média de 16 leituras para reduzir ruído elétrico do ADC
  uint32_t acc = 0;
  for (int i = 0; i < 16; i++) {
    acc += analogRead(SENSOR_DATA_PIN);
    delay(2);
  }
  float adc = acc / 16.0f;
  if (adc <= 5.0f || adc >= 4090.0f) {
    Serial.println("[SENSOR] ERRO: Leitura NTC fora de faixa (curto ou circuito aberto).");
    return false;
  }
  float resistance = NTC_SERIES_RESISTOR * ((4095.0f / adc) - 1.0f);
  float steinhart = logf(resistance / NTC_NOMINAL_RES) / NTC_BETA_COEFF;
  steinhart += 1.0f / (NTC_NOMINAL_TEMP + 273.15f);
  tempC = (1.0f / steinhart) - 273.15f;
#endif

  if (isnan(tempC) || isinf(tempC) || tempC < -100.0f || tempC > 125.0f) {
    Serial.printf("[SENSOR] Leitura descartada (fora de faixa física): %.2f C\n", tempC);
    return false;
  }
  return true;
}

int readDoorState() {
#if DOOR_SENSOR_PIN >= 0
  return (digitalRead(DOOR_SENSOR_PIN) == HIGH) ? 1 : 0;
#else
  return 0;
#endif
}

// ============================================================================
// 8. DESCOBERTA AUTOMÁTICA DO RASPBERRY PI 4 NA REDE S23
// ============================================================================
bool probeColdChainGateway(const IPAddress& candidate, uint16_t timeoutMs = 120) {
  if (candidate == IPAddress(0, 0, 0, 0) || candidate == WiFi.localIP()) {
    return false;
  }

  WiFiClient probe;
  if (!probe.connect(candidate, HTTP_PORT, timeoutMs)) {
    return false;
  }

  probe.print("GET /healthz HTTP/1.1\r\nHost: coldchain\r\nConnection: close\r\n\r\n");
  uint32_t start = millis();
  String resp = "";
  while (probe.connected() && (millis() - start < 400)) {
    while (probe.available()) {
      char c = (char)probe.read();
      if (resp.length() < 512) resp += c;
    }
    if (resp.indexOf("\"status\":\"ok\"") >= 0 || resp.indexOf("\"database\":\"ok\"") >= 0) {
      break;
    }
    delay(2);
  }
  probe.stop();

  if (resp.indexOf("\"database\":\"ok\"") < 0 && resp.indexOf("\"status\":\"ok\"") < 0) {
    return false;
  }

  // Confirma que a porta MQTT 1883 também está acessível no mesmo host
  WiFiClient mqttProbe;
  bool mqttOpen = mqttProbe.connect(candidate, MQTT_PORT, 250);
  if (mqttOpen) {
    mqttProbe.stop();
  }
  return mqttOpen;
}

bool discoverRaspberryPiBroker() {
  // 1. IP manual explícito (se configurado no topo do código)
  if (strlen(BROKER_IP_MANUAL) > 0) {
    discoveredBrokerIP = String(BROKER_IP_MANUAL);
    Serial.printf("[DISCOVERY] Usando IP manual configurado: %s\n", discoveredBrokerIP.c_str());
    return true;
  }

  // 2. Resolução mDNS (coldchain-rpi4.local anunciado pelo avahi-daemon da RPi4)
  if (!mdnsStarted) {
    if (MDNS.begin(DEVICE_ID)) {
      mdnsStarted = true;
    }
  }
  if (mdnsStarted) {
    Serial.printf("[DISCOVERY] Consultando mDNS %s.local...\n", GATEWAY_MDNS_HOST);
    IPAddress mdnsIP = MDNS.queryHost(GATEWAY_MDNS_HOST, 2500);
    if (mdnsIP != IPAddress(0, 0, 0, 0) && probeColdChainGateway(mdnsIP, 300)) {
      discoveredBrokerIP = mdnsIP.toString();
      prefs.putString("broker_ip", discoveredBrokerIP);
      Serial.printf("[DISCOVERY] Raspberry Pi 4 encontrado via mDNS: %s\n", discoveredBrokerIP.c_str());
      return true;
    }
  }

  // 3. Último IP válido salvo em memória não-volátil (NVS)
  String savedIP = prefs.getString("broker_ip", "");
  if (savedIP.length() >= 7) {
    IPAddress cached;
    if (cached.fromString(savedIP) && probeColdChainGateway(cached, 250)) {
      discoveredBrokerIP = savedIP;
      Serial.printf("[DISCOVERY] Raspberry Pi 4 confirmado via cache NVS: %s\n", discoveredBrokerIP.c_str());
      return true;
    }
  }

  // 4. Varredura rápida da sub-rede /24 do Hotspot S23 (detecta qualquer IP DHCP da RPi4)
  IPAddress local = WiFi.localIP();
  Serial.printf("[DISCOVERY] Procurando Gateway ColdChain na sub-rede %u.%u.%u.1-254...\n",
                local[0], local[1], local[2]);

  for (int host = 1; host <= 254; host++) {
    if (host == local[3]) continue;
    if (WiFi.status() != WL_CONNECTED) return false;

    IPAddress cand(local[0], local[1], local[2], host);
    if (probeColdChainGateway(cand, 75)) {
      discoveredBrokerIP = cand.toString();
      prefs.putString("broker_ip", discoveredBrokerIP);
      Serial.printf("[DISCOVERY] Raspberry Pi 4 encontrado na rede S23: %s\n", discoveredBrokerIP.c_str());
      return true;
    }
    yield();
  }

  Serial.println("[DISCOVERY] Gateway Raspberry Pi 4 ainda não encontrado na rede S23.");
  return false;
}

// ============================================================================
// 9. CALLBACK MQTT (ACK TRANSACIONAL E COMANDOS DO GATEWAY)
// ============================================================================
void onMQTTMessage(char* topic, byte* payload, unsigned int length) {
  char buf[256];
  unsigned int copyLen = (length < sizeof(buf) - 1) ? length : (sizeof(buf) - 1);
  memcpy(buf, payload, copyLen);
  buf[copyLen] = '\0';

  if (strcmp(topic, topicAck) == 0) {
    const char* seqPos = strstr(buf, "\"seq\"");
    if (seqPos != nullptr) {
      const char* colon = strchr(seqPos, ':');
      if (colon != nullptr) {
        uint32_t ackSeq = (uint32_t)strtoul(colon + 1, nullptr, 10);
        if (ackSeq > lastAckedSeq) {
          lastAckedSeq = ackSeq;
        }
        Serial.printf("[MQTT] ACK confirmado pelo Gateway/Ledger: seq=%lu\n", (unsigned long)ackSeq);
      }
    }
  } else if (strcmp(topic, topicCmd) == 0) {
    Serial.printf("[MQTT] Comando recebido do Gateway: %s\n", buf);
  }
}

// ============================================================================
// 10. CONEXÃO WI-FI E MQTT COM RECONEXÃO AUTOMÁTICA
// ============================================================================
void ensureWiFiConnected() {
  if (WiFi.status() == WL_CONNECTED) return;

  uint32_t now = millis();
  if (now - lastWiFiAttemptMs < WIFI_RETRY_INTERVAL_MS && lastWiFiAttemptMs != 0) {
    return;
  }
  lastWiFiAttemptMs = now;

  Serial.printf("[WIFI] Conectando na rede '%s'...\n", WIFI_SSID);
  WiFi.disconnect(false);
  WiFi.begin(WIFI_SSID, WIFI_PASS);

  uint32_t start = millis();
  while (WiFi.status() != WL_CONNECTED && (millis() - start < 10000)) {
    delay(250);
    Serial.print(".");
  }
  Serial.println();

  if (WiFi.status() == WL_CONNECTED) {
    Serial.printf("[WIFI] Conectado! IP ESP32-S3: %s | Gateway: %s | RSSI: %d dBm\n",
                  WiFi.localIP().toString().c_str(),
                  WiFi.gatewayIP().toString().c_str(),
                  WiFi.RSSI());
    setupNTP();
  } else {
    Serial.println("[WIFI] Falha ao conectar em 'S23'. Verifique se o hotspot 2.4 GHz está ativo.");
  }
}

bool ensureMQTTConnected() {
  if (WiFi.status() != WL_CONNECTED) return false;
  if (mqttClient.connected()) return true;

  uint32_t now = millis();
  if (now - lastMQTTAttemptMs < MQTT_RETRY_INTERVAL_MS && lastMQTTAttemptMs != 0) {
    return false;
  }
  lastMQTTAttemptMs = now;

  if (discoveredBrokerIP.length() == 0) {
    if (!discoverRaspberryPiBroker()) {
      return false;
    }
  }

  mqttClient.setServer(discoveredBrokerIP.c_str(), MQTT_PORT);
  Serial.printf("[MQTT] Conectando ao broker %s:%u como '%s'...\n",
                discoveredBrokerIP.c_str(), MQTT_PORT, MQTT_USER);

  // Conecta com usuário/senha Mosquitto + Last Will and Testament (LWT "offline")
  bool ok = mqttClient.connect(
    DEVICE_ID,
    MQTT_USER,
    MQTT_PASS,
    topicStatus,
    1,
    true,
    "offline"
  );

  if (ok) {
    Serial.println("[MQTT] Conectado com sucesso ao Raspberry Pi 4!");
    mqttClient.publish(topicStatus, "online", true);
    mqttClient.subscribe(topicAck, 1);
    mqttClient.subscribe(topicCmd, 1);
    return true;
  }

  Serial.printf("[MQTT] Falha de conexão (rc=%d). Revalidando IP do Gateway...\n", mqttClient.state());
  // Se não for IP manual fixo, força nova descoberta caso o IP da RPi4 tenha mudado no S23
  if (strlen(BROKER_IP_MANUAL) == 0) {
    discoveredBrokerIP = "";
  }
  return false;
}

// ============================================================================
// 11. ENVIO DE TELEMETRIA JSON + FLUSH DO BUFFER OFFLINE
// ============================================================================
bool publishSample(const TelemetrySample& s) {
  char payload[220];
  int len = snprintf(
    payload, sizeof(payload),
    "{\"ts\":%lld,\"t\":%.2f,\"h\":%.1f,\"door\":%d,\"seq\":%lu,\"sync\":%d,\"fw\":\"%s\"}",
    (long long)s.ts,
    (double)s.temp,
    (double)s.hum,
    s.door,
    (unsigned long)s.seq,
    s.sync,
    FW_VERSION
  );
  if (len <= 0 || len >= (int)sizeof(payload)) {
    return false;
  }

  bool sent = mqttClient.publish(topicTelemetry, payload, false);
  if (sent) {
    Serial.printf("[TX] %s -> %s\n", topicTelemetry, payload);
  }
  return sent;
}

void flushOfflineRing() {
  if (!mqttClient.connected() || ringCount == 0) return;

  TelemetrySample s;
  int burst = 0;
  while (ringCount > 0 && mqttClient.connected() && burst < 10) {
    if (!ringPeek(s)) break;
    if (publishSample(s)) {
      ringPop();
      burst++;
      mqttClient.loop();
      delay(40);
    } else {
      break;
    }
  }
}

// ============================================================================
// 12. SETUP E LOOP PRINCIPAL
// ============================================================================
void setup() {
  Serial.begin(115200);
  delay(800);

  Serial.println();
  Serial.println("============================================================");
  Serial.println("  ColdChain Pharma RT — Sensor Físico ESP32-S3 (MQTT Real)");
  Serial.printf("  Device ID : %s | Firmware: %s\n", DEVICE_ID, FW_VERSION);
  Serial.printf("  Wi-Fi     : %s\n", WIFI_SSID);
  Serial.println("============================================================");

  snprintf(topicTelemetry, sizeof(topicTelemetry), "coldchain/%s/telemetry", DEVICE_ID);
  snprintf(topicStatus,    sizeof(topicStatus),    "coldchain/%s/status",    DEVICE_ID);
  snprintf(topicAck,       sizeof(topicAck),       "coldchain/%s/ack",       DEVICE_ID);
  snprintf(topicCmd,       sizeof(topicCmd),       "coldchain/%s/cmd",       DEVICE_ID);

  prefs.begin("coldchain", false);
  sequenceCounter = prefs.getUInt("last_seq", 0);

  WiFi.mode(WIFI_STA);
  WiFi.setHostname(DEVICE_ID);
  WiFi.setAutoReconnect(true);
  WiFi.setSleep(false); // Evita queda de conexão MQTT por economia de energia do rádio

  mqttClient.setBufferSize(512);
  mqttClient.setKeepAlive(30);
  mqttClient.setSocketTimeout(5);
  mqttClient.setCallback(onMQTTMessage);

  initTemperatureSensor();
  ensureWiFiConnected();
  ensureMQTTConnected();
}

void loop() {
  ensureWiFiConnected();
  ensureMQTTConnected();

  if (mqttClient.connected()) {
    mqttClient.loop();
    flushOfflineRing();
  }

  uint32_t now = millis();
  if (now - lastSampleMs >= SAMPLE_INTERVAL_MS || lastSampleMs == 0) {
    lastSampleMs = now;

    float tempC = 0.0f;
    float humPct = -1.0f;
    if (readTemperatureSensor(tempC, humPct)) {
      sequenceCounter++;
      // Persiste contador a cada 10 leituras para manter monotonicidade sem desgastar a flash
      if (sequenceCounter % 10 == 0) {
        prefs.putUInt("last_seq", sequenceCounter);
      }

      int64_t epochSec = 0;
      int synced = isTimeSynced(epochSec) ? 1 : 0;

      TelemetrySample sample;
      sample.ts   = epochSec;
      sample.temp = tempC;
      sample.hum  = humPct;
      sample.door = readDoorState();
      sample.seq  = sequenceCounter;
      sample.sync = synced;

      if (mqttClient.connected() && ringCount == 0) {
        if (!publishSample(sample)) {
          ringPush(sample);
          Serial.printf("[BUFFER] Falha no envio imediato; armazenado offline (%u/%u)\n",
                        (unsigned)ringCount, (unsigned)OFFLINE_RING_CAP);
        }
      } else {
        ringPush(sample);
        Serial.printf("[BUFFER] Sem broker no momento; leitura %.2f C enfileirada (%u/%u)\n",
                      tempC, (unsigned)ringCount, (unsigned)OFFLINE_RING_CAP);
      }
    }
  }

  delay(20);
}
