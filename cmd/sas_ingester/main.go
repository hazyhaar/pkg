package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hazyhaar/pkg/sas_ingester"
)

func main() {
	cfgPath := "sas_ingester.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := sas_ingester.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if err := os.MkdirAll(cfg.ChunksDir, 0755); err != nil {
		log.Fatalf("create chunks dir: %v", err)
	}

	ing, err := sas_ingester.NewIngester(cfg)
	if err != nil {
		log.Fatalf("init ingester: %v", err)
	}
	defer ing.Close()

	// Start retry loop in background.
	go retryLoop(ing)

	mux := http.NewServeMux()
	mux.HandleFunc("/upload", uploadHandler(ing))
	mux.HandleFunc("/dossier/", dossierHandler(ing))
	mux.HandleFunc("/health", healthHandler(ing))

	log.Printf("sas_ingester listening on %s", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, mux); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func uploadHandler(ing *sas_ingester.Ingester) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Authenticate via JWT.
		claims, err := extractClaims(r, ing.Config.JWTSecret)
		if err != nil {
			http.Error(w, fmt.Sprintf("auth: %v", err), http.StatusUnauthorized)
			return
		}

		dossierID := sas_ingester.ExtractDossierID(claims)

		// Parse multipart: expect a "file" field.
		if err := r.ParseMultipartForm(ing.Config.MaxFileBytes()); err != nil {
			http.Error(w, fmt.Sprintf("parse form: %v", err), http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, fmt.Sprintf("missing file field: %v", err), http.StatusBadRequest)
			return
		}
		defer file.Close()

		result, err := ing.Ingest(file, dossierID, claims.Sub)
		if err != nil {
			log.Printf("[upload] error: %v", err)
			http.Error(w, fmt.Sprintf("ingest: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if result.State == "blocked" {
			w.WriteHeader(http.StatusForbidden)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		json.NewEncoder(w).Encode(result)
	}
}

func dossierHandler(ing *sas_ingester.Ingester) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract dossier ID from path: /dossier/{id}
		id := strings.TrimPrefix(r.URL.Path, "/dossier/")
		if id == "" {
			http.Error(w, "missing dossier id", http.StatusBadRequest)
			return
		}

		claims, err := extractClaims(r, ing.Config.JWTSecret)
		if err != nil {
			http.Error(w, fmt.Sprintf("auth: %v", err), http.StatusUnauthorized)
			return
		}

		switch r.Method {
		case http.MethodGet:
			dossier, err := ing.Store.GetDossier(id)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if dossier == nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			if dossier.OwnerJWTSub != claims.Sub {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			pieces, _ := ing.Store.ListPieces(id)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"dossier": dossier,
				"pieces":  pieces,
			})

		case http.MethodDelete:
			dossier, err := ing.Store.GetDossier(id)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if dossier == nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			if dossier.OwnerJWTSub != claims.Sub {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if err := ing.Store.DeleteDossier(id); err != nil {
				http.Error(w, "delete failed", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func healthHandler(ing *sas_ingester.Ingester) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		total, _ := ing.Store.PiecesCount("")
		ready, _ := ing.Store.PiecesCount("ready")
		blocked, _ := ing.Store.PiecesCount("blocked")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":        "ok",
			"pieces_total":  total,
			"pieces_ready":  ready,
			"pieces_blocked": blocked,
		})
	}
}

func extractClaims(r *http.Request, secret string) (*sas_ingester.JWTClaims, error) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return nil, fmt.Errorf("missing Bearer token")
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	return sas_ingester.ParseJWT(token, secret)
}

func retryLoop(ing *sas_ingester.Ingester) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		ing.Router.ProcessRetries()
	}
}
