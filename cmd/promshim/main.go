package main

import (
	"log"
	"net/http"
	"os"

	"github.com/BadLiveware/promshim-ch/internal/promshim"
)

func main() {
	opts, err := promshim.LoadOptionsFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	handler, err := promshim.NewHandler(opts)
	if err != nil {
		log.Fatal(err)
	}

	addr := ":9090"
	if value := os.Getenv("PROM_SHIM_LISTEN_ADDR"); value != "" {
		addr = value
	}

	server := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	log.Printf("starting promshim on %s", addr)
	log.Fatal(server.ListenAndServe())
}
