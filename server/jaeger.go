package main

import (
	"fmt"
	"io"

	"github.com/opentracing/opentracing-go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	jaegerConfig "github.com/uber/jaeger-client-go/config"
)

// jaegerLogger Logger to wrap logrus so we can use with Jaeger
type jaegerLogger struct {
	logger *zerolog.Logger
}

func (j *jaegerLogger) Error(msg string) {
	j.logger.Error().Msg(msg)
}

func (j *jaegerLogger) Infof(msg string, args ...interface{}) {
	j.logger.Info().Interface("args", args).Msg(msg)
}

func initJaeger(service string) (opentracing.Tracer, io.Closer) {
	jcfg := &jaegerConfig.Configuration{
		Sampler: &jaegerConfig.SamplerConfig{
			Type:  "const",
			Param: 1,
		},
		Reporter: &jaegerConfig.ReporterConfig{
			LogSpans: cfg.DebugSpans,
		},
	}

	jl := jaegerLogger{&log.Logger}

	tracer, closer, err := jcfg.New(service, jaegerConfig.Logger(&jl))
	if err != nil {
		panic(fmt.Sprintf("ERROR: cannot init Jaeger: %v\n", err))
	}

	return tracer, closer
}
