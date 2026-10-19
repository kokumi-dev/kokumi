package scanner

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Run runs the scanner command with args, without the program name:
//
//	install <path>   copies the running binary to path, for an init
//	                 container that hands it to the Trivy image
//	[serve] [flags]  serves the scanner API until the process is stopped
func Run(ctx context.Context, args []string, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "install" {
		if len(args) != 2 {
			return errors.New("usage: scanner install <path>")
		}
		return install(args[1])
	}
	if len(args) > 0 && args[0] == "serve" {
		args = args[1:]
	}

	fs := flag.NewFlagSet("scanner", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", ":8080", "address to serve the scanner API on")
	cacheDir := fs.String("cache-dir", "/cache", "directory for the vulnerability databases and the images being scanned")
	trivyBinary := fs.String("trivy", "trivy", "path of the Trivy binary")
	maxConcurrent := fs.Int("max-concurrent", 2, "requests scanned at the same time; more are answered 503")
	opts := zap.Options{}
	opts.BindFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	log.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	logger := log.Log.WithName("scanner")

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	engine, err := NewTrivy(ctx, *trivyBinary, *cacheDir)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           NewHandler(engine, *maxConcurrent, logger),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errs := make(chan error, 1)
	go func() {
		logger.Info("Serving the scanner API", "address", *listen, "trivy", engine.version, "cacheDir", *cacheDir)
		errs <- srv.ListenAndServe()
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	// Scans in flight get a moment to finish; the manager retries the rest.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("shutting down: %w", err)
	}
	return nil
}

// install copies the running binary to path, executable by everyone.
func install(path string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating the scanner binary: %w", err)
	}
	src, err := os.Open(self)
	if err != nil {
		return fmt.Errorf("opening %s: %w", self, err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("copying to %s: %w", path, err)
	}
	return dst.Close()
}
