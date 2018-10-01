package main

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/caarlos0/env"
	pb "github.com/episub/gedoc/gedoc/lib"
	"github.com/gofrs/uuid"
	"github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_opentracing "github.com/grpc-ecosystem/go-grpc-middleware/tracing/opentracing"
	"github.com/opentracing/opentracing-go"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

type config struct {
	ExternalPort int    `env:"PORT" envDefault:"50051"`
	InternalPort int    `env:"INTERNAL_PORT" envDefault:"50052"`
	Debug        bool   `env:"DEBUG" envDefault:"false"`
	DebugSpans   bool   `env:"DEBUG_SPANS" envDefault:"false"`
	ServiceName  string `env:"SERVICE_NAME" envDefault:"gedoc"`
}

var cfg config
var log = logrus.New()

// server is used to implement helloworld.GreeterServer.
type server struct{}

// BuildLatex Implements BuildLatex, taking some files and reteurning a PDF
func (s *server) BuildLatex(ctx context.Context, in *pb.BuildLatexRequest) (*pb.BuildReply, error) {
	span, _ := opentracing.StartSpanFromContext(ctx, "rpc_BuildLatex")
	defer span.Finish()

	final, err := buildLatexPDF(opentracing.ContextWithSpan(ctx, span), in.Files)

	note := "Build successful"

	if err != nil {
		note = err.Error()
	}

	reply := &pb.BuildReply{
		Data:     final,
		FileType: "PDF",
		Success:  (err == nil),
		Note:     note,
	}

	return reply, nil
}

// Health Implements health, and simply returns true for now.  If server is unreachable, no reply will be given
func (s *server) Health(ctx context.Context, in *pb.HealthRequest) (*pb.HealthReply, error) {
	span, _ := opentracing.StartSpanFromContext(ctx, "rpc_Health")
	defer span.Finish()

	return &pb.HealthReply{Healthy: true}, nil
}

func main() {
	log.Infof("gedoc service")
	err := env.Parse(&cfg)

	if err != nil {
		log.Fatal(err)
	}

	tracer, closer := initJaeger(cfg.ServiceName)
	defer closer.Close()

	// StartSpanFromContext uses the global tracer, so we need to set it here to
	// be our jaeger tracer
	opentracing.SetGlobalTracer(tracer)

	// Start router for reporting and metrics
	go startRouters(tracer)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.ExternalPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer(
		grpc.StreamInterceptor(grpc_middleware.ChainStreamServer(
			grpc_opentracing.StreamServerInterceptor(),
		)),
		grpc.UnaryInterceptor(grpc_middleware.ChainUnaryServer(
			grpc_opentracing.UnaryServerInterceptor(),
		)),
	)
	pb.RegisterBuilderServer(s, &server{})
	// Register reflection service on gRPC server.
	reflection.Register(s)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func buildLatexPDF(ctx context.Context, files []*pb.File) ([]byte, error) {
	span, _ := opentracing.StartSpanFromContext(ctx, "buildLatexPDF")
	defer span.Finish()

	var final []byte

	// To allow multiple PDF's to be built at once, build in unique folders
	v4, err := uuid.NewV4()

	if err != nil {
		return final, err
	}

	folder := v4.String()

	log.Printf("Folder: %s", folder)

	err = os.MkdirAll("build/"+folder, 0766)

	if err != nil && !strings.Contains(fmt.Sprintf("%s", err), "file exists") {
		return final, err
	}

	if len(files) == 0 {
		return final, fmt.Errorf("Must provide one or more files")
	}

	// Use our predefined settings
	err = copyLatexSettings(folder)
	if err != nil {
		return final, err
	}

	// Create the provided files in a unique folder
	for _, f := range files {
		err := ioutil.WriteFile("build/"+folder+"/"+f.Name, f.Data, os.ModePerm)

		if err != nil {
			return final, err
		}
	}

	// Clean, and then run the build
	clean := exec.Command("latexmk", "-C")
	cmd := exec.Command("latexmk", fmt.Sprintf("-jobname=%s", folder))

	cmd.Dir = "build/" + folder
	clean.Dir = "build/" + folder

	log.Printf("Cleaning...")
	err = clean.Run()

	if err != nil {
		return final, err
	}

	log.Printf("Building...")
	err = cmd.Run()

	if err != nil {
		return final, err
	}

	// Load the produced PDF to return
	final, err = ioutil.ReadFile("build/" + folder + "/" + folder + ".pdf")

	if err != nil {
		return final, err
	}

	// Remove temporary folder
	err = os.RemoveAll("build/" + folder)

	return final, err
}

func copyLatexSettings(folder string) error {
	source, err := ioutil.ReadFile("build/.latexmkrc")

	if err != nil {
		return err
	}

	dest, err := os.Create("build/" + folder + "/.latexmkrc")

	if err != nil {
		return err
	}

	_, err = dest.Write(source)

	return err
}
