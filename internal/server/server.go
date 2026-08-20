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
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/kokumi-dev/kokumi/internal/namespace"
)

type Config struct {
	Host string
	Port string
}

func NewServer(
	config *Config,
	h *hub,
	deps *apiDeps,
	auth *authenticator,
	installNamespace string,
) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, h, deps, auth, installNamespace)
	var handler http.Handler = mux
	if auth != nil {
		handler = auth.middleware(handler)
	}
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

	opts := zap.Options{
		Development: true,
	}

	log.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	config := &Config{
		Host: "0.0.0.0",
		Port: "8080",
	}

	logger := log.FromContext(ctx)

	h := newHub()
	deps, err := startK8sWatcher(ctx, logger, h)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Warning: failed to start Kubernetes watcher: %s\n", err)
	}

	installNamespace := namespace.Current(getenv)
	var auth *authenticator
	if deps != nil {
		secretName := authSecretName(getenv)
		if a, aerr := loadAuthenticator(ctx, deps.writer, installNamespace, secretName); aerr != nil {
			logger.Info("Authentication disabled", "reason", aerr.Error())
		} else {
			applyAuthConfig(a, getenv)
			auth = a
			logger.Info("Authentication enabled", "namespace", installNamespace, "secret", secretName)
		}
	}

	srv := NewServer(config, h, deps, auth, installNamespace)
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
