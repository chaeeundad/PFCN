package qeparse

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"testing"
)

func TestParseSiSCF(t *testing.T) {
	x, _ := os.ReadFile("testdata/data-file-schema.xml")
	out, _ := os.ReadFile("testdata/stdout.txt")
	b, err := Parse(x, out)
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := Parse(x, out)
	if !bytes.Equal(b, b2) {
		t.Fatal("parser output must be deterministic")
	}
	var r Result
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if !r.Converged || r.SCFSteps != 7 || r.Calculation != "scf" || r.ExitStatus == nil || *r.ExitStatus != 0 {
		t.Fatalf("unexpected header: %+v", r)
	}
	// -22.82641089 Ry = -310.5683... eV
	if math.Abs(r.Energy.Value-(-22.82641089*13.605693122994)) > 1e-4 {
		t.Fatalf("energy %v", r.Energy.Value)
	}
	if math.Abs(r.HighestOccupied.Value-6.0284) > 1e-3 {
		t.Fatalf("HOMO %v", r.HighestOccupied.Value)
	}
	if len(r.Forces.Values) != 2 || len(r.Stress.Values) != 3 || len(r.FinalStructure.Species) != 2 {
		t.Fatal("matrix shapes")
	}
	if math.Abs(r.FinalStructure.Cell.Values[0][0]-3.8669746) > 1e-5 {
		t.Fatalf("cell a %v", r.FinalStructure.Cell.Values[0][0])
	}
}

func TestParseMissingXML(t *testing.T) {
	b, err := Parse(nil, []byte("Error in routine"))
	if err != nil {
		t.Fatal(err)
	}
	var r Result
	json.Unmarshal(b, &r)
	if r.Converged || len(r.Warnings) == 0 {
		t.Fatal("missing XML must yield unconverged result with warning")
	}
}
