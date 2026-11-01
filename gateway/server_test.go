package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer(t *testing.T, status int) (*Server, *httptest.Server, *int) {
	t.Helper()
	writes := 0
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writes++
		if r.Header.Get("Authorization") != "Bearer valid" || !strings.Contains(r.URL.Path, "/p1/collections/c1/access") {
			w.WriteHeader(401)
			return
		}
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		writeJSON(w, 200, accessGrant{ProjectID: "p1", CollectionID: "c1", Name: "notes", Schema: collectionSchema{
			Type: "object", Properties: map[string]struct {
				Type string `json:"type"`
			}{"title": {Type: "string"}}, Required: []string{"title"},
		}})
	}))
	t.Cleanup(control.Close)
	auth, err := newAuthorizer(control.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{auth: auth, client: &engineClient{addr: "127.0.0.1:1"}}, control, &writes
}

func request(s *Server, method, path, body, token string) *httptest.ResponseRecorder {
	mux := s.handler()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestGatewayFailsClosedBeforeStorage(t *testing.T) {
	for _, status := range []int{401, 403, 404, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s, _, _ := testServer(t, status)
			for _, method := range []string{"PUT", "GET", "DELETE"} {
				w := request(s, method, "/v1/data/p1/c1/r1", `{"data":{"title":"secret"}}`, "valid")
				want := status
				if status == 503 {
					want = 503
				}
				if w.Code != want {
					t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body)
				}
			}
			w := request(s, "GET", "/v1/data/p1/c1", "", "valid")
			if w.Code != status {
				t.Fatalf("list status=%d", w.Code)
			}
		})
	}
}

func TestGatewayRequiresTokenAndWriteMembership(t *testing.T) {
	s, control, writes := testServer(t, 200)
	if w := request(s, "PUT", "/v1/data/p1/c1/r1", `{"data":{"title":"x"}}`, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if *writes != 0 {
		t.Fatal("unauthenticated request reached control plane")
	}
	w := request(s, "PUT", "/v1/data/p1/c1/r1", `{"data":{"title":"x"}}`, "valid")
	// Authorization must succeed, but the deliberately unavailable engine must fail.
	if w.Code != 503 {
		t.Fatalf("expected storage failure after valid schema, got %d: %s", w.Code, w.Body)
	}
	control.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*writes++
		if r.URL.Query().Get("write") != "true" {
			t.Error("write request did not require write access")
		}
		w.WriteHeader(403)
	})
	if w := request(s, "DELETE", "/v1/data/p1/c1/r1", "", "valid"); w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestGatewayValidatesBeforeWriting(t *testing.T) {
	s, _, _ := testServer(t, 200)
	for _, body := range []string{`{"data":{}}`, `{"data":{"title":4}}`, `{"data":{"title":"x","extra":1}}`, `{"data":null}`, `{"data":{},"extra":1}`, `{} {}`} {
		if w := request(s, "PUT", "/v1/data/p1/c1/r1", body, "valid"); w.Code != 422 {
			t.Fatalf("body=%s status=%d", body, w.Code)
		}
	}
}

func TestGatewayBoundsListAndRejectsRawScan(t *testing.T) {
	s, _, _ := testServer(t, 200)
	if w := request(s, "GET", "/v1/data/p1/c1?limit=101", "", "valid"); w.Code != 422 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/v1/scan", `{"prefix":""}`, "valid"); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestAuthorizerRejectsUnsafeURLs(t *testing.T) {
	for _, url := range []string{"file:///tmp/x", "https://user:pass@example.com", "http://example.com?token=x", "not-a-url"} {
		if _, err := newAuthorizer(url); err == nil {
			t.Fatalf("accepted %s", url)
		}
	}
}

func TestCursorAndKeyIsolation(t *testing.T) {
	a := collectionKey("p1", "c1")
	b := collectionKey("p2", "c1")
	if strings.HasPrefix(b, a) || recordKey("p1", "c1", "r") == recordKey("p1", "c2", "r") {
		t.Fatal("namespace collision")
	}
	key := recordKey("p1", "c1", "record/with:segments")
	_, id := splitRecordKey(key)
	if id != "record/with:segments" {
		t.Fatal(id)
	}
	data, err := decodeData(strings.NewReader(`{"data":{"x":9007199254740993}}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(data)
	if !strings.Contains(string(encoded), "9007199254740993") {
		t.Fatal("large integer lost precision")
	}
}

func TestGatewayBatchPurgeAndStatsAuthorizationAndValidation(t *testing.T) {
	s, _, _ := testServer(t, 200)
	if w := request(s, "POST", "/v1/data/p1/c1/batch", `{"records":[{"id":"r1","data":{"title":"ok"}}]}`, ""); w.Code != 401 {
		t.Fatalf("expected 401 for unauthenticated batch, got %d", w.Code)
	}
	for _, badBatch := range []string{
		`{"records":[]}`,
		`{"records":[{"id":"","data":{"title":"x"}}]}`,
		`{"records":[{"id":"r1","data":{"title":123}}]}`,
		`{"records":[{"id":"r1","data":{"unknown":"x"}}]}`,
	} {
		if w := request(s, "POST", "/v1/data/p1/c1/batch", badBatch, "valid"); w.Code != 422 {
			t.Fatalf("batch=%s got status=%d", badBatch, w.Code)
		}
	}
	// Valid batch passes authorization and schema check, then hits offline engine (503).
	if w := request(s, "POST", "/v1/data/p1/c1/batch", `{"records":[{"id":"r1","data":{"title":"valid"}}],"deletes":["r2"]}`, "valid"); w.Code != 503 {
		t.Fatalf("expected 503 for valid batch against offline engine, got %d: %s", w.Code, w.Body)
	}
	if w := request(s, "DELETE", "/v1/data/p1/c1", "", "valid"); w.Code != 503 {
		t.Fatalf("expected 503 for authorized collection purge against offline engine, got %d", w.Code)
	}
	if w := request(s, "GET", "/v1/stats/p1/c1", "", "valid"); w.Code != 503 {
		t.Fatalf("expected 503 for authorized stats against offline engine, got %d", w.Code)
	}
}
