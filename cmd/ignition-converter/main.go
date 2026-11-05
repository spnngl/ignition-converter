// Command ignition-converter is a mutating admission webhook that rewrites the
// Ignition spec 2.x user data Cluster API stores in bootstrap Secrets to spec
// 3.0, the only family Fedora CoreOS accepts.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// version is the release, set by the release build:
// go build -ldflags "-X main.version=<version>".
var version = "dev"

func main() {
	addr := flag.String("addr", ":8443", "listen address")
	certFile := flag.String("cert-file", "", "TLS certificate file")
	keyFile := flag.String("key-file", "", "TLS private key file")
	oneShot := flag.Bool("convert", false, "convert the Ignition config on stdin to stdout, then exit (for tests)")
	printVersion := flag.Bool("version", false, "print the version, then exit")
	flag.Parse()
	if *printVersion {
		fmt.Println("ignition-converter", version)
		return
	}
	if *oneShot {
		in, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Fatal(err)
		}
		out, converted, err := convert(in)
		if err != nil {
			log.Fatal(err)
		}
		if !converted {
			out = in
		}
		if _, err := os.Stdout.Write(out); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *certFile == "" || *keyFile == "" {
		log.Fatal("--cert-file and --key-file are required")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /mutate", handleMutate)
	mux.HandleFunc("GET /healthz", handleHealthz)

	srv := &http.Server{
		Addr:    *addr,
		Handler: mux,
		// The API server gives up on a webhook after its timeoutSeconds, 30 s at
		// most: a connection still reading or writing past that is stuck.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		// Closes the API server's unused keep-alive connections; it redials.
		IdleTimeout: 2 * time.Minute,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			// Reload on every handshake so cert-manager renewals need no restart.
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
				if err != nil {
					return nil, err
				}
				return &cert, nil
			},
		},
	}
	if err := serve(srv); err != nil {
		log.Fatal(err)
	}
}

// serve runs srv until SIGTERM (the kubelet stopping the pod) or SIGINT, then
// shuts it down: it accepts no new connection and answers the reviews in
// flight, which the signal's default action would cut.
func serve(srv *http.Server) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	errc := make(chan error, 1)
	// Certificates come from TLSConfig, hence the empty file arguments.
	go func() { errc <- srv.ListenAndServeTLS("", "") }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	// WriteTimeout bounds a review in flight.
	ctx, cancel := context.WithTimeout(context.Background(), srv.WriteTimeout)
	defer cancel()
	return srv.Shutdown(ctx)
}
