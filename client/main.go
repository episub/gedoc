package main

import (
	"io/ioutil"
	"log"
	"os"
	"time"

	pb "github.com/episub/gedoc/gedoc/lib"
	"github.com/opentracing/opentracing-go"
	"golang.org/x/net/context"
)

const (
	address     = "localhost:50051"
	defaultName = "world"
)

func main() {
	// Opentracing
	tracer, closer := initJaeger("gRPCclient")
	defer closer.Close()

	// StartSpanFromContext uses the global tracer, so we need to set it here to
	// be our jaeger tracer
	opentracing.SetGlobalTracer(tracer)

	ctx := context.Background()

	mergeFiles(ctx)
	fetchPDF(ctx)
}

func mergeFiles(ctx context.Context) {
	span, _ := opentracing.StartSpanFromContext(ctx, "mergeFiles")
	defer span.Finish()

	// Set up a connection to the server.
	//conn, err := grpc.Dial(address, grpc.WithInsecure())
	conn, err := createClientGRPCConn(ctx, address)
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := pb.NewBuilderClient(conn)

	files, err := loadFiles(ctx, "./examples/merge")
	if err != nil {
		panic(err)
	}
	log.Printf("Loaded %d files.", len(files))

	// Contact the server and print out its response.
	ctx, cancel := context.WithTimeout(ctx, time.Second*60)
	defer cancel()
	r, err := c.Merge(ctx, &pb.MergeRequest{
		Files: files,
	})
	if err != nil {
		log.Fatalf("could not greet: %v", err)
	}
	log.Printf("Success: %t. Note: %s", r.Success, r.Note)

	if r.Success {
		log.Printf("Saving")
		err := ioutil.WriteFile("merged.pdf", r.Data, os.ModePerm)

		if err != nil {
			log.Println("Error saving: ", err)
		}
	}
}

func fetchPDF(ctx context.Context) {
	span, _ := opentracing.StartSpanFromContext(ctx, "fetchPDF")
	defer span.Finish()

	// Set up a connection to the server.
	//conn, err := grpc.Dial(address, grpc.WithInsecure())
	conn, err := createClientGRPCConn(ctx, address)
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := pb.NewBuilderClient(conn)

	files, err := loadFiles(ctx, "./examples/latex")
	if err != nil {
		panic(err)
	}
	log.Printf("Loaded %d files.", len(files))

	// Contact the server and print out its response.
	ctx, cancel := context.WithTimeout(ctx, time.Second*60)
	defer cancel()
	r, err := c.BuildLatex(ctx, &pb.BuildLatexRequest{
		Files: files,
	})
	if err != nil {
		log.Fatalf("could not greet: %v", err)
	}
	log.Printf("Success: %t. Note: %s", r.Success, r.Note)

	if r.Success {
		log.Printf("Saving")
		err := ioutil.WriteFile("saved.pdf", r.Data, os.ModePerm)

		if err != nil {
			log.Println("Error saving: ", err)
		}
	}
}

// loadFiles loads all files in the listed folder, returning their bytes in an array
func loadFiles(ctx context.Context, folder string) ([]*pb.File, error) {
	span, _ := opentracing.StartSpanFromContext(ctx, "loadFiles")
	defer span.Finish()

	var files []*pb.File
	fileNames, err := ioutil.ReadDir(folder)

	if err != nil {
		return files, err
	}

	for _, file := range fileNames {
		if !file.IsDir() {
			fileBytes, err := ioutil.ReadFile(folder + "/" + file.Name())
			if err != nil {
				return files, err
			}

			files = append(files, &pb.File{Name: file.Name(), Data: fileBytes})
		}
	}

	return files, nil
}
