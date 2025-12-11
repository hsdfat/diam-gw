package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hsdfat8/diam-gw/codegen"
)

func main() {
	var (
		protoFile   = flag.String("proto", "", "Path to the .proto file")
		outputFile  = flag.String("output", "", "Path to the output .go file")
		packageName = flag.String("package", "base", "Go package name for generated code")
	)

	flag.Parse()

	if *protoFile == "" {
		fmt.Fprintf(os.Stderr, "Error: -proto flag is required\n")
		flag.Usage()
		os.Exit(1)
	}

	if *outputFile == "" {
		// Default output file name based on proto file
		base := filepath.Base(*protoFile)
		ext := filepath.Ext(base)
		name := base[:len(base)-len(ext)]
		*outputFile = name + ".pb.go"
	}

	// Parse the proto file
	parser := codegen.NewProtoParser()
	if err := parser.ParseFile(*protoFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing proto file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Parsed %d AVPs and %d commands from %s\n",
		len(parser.AVPs), len(parser.Commands), *protoFile)

	// Generate code
	generator := codegen.NewGenerator(parser, *packageName)
	if err := generator.GenerateToFile(*outputFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating code: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated code written to %s\n", *outputFile)
}
