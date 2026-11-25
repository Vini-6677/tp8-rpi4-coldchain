package main

import (
	"coldchain-app/internal/pharma"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (a *App) certStorageDir() string {
	if a.certDir != "" {
		return a.certDir
	}
	if envDir := strings.TrimSpace(os.Getenv("CERT_STORAGE_DIR")); envDir != "" {
		return envDir
	}
	return "/data/coldchain/certificates"
}

// readUploadedPDF lê e valida rigorosamente um arquivo PDF de um formulário multipart.
// Retorna (nil, "", "", nil) quando required=false e o campo de arquivo não foi enviado.
func readUploadedPDF(r *http.Request, fieldName string, required bool) ([]byte, string, string, error) {
	file, header, err := r.FormFile(fieldName)
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) && !required {
			return nil, "", "", nil
		}
		if errors.Is(err, http.ErrMissingFile) {
			return nil, "", "", errors.New("selecione um arquivo PDF emitido pelo órgão regulador")
		}
		return nil, "", "", fmt.Errorf("erro ao ler arquivo enviado: %w", err)
	}
	defer file.Close()

	// Limita leitura a MaxCertificateSizeBytes + 1 byte para detectar excesso sem estourar memória
	lr := io.LimitReader(file, pharma.MaxCertificateSizeBytes+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, "", "", fmt.Errorf("falha ao ler conteúdo do PDF: %w", err)
	}
	if int64(len(data)) > pharma.MaxCertificateSizeBytes {
		return nil, "", "", fmt.Errorf("o arquivo PDF excede o limite máximo permitido de %d MB", pharma.MaxCertificateSizeBytes/(1024*1024))
	}

	declaredMIME := ""
	if header != nil {
		declaredMIME = header.Header.Get("Content-Type")
	}
	filename := ""
	if header != nil {
		filename = header.Filename
	}

	// Executa validação rigorosa antecipada (extensão, MIME, %PDF-, %%EOF, tamanho)
	if _, err := pharma.ValidatePDFUpload(filename, declaredMIME, data); err != nil {
		return nil, "", "", err
	}
	return data, filename, declaredMIME, nil
}

func (a *App) registerCertificateRoutes(mux *http.ServeMux) {
	// 1. Listar Certificados + Pendências + Resumo (GET) e Enviar Novo Certificado PDF (POST)
	mux.HandleFunc("/api/pharma/certificates", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}

		if r.Method == http.MethodGet {
			entityType := r.URL.Query().Get("entity_type")
			entityID := r.URL.Query().Get("entity_id")
			statusFilter := r.URL.Query().Get("status")

			certs, err := pharma.ListCertificates(a.db, entityType, entityID, statusFilter)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			pending, err := pharma.GetPendingCertificateItems(a.db)
			if err != nil {
				pending = []pharma.PendingCertificateItem{}
			}
			summary, _ := pharma.GetCertificateSummary(a.db)
			json.NewEncoder(w).Encode(map[string]any{
				"certificates": certs,
				"pending":      pending,
				"summary":      summary,
			})
			return
		}

		if r.Method == http.MethodPost {
			if sess.Role != "tecnico" && sess.Role != "admin" {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]any{"error": "Envio de certificados restrito ao Responsável Técnico ou Administrador."})
				return
			}

			r.Body = http.MaxBytesReader(w, r.Body, pharma.MaxCertificateSizeBytes+2*1024*1024)
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				json.NewEncoder(w).Encode(map[string]any{"error": fmt.Sprintf("Arquivo excede o limite de %d MB ou formulário multipart inválido.", pharma.MaxCertificateSizeBytes/(1024*1024))})
				return
			}

			pwd := r.FormValue("password")
			if pwd == "" || !a.verifySignature(sess.Username, pwd) {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]any{"error": "Assinatura digital obrigatória: forneça sua Chave Criptográfica Pessoal (CC-KEY-...) ou assine com seu Pen Drive USB (21 CFR Part 11)."})
				return
			}

			pdfBytes, filename, declaredMIME, err := readUploadedPDF(r, "file", true)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}

			in := pharma.CertificateUploadInput{
				EntityType:     r.FormValue("entity_type"),
				EntityID:       r.FormValue("entity_id"),
				EntityLabel:    r.FormValue("entity_label"),
				DocType:        r.FormValue("doc_type"),
				DocumentNumber: r.FormValue("document_number"),
				Issuer:         r.FormValue("issuer"),
				IssuedAt:       r.FormValue("issued_at"),
				ValidUntil:     r.FormValue("valid_until"),
				Notes:          r.FormValue("notes"),
				ReplaceReason:  r.FormValue("replace_reason"),
				UploadedBy:     sess.Username,
				UploadedByName: sess.Name,
				Filename:       filename,
				DeclaredMIME:   declaredMIME,
				FileBytes:      pdfBytes,
			}

			cert, ver, isReplacement, err := pharma.SaveOrReplaceCertificate(a.db, a.certStorageDir(), in)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}

			actionCode := "CERTIFICADO_ENVIADO"
			actionDesc := fmt.Sprintf("Certificado '%s' (%s) vinculado a %s enviado por %s (Arquivo: %s, v%d, Validade: %s, SHA-256: %s)",
				cert.DocumentNumber, cert.Issuer, cert.EntityLabel, sess.Name, ver.OriginalFilename, ver.VersionNumber, cert.ValidUntil, ver.SHA256Hash[:16])
			if isReplacement {
				actionCode = "CERTIFICADO_SUBSTITUIDO"
				actionDesc = fmt.Sprintf("Certificado '%s' (%s) atualizado para v%d por %s. Motivo: %s (Arquivo: %s, Validade: %s, SHA-256: %s)",
					cert.DocumentNumber, cert.EntityLabel, ver.VersionNumber, sess.Name, ver.ReplaceReason, ver.OriginalFilename, cert.ValidUntil, ver.SHA256Hash[:16])
			}
			a.logAudit(sess.Username, sess.Role, actionCode, actionDesc, getClientIP(r))

			if a.ledger != nil {
				ledgerPayload, _ := json.Marshal(map[string]any{
					"event":          actionCode,
					"certificate_id": cert.ID,
					"version":        ver.VersionNumber,
					"entity_type":    cert.EntityType,
					"entity_id":      cert.EntityID,
					"doc_number":     cert.DocumentNumber,
					"issuer":         cert.Issuer,
					"valid_until":    cert.ValidUntil,
					"sha256":         ver.SHA256Hash,
					"by":             sess.Username,
					"timestamp":      time.Now().UTC().Format(time.RFC3339),
				})
				if rec, lErr := a.ledger.AppendData("PHARMA-CERTIFICATES", uint64(time.Now().UnixNano()), ledgerPayload, 1); lErr == nil && rec != nil {
					pharma.UpdateCertificateVersionLedgerID(a.db, ver.ID, rec.ID)
				}
			}

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"status":      "ok",
				"certificate": cert,
				"version":     ver,
				"replaced":    isReplacement,
			})
			return
		}

		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
	})

	// 2. Substituir / Atualizar Certificado mantendo histórico imutável de versões (POST)
	mux.HandleFunc("/api/pharma/certificates/replace", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
			return
		}
		sess, ok := a.getSession(r)
		if !ok {
			a.writeSessionExpired(w)
			return
		}
		if sess.Role != "tecnico" && sess.Role != "admin" {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]any{"error": "Substituição de certificados restrita ao Responsável Técnico ou Administrador."})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, pharma.MaxCertificateSizeBytes+2*1024*1024)
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			json.NewEncoder(w).Encode(map[string]any{"error": fmt.Sprintf("Arquivo excede o limite de %d MB ou formulário inválido.", pharma.MaxCertificateSizeBytes/(1024*1024))})
			return
		}

		pwd := r.FormValue("password")
		if pwd == "" || !a.verifySignature(sess.Username, pwd) {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{"error": "Assinatura digital obrigatória: forneça sua Chave Criptográfica Pessoal (CC-KEY-...) ou assine com seu Pen Drive USB (21 CFR Part 11)."})
			return
		}

		certID := strings.TrimSpace(r.FormValue("certificate_id"))
		if certID == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "certificate_id é obrigatório para substituição."})
			return
		}

		pdfBytes, filename, declaredMIME, err := readUploadedPDF(r, "file", true)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		in := pharma.CertificateUploadInput{
			CertificateID:  certID,
			DocumentNumber: r.FormValue("document_number"),
			Issuer:         r.FormValue("issuer"),
			IssuedAt:       r.FormValue("issued_at"),
			ValidUntil:     r.FormValue("valid_until"),
			Notes:          r.FormValue("notes"),
			ReplaceReason:  r.FormValue("replace_reason"),
			UploadedBy:     sess.Username,
			UploadedByName: sess.Name,
			Filename:       filename,
			DeclaredMIME:   declaredMIME,
			FileBytes:      pdfBytes,
		}

		cert, ver, _, err := pharma.SaveOrReplaceCertificate(a.db, a.certStorageDir(), in)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		a.logAudit(sess.Username, sess.Role, "CERTIFICADO_SUBSTITUIDO",
			fmt.Sprintf("Certificado '%s' (%s) substituído para versão v%d por %s. Motivo: %s (Arquivo: %s, Validade: %s, SHA-256: %s)",
				cert.DocumentNumber, cert.EntityLabel, ver.VersionNumber, sess.Name, ver.ReplaceReason, ver.OriginalFilename, cert.ValidUntil, ver.SHA256Hash[:16]),
			getClientIP(r))

		if a.ledger != nil {
			ledgerPayload, _ := json.Marshal(map[string]any{
				"event":          "CERTIFICADO_SUBSTITUIDO",
				"certificate_id": cert.ID,
				"version":        ver.VersionNumber,
				"entity_type":    cert.EntityType,
				"entity_id":      cert.EntityID,
				"doc_number":     cert.DocumentNumber,
				"issuer":         cert.Issuer,
				"valid_until":    cert.ValidUntil,
				"replace_reason": ver.ReplaceReason,
				"sha256":         ver.SHA256Hash,
				"by":             sess.Username,
				"timestamp":      time.Now().UTC().Format(time.RFC3339),
			})
			if rec, lErr := a.ledger.AppendData("PHARMA-CERTIFICATES", uint64(time.Now().UnixNano()), ledgerPayload, 1); lErr == nil && rec != nil {
				pharma.UpdateCertificateVersionLedgerID(a.db, ver.ID, rec.ID)
			}
		}

		json.NewEncoder(w).Encode(map[string]any{
			"status":      "ok",
			"certificate": cert,
			"version":     ver,
		})
	})

	// 3. Histórico imutável de versões de um certificado (GET)
	mux.HandleFunc("/api/pharma/certificates/versions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, ok := a.getSession(r); !ok {
			a.writeSessionExpired(w)
			return
		}
		certID := strings.TrimSpace(r.URL.Query().Get("id"))
		if certID == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "Parâmetro 'id' obrigatório."})
			return
		}
		cert, err := pharma.GetCertificateByID(a.db, certID)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"error": "Certificado não encontrado."})
			return
		}
		versions, err := pharma.GetCertificateVersions(a.db, certID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"certificate": cert,
			"versions":    versions,
		})
	})

	// 4. Visualizar (inline no iframe) ou Baixar (attachment) o PDF original verificado via SHA-256 (GET)
	mux.HandleFunc("/api/pharma/certificates/file", func(w http.ResponseWriter, r *http.Request) {
		sess, ok := a.getSession(r)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			http.Error(w, "Autenticação requerida para acessar certificados regulatórios.", http.StatusUnauthorized)
			return
		}
		certID := strings.TrimSpace(r.URL.Query().Get("id"))
		if certID == "" {
			http.Error(w, "Parâmetro 'id' obrigatório.", http.StatusBadRequest)
			return
		}
		verNum := 0
		if vStr := strings.TrimSpace(r.URL.Query().Get("version")); vStr != "" {
			verNum, _ = strconv.Atoi(vStr)
		}
		mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
		if mode != "download" {
			mode = "inline"
		}

		ver, pdfBytes, err := pharma.LoadCertificateFileVerified(a.db, a.certStorageDir(), certID, verNum)
		if err != nil {
			if strings.Contains(err.Error(), "VIOLAÇÃO DE INTEGRIDADE") {
				a.logAudit(sess.Username, sess.Role, "CERTIFICADO_INTEGRIDADE_FALHA", fmt.Sprintf("Falha de verificação SHA-256 ao abrir certificado %s v%d: %v", certID, verNum, err), getClientIP(r))
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		safeName := filepath.Base(ver.OriginalFilename)
		if safeName == "" || safeName == "." {
			safeName = fmt.Sprintf("%s_v%d.pdf", certID, ver.VersionNumber)
		}

		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Content-SHA256", ver.SHA256Hash)
		w.Header().Set("Cache-Control", "private, no-store")
		if mode == "inline" {
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
			w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, safeName))
		} else {
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, safeName))
			a.logAudit(sess.Username, sess.Role, "CERTIFICADO_DOWNLOAD",
				fmt.Sprintf("Download do certificado %s (v%d — %s, SHA-256: %s) por %s", certID, ver.VersionNumber, safeName, ver.SHA256Hash[:16], sess.Name),
				getClientIP(r))
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(pdfBytes)))
		_, _ = w.Write(pdfBytes)
	})
}
