package main

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

const (
	codeLength = 7
	maxURLLen  = 2048
	listLimit  = 200
)

var (
	aliasRe  = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)
	reserved = map[string]bool{
		"api": true, "static": true, "healthz": true, "admin": true,
		"favicon.ico": true, "robots.txt": true,
	}
)

type server struct {
	cfg     config
	store   *Store
	limiter *limiter
	static  http.Handler
	index   []byte
	ownHost string
}

func newServer(cfg config, store *Store) (*server, error) {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, err
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(cfg.baseURL)
	if err != nil {
		return nil, err
	}
	return &server{
		cfg:     cfg,
		store:   store,
		limiter: newLimiter(30, time.Minute),
		static:  http.FileServerFS(sub),
		index:   index,
		ownHost: strings.ToLower(base.Hostname()),
	}, nil
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.Handle("GET /static/", http.StripPrefix("/static/", s.static))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("POST /api/links", s.guard(s.handleCreate))
	mux.HandleFunc("GET /api/links", s.guard(s.handleList))
	mux.HandleFunc("GET /api/links/{code}", s.guard(s.handleGet))
	mux.HandleFunc("DELETE /api/links/{code}", s.guard(s.handleDelete))
	mux.HandleFunc("GET /{code}", s.handleRedirect)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(s.index)
}

func (s *server) handleRedirect(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !aliasRe.MatchString(code) {
		http.NotFound(w, r)
		return
	}
	target, err := s.store.Resolve(r.Context(), code)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("resolver %q: %v", code, err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	// 302 sin caché para que cada visita llegue al backend y cuente el clic.
	w.Header().Set("Cache-Control", "private, no-store")
	http.Redirect(w, r, target, http.StatusFound)
}

type createRequest struct {
	URL   string `json:"url"`
	Alias string `json:"alias"`
}

type linkResponse struct {
	Link
	ShortURL string `json:"short_url"`
}

func (s *server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	target, err := s.validateURL(req.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	alias := strings.TrimSpace(req.Alias)
	if alias != "" {
		if !aliasRe.MatchString(alias) {
			writeError(w, http.StatusBadRequest, "el alias debe tener 3-32 caracteres: letras, números, - o _")
			return
		}
		if reserved[strings.ToLower(alias)] {
			writeError(w, http.StatusBadRequest, "ese alias está reservado")
			return
		}
		link, err := s.store.Create(r.Context(), alias, target)
		if errors.Is(err, ErrExists) {
			writeError(w, http.StatusConflict, "ese alias ya está en uso")
			return
		}
		if err != nil {
			s.internalError(w, "crear link", err)
			return
		}
		writeJSON(w, http.StatusCreated, s.toResponse(link))
		return
	}

	for range 5 {
		code, err := randomCode(codeLength)
		if err != nil {
			s.internalError(w, "generar código", err)
			return
		}
		link, err := s.store.Create(r.Context(), code, target)
		if errors.Is(err, ErrExists) {
			continue
		}
		if err != nil {
			s.internalError(w, "crear link", err)
			return
		}
		writeJSON(w, http.StatusCreated, s.toResponse(link))
		return
	}
	s.internalError(w, "crear link", errors.New("demasiadas colisiones de código"))
}

func (s *server) handleList(w http.ResponseWriter, r *http.Request) {
	links, err := s.store.List(r.Context(), listLimit)
	if err != nil {
		s.internalError(w, "listar links", err)
		return
	}
	out := make([]linkResponse, len(links))
	for i, l := range links {
		out[i] = s.toResponse(l)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleGet(w http.ResponseWriter, r *http.Request) {
	link, err := s.store.Get(r.Context(), r.PathValue("code"))
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "no existe")
		return
	}
	if err != nil {
		s.internalError(w, "leer link", err)
		return
	}
	writeJSON(w, http.StatusOK, s.toResponse(link))
}

func (s *server) handleDelete(w http.ResponseWriter, r *http.Request) {
	err := s.store.Delete(r.Context(), r.PathValue("code"))
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "no existe")
		return
	}
	if err != nil {
		s.internalError(w, "borrar link", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) validateURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("falta la URL")
	}
	if len(raw) > maxURLLen {
		return "", errors.New("la URL es demasiado larga")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("la URL debe empezar con http:// o https://")
	}
	if strings.EqualFold(u.Hostname(), s.ownHost) {
		return "", errors.New("no se puede acortar un link del propio acortador")
	}
	return u.String(), nil
}

func (s *server) toResponse(l Link) linkResponse {
	return linkResponse{Link: l, ShortURL: s.cfg.baseURL + "/" + l.Code}
}

// guard exige el token. El límite por IP solo cuenta las peticiones sin token
// válido (todas, si no hay ADMIN_TOKEN): frena la fuerza bruta sin limitar a
// los clientes autenticados, que pueden crear muchos links seguidos.
func (s *server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.adminToken != "" && s.authorized(r) {
			next(w, r)
			return
		}
		if !s.limiter.allow(s.clientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "demasiadas peticiones, espera un minuto")
			return
		}
		if s.cfg.adminToken != "" {
			writeError(w, http.StatusUnauthorized, "token de acceso inválido")
			return
		}
		next(w, r)
	}
}

func (s *server) authorized(r *http.Request) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.adminToken)) == 1
}

func (s *server) clientIP(r *http.Request) string {
	if s.cfg.trustProxy {
		if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
			return ip
		}
		if ip := r.Header.Get("X-Real-IP"); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *server) internalError(w http.ResponseWriter, what string, err error) {
	log.Printf("%s: %v", what, err)
	writeError(w, http.StatusInternalServerError, "error interno")
}

const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomCode(n int) (string, error) {
	// Rechazo por encima del mayor múltiplo del alfabeto para evitar sesgo.
	limit := byte(256 - 256%len(alphabet))
	out := make([]byte, 0, n)
	buf := make([]byte, n*2)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if b < limit {
				out = append(out, alphabet[int(b)%len(alphabet)])
				if len(out) == n {
					break
				}
			}
		}
	}
	return string(out), nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
