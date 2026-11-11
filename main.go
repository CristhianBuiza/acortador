package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type config struct {
	addr       string
	dbPath     string
	baseURL    string
	adminToken string
	trustProxy bool
}

func loadConfig() config {
	return config{
		addr:       env("ADDR", "127.0.0.1:8095"),
		dbPath:     env("DB_PATH", "acortador.db"),
		baseURL:    strings.TrimRight(env("BASE_URL", "http://127.0.0.1:8095"), "/"),
		adminToken: os.Getenv("ADMIN_TOKEN"),
		trustProxy: os.Getenv("TRUST_PROXY") == "1",
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	cfg := loadConfig()
	if cfg.adminToken == "" {
		log.Println("AVISO: ADMIN_TOKEN vacío, cualquiera puede crear y borrar links")
	}

	store, err := openStore(cfg.dbPath)
	if err != nil {
		log.Fatalf("abrir base de datos: %v", err)
	}
	defer store.Close()

	srv, err := newServer(cfg, store)
	if err != nil {
		log.Fatalf("iniciar servidor: %v", err)
	}

	httpSrv := &http.Server{
		Addr:              cfg.addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("escuchando en %s (base %s)", cfg.addr, cfg.baseURL)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("servidor: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("apagado: %v", err)
	}
}
