// Package qeparse converts Quantum ESPRESSO pw.x output into the canonical
// materials result schema (spec §23.2).
//
// It has no OS dependencies so it can be compiled to WASI and run as the
// sandboxed, deterministic parser artifact (§23.1). Raw output stays the
// evidence; this result is a derived convenience.
package qeparse

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

const (
	Schema  = "pumat.result.materials.v1alpha1"
	Version = "0.1.0"

	hartreeEV       = 27.211386245988
	bohrAngstrom    = 0.529177210903
	hartreeBohrEVA  = hartreeEV / bohrAngstrom
	hartreeBohr3GPa = 29421.02648438959
	maxXMLBytes     = 256 << 20
)

type Quantity struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

type Matrix struct {
	Unit   string      `json:"unit"`
	Values [][]float64 `json:"values"`
}

type Structure struct {
	Cell      Matrix   `json:"cell"`
	Species   []string `json:"species"`
	Positions Matrix   `json:"positions"`
}

// Result is the canonical parsed result.
type Result struct {
	Schema          string     `json:"schema"`
	Parser          string     `json:"parser"`
	Solver          string     `json:"solver"`
	Calculation     string     `json:"calculation"`
	Converged       bool       `json:"converged"`
	SCFSteps        int        `json:"scf_steps"`
	ExitStatus      *int       `json:"exit_status"`
	Energy          *Quantity  `json:"energy"`
	FermiEnergy     *Quantity  `json:"fermi_energy"`
	HighestOccupied *Quantity  `json:"highest_occupied_level"`
	Forces          *Matrix    `json:"forces"`
	Stress          *Matrix    `json:"stress"`
	FinalStructure  *Structure `json:"final_structure"`
	Warnings        []string   `json:"warnings"`
}

type qesXML struct {
	Input struct {
		Control struct {
			Calculation string `xml:"calculation"`
		} `xml:"control_variables"`
	} `xml:"input"`
	Output struct {
		Convergence struct {
			SCF struct {
				Achieved bool `xml:"convergence_achieved"`
				Steps    int  `xml:"n_scf_steps"`
			} `xml:"scf_conv"`
			Opt *struct {
				Achieved bool `xml:"convergence_achieved"`
			} `xml:"opt_conv"`
		} `xml:"convergence_info"`
		Structure struct {
			Atoms []struct {
				Name string `xml:"name,attr"`
				Pos  string `xml:",chardata"`
			} `xml:"atomic_positions>atom"`
			A1 string `xml:"cell>a1"`
			A2 string `xml:"cell>a2"`
			A3 string `xml:"cell>a3"`
		} `xml:"atomic_structure"`
		Etot  *string `xml:"total_energy>etot"`
		Bands struct {
			Fermi   *string `xml:"fermi_energy"`
			Highest *string `xml:"highestOccupiedLevel"`
		} `xml:"band_structure"`
		Forces *string `xml:"forces"`
		Stress *string `xml:"stress"`
	} `xml:"output"`
	ExitStatus *int `xml:"exit_status"`
}

// Parse builds the result from data-file-schema.xml (may be nil) and stdout.
func Parse(xmlData, stdout []byte) ([]byte, error) {
	r := Result{Schema: Schema, Parser: "pumat-qe-parser " + Version, Solver: "quantum-espresso", Warnings: []string{}}
	if len(xmlData) > maxXMLBytes {
		return nil, fmt.Errorf("qeparse: XML output too large")
	}
	if len(xmlData) == 0 {
		r.Warnings = append(r.Warnings, "data-file-schema.xml missing; solver likely failed before writing results")
		r.Calculation = calcFromStdout(stdout)
		return encode(r)
	}
	var q qesXML
	if err := xml.Unmarshal(xmlData, &q); err != nil {
		return nil, fmt.Errorf("qeparse: %w", err)
	}
	r.Calculation = strings.TrimSpace(q.Input.Control.Calculation)
	r.ExitStatus = q.ExitStatus
	r.SCFSteps = q.Output.Convergence.SCF.Steps
	r.Converged = q.Output.Convergence.SCF.Achieved
	if q.Output.Convergence.Opt != nil {
		r.Converged = r.Converged && q.Output.Convergence.Opt.Achieved
	}
	if !bytes.Contains(stdout, []byte("JOB DONE")) {
		r.Warnings = append(r.Warnings, "stdout has no 'JOB DONE' marker")
	}

	if q.Output.Etot != nil {
		v, err := num(*q.Output.Etot)
		if err != nil {
			return nil, err
		}
		r.Energy = &Quantity{Value: round(v * hartreeEV), Unit: "eV"}
	}
	if q.Output.Bands.Fermi != nil {
		v, err := num(*q.Output.Bands.Fermi)
		if err != nil {
			return nil, err
		}
		r.FermiEnergy = &Quantity{Value: round(v * hartreeEV), Unit: "eV"}
	}
	if q.Output.Bands.Highest != nil {
		v, err := num(*q.Output.Bands.Highest)
		if err != nil {
			return nil, err
		}
		r.HighestOccupied = &Quantity{Value: round(v * hartreeEV), Unit: "eV"}
	}
	nat := len(q.Output.Structure.Atoms)
	if q.Output.Forces != nil && nat > 0 {
		m, err := matrix(*q.Output.Forces, nat, 3, hartreeBohrEVA)
		if err != nil {
			return nil, fmt.Errorf("qeparse: forces: %w", err)
		}
		r.Forces = &Matrix{Unit: "eV/angstrom", Values: m}
	}
	if q.Output.Stress != nil {
		m, err := matrix(*q.Output.Stress, 3, 3, hartreeBohr3GPa)
		if err != nil {
			return nil, fmt.Errorf("qeparse: stress: %w", err)
		}
		r.Stress = &Matrix{Unit: "GPa", Values: m}
	}
	if nat > 0 {
		s := &Structure{Cell: Matrix{Unit: "angstrom"}, Positions: Matrix{Unit: "angstrom"}}
		for _, a := range []string{q.Output.Structure.A1, q.Output.Structure.A2, q.Output.Structure.A3} {
			row, err := vec(a, 3, bohrAngstrom)
			if err != nil {
				return nil, fmt.Errorf("qeparse: cell: %w", err)
			}
			s.Cell.Values = append(s.Cell.Values, row)
		}
		for _, a := range q.Output.Structure.Atoms {
			row, err := vec(a.Pos, 3, bohrAngstrom)
			if err != nil {
				return nil, fmt.Errorf("qeparse: positions: %w", err)
			}
			s.Species = append(s.Species, strings.TrimSpace(a.Name))
			s.Positions.Values = append(s.Positions.Values, row)
		}
		r.FinalStructure = s
	}
	return encode(r)
}

func encode(r Result) ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func calcFromStdout(stdout []byte) string {
	for _, line := range strings.Split(string(stdout), "\n") {
		if strings.Contains(line, "calculation") && strings.Contains(line, "=") {
			_, v, _ := strings.Cut(line, "=")
			return strings.Trim(strings.TrimSpace(v), "'\",")
		}
	}
	return ""
}

func num(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}

func vec(s string, n int, scale float64) ([]float64, error) {
	f := strings.Fields(s)
	if len(f) != n {
		return nil, fmt.Errorf("expected %d values, got %d", n, len(f))
	}
	out := make([]float64, n)
	for i, x := range f {
		v, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return nil, err
		}
		out[i] = round(v * scale)
	}
	return out, nil
}

func matrix(s string, rows, cols int, scale float64) ([][]float64, error) {
	f := strings.Fields(s)
	if len(f) != rows*cols {
		return nil, fmt.Errorf("expected %d values, got %d", rows*cols, len(f))
	}
	out := make([][]float64, rows)
	for i := range out {
		row, err := vec(strings.Join(f[i*cols:(i+1)*cols], " "), cols, scale)
		if err != nil {
			return nil, err
		}
		out[i] = row
	}
	return out, nil
}

// round keeps 10 significant digits so tiny unit-conversion noise does not
// leak into the canonical result.
func round(v float64) float64 {
	r, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'g', 10, 64), 64)
	return r
}
