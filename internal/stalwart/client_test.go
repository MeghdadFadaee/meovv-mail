package stalwart

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBootstrapUsesPinnedManagementJMAPEndpoint(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/jmap" {
			t.Errorf("path = %s, want /jmap", r.URL.Path)
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "admin" || password != "recovery-secret" {
			t.Errorf("unexpected recovery authentication: %q %q %v", username, password, ok)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}

		var request struct {
			Using       []string            `json:"using"`
			MethodCalls [][]json.RawMessage `json:"methodCalls"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.MethodCalls) != 1 || len(request.MethodCalls[0]) != 3 {
			t.Fatalf("unexpected method calls: %#v", request.MethodCalls)
		}
		var method string
		if err := json.Unmarshal(request.MethodCalls[0][0], &method); err != nil {
			t.Fatal(err)
		}
		if method != "x:Bootstrap/set" {
			t.Errorf("method = %q, want x:Bootstrap/set", method)
		}
		var arguments struct {
			Update map[string]struct {
				Tracer map[string]any `json:"tracer"`
			} `json:"update"`
		}
		if err := json.Unmarshal(request.MethodCalls[0][1], &arguments); err != nil {
			t.Fatal(err)
		}
		if got := arguments.Update["singleton"].Tracer["@type"]; got != "Stdout" {
			t.Errorf("tracer type = %v, want Stdout", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"methodResponses":[["x:Bootstrap/set",{"updated":{"singleton":{"username":"admin@example.com","secret":"generated"}}},"bootstrap"]]}`)
	}))
	defer server.Close()

	client := New(server.URL, false)
	if _, err := client.Bootstrap(context.Background(), server.URL, "admin:recovery-secret", "mail.example.com", "example.com"); err != nil {
		t.Fatal(err)
	}
}

func TestProxyManagementUsesJMAPEndpoint(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jmap" {
			t.Errorf("path = %s, want /jmap", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = io.WriteString(w, `{"methodResponses":[]}`)
	}))
	defer server.Close()

	client := New(server.URL, false)
	status, _, _, err := client.ProxyManagement(context.Background(), "access-token", []byte(`{"methodCalls":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
}
