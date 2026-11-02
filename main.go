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
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/api/qr", rateLimit(handleQR, 60))
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/robots.txt", handleRobots)

	srv := &http.Server{
		Addr:              ":" + *port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("qr-gen escuchando en :%s", *port)
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
