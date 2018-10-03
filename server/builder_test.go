package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "github.com/episub/gedoc/gedoc/lib"
)

const (
	testAddress = "localhost:50051"
)

func init() {
	// Start server to accept requests for testing
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", 50051))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer()
	pb.RegisterBuilderServer(s, &server{})
	// Register reflection service on gRPC server.
	reflection.Register(s)
	go func() {
		if err := s.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	// Give time for server to start listening
	time.Sleep(time.Second * 3)
}

// TestBuildLatex Test building pdf from latex
func TestBuildLatex(t *testing.T) {
	conn, err := grpc.Dial(testAddress, grpc.WithInsecure())
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()

	c := pb.NewBuilderClient(conn)

	files, err := loadFiles("../examples/latex")
	if err != nil {
		t.Error(err)
		return
	}

	r, err := c.BuildLatex(context.Background(), &pb.BuildLatexRequest{
		Files: files,
	})

	if err != nil {
		t.Error(fmt.Sprintf("Build failed with error %s and note %s", err.Error(), r.Note))
		return
	}

	if !r.Success {
		wd, _ := os.Getwd()
		t.Errorf("Expected success, but failed with note %s.  Working directory: %s", r.Note, wd)
	}

	if len(r.Data) < 10000 {
		t.Errorf("With length of %d, payload is smaller than expected", len(r.Data))
	}
}

// TestMergeFiles Send some files, and test merging them
func TestMergeFiles(t *testing.T) {
	conn, err := grpc.Dial(testAddress, grpc.WithInsecure())
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()

	c := pb.NewBuilderClient(conn)

	files, err := loadFiles("../examples/merge")
	if err != nil {
		t.Error(err)
		return
	}

	r, err := c.Merge(context.Background(), &pb.MergeRequest{
		Files: files,
	})

	if err != nil {
		t.Error(err)
		return
	}

	if !r.Success {
		t.Errorf("Expected success, but failed")
	}

	if len(r.Data) < 10000 {
		t.Errorf("With length of %d, payload is smaller than expected", len(r.Data))
	}
}

// loadFiles loads all files in the listed folder, returning their bytes in an array
func loadFiles(folder string) ([]*pb.File, error) {
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
