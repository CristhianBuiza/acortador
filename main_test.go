package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const testToken = "secreto-de-prueba"

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	store, err := openStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv, err := newServer(config{baseURL: "https://s.ejemplo.com", adminToken: testToken}, store)
	if err != nil {
		t.Fatal(err)
	}
	return srv.routes()
}

func do(t *testing.T, h http.Handler, method, path, body string, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth {
		req.Header.Set("Authorization", "Bearer "+testToken)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCreateAndRedirect(t *testing.T) {
	h := newTestServer(t)

	if rec := do(t, h, "POST", "/api/links", `{"url":"https://example.com/a"}`, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("sin token: esperaba 401, obtuve %d", rec.Code)
	}

	rec := do(t, h, "POST", "/api/links", `{"url":"https://example.com/a?b=1"}`, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("crear: esperaba 201, obtuve %d: %s", rec.Code, rec.Body)
	}
	var link linkResponse
	json.NewDecoder(rec.Body).Decode(&link)
	if len(link.Code) != codeLength || link.ShortURL != "https://s.ejemplo.com/"+link.Code {
		t.Fatalf("respuesta inesperada: %+v", link)
	}

	for range 2 {
		rec = do(t, h, "GET", "/"+link.Code, "", false)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://example.com/a?b=1" {
			t.Fatalf("redirect: %d %q", rec.Code, rec.Header().Get("Location"))
		}
	}

	rec = do(t, h, "GET", "/api/links", "", true)
	var links []linkResponse
	json.NewDecoder(rec.Body).Decode(&links)
	if len(links) != 1 || links[0].Clicks != 2 {
		t.Fatalf("listado: %+v", links)
	}
}

func TestAlias(t *testing.T) {
	h := newTestServer(t)

	cases := []struct {
		body string
		want int
	}{
		{`{"url":"https://example.com","alias":"promo"}`, http.StatusCreated},
		{`{"url":"https://example.com","alias":"promo"}`, http.StatusConflict},
		{`{"url":"https://example.com","alias":"api"}`, http.StatusBadRequest},
		{`{"url":"https://example.com","alias":"a b"}`, http.StatusBadRequest},
		{`{"url":"https://example.com","alias":"x"}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		if rec := do(t, h, "POST", "/api/links", c.body, true); rec.Code != c.want {
			t.Errorf("%s: esperaba %d, obtuve %d", c.body, c.want, rec.Code)
		}
	}

	if rec := do(t, h, "GET", "/promo", "", false); rec.Code != http.StatusFound {
		t.Fatalf("alias redirect: %d", rec.Code)
	}
}

func TestInvalidURLs(t *testing.T) {
	h := newTestServer(t)
	for _, u := range []string{"", "ftp://x.com", "javascript:alert(1)", "example.com", "https://s.ejemplo.com/abc", "https://" + strings.Repeat("a", maxURLLen)} {
		body, _ := json.Marshal(createRequest{URL: u})
		if rec := do(t, h, "POST", "/api/links", string(body), true); rec.Code != http.StatusBadRequest {
			t.Errorf("%q: esperaba 400, obtuve %d", u, rec.Code)
		}
	}
}

func TestDelete(t *testing.T) {
	h := newTestServer(t)
	do(t, h, "POST", "/api/links", `{"url":"https://example.com","alias":"borrar"}`, true)

	if rec := do(t, h, "DELETE", "/api/links/borrar", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("borrar sin token: %d", rec.Code)
	}
	if rec := do(t, h, "DELETE", "/api/links/borrar", "", true); rec.Code != http.StatusNoContent {
		t.Fatalf("borrar: %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/borrar", "", false); rec.Code != http.StatusNotFound {
		t.Fatalf("tras borrar: %d", rec.Code)
	}
}

func TestStaticAndIndex(t *testing.T) {
	h := newTestServer(t)
	for _, p := range []string{"/", "/static/app.js", "/static/style.css", "/healthz"} {
		if rec := do(t, h, "GET", p, "", false); rec.Code != http.StatusOK {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	if rec := do(t, h, "GET", "/no-existe", "", false); rec.Code != http.StatusNotFound {
		t.Errorf("código inexistente: %d", rec.Code)
	}
}

func TestGetLink(t *testing.T) {
	h := newTestServer(t)
	do(t, h, "POST", "/api/links", `{"url":"https://example.com/x","alias":"leer"}`, true)

	if rec := do(t, h, "GET", "/api/links/leer", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("leer sin token: %d", rec.Code)
	}
	rec := do(t, h, "GET", "/api/links/leer", "", true)
	var link linkResponse
	json.NewDecoder(rec.Body).Decode(&link)
	if rec.Code != http.StatusOK || link.URL != "https://example.com/x" || link.ShortURL != "https://s.ejemplo.com/leer" {
		t.Fatalf("leer: %d %+v", rec.Code, link)
	}
	if rec := do(t, h, "GET", "/api/links/no-existe", "", true); rec.Code != http.StatusNotFound {
		t.Fatalf("leer inexistente: %d", rec.Code)
	}
}

func TestRateLimit(t *testing.T) {
	h := newTestServer(t)
	for i := range 40 {
		if code := do(t, h, "GET", "/api/links", "", true).Code; code != http.StatusOK {
			t.Fatalf("petición autenticada %d limitada: %d", i+1, code)
		}
	}
	var last int
	for range 31 {
		last = do(t, h, "GET", "/api/links", "", false).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("esperaba 429 tras 30 intentos sin token, obtuve %d", last)
	}
}

func TestRandomCode(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		c, err := randomCode(codeLength)
		if err != nil || len(c) != codeLength || !aliasRe.MatchString(c) || seen[c] {
			t.Fatalf("código inválido o repetido: %q %v", c, err)
		}
		seen[c] = true
	}
}
