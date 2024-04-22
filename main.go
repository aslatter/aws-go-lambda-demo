package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"

	"github.com/elnormous/contenttype"
	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/aslatter/aws-go-lambda-demo/internal/mlambda"
)

func main() {
	err := mainErr()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func mainErr() error {
	ctx, close := signal.NotifyContext(context.Background(), unix.SIGINT, unix.SIGTERM)
	defer close()

	// fake rest-like API
	mux := &http.ServeMux{}
	mux.HandleFunc("POST /thing", func(w http.ResponseWriter, r *http.Request) {
		if err := checkRequestJSON(r); err != nil {
			w.WriteHeader(400)
			fmt.Fprintln(w, "error parsing request: ", err.Error())
			return
		}

		w.Header().Add("content-type", "application/json")
		w.WriteHeader(201)
		fmt.Fprintln(w, `{"id": "1234567"}`)
	})
	mux.HandleFunc("GET /thing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("content-type", "application/json")
		w.WriteHeader(200)
		fmt.Fprintln(w, `[{"id":"1"},{"id":"2"},{"id":"3"}]`)
	})
	mux.HandleFunc("PUT /thing/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := checkRequestJSON(r); err != nil {
			w.WriteHeader(400)
			fmt.Fprintln(w, "error parsing request: ", err.Error())
			return
		}

		id := r.PathValue("id")
		if id == "" {
			w.WriteHeader(400)
			fmt.Fprintln(w, "Missing id-path-component")
			return
		}
		w.Header().Add("content-type", "application/json")
		w.WriteHeader(200)
		fmt.Fprintf(w, "{\"id\":%s}\n", jsonQuote(id))
	})
	mux.HandleFunc("GET /thing/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			w.WriteHeader(400)
			fmt.Fprintln(w, "Missing id-path-component")
			return
		}
		w.Header().Add("content-type", "application/json")
		w.WriteHeader(200)
		fmt.Fprintf(w, "{\"id\":%s}\n", jsonQuote(id))
	})
	mux.HandleFunc("DELETE /thing/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			w.WriteHeader(400)
			fmt.Fprintln(w, "Missing id-path-component")
			return
		}
	})
	mux.Handle("/", http.NotFoundHandler())

	// wrap the mux with some handling to prove we can work with http-headers
	availableMediaTypes := []contenttype.MediaType{contenttype.NewMediaType("application/json")}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			if r.Header.Get("content-type") != "application/json" {
				w.WriteHeader(400)
				fmt.Fprintln(w, "content-type header must be application/json")
				return
			}
		}
		if r.Method == http.MethodGet {
			_, _, err := contenttype.GetAcceptableMediaType(r, availableMediaTypes)
			if err != nil {
				w.WriteHeader(400)
				fmt.Fprintln(w, "accept header must be application/json")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})

	srv := mlambda.Server{
		Handler: mlambda.HttpHandler(handler),
	}

	return srv.Start(ctx)
}

func jsonQuote(s string) string {
	b, _ := jsontext.AppendQuote(nil, s)
	return string(b)
}

func checkRequestJSON(r *http.Request) error {
	var v any
	return json.UnmarshalRead(r.Body, &v)
}
