package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

type Config struct {
	Host string
	Port string
}

func NewServer(
	config *Config,
	h *hub,
) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, h)
	var handler http.Handler = mux
	return handler
}

func Run(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	config := &Config{
		Host: "0.0.0.0",
		Port: "8080",
	}

	logger := log.FromContext(ctx)

	h := newHub()
	if err := startK8sWatcher(ctx, logger, h); err != nil {
		_, _ = fmt.Fprintf(stderr, "Warning: failed to start Kubernetes watcher: %s\n", err)
	}

	srv := NewServer(config, h)
	httpServer := &http.Server{
		Addr:    net.JoinHostPort(config.Host, config.Port),
		Handler: srv,
	}

	go func() {
		logger.Info("Starting HTTP server", "host", config.Host, "port", config.Port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			_, _ = fmt.Fprintf(stderr, "Error listening and serving: %s\n", err)
		}
	}()

	var wg sync.WaitGroup
	wg.Go(func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error shutting down HTTP server: %s\n", err)
		}
	})
	wg.Wait()
	return nil
}
