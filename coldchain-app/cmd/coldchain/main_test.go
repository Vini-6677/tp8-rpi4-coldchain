package main

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMandatoryPhraseAndContentNegotiation(t *testing.T) {
	app := &App{
		subscribers: make(map[chan Telemetry]struct{}),
		latestSeq:   make(map[string]int64),
		devicesSeen: make(map[string]time.Time),
	}
	handler := app.routes()

	// 1. Teste de curl puro: deve devolver a frase exata em linha limpa
	reqCurl := httptest.NewRequest("GET", "/", nil)
	wCurl := httptest.NewRecorder()
	handler.ServeHTTP(wCurl, reqCurl)

	resCurl := wCurl.Result()
	bodyCurl, _ := io.ReadAll(resCurl.Body)
	textCurl := strings.TrimSpace(string(bodyCurl))

	if textCurl != mandatoryPhrase {
		t.Fatalf("Esperado '%s', recebido '%s'", mandatoryPhrase, textCurl)
	}
	if wCurl.Header().Get("Vary") != "Accept" {
		t.Fatalf("Vary header deve ser 'Accept'")
	}

	// 2. Teste de navegador: deve devolver a frase dentro de <h1>
	reqBrowser := httptest.NewRequest("GET", "/", nil)
	reqBrowser.Header.Set("Accept", "text/html,application/xhtml+xml")
	wBrowser := httptest.NewRecorder()
	handler.ServeHTTP(wBrowser, reqBrowser)

	bodyBrowser, _ := io.ReadAll(wBrowser.Result().Body)
	expectedH1 := "<h1>" + mandatoryPhrase + "</h1>"
	if !strings.Contains(string(bodyBrowser), expectedH1) {
		t.Fatalf("HTML deve conter '%s'", expectedH1)
	}
}
