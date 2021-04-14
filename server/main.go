package main

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/caarlos0/env/v6"
	pb "github.com/episub/gedoc/gedoc/lib"
	"github.com/gofrs/uuid"
	grpcMiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcOpentracing "github.com/grpc-ecosystem/go-grpc-middleware/tracing/opentracing"
	"github.com/h2non/filetype"
	"github.com/opentracing/opentracing-go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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
	PdfBlankPath string `env:"PDF_BLANK_PATH" envDefault:"/gedoc/blank.pdf"`
	HumanLogs    bool   `env:"HUMAN" envDefault:"false"`
}

var cfg config

type server struct{}

// BuildLatex Implements BuildLatex, taking some files and returning a PDF
func (s *server) BuildLatex(ctx context.Context, in *pb.BuildLatexRequest) (*pb.FileReply, error) {
	span, _ := opentracing.StartSpanFromContext(ctx, "BuildLatex")
	defer span.Finish()

	final, err := buildLatexPDF(opentracing.ContextWithSpan(ctx, span), in.Files)

	note := "build successful"

	if err != nil {
		note = err.Error()
	}

	reply := &pb.FileReply{
		Data:    final,
		Success: err == nil,
		Note:    note,
	}

	return reply, nil
}

// Merge Merges the provided files into a single PDF
func (s *server) Merge(ctx context.Context, in *pb.MergeRequest) (*pb.FileReply, error) {
	span, _ := opentracing.StartSpanFromContext(ctx, "MergePDF")
	defer span.Finish()

	final, err := mergeFiles(opentracing.ContextWithSpan(ctx, span), in.Files, in.ForceEven)

	note := "merge successful"

	if err != nil {
		log.Error().Err(err).Msg("merge failed")
		note = err.Error()
	}

	reply := &pb.FileReply{
		Data:    final,
		Success: err == nil,
		Note:    note,
	}

	return reply, nil
}

// Health Implements health, and simply returns true for now.  If server is unreachable, no reply will be given
func (s *server) Health(ctx context.Context, _ *pb.HealthRequest) (*pb.HealthReply, error) {
	span, _ := opentracing.StartSpanFromContext(ctx, "rpc_Health")
	defer span.Finish()

	return &pb.HealthReply{Healthy: true}, nil
}

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	log.Info().Msg("gedoc service starting")

	err := env.Parse(&cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("parsing env vars")
	}

	if cfg.Debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	if cfg.HumanLogs {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	tracer, closer := initJaeger(cfg.ServiceName)
	defer func(closer io.Closer) {
		err := closer.Close()
		if err != nil {
			log.Fatal().Err(err).Msg("jaeger init")
		}
	}(closer)

	// StartSpanFromContext uses the global tracer, so we need to set it here to
	// be our jaeger tracer
	opentracing.SetGlobalTracer(tracer)

	// Start router for reporting and metrics
	go startRouters(tracer)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.ExternalPort))
	if err != nil {
		log.Fatal().Err(err).Msg("failed to listen")
	}
	s := grpc.NewServer(
		grpc.StreamInterceptor(grpcMiddleware.ChainStreamServer(
			grpcOpentracing.StreamServerInterceptor(),
		)),
		grpc.UnaryInterceptor(grpcMiddleware.ChainUnaryServer(
			grpcOpentracing.UnaryServerInterceptor(),
		)),
		grpc.MaxRecvMsgSize(1024000000),
	)
	pb.RegisterBuilderServer(s, &server{})
	// Register reflection service on gRPC server.
	reflection.Register(s)

	go gracefulStopChecker(s)

	if err := s.Serve(lis); err != nil {
		log.Fatal().Err(err).Msg("failed to serve")
	}
}

func gracefulStopChecker(s *grpc.Server) {
	var gracefulStop = make(chan os.Signal)
	signal.Notify(gracefulStop, syscall.SIGTERM)
	signal.Notify(gracefulStop, syscall.SIGKILL)
	signal.Notify(gracefulStop, syscall.SIGINT)

	sig := <-gracefulStop
	log.Info().Str("signal", sig.String()).Msg("caught sig")
	if s != nil {
		s.GracefulStop()
	}
	os.Exit(0)
}

func buildLatexPDF(ctx context.Context, files []*pb.File) ([]byte, error) {
	span, _ := opentracing.StartSpanFromContext(ctx, "buildLatexPDF")
	defer span.Finish()

	var final []byte

	id, err := uuid.NewV4()
	if err != nil {
		return final, err
	}

	resultFileName := id.String() + ".pdf"

	directory, err := ioutil.TempDir("", "buildLatexPDF")
	if err != nil {
		return final, err
	}
	directoryLogger := log.With().Str("directory", directory).Logger()
	directoryLogger.Info().Msg("temp directory created")
	defer func(path string) {
		directoryLogger.Info().Msg("removing temp directory")
		err := os.RemoveAll(path)
		if err != nil {
			directoryLogger.Error().Err(err).Msg("temp directory")
		}
	}(directory)

	if len(files) == 0 {
		return final, fmt.Errorf("must provide one or more files")
	}

	// Use our predefined settings
	err = copyLatexSettings(directory)
	if err != nil {
		return final, err
	}

	// Create the provided files in a unique folder
	for _, f := range files {
		err := ioutil.WriteFile(directory+"/"+f.Name, f.Data, os.ModePerm)

		if err != nil {
			return final, err
		}
	}

	// Clean, and then run the build
	clean := exec.Command("latexmk", "-C")
	cmd := exec.Command("latexmk", fmt.Sprintf("-jobname=%s", id))

	cmd.Dir = directory
	clean.Dir = directory

	log.Info().Msg("cleaning")
	out, err := clean.Output()
	if err != nil {
		log.Error().Err(err).Str("stdout", string(out)).Msg("running latexmk clean")
		return final, err
	}

	log.Printf("building")
	out, err = cmd.Output()
	if err != nil {
		log.Error().Err(err).Str("stdout", string(out)).Msg("running latexmk build")
		return final, err
	}

	// Load the produced PDF to return
	return ioutil.ReadFile(directory + "/" + resultFileName)
}

func copyLatexSettings(folder string) error {
	var latexMakeConfig = []byte(`
$pdf_mode = 1;
$pdflatex=q/xelatex -synctex=1 %O %S/
`)

	dest, err := os.Create(folder + "/.latexmkrc")

	if err != nil {
		return err
	}

	_, err = dest.Write(latexMakeConfig)

	return err
}

func mergeFiles(ctx context.Context, files []*pb.File, forceEven bool) ([]byte, error) {
	span, _ := opentracing.StartSpanFromContext(ctx, "mergeFiles")
	defer span.Finish()

	var merged []byte
	var prepared [][]byte // We need to store each file as a PDF first before merging

	id, err := uuid.NewV4()
	if err != nil {
		return merged, err
	}

	directory, err := ioutil.TempDir("", "mergeFiles")
	if err != nil {
		return merged, err
	}
	directoryLogger := log.With().Str("directory", directory).Logger()
	directoryLogger.Info().Msg("temp directory created")
	defer func(path string) {
		directoryLogger.Info().Msg("removing temp directory")
		err := os.RemoveAll(path)
		if err != nil {
			directoryLogger.Error().Err(err).Msg("temp directory")
		}
	}(directory)

	for _, f := range files {
		kind, unknown := filetype.Match(f.Data)

		if unknown != nil {
			return merged, fmt.Errorf("file type for %s unsupported", f.Name)
		}

		switch kind.Extension {
		case "pdf":
			prepared = append(prepared, f.Data)
		case "jpg", "png":
			converted, err := imageToPDF(f.Data)
			if err != nil {
				return merged, fmt.Errorf("failed to convert image %s to pdf: %s", f.Name, err)
			}
			prepared = append(prepared, converted)
		}

		log.Info().
			Str("file_type", kind.MIME.Value).
			Str("extension", kind.Extension).
			Str("filename", f.Name).
			Msg("file info")
	}

	// Create the provided files in a unique folder, and note their names
	outputFileName := id.String() + ".pdf"
	var args = []string{
		"--empty",
		outputFileName,
		"--pages",
	}
	for i, p := range prepared {
		where := fmt.Sprintf("%s/%d.pdf", directory, i)
		pdfFileName := fmt.Sprintf("%d.pdf", i)

		log.Debug().Int("bytes", len(p)).Str("file_location", where).Msg("writing file")
		if err := ioutil.WriteFile(where, p, os.ModePerm); err != nil {
			return merged, err
		}

		if forceEven {
			wd, _ := os.Getwd()
			log.Debug().Str("working_dir", wd).Msg("")

			// read file back and check page number, if odd then merge blank.pdf to the end
			cmd := exec.Command("qpdf", "--show-npages", pdfFileName)
			cmd.Dir = directory
			out, err := cmd.Output()
			if err != nil {
				return merged, fmt.Errorf("exec qpdf page count: %v", err)
			}

			pageCount, err := strconv.Atoi(strings.TrimSpace(string(out)))
			if err != nil {
				return merged, fmt.Errorf("show-npages output to int: %v", err)
			}

			isOdd := pageCount%2 == 1
			log.Debug().
				Str("pdf_filename", pdfFileName).
				Int("page_count", pageCount).
				Bool("is_odd", isOdd).
				Msg("pdf stats")
			if isOdd {
				blankMergeCmd := exec.Command("qpdf", "--replace-input", pdfFileName, "--pages", pdfFileName, cfg.PdfBlankPath, "--")
				blankMergeCmd.Dir = directory
				if err := blankMergeCmd.Run(); err != nil {
					return merged, fmt.Errorf("adding blank to odd numberd pdf %d: %v", i, err)
				}
			}
		}

		args = append(args, pdfFileName)
	}

	args = append(args, "--")

	cmd := exec.Command("qpdf", args...)
	cmd.Dir = directory

	// Merge the files
	log.Debug().Str("cmd", cmd.String()).Msg("running merge command")
	if err = cmd.Run(); err != nil && !strings.Contains(err.Error(), "exit status 3") {
		return merged, fmt.Errorf("failed merging pdf files: %s", err)
	}

	// Load the produced PDF to return
	merged, err = ioutil.ReadFile(directory + "/" + outputFileName)

	if err != nil {
		return merged, fmt.Errorf("failed reading produced PDF: %s", err)
	}

	return merged, nil
}

func imageToPDF(file []byte) ([]byte, error) {
	var pdf []byte

	id, err := uuid.NewV4()
	if err != nil {
		return pdf, err
	}

	resultFileName := id.String() + ".pdf"

	directory, err := ioutil.TempDir("", "imageToPDF")
	if err != nil {
		return pdf, err
	}
	directoryLogger := log.With().Str("directory", directory).Logger()
	directoryLogger.Info().Msg("temp directory created")
	defer func(path string) {
		directoryLogger.Info().Msg("removing temp directory")
		err := os.RemoveAll(path)
		if err != nil {
			directoryLogger.Error().Err(err).Msg("temp directory")
		}
	}(directory)

	// Save image so we can work with it
	err = ioutil.WriteFile(directory+"/img", file, os.ModePerm)
	if err != nil {
		return pdf, err
	}

	cmd := exec.Command(
		"convert",
		"img",
		"-resize",
		"595x842",
		"-background",
		"white",
		"-page",
		"a4",
		resultFileName,
	)
	cmd.Dir = directory

	// Create pdf from image
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		return pdf, fmt.Errorf(err.Error() + ": " + stderr.String())
	}

	return ioutil.ReadFile(directory + "/" + resultFileName)
}
