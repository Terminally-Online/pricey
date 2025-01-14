package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	abiDirectory      = "./contracts/abis"
	bindingsDirectory = "./contracts/bindings"
	packageName       = "bindings"
)

func main() {
	if err := os.RemoveAll(bindingsDirectory); err != nil {
		log.Fatalf("Failed to clean up bindings directory: %v", err)
	}

	if err := os.MkdirAll(bindingsDirectory, 0755); err != nil {
		log.Fatalf("Failed to create bindings directory: %v", err)
	}

	err := filepath.Walk(abiDirectory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}

		relPath, err := filepath.Rel(abiDirectory, filepath.Dir(path))
		if err != nil {
			return err
		}

		outFile := strings.TrimSuffix(info.Name(), ".json") + ".go"
		outFile = strings.ToLower(outFile)
		outDir := filepath.Join(bindingsDirectory, relPath)

		if err := os.MkdirAll(outDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory %s: %v", outDir, err)
		}

		outPath := filepath.Join(outDir, outFile)

		cmd := exec.Command("abigen",
			"--abi", path,
			"--pkg", packageName,
			"--out", outPath,
		)

		fmt.Printf("Generating bindings for %s...\n", path)
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("Failed to generate bindings for %s: %v\nOutput: %s\n",
				path, err, string(output))
		} else {
			fmt.Printf("Successfully generated bindings for %s\n", path)
		}

		return nil
	})

	if err != nil {
		log.Fatalf("Failed to walk directories: %v", err)
	}
}
