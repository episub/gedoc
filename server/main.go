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
	"github.com/h2non/filetype"
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
func (s *server) BuildLatex(ctx context.Context, in *pb.BuildLatexRequest) (*pb.FileReply, error) {
	span, _ := opentracing.StartSpanFromContext(ctx, "BuildLatex")
	defer span.Finish()

	final, err := buildLatexPDF(opentracing.ContextWithSpan(ctx, span), in.Files)

	note := "Build successful"

	if err != nil {
		note = err.Error()
	}

	reply := &pb.FileReply{
		Data:    final,
		Success: (err == nil),
		Note:    note,
	}

	return reply, nil
}

// Merge Merges the provided files into a single PDF
func (s *server) Merge(ctx context.Context, in *pb.MergeRequest) (*pb.FileReply, error) {
	span, _ := opentracing.StartSpanFromContext(ctx, "MergePDF")
	defer span.Finish()

	final, err := mergeFiles(opentracing.ContextWithSpan(ctx, span), in.Files)

	note := "Merge successful"

	if err != nil {
		log.Printf("Merge failed: %s", err)
		note = err.Error()
	}

	reply := &pb.FileReply{
		Data:    final,
		Success: (err == nil),
		Note:    note,
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
		grpc.MaxMsgSize(1024000000),
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

	// Get a new folder to work in
	id, folder, err := setupFolder()
	if err != nil {
		return final, err
	}
	defer os.RemoveAll(folder)

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
		err := ioutil.WriteFile(folder+"/"+f.Name, f.Data, os.ModePerm)

		if err != nil {
			return final, err
		}
	}

	// Clean, and then run the build
	clean := exec.Command("latexmk", "-C")
	cmd := exec.Command("latexmk", fmt.Sprintf("-jobname=%s", id))

	cmd.Dir = folder
	clean.Dir = folder

	log.Printf("Cleaning...")
	out, err := clean.Output()

	if err != nil {
		log.Printf("%s", out)
		return final, err
	}

	log.Printf("Building...")
	out, err = cmd.Output()

	if err != nil {
		log.Printf("%s", out)
		return final, err
	}

	// Load the produced PDF to return
	return ioutil.ReadFile(folder + "/" + id + ".pdf")
}

func copyLatexSettings(folder string) error {
	source, err := ioutil.ReadFile("build/.latexmkrc")

	if err != nil {
		return err
	}

	dest, err := os.Create(folder + "/.latexmkrc")

	if err != nil {
		return err
	}

	_, err = dest.Write(source)

	return err
}

func mergeFiles(ctx context.Context, files []*pb.File) ([]byte, error) {
	var merged []byte
	var prepared [][]byte // We need to store each file as a PDF first before merging

	for _, f := range files {
		kind, unknown := filetype.Match(f.Data)

		if unknown != nil {
			return merged, fmt.Errorf("File type for %s unsupported", f.Name)
		}

		switch kind.Extension {
		case "pdf":
			prepared = append(prepared, f.Data)
		case "jpg", "png":
			converted, err := imageToPDF(f.Data)
			if err != nil {
				return merged, err
			}
			prepared = append(prepared, converted)
		}

		log.Printf("File type for %s: %s (Mime: %s)", f.Name, kind.Extension, kind.MIME.Value)
	}

	// Get a new folder to work in
	id, folder, err := setupFolder()
	if err != nil {
		return merged, err
	}
	defer os.RemoveAll(folder)

	// Create the provided files in a unique folder, and note their names
	outName := id + ".pdf"
	var args = []string{
		"--empty",
		outName,
		"--pages",
	}
	for i, p := range prepared {
		where := fmt.Sprintf("%s/%d.pdf", folder, i)
		log.Printf("Writing %d bytes to %s", len(p), where)
		err := ioutil.WriteFile(where, p, os.ModePerm)

		if err != nil {
			log.Printf("ERRORRED")
			return merged, err
		}
		args = append(args, fmt.Sprintf("%d.pdf", i))
		args = append(args, "1-z")
	}

	args = append(args, "--")

	cmd := exec.Command("qpdf", args...)
	cmd.Dir = folder

	// Merge the files
	err = cmd.Run()

	if err != nil {
		return merged, err
	}

	// Load the produced PDF to return
	merged, err = ioutil.ReadFile(folder + "/" + id + ".pdf")

	if err != nil {
		return merged, err
	}

	return merged, nil
}

func imageToPDF(file []byte) ([]byte, error) {
	var pdf []byte

	// Get a new folder to work in
	id, folder, err := setupFolder()
	if err != nil {
		return pdf, err
	}
	defer os.RemoveAll(folder)

	// Save image so we can work with it
	err = ioutil.WriteFile(folder+"/img", file, os.ModePerm)

	if err != nil {
		return pdf, err
	}

	cmd := exec.Command("convert", "img", "-background", "white", "-page", "a4", id+".pdf")
	cmd.Dir = folder

	// Create pdf from image
	err = cmd.Run()

	if err != nil {
		return pdf, err
	}

	return ioutil.ReadFile(folder + "/" + id + ".pdf")
}

// setupFolder Sets up a new folder for building inside.  Returns full folder, uuid, and error
func setupFolder() (string, string, error) {
	var id, full string
	v4, err := uuid.NewV4()
	if err != nil {
		return id, full, err
	}

	id = v4.String()
	full = "build/" + id
	err = os.MkdirAll(full, 0766)

	return id, full, err
}
