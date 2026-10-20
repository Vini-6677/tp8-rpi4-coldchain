# Mosquitto MQTT Broker Configuration

Configurações do Eclipse Mosquitto 2.x para o Gateway de Cadeia de Frio.
- Senhas geradas via `mosquitto_passwd` (PBKDF2-SHA512).
- Usuário não-root no container: 1883:1883.
- ACL dinâmica com restrição por dispositivo (%u).
