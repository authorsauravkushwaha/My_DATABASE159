package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
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
	secret []byte
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok","service":"kestrel-gateway"}`))
}

func (s *Server) putRecord(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	collection := r.PathValue("collection")
	record := r.PathValue("record")

	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Data == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":"request body must be {\"data\": {...}}"}`))
		return
	}
	value, err := json.Marshal(body.Data)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":"cannot encode record data"}`))
		return
	}
	if err := s.client.Set(recordKey(project, collection, record), string(value)); err != nil {
		log.Printf("gateway: set %s/%s/%s: %v", project, collection, record, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"storage engine unavailable"}`))
		return
	}
	idJSON, err := json.Marshal(record)
	if err != nil {
		http.Error(w, `{"error":"cannot encode record id"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(`{"id":` + string(idJSON) + `}`))
}

func (s *Server) getRecord(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	collection := r.PathValue("collection")
	record := r.PathValue("record")

	value, ok, err := s.client.Get(recordKey(project, collection, record))
	if err != nil {
		log.Printf("gateway: get %s/%s/%s: %v", project, collection, record, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"storage engine unavailable"}`))
		return
	}
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"record not found"}`))
		return
	}
	if _, err := json.Unmarshal([]byte(value), &map[string]any{}); err != nil {
		http.Error(w, `{"error":"corrupt record data"}`, http.StatusInternalServerError)
		return
	}
	idJSON, err := json.Marshal(record)
	if err != nil {
		http.Error(w, `{"error":"cannot encode record id"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"id":` + string(idJSON) + `,"data":` + value + `}`))
}

func (s *Server) deleteRecord(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	collection := r.PathValue("collection")
	record := r.PathValue("record")

	if err := s.client.Delete(recordKey(project, collection, record)); err != nil {
		log.Printf("gateway: delete %s/%s/%s: %v", project, collection, record, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"storage engine unavailable"}`))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type recordEnvelope struct {
	ID   string         `json:"id"`
	Data map[string]any `json:"data"`
}

func (s *Server) listRecords(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	collection := r.PathValue("collection")

	kvs, err := s.client.Scan(collectionKey(project, collection), 0)
	if err != nil {
		log.Printf("gateway: list %s/%s: %v", project, collection, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"storage engine unavailable"}`))
		return
	}
	records := make([]recordEnvelope, 0, len(kvs))
	for _, kv := range kvs {
		_, record := splitRecordKey(kv.Key)
		var data map[string]any
		if err := json.Unmarshal([]byte(kv.Value), &data); err != nil {
			continue
		}
		records = append(records, recordEnvelope{ID: record, Data: data})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(records)
}

func (s *Server) scan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prefix string `json:"prefix"`
		Limit  int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":"invalid request body"}`))
		return
	}
	kvs, err := s.client.Scan(body.Prefix, body.Limit)
	if err != nil {
		log.Printf("gateway: scan %q: %v", body.Prefix, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"storage engine unavailable"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(kvs)
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
	projectBytes, _ := hex.DecodeString(parts[0])
	collectionBytes, _ := hex.DecodeString(tail[0])
	recordBytes, _ := hex.DecodeString(tail[1])
	return string(collectionBytes), string(recordBytes)
}

func main() {
	addr := flag.String("addr", ":8100", "HTTP listen address")
	storage := flag.String("storage", "engine:9100", "storage engine address")
	jwtSecret := flag.String("jwt-secret", os.Getenv("JWT_SECRET"), "HMAC secret for JWT verification (shared with the control plane)")
	flag.Parse()
	if len(*jwtSecret) < 32 {
		log.Fatal("gateway: JWT_SECRET must be set to at least 32 characters")
	}

	s := &Server{
		client: &engineClient{addr: *storage},
		secret: []byte(*jwtSecret),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("PUT /v1/data/{project}/{collection}/{record}", requireAuth(s.secret, s.putRecord))
	mux.HandleFunc("GET /v1/data/{project}/{collection}/{record}", requireAuth(s.secret, s.getRecord))
	mux.HandleFunc("DELETE /v1/data/{project}/{collection}/{record}", requireAuth(s.secret, s.deleteRecord))
	mux.HandleFunc("GET /v1/data/{project}/{collection}", requireAuth(s.secret, s.listRecords))
	mux.HandleFunc("POST /v1/scan", requireAuth(s.secret, s.scan))

	log.Printf("kestrel gateway listening on %s (storage=%s)", *addr, *storage)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("gateway: listen: %v", err)
	}
}
