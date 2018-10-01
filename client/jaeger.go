package main

import (
	"context"
	"fmt"
	"io"
	"log"

	grpc_opentracing "github.com/grpc-ecosystem/go-grpc-middleware/tracing/opentracing"
	opentracing "github.com/opentracing/opentracing-go"
	jaeger "github.com/uber/jaeger-client-go"
	config "github.com/uber/jaeger-client-go/config"
	jaegerConfig "github.com/uber/jaeger-client-go/config"
	"google.golang.org/grpc"
)

func initJaeger(service string) (opentracing.Tracer, io.Closer) {
	jcfg := &jaegerConfig.Configuration{
		Sampler: &jaegerConfig.SamplerConfig{
			Type:  "const",
			Param: 1,
		},
		Reporter: &jaegerConfig.ReporterConfig{
			LogSpans: true,
		},
	}

	tracer, closer, err := jcfg.New(service, config.Logger(jaeger.StdLogger))
	if err != nil {
		panic(fmt.Sprintf("ERROR: cannot init Jaeger: %v\n", err))
	}

	return tracer, closer
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

	conn, err := grpc.DialContext(ctx, addr, opts...)
	if err != nil {
		log.Println("Failed to connect to application addr: ", err)
		return nil, err
	}
	return conn, nil
}
