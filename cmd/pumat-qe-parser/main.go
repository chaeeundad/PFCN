// Command pumat-qe-parser is the Quantum ESPRESSO parser artifact (spec §23).
//
// It is compiled to WASI (GOOS=wasip1 GOARCH=wasm) and run by the requester
// agent with the output bundle mounted read-only at /in. It writes the
// canonical result JSON to stdout.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/chaeeundad/PFCN/internal/qeparse"
)

func main() {
	xml, err := os.ReadFile("/in/data-file-schema.xml")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fail(err)
	}
	stdout, err := os.ReadFile("/in/stdout.txt")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fail(err)
	}
	out, err := qeparse.Parse(xml, stdout)
	if err != nil {
		fail(err)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "pumat-qe-parser:", err)
	os.Exit(1)
}
