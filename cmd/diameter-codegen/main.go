package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hsdfat8/diam-gw/codegen"
)

func main() {
	var (
		protoFile   = flag.String("proto", "", "Path to a single .proto file")
		protoDir    = flag.String("proto-dir", "", "Directory containing .proto files (processes all)")
		outputDir   = flag.String("output-dir", "commands", "Base directory for generated code")
		outputFile  = flag.String("output", "", "Path to the output .go file (for single file mode)")
		packageName = flag.String("package", "", "Go package name (for single file mode, auto-detected from proto)")
		genTests    = flag.Bool("tests", false, "Generate test files with pcap writing capabilities")
	)

	flag.Parse()

	// Determine mode: single file or directory
	if *protoFile != "" {
		// Single file mode
		if err := processProtoFile(*protoFile, *outputFile, *packageName, *outputDir, *genTests); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else if *protoDir != "" {
		// Directory mode - process all .proto files
		if err := processProtoDirectory(*protoDir, *outputDir, *genTests); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Fprintf(os.Stderr, "Error: either -proto or -proto-dir flag is required\n")
		flag.Usage()
		os.Exit(1)
	}
}

func processProtoFile(protoFile, outputFile, packageName, outputDir string, genTests bool) error {
	// Parse the proto file
	parser := codegen.NewProtoParser()
	if err := parser.ParseFile(protoFile); err != nil {
		return fmt.Errorf("parsing proto file: %w", err)
	}

	fmt.Printf("Parsed %d AVPs and %d commands from %s\n",
		len(parser.AVPs), len(parser.Commands), protoFile)

	// Use package name from proto file if not specified
	if packageName == "" {
		packageName = parser.PackageName
		if packageName == "" {
			packageName = "base"
		}
	}

	// Determine output file path
	var finalOutputFile string
	if outputFile != "" {
		// User specified output file
		finalOutputFile = outputFile
	} else {
		// Auto-generate output path based on package
		if parser.PackageName != "" {
			// Create package directory
			packageDir := filepath.Join(outputDir, parser.PackageName)
			if err := os.MkdirAll(packageDir, 0755); err != nil {
				return fmt.Errorf("creating package directory: %w", err)
			}

			// Default output file name
			base := filepath.Base(protoFile)
			ext := filepath.Ext(base)
			name := base[:len(base)-len(ext)]
			finalOutputFile = filepath.Join(packageDir, name+".pb.go")
		} else {
			// Fallback to simple filename
			base := filepath.Base(protoFile)
			ext := filepath.Ext(base)
			name := base[:len(base)-len(ext)]
			finalOutputFile = name + ".pb.go"
		}
	}

	// Generate code
	generator := codegen.NewGenerator(parser, packageName)
	if err := generator.GenerateToFile(finalOutputFile); err != nil {
		return fmt.Errorf("generating code: %w", err)
	}

	fmt.Printf("Generated code written to %s\n", finalOutputFile)

	// Generate constants file
	constFile := filepath.Join(filepath.Dir(finalOutputFile), "constants.go")
	if err := generator.GenerateConstantsFile(constFile); err != nil {
		return fmt.Errorf("generating constants file: %w", err)
	}
	fmt.Printf("Generated constants file written to %s\n", constFile)

	// Generate test files if requested
	if genTests {
		baseFile := strings.TrimSuffix(finalOutputFile, ".pb.go")

		// Generate unit test file
		unitTestFile := baseFile + "_test.go"
		if err := generator.GenerateUnitTestFile(unitTestFile); err != nil {
			return fmt.Errorf("generating unit test file: %w", err)
		}
		fmt.Printf("Generated unit test file written to %s\n", unitTestFile)

		// Generate benchmark test file
		benchmarkTestFile := baseFile + "_benchmark_test.go"
		if err := generator.GenerateBenchmarkTestFile(benchmarkTestFile); err != nil {
			return fmt.Errorf("generating benchmark test file: %w", err)
		}
		fmt.Printf("Generated benchmark test file written to %s\n", benchmarkTestFile)

		// Generate pcap test file
		pcapTestFile := baseFile + "_pcap_test.go"
		if err := generator.GeneratePcapTestFile(pcapTestFile); err != nil {
			return fmt.Errorf("generating pcap test file: %w", err)
		}
		fmt.Printf("Generated pcap test file written to %s\n", pcapTestFile)
	}

	return nil
}

func processProtoDirectory(protoDir, outputDir string, genTests bool) error {
	// Find all .proto files in the directory
	protoFiles, err := filepath.Glob(filepath.Join(protoDir, "*.proto"))
	if err != nil {
		return fmt.Errorf("finding proto files: %w", err)
	}

	if len(protoFiles) == 0 {
		return fmt.Errorf("no .proto files found in directory: %s", protoDir)
	}

	fmt.Printf("Found %d proto files in %s\n", len(protoFiles), protoDir)

	// First pass: Parse ALL proto files into a complete AVP registry
	completeAVPs := make(map[string]*codegen.AVPDefinition)

	for _, protoFile := range protoFiles {
		parser := codegen.NewProtoParser()
		if err := parser.ParseFile(protoFile); err != nil {
			return fmt.Errorf("parsing %s: %w", filepath.Base(protoFile), err)
		}
		// Merge all AVPs into the complete registry
		for name, avp := range parser.AVPs {
			completeAVPs[name] = avp
		}
	}

	// Create a temporary parser to resolve grouped field references within completeAVPs
	resolverParser := codegen.NewProtoParser()
	resolverParser.AVPs = completeAVPs
	resolverParser.ResolveAVPReferences()

	// Second pass: Generate code for each proto file using the complete AVP registry
	for _, protoFile := range protoFiles {
		fmt.Printf("\nProcessing %s...\n", filepath.Base(protoFile))

		// Parse this specific proto file
		fileParser := codegen.NewProtoParser()
		if err := fileParser.ParseFile(protoFile); err != nil {
			return fmt.Errorf("parsing %s: %w", filepath.Base(protoFile), err)
		}

		// Replace AVPs with complete registry so all referenced types are available
		fileParser.AVPs = completeAVPs

		// Resolve all field references using the complete AVP registry
		fileParser.ResolveAVPReferences()

		packageName := fileParser.PackageName
		if packageName == "" {
			packageName = "base"
		}

		// Create package directory
		packageDir := filepath.Join(outputDir, packageName)
		if err := os.MkdirAll(packageDir, 0755); err != nil {
			return fmt.Errorf("creating package directory: %w", err)
		}

		// Generate output file name
		base := filepath.Base(protoFile)
		ext := filepath.Ext(base)
		name := base[:len(base)-len(ext)]
		finalOutputFile := filepath.Join(packageDir, name+".pb.go")

		// Generate code
		generator := codegen.NewGenerator(fileParser, packageName)
		if err := generator.GenerateToFile(finalOutputFile); err != nil {
			return fmt.Errorf("generating code for %s: %w", filepath.Base(protoFile), err)
		}

		fmt.Printf("Generated code written to %s\n", finalOutputFile)

		// Generate constants file
		constFile := filepath.Join(packageDir, "constants.go")
		if err := generator.GenerateConstantsFile(constFile); err != nil {
			return fmt.Errorf("generating constants file for %s: %w", filepath.Base(protoFile), err)
		}
		fmt.Printf("Generated constants file written to %s\n", constFile)

		// Generate test files if requested
		if genTests {
			baseFile := strings.TrimSuffix(finalOutputFile, ".pb.go")

			// Generate unit test file
			unitTestFile := baseFile + "_test.go"
			if err := generator.GenerateUnitTestFile(unitTestFile); err != nil {
				return fmt.Errorf("generating unit test file: %w", err)
			}
			fmt.Printf("Generated unit test file written to %s\n", unitTestFile)

			// Generate benchmark test file
			benchmarkTestFile := baseFile + "_benchmark_test.go"
			if err := generator.GenerateBenchmarkTestFile(benchmarkTestFile); err != nil {
				return fmt.Errorf("generating benchmark test file: %w", err)
			}
			fmt.Printf("Generated benchmark test file written to %s\n", benchmarkTestFile)

			// Generate pcap test file
			pcapTestFile := baseFile + "_pcap_test.go"
			if err := generator.GeneratePcapTestFile(pcapTestFile); err != nil {
				return fmt.Errorf("generating pcap test file: %w", err)
			}
			fmt.Printf("Generated pcap test file written to %s\n", pcapTestFile)
		}
	}

	fmt.Printf("\nSuccessfully processed all proto files!\n")
	return nil
}
