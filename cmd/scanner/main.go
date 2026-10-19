// Command scanner is the kokumi-scanner component. It runs in the official
// Trivy image, into which an init container installs it, and serves the scan
// API the manager calls.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/kokumi-dev/kokumi/internal/scanner"
)

func main() {
	if err := scanner.Run(context.Background(), os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}
