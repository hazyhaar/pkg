package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hazyhaar/pkg/idgen"
	"github.com/hazyhaar/pkg/kit"
	"github.com/hazyhaar/pkg/observability"
	"github.com/hazyhaar/pkg/sas_ingester"
	"github.com/hazyhaar/pkg/trace"
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

	// --- Trace store (raw "sqlite" driver to avoid recursion) ---
	traceDB, err := sql.Open("sqlite", cfg.DBPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("trace db: %v", err)
	}
	traceStore := trace.NewStore(traceDB)
	if err := traceStore.Init(); err != nil {
		log.Fatalf("trace init: %v", err)
	}
	trace.SetStore(traceStore)
	defer traceStore.Close()
	defer traceDB.Close()

	// --- Observability DB (separate from app DB to avoid write contention) ---
	obsDBPath := filepath.Join(filepath.Dir(cfg.DBPath), "observability.db")
	obsDB, err := sql.Open("sqlite", obsDBPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("observability db: %v", err)
	}
	defer obsDB.Close()
	if err := observability.Init(obsDB); err != nil {
		log.Fatalf("observability schema: %v", err)
	}

	// --- Observability components ---
	auditLogger := observability.NewAuditLogger(obsDB, 1000,
		observability.WithAuditIDGenerator(idgen.Prefixed("aud_", idgen.Default)),
	)
	metrics := observability.NewMetricsManager(obsDB, 100, 5*time.Second)
	events := observability.NewEventLogger(obsDB,
		observability.WithEventIDGenerator(idgen.Prefixed("evt_", idgen.Default)),
	)

	// Heartbeat: write liveness + runtime metrics every 15s.
	heartbeat := observability.NewHeartbeatWriter(obsDB, "sas_ingester", 15*time.Second)
	heartbeat.Start(context.Background())
	defer heartbeat.Stop()

	// --- ID generators ---
	dossierIDGen := idgen.Prefixed("dos_", idgen.Default)
	requestIDGen := idgen.Prefixed("req_", idgen.Default)

	// --- Ingester ---
	ing, err := sas_ingester.NewIngester(cfg,
		sas_ingester.WithIDGenerator(dossierIDGen),
		sas_ingester.WithAudit(auditLogger),
		sas_ingester.WithMetrics(metrics),
		sas_ingester.WithEvents(events),
	)
	if err != nil {
		log.Fatalf("init ingester: %v", err)
	}
	defer ing.Close()

	// Start retry loop in background.
	go retryLoop(ing)

	// --- HTTP mux with kit context enrichment ---
	mux := http.NewServeMux()
	mux.Handle("/upload", contextMiddleware(requestIDGen, uploadHandler(ing)))
	mux.Handle("/dossier/", contextMiddleware(requestIDGen, dossierHandler(ing)))
	mux.HandleFunc("/health", healthHandler(ing, obsDB))

	log.Printf("sas_ingester listening on %s", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, mux); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

// contextMiddleware enriches the request context with kit values (request ID,
// trace ID, user ID, transport) so that trace and audit can correlate entries.
func contextMiddleware(reqIDGen idgen.Generator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := reqIDGen()
		ctx = kit.WithRequestID(ctx, reqID)
		ctx = kit.WithTraceID(ctx, reqID) // use same ID for trace correlation
		ctx = kit.WithTransport(ctx, "http")

		w.Header().Set("X-Request-ID", reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
			log.Printf("[upload] req=%s error: %v", kit.GetRequestID(r.Context()), err)
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

func healthHandler(ing *sas_ingester.Ingester, obsDB *sql.DB) http.HandlerFunc {
	// Staleness threshold = 3× heartbeat interval (15s × 3 = 45s).
	const stalenessThreshold = 45 * time.Second

	return func(w http.ResponseWriter, r *http.Request) {
		total, _ := ing.Store.PiecesCount("")
		ready, _ := ing.Store.PiecesCount("ready")
		blocked, _ := ing.Store.PiecesCount("blocked")

		resp := map[string]interface{}{
			"status":         "ok",
			"pieces_total":   total,
			"pieces_ready":   ready,
			"pieces_blocked": blocked,
		}

		// Heartbeat: last known liveness + runtime snapshot.
		hb, err := observability.LatestHeartbeat(r.Context(), obsDB, "sas_ingester", stalenessThreshold)
		if err == nil && hb != nil {
			resp["heartbeat"] = hb
			if !hb.Alive {
				resp["status"] = "degraded"
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
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
