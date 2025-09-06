package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"unglued/internal/httpx"
	"unglued/internal/store"
)

func main() {
	var listenAddr string
	var publicBase string
	flag.StringVar(&listenAddr, "listen", ":8080", "HTTP listen address")
	flag.StringVar(&publicBase, "public", "", "public base URL (e.g. https://paste.example.com)")
	flag.Parse()

	st := store.New(30 * time.Second)
	defer st.Close()

	// ⬇️ Templates laden und an den Server übergeben
	indexTmpl, viewTmpl, editTmpl := httpx.LoadTemplates()

	srv := httpx.NewServer(
		httpx.Config{PublicBase: publicBase},
		st,
		indexTmpl, viewTmpl, editTmpl,
	)

	r := chi.NewRouter()
	r.Use(httpx.NoIndex)
	httpx.MountRoutes(r, srv)

	log.Printf("HTTP: http://localhost%s\n", listenAddr)
	httpSrv := &http.Server{Addr: listenAddr, Handler: r}

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}

