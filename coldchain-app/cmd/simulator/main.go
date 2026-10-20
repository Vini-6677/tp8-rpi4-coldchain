package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type TelemetryPayload struct {
	TS   int64   `json:"ts"`
	T    float64 `json:"t"`
	H    float64 `json:"h"`
	Door int     `json:"door"`
	Seq  int64   `json:"seq"`
	Sync int     `json:"sync"`
	FW   string  `json:"fw"`
}

func main() {
	deviceID := flag.String("device", "esp32-sensor-01", "Identificador do sensor ESP32")
	broker := flag.String("broker", "tcp://127.0.0.1:1883", "Endereço do broker MQTT")
	username := flag.String("user", "esp32-sensor-01", "Usuário MQTT")
	password := flag.String("pass", "SensorSecret2026!", "Senha MQTT")
	interval := flag.Duration("interval", 2*time.Second, "Intervalo entre telemetrias")
	flag.Parse()

	fmt.Printf("[+] Simulador ESP32 iniciado para '%s' em %s\n", *deviceID, *broker)

	opts := mqtt.NewClientOptions()
	opts.AddBroker(*broker)
	opts.SetClientID(*deviceID)
	opts.SetCleanSession(false)
	if *username != "" {
		opts.SetUsername(*username)
		opts.SetPassword(*password)
	}
	opts.SetWill(fmt.Sprintf("coldchain/%s/status", *deviceID), "offline", 1, true)

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		fmt.Printf("[-] Erro ao conectar ao Mosquitto: %v\n", token.Error())
		os.Exit(1)
	}

	// Inscreve no tópico de ACK ponta a ponta
	ackTopic := fmt.Sprintf("coldchain/%s/ack", *deviceID)
	client.Subscribe(ackTopic, 1, func(c mqtt.Client, m mqtt.Message) {
		fmt.Printf("[ACK recebido] %s -> %s\n", m.Topic(), string(m.Payload()))
	})

	// Publica status online
	client.Publish(fmt.Sprintf("coldchain/%s/status", *deviceID), 1, true, "online")

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	var seq int64 = 1
	for {
		select {
		case <-sigChan:
			fmt.Println("\n[!] Encerrando simulador ESP32...")
			client.Publish(fmt.Sprintf("coldchain/%s/status", *deviceID), 1, true, "offline")
			client.Disconnect(250)
			return
		case <-ticker.C:
			temp := 4.0 + (rand.Float64()*3.0 - 1.5) // Oscila entre 2.5 e 5.5 °C
			payload := TelemetryPayload{
				TS:   time.Now().Unix(),
				T:    temp,
				H:    55.0,
				Door: 0,
				Seq:  seq,
				Sync: 1,
				FW:   "1.0.0",
			}
			bytes, _ := json.Marshal(payload)
			topic := fmt.Sprintf("coldchain/%s/telemetry", *deviceID)
			client.Publish(topic, 1, false, bytes)
			fmt.Printf("[Telemetria enviada] #%d Temp: %.2f °C\n", seq, temp)
			seq++
		}
	}
}
