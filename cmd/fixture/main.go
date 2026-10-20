// Fixture is a local HTTP target for exercising the automation engine.
package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

func main() {
	var mu sync.Mutex
	calls := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("GET /flaky", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		key := r.Header.Get("Idempotency-Key")
		calls[key]++
		n := calls[key]
		mu.Unlock()
		if n == 1 {
			http.Error(w, "indisponível na primeira tentativa", 503)
			return
		}
		fmt.Fprintln(w, "recuperado")
	})
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(10 * time.Second):
			fmt.Fprintln(w, "resposta lenta")
		}
	})
	mux.HandleFunc("GET /large", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat("x", 8192)) })
	log.Println("destino de demonstração: http://127.0.0.1:19091")
	log.Fatal((&http.Server{Addr: "127.0.0.1:19091", Handler: mux, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe())
}
