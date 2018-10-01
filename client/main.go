package main

import (
	"io/ioutil"
	"log"
	"os"
	"time"

	pb "github.com/episub/gedoc/gedoc/lib"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

const (
	address     = "localhost:50051"
	defaultName = "world"
)

func main() {
	// Set up a connection to the server.
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := pb.NewBuilderClient(conn)

	files, err := loadFiles("./examples/latex")
	if err != nil {
		panic(err)
	}
	log.Printf("Loaded %d files.", len(files))

	// Contact the server and print out its response.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*60)
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
			panic(err)
		}
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
