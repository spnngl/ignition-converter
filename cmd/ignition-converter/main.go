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
	"net"
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
	// SIGTERM: the kubelet stopping the pod.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *addr)
	if err == nil {
		err = serve(ctx, srv, ln)
	}
	stop()
	if err != nil {
		log.Fatal(err)
	}
}

// shutdownTimeout bounds the wait for the reviews in flight: the API server
// stops waiting after the webhook's timeoutSeconds (10 in deploy.yaml), and the
// kubelet kills the pod after terminationGracePeriodSeconds (30 by default,
// deploy.yaml's preStop sleep included).
const shutdownTimeout = 10 * time.Second

// serve serves srv over TLS on ln until ctx ends, then shuts it down: it
// accepts no new connection and answers the reviews in flight, which a
// signal's default action would cut. It returns nil after a clean shutdown.
func serve(ctx context.Context, srv *http.Server, ln net.Listener) error {
	errc := make(chan error, 1)
	// Certificates come from TLSConfig, hence the empty file arguments.
	go func() { errc <- srv.ServeTLS(ln, "", "") }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(ctx)
}
