// Command gen writes deploy/rabbitmq-definitions.json from the mq topology.
// Run with: go generate ./internal/platform/mq
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

func main() {
	out := flag.String("out", "deploy/rabbitmq-definitions.json", "output path")
	flag.Parse()
	b, err := mq.DefinitionsJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
