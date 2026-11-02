package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	port := flag.String("port", envOr("PORT", "8080"), "puerto HTTP")
	dbPath := flag.String("db", envOr("DB_PATH", "./data/qrgen.db"), "ruta del archivo SQLite")
	flag.Parse()

	if err := openStore(*dbPath); err != nil {
		log.Fatalf("no se pudo abrir la base de datos: %v", err)
	}
	go func() {
		for {
			purgeExpiredSessions()
			time.Sleep(12 * time.Hour)
		}
	}()

	mux := http.NewServeMux()

	// paginas
	mux.HandleFunc("GET /{$}", handleIndex)
	mux.HandleFunc("GET /cuenta", handleCuentaPage)

	// codigos QR
	mux.HandleFunc("GET /api/qr", rateLimit(handleQR, 60))
	mux.HandleFunc("POST /api/qr", rateLimit(handleQR, 60))

	// cuenta
	mux.HandleFunc("POST /api/auth/registro", rateLimit(handleRegistro, 10))
	mux.HandleFunc("POST /api/auth/login", rateLimit(handleLogin, 10))
	mux.HandleFunc("POST /api/auth/logout", handleLogout)
	mux.HandleFunc("GET /api/auth/me", handleMe)

	// codigos guardados
	mux.HandleFunc("GET /api/codigos", handleListCodes)
	mux.HandleFunc("POST /api/codigos", rateLimit(handleSaveCode, 30))
	mux.HandleFunc("GET /api/codigos/{id}", handleGetCode)
	mux.HandleFunc("PATCH /api/codigos/{id}", handleUpdateCode)
	mux.HandleFunc("DELETE /api/codigos/{id}", handleDeleteCode)
	mux.HandleFunc("GET /api/codigos/{id}/qr", handleCodeImage)

	// enlace corto de los QR dinamicos
	mux.HandleFunc("GET /r/{slug}", handleRedirect)

	// varios
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /robots.txt", handleRobots)

	srv := &http.Server{
		Addr:              ":" + *port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("qr-gen escuchando en :%s (datos en %s)", *port, *dbPath)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
