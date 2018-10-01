package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/grpc"

	pb "github.com/episub/gedoc/gedoc/lib"
	"github.com/go-chi/chi"
	grpc_opentracing "github.com/grpc-ecosystem/go-grpc-middleware/tracing/opentracing"
	"github.com/opentracing-contrib/go-stdlib/nethttp"
	opentracing "github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	address = "localhost:50051"
)

// startRouters Starts each of the external and internal routers
func startRouters(tracer opentracing.Tracer) {
	log.Println("Starting routers")
	internalRouter := newRouter(tracer)
	internalRouter.Get("/health", healthHandler)
	internalRouter.Get("/live", liveHandler)
	internalRouter.Handle("/metrics", promhttp.Handler())

	log.Println(fmt.Sprintf("internal endpoints available on port %d", cfg.InternalPort))
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", cfg.InternalPort), internalRouter))
}

// newRouter returns a new router with all default values set
func newRouter(tracer opentracing.Tracer) chi.Router {
	router := chi.NewRouter()
	router.Use(Opentracing(tracer))

	return router
}

// Opentracing Adds opentracing to context
func Opentracing(tracer opentracing.Tracer) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return nethttp.Middleware(tracer, next)
	}
}

// liveHandler Returns true when the service is live and ready to receive requests
// Works by acting as a client, and actually performing a request
func liveHandler(w http.ResponseWriter, r *http.Request) {
	span, _ := opentracing.StartSpanFromContext(r.Context(), "liveHandler")
	defer span.Finish()

	log.Printf("Liveness request received")
	healthy, err := checkGRPCHealth(opentracing.ContextWithSpan(r.Context(), span))

	if err != nil {
		log.Warningf("Liveness report (%t) had error: %s", healthy, err)
	}

	log.Printf("Health reply: %t", healthy)

	if !healthy {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// healthHandler Returns true when the service is live and ready to receive requests
// Works by acting as a client, and actually performing a request
func healthHandler(w http.ResponseWriter, r *http.Request) {
	span, _ := opentracing.StartSpanFromContext(r.Context(), "healthHandler")
	defer span.Finish()

	log.Printf("Health request received")
	healthy, err := checkGRPCHealth(opentracing.ContextWithSpan(r.Context(), span))

	if err != nil {
		log.Warningf("Liveness report (%t) had error: %s", healthy, err)
	}

	log.Printf("Health reply: %t", healthy)

	if !healthy {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func checkGRPCHealth(ctx context.Context) (bool, error) {
	span, _ := opentracing.StartSpanFromContext(ctx, "checkGRPCHealth")
	defer span.Finish()

	// Set up a local grpc client so that server can query itself for liveness.  This is a better simulation, to ensure that the grpc server is still receiving at least some requests
	//conn, err := grpc.Dial(address, grpc.WithInsecure())
	conn, err := createClientGRPCConn(opentracing.ContextWithSpan(ctx, span), address)

	if err != nil {
		return false, err
	}

	defer conn.Close()
	c := pb.NewBuilderClient(conn)

	ctx, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	r, err := c.Health(opentracing.ContextWithSpan(ctx, span), &pb.HealthRequest{})

	if err != nil {
		return false, err
	}

	return r.Healthy, nil
}

// ctx is the incoming gRPC request's context
// addr is the address for the new outbound request
func createClientGRPCConn(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	span, _ := opentracing.StartSpanFromContext(ctx, "createGRPCConn")
	defer span.Finish()

	var opts []grpc.DialOption

	opts = append(opts, grpc.WithStreamInterceptor(grpc_opentracing.StreamClientInterceptor()))
	opts = append(opts, grpc.WithUnaryInterceptor(grpc_opentracing.UnaryClientInterceptor()))

	opts = append(opts, grpc.WithInsecure())
	if !cfg.Debug {
		log.Warning("Using grpc.WithInsecure()")
	}

	conn, err := grpc.DialContext(ctx, addr, opts...)
	if err != nil {
		log.Error("Failed to connect to application addr: ", err)
		return nil, err
	}
	return conn, nil
}
