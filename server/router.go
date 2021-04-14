package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/grpc"

	pb "github.com/episub/gedoc/gedoc/lib"
	"github.com/go-chi/chi"
	grpcOpentracing "github.com/grpc-ecosystem/go-grpc-middleware/tracing/opentracing"
	"github.com/opentracing-contrib/go-stdlib/nethttp"
	"github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

const (
	address = "localhost:50051"
)

// startRouters Starts each of the external and internal routers
func startRouters(tracer opentracing.Tracer) {
	log.Info().Msg("Starting routers")
	internalRouter := newRouter(tracer)
	internalRouter.Get("/health", healthHandler)
	internalRouter.Get("/live", liveHandler)
	internalRouter.Handle("/metrics", promhttp.Handler())

	log.Info().Int("internal_port", cfg.InternalPort).Int("external_port", cfg.ExternalPort).Msg("listening on ports")
	err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.InternalPort), internalRouter)
	if err != nil {
		log.Fatal().Err(err).Msg("server crashed")
	}
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

	log.Info().Msg("liveness request received")
	healthy, err := checkGRPCHealth(opentracing.ContextWithSpan(r.Context(), span))

	if err != nil {
		log.Warn().Bool("healthy", healthy).Err(err).Msg("liveness report encountered error")
	}

	log.Info().Bool("healthy", healthy).Msg("health reply")

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
		log.Warn().Bool("healthy", healthy).Err(err).Msg("liveness report encountered error")
	}

	log.Info().Bool("healthy", healthy).Msg("health reply")

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
	defer func(conn *grpc.ClientConn) {
		err := conn.Close()
		if err != nil {
			log.Fatal().Err(err).Msg("closing grpc client connection")
		}
	}(conn)

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

	opts = append(opts, grpc.WithStreamInterceptor(grpcOpentracing.StreamClientInterceptor()))
	opts = append(opts, grpc.WithUnaryInterceptor(grpcOpentracing.UnaryClientInterceptor()))

	opts = append(opts, grpc.WithInsecure())
	if !cfg.Debug {
		log.Warn().Msg("using grpc in insecure mode")
	}

	conn, err := grpc.DialContext(ctx, addr, opts...)
	if err != nil {
		log.Error().Err(err).Msg("connection to application address")
		return nil, err
	}
	return conn, nil
}
