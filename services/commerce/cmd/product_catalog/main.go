// Command product_catalog is the Commerce product catalogue service.
//
// It serves the HTTP API defined by product.proto. The gRPC surface declared in
// that file is not yet served: it needs a gRPC runtime, and go.mod currently
// carries no third-party dependencies. See the note on grpc in the README.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ecommerce/libs/go/logging"
	obslogging "github.com/ecommerce/platform/observability/logging"
	"github.com/ecommerce/services/commerce/internal/handler"
	"github.com/ecommerce/services/commerce/internal/service"
	"github.com/ecommerce/services/commerce/internal/store"
)

// serviceName is the logical service name, used in logs and metrics.
const serviceName = "product-catalog"

// Tunables, overridable by environment so that the same binary works in
// development and in production without a rebuild.
const (
	defaultAddr         = ":8080"
	defaultReadTimeout  = 15 * time.Second
	defaultWriteTimeout = 30 * time.Second
	defaultIdleTimeout  = 60 * time.Second
	defaultShutdownWait = 20 * time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "product-catalog: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	level, ok := logging.ParseLevel(envOrDefault("LOG_LEVEL", "INFO"))
	if !ok {
		fmt.Fprintf(os.Stderr, "product-catalog: unknown LOG_LEVEL, defaulting to INFO\n")
	}

	// One process-wide logger, created once and passed down explicitly. A
	// package-level logger would be untestable and would hide the dependency.
	logger := logging.NewLogger(logging.Config{
		Level:   level,
		Service: serviceName,
	})
	defer func() { _ = logger.Sync() }()

	// Root context cancelled on SIGINT/SIGTERM. Everything downstream inherits
	// it, so a shutdown propagates instead of leaving goroutines running.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The store is injected so the business logic is testable without one.
	productStore := store.NewMemoryStore()
	productService := service.NewProductService(productStore)
	productHandler := handler.NewProductHandler(productService, nil)

	mux := http.NewServeMux()
	productHandler.Routes(mux)
	// The platform observability capability is used where an explicit writer
	// and request context are available. Middleware here keeps every request
	// accounted for without threading a logger through each handler.
	observability := newObservabilityMiddleware(logger, level)

	srv := &http.Server{
		Addr:              envOrDefault("HTTP_ADDR", defaultAddr),
		Handler:           observability(mux),
		ReadHeaderTimeout: defaultReadTimeout,
		ReadTimeout:       defaultReadTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
		ErrorLog:          nil,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("starting server", map[string]interface{}{"addr": srv.Addr, "store": "memory"})
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received", map[string]interface{}{"grace": defaultShutdownWait.String()})
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownWait)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		// A failed graceful shutdown still has to release the listener, or the
		// process hangs instead of exiting.
		logger.Error("graceful shutdown failed, forcing close", map[string]interface{}{"error": err.Error()})
		if closeErr := srv.Close(); closeErr != nil {
			return fmt.Errorf("shutdown: %w (close: %v)", err, closeErr)
		}
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	logger.Info("shutdown complete")
	return nil
}

// newObservabilityMiddleware logs one line per request and records latency.
//
// It uses the platform observability capability rather than the process logger
// so that the same structured format is emitted regardless of which logger the
// handler was constructed with.
func newObservabilityMiddleware(logger *logging.Logger, level logging.Level) func(http.Handler) http.Handler {
	requestLogger := obslogging.NewLogger(serviceName, obslogging.Level(level))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(recorder, r)

			elapsed := time.Since(started)
			fields := map[string]interface{}{
				"method":   r.Method,
				"path":     r.URL.Path,
				"status":   recorder.status,
				"duration": elapsed.String(),
			}
			if recorder.status >= http.StatusInternalServerError {
				requestLogger.Error(r.Context(), "request failed", fields)
			} else {
				requestLogger.Info(r.Context(), "request served", fields)
			}
			logger.Debug("http", fields)
		})
	}
}

// statusRecorder captures the status code a handler wrote.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(b)
}

func envOrDefault(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// Unused today, but kept honest: if the addr ever becomes configurable per
// environment, this is where it is validated.
var _ = strconv.Itoa
