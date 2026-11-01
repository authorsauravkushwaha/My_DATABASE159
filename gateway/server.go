package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// segment encodes one path component so keys can be nested safely under it.
func segment(s string) string {
	return hex.EncodeToString([]byte(s))
}

func collectionKey(project, collection string) string {
	return "p:" + segment(project) + ":c:" + segment(collection) + ":r:"
}

func recordKey(project, collection, record string) string {
	return collectionKey(project, collection) + segment(record)
}

type Server struct {
	client *engineClient
	auth   *authorizer
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok","service":"kestrel-gateway"}`))
}

func decodeStoredObject(value string, target *map[string]any) error {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func (s *Server) authorized(w http.ResponseWriter, r *http.Request, write bool) (accessGrant, bool) {
	grant, status, err := s.auth.authorize(r.Context(), r.Header.Get("Authorization"), r.PathValue("project"), r.PathValue("collection"), write)
	if err != nil {
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", "Bearer")
		}
		fail(w, status, err.Error())
		return grant, false
	}
	return grant, true
}

func isValidRecordIDString(id string) bool {
	return id != "" && len(id) <= 128 && utf8.ValidString(id) && !strings.ContainsAny(id, "\x00\r\n")
}

func validRecordID(w http.ResponseWriter, id string) bool {
	if !isValidRecordIDString(id) {
		fail(w, 422, "record ID must be 1–128 UTF-8 bytes without control characters")
		return false
	}
	return true
}

func (s *Server) putRecord(w http.ResponseWriter, r *http.Request) {
	grant, ok := s.authorized(w, r, true)
	record := r.PathValue("record")
	if !ok || !validRecordID(w, record) {
		return
	}
	data, err := decodeData(r.Body)
	if err == nil {
		err = validateData(grant.Schema, data)
	}
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	value, err := json.Marshal(data)
	if err != nil {
		fail(w, 422, "cannot encode record")
		return
	}
	if err := s.client.Set(recordKey(grant.ProjectID, grant.CollectionID, record), string(value)); err != nil {
		log.Printf("gateway: storage write failed: %v", err)
		fail(w, 503, "storage engine unavailable")
		return
	}
	writeJSON(w, 201, map[string]string{"id": record})
}

func (s *Server) batchRecords(w http.ResponseWriter, r *http.Request) {
	grant, ok := s.authorized(w, r, true)
	if !ok {
		return
	}
	env, err := decodeBatch(r.Body)
	if err != nil {
		fail(w, 422, err.Error())
		return
	}
	ops := make([]wireBatchOp, 0, len(env.Records)+len(env.Deletes))
	for _, item := range env.Records {
		if !isValidRecordIDString(item.ID) {
			fail(w, 422, "record ID must be 1–128 UTF-8 bytes without control characters")
			return
		}
		if item.Data == nil {
			fail(w, 422, "each batch record must include a data object")
			return
		}
		if err := validateData(grant.Schema, item.Data); err != nil {
			fail(w, 422, err.Error())
			return
		}
		encoded, err := json.Marshal(item.Data)
		if err != nil {
			fail(w, 422, "cannot encode record")
			return
		}
		ops = append(ops, wireBatchOp{
			Key:   recordKey(grant.ProjectID, grant.CollectionID, item.ID),
			Value: string(encoded),
		})
	}
	for _, delID := range env.Deletes {
		if !isValidRecordIDString(delID) {
			fail(w, 422, "record ID must be 1–128 UTF-8 bytes without control characters")
			return
		}
		ops = append(ops, wireBatchOp{
			Delete: true,
			Key:    recordKey(grant.ProjectID, grant.CollectionID, delID),
		})
	}
	if err := s.client.Batch(ops); err != nil {
		log.Printf("gateway: storage batch write failed: %v", err)
		fail(w, 503, "storage engine unavailable")
		return
	}
	writeJSON(w, 201, map[string]int{
		"written": len(env.Records),
		"deleted": len(env.Deletes),
	})
}

func (s *Server) purgeCollection(w http.ResponseWriter, r *http.Request) {
	grant, ok := s.authorized(w, r, true)
	if !ok {
		return
	}
	deleted, err := s.client.DeletePrefix(collectionKey(grant.ProjectID, grant.CollectionID))
	if err != nil {
		log.Printf("gateway: collection purge failed: %v", err)
		fail(w, 503, "storage engine unavailable")
		return
	}
	writeJSON(w, 200, map[string]int{"deleted": deleted})
}

func (s *Server) compactCollection(w http.ResponseWriter, r *http.Request) {
	_, ok := s.authorized(w, r, true)
	if !ok {
		return
	}
	if err := s.client.Compact(); err != nil {
		log.Printf("gateway: compact failed: %v", err)
		fail(w, 503, "storage engine unavailable")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "compacted"})
}

func (s *Server) collectionStats(w http.ResponseWriter, r *http.Request) {
	_, ok := s.authorized(w, r, false)
	if !ok {
		return
	}
	stats, err := s.client.Stats()
	if err != nil {
		fail(w, 503, "storage engine unavailable")
		return
	}
	writeJSON(w, 200, stats)
}

func (s *Server) getRecord(w http.ResponseWriter, r *http.Request) {
	grant, ok := s.authorized(w, r, false)
	record := r.PathValue("record")
	if !ok || !validRecordID(w, record) {
		return
	}
	value, found, err := s.client.Get(recordKey(grant.ProjectID, grant.CollectionID, record))
	if err != nil {
		fail(w, 503, "storage engine unavailable")
		return
	}
	if !found {
		fail(w, 404, "record not found")
		return
	}
	var data map[string]any
	if decodeStoredObject(value, &data) != nil || data == nil {
		fail(w, 500, "corrupt record data")
		return
	}
	writeJSON(w, 200, recordEnvelope{ID: record, Data: data})
}

func (s *Server) deleteRecord(w http.ResponseWriter, r *http.Request) {
	grant, ok := s.authorized(w, r, true)
	record := r.PathValue("record")
	if !ok || !validRecordID(w, record) {
		return
	}
	if err := s.client.Delete(recordKey(grant.ProjectID, grant.CollectionID, record)); err != nil {
		fail(w, 503, "storage engine unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type recordEnvelope struct {
	ID   string         `json:"id"`
	Data map[string]any `json:"data"`
}

func (s *Server) listRecords(w http.ResponseWriter, r *http.Request) {
	grant, ok := s.authorized(w, r, false)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			fail(w, 422, "limit must be between 1 and 100")
			return
		}
		limit = n
	}
	prefix := collectionKey(grant.ProjectID, grant.CollectionID)
	after := r.URL.Query().Get("after")
	afterKey := ""
	if after != "" {
		if !validRecordID(w, after) {
			return
		}
		// Compare full encoded keys, excluding the cursor itself.
		afterKey = recordKey(grant.ProjectID, grant.CollectionID, after)
	}
	kvs, err := s.client.Scan(prefix, afterKey, limit+1)
	if err != nil {
		fail(w, 503, "storage engine unavailable")
		return
	}
	next := ""
	if len(kvs) > limit {
		kvs = kvs[:limit]
		_, next = splitRecordKey(kvs[len(kvs)-1].Key)
	}
	records := make([]recordEnvelope, 0, len(kvs))
	for _, kv := range kvs {
		_, record := splitRecordKey(kv.Key)
		var data map[string]any
		if decodeStoredObject(kv.Value, &data) != nil || data == nil {
			fail(w, 500, "corrupt record data")
			return
		}
		records = append(records, recordEnvelope{ID: record, Data: data})
	}
	writeJSON(w, 200, map[string]any{"records": records, "next_cursor": next})
}

// splitRecordKey reverses recordKey for listing output.
func splitRecordKey(key string) (collection, record string) {
	// p:<hex>:c:<hex>:r:<hex>
	rest := strings.TrimPrefix(key, "p:")
	parts := strings.SplitN(rest, ":c:", 2)
	if len(parts) != 2 {
		return "", ""
	}
	tail := strings.SplitN(parts[1], ":r:", 2)
	if len(tail) != 2 {
		return "", ""
	}
	_, _ = hex.DecodeString(parts[0])
	collectionBytes, _ := hex.DecodeString(tail[0])
	recordBytes, _ := hex.DecodeString(tail[1])
	return string(collectionBytes), string(recordBytes)
}

func (s *Server) handler() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /v1/data/{project}/{collection}/batch", s.batchRecords)
	mux.HandleFunc("POST /v1/data/{project}/{collection}/compact", s.compactCollection)
	mux.HandleFunc("GET /v1/stats/{project}/{collection}", s.collectionStats)
	mux.HandleFunc("DELETE /v1/data/{project}/{collection}", s.purgeCollection)
	mux.HandleFunc("PUT /v1/data/{project}/{collection}/{record}", s.putRecord)
	mux.HandleFunc("GET /v1/data/{project}/{collection}/{record}", s.getRecord)
	mux.HandleFunc("DELETE /v1/data/{project}/{collection}/{record}", s.deleteRecord)
	mux.HandleFunc("GET /v1/data/{project}/{collection}", s.listRecords)
	return mux
}

func main() {
	addr := flag.String("addr", ":8100", "HTTP listen address")
	storage := flag.String("storage", "storage:9100", "storage engine address")
	controlPlane := flag.String("control-plane", envOr("CONTROL_PLANE_URL", "http://api:8000"), "control-plane base URL")
	flag.Parse()
	auth, err := newAuthorizer(*controlPlane)
	if err != nil {
		log.Fatal(err)
	}
	s := &Server{client: &engineClient{addr: *storage}, auth: auth}
	server := &http.Server{
		Addr:              *addr,
		Handler:           s.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16384,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("kestrel gateway listening on %s (authorization delegated to control plane)", *addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
