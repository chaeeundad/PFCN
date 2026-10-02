// Package qe is the Quantum ESPRESSO solver adapter (spec §39, §39.1).
package qe

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/chaeeundad/PFCN/internal/job"
)

const (
	Kind       = "QuantumEspressoJob"
	SolverName = "quantum-espresso"
	Entrypoint = "pw.x"
)

// Allowed calculation types. nscf/bands need state from a previous run and
// are deferred until multi-step workflows exist.
var calcTypes = map[string]bool{"scf": true, "relax": true, "vc-relax": true}

type paramKind int

const (
	pFloat paramKind = iota
	pInt
	pBool
	pEnum
	pKpoints
	pSpeciesFloat
)

type paramSpec struct {
	kind     paramKind
	namelist string
	min, max float64
	minOpen  bool
	enum     map[string]bool
	calcs    map[string]bool // nil = all
}

var relaxOnly = map[string]bool{"relax": true, "vc-relax": true}

var params = map[string]paramSpec{
	"ecutwfc":                {kind: pFloat, namelist: "SYSTEM", min: 0, max: 1000, minOpen: true},
	"ecutrho":                {kind: pFloat, namelist: "SYSTEM", min: 0, max: 10000, minOpen: true},
	"occupations":            {kind: pEnum, namelist: "SYSTEM", enum: set("fixed", "smearing", "tetrahedra", "tetrahedra_opt")},
	"smearing":               {kind: pEnum, namelist: "SYSTEM", enum: set("gaussian", "gauss", "methfessel-paxton", "m-p", "mp", "marzari-vanderbilt", "cold", "m-v", "mv", "fermi-dirac", "f-d", "fd")},
	"degauss":                {kind: pFloat, namelist: "SYSTEM", min: 0, max: 1},
	"nspin":                  {kind: pInt, namelist: "SYSTEM", min: 1, max: 2},
	"starting_magnetization": {kind: pSpeciesFloat, namelist: "SYSTEM", min: -1, max: 1},
	"nbnd":                   {kind: pInt, namelist: "SYSTEM", min: 1, max: 100000},
	"conv_thr":               {kind: pFloat, namelist: "ELECTRONS", min: 0, max: 1, minOpen: true},
	"mixing_beta":            {kind: pFloat, namelist: "ELECTRONS", min: 0, max: 1, minOpen: true},
	"electron_maxstep":       {kind: pInt, namelist: "ELECTRONS", min: 1, max: 1000},
	"nstep":                  {kind: pInt, namelist: "CONTROL", min: 1, max: 1000, calcs: relaxOnly},
	"etot_conv_thr":          {kind: pFloat, namelist: "CONTROL", min: 0, max: 1, minOpen: true, calcs: relaxOnly},
	"forc_conv_thr":          {kind: pFloat, namelist: "CONTROL", min: 0, max: 1, minOpen: true, calcs: relaxOnly},
	"ion_dynamics":           {kind: pEnum, namelist: "IONS", enum: set("bfgs", "damp"), calcs: relaxOnly},
	"cell_dynamics":          {kind: pEnum, namelist: "CELL", enum: set("bfgs", "none"), calcs: map[string]bool{"vc-relax": true}},
	"press":                  {kind: pFloat, namelist: "CELL", min: -1e6, max: 1e6, calcs: map[string]bool{"vc-relax": true}},
	"kpoints":                {kind: pKpoints},
}

// adapterControlled are set by BuildInput and can never come from a job.
var adapterControlled = map[string]bool{
	"outdir": true, "pseudo_dir": true, "wfcdir": true, "prefix": true,
	"disk_io": true, "max_seconds": true, "calculation": true, "ibrav": true,
	"nat": true, "ntyp": true, "tprnfor": true, "tstress": true,
	"restart_mode": true, "input_dft": true,
}

func set(xs ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// normalizeParams validates raw YAML parameters and returns canonical values:
// floats as decimal strings, ints as int64.
func normalizeParams(calcType string, raw map[string]any) (map[string]any, error) {
	out := map[string]any{}
	if _, ok := raw["kpoints"]; !ok {
		return nil, fmt.Errorf("qe: calculation.parameters.kpoints is required")
	}
	if _, ok := raw["ecutwfc"]; !ok {
		return nil, fmt.Errorf("qe: calculation.parameters.ecutwfc is required")
	}
	for k, v := range raw {
		if adapterControlled[k] {
			return nil, fmt.Errorf("qe: parameter %q is controlled by the adapter and cannot be set", k)
		}
		spec, ok := params[k]
		if !ok {
			return nil, fmt.Errorf("qe: parameter %q is not in the allowlist", k)
		}
		if spec.calcs != nil && !spec.calcs[calcType] {
			return nil, fmt.Errorf("qe: parameter %q is not valid for calculation %q", k, calcType)
		}
		nv, err := normalizeValue(k, spec, v)
		if err != nil {
			return nil, err
		}
		out[k] = nv
	}
	return out, nil
}

func normalizeValue(name string, spec paramSpec, v any) (any, error) {
	switch spec.kind {
	case pFloat:
		f, ok := toFloat(v)
		if !ok {
			return nil, fmt.Errorf("qe: %s must be a number", name)
		}
		if err := checkRange(name, f, spec); err != nil {
			return nil, err
		}
		return formatFloat(f), nil
	case pInt:
		i, ok := toInt(v)
		if !ok {
			return nil, fmt.Errorf("qe: %s must be an integer", name)
		}
		if err := checkRange(name, float64(i), spec); err != nil {
			return nil, err
		}
		return i, nil
	case pBool:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("qe: %s must be true or false", name)
		}
		return b, nil
	case pEnum:
		s, ok := v.(string)
		if !ok || !spec.enum[s] {
			return nil, fmt.Errorf("qe: %s must be one of %s", name, keys(spec.enum))
		}
		return s, nil
	case pKpoints:
		list, ok := v.([]any)
		if !ok || (len(list) != 3 && len(list) != 6) {
			return nil, fmt.Errorf("qe: kpoints must be [nk1, nk2, nk3] or [nk1, nk2, nk3, sk1, sk2, sk3]")
		}
		out := make([]any, 0, 6)
		for i, e := range list {
			n, ok := toInt(e)
			if !ok {
				return nil, fmt.Errorf("qe: kpoints entries must be integers")
			}
			if i < 3 && (n < 1 || n > 64) {
				return nil, fmt.Errorf("qe: kpoint grid values must be 1-64")
			}
			if i >= 3 && n != 0 && n != 1 {
				return nil, fmt.Errorf("qe: kpoint offsets must be 0 or 1")
			}
			out = append(out, n)
		}
		for len(out) < 6 {
			out = append(out, int64(0))
		}
		return out, nil
	case pSpeciesFloat:
		m, ok := v.(map[string]any)
		if !ok || len(m) == 0 {
			return nil, fmt.Errorf("qe: %s must map element symbols to numbers", name)
		}
		out := map[string]any{}
		for el, x := range m {
			f, ok := toFloat(x)
			if !ok {
				return nil, fmt.Errorf("qe: %s.%s must be a number", name, el)
			}
			if err := checkRange(name, f, spec); err != nil {
				return nil, err
			}
			out[el] = formatFloat(f)
		}
		return out, nil
	}
	return nil, fmt.Errorf("qe: unsupported parameter %s", name)
}

// validateCanonicalParams re-checks parameters received in canonical form.
func validateCanonicalParams(calcType string, p map[string]any) error {
	if _, ok := p["kpoints"]; !ok {
		return fmt.Errorf("qe: kpoints missing")
	}
	if _, ok := p["ecutwfc"]; !ok {
		return fmt.Errorf("qe: ecutwfc missing")
	}
	for k, v := range p {
		spec, ok := params[k]
		if !ok || adapterControlled[k] {
			return fmt.Errorf("qe: parameter %q not allowed", k)
		}
		if spec.calcs != nil && !spec.calcs[calcType] {
			return fmt.Errorf("qe: parameter %q not valid for %q", k, calcType)
		}
		switch spec.kind {
		case pFloat:
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("qe: %s must be a decimal string", k)
			}
			f, err := strconv.ParseFloat(s, 64)
			if err != nil || formatFloat(f) != s {
				return fmt.Errorf("qe: %s is not canonical", k)
			}
			if err := checkRange(k, f, spec); err != nil {
				return err
			}
		case pSpeciesFloat:
			m, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("qe: %s must be a map", k)
			}
			for el, x := range m {
				s, ok := x.(string)
				f, err := strconv.ParseFloat(s, 64)
				if !ok || err != nil || formatFloat(f) != s {
					return fmt.Errorf("qe: %s.%s is not canonical", k, el)
				}
				if err := checkRange(k, f, spec); err != nil {
					return err
				}
			}
		default:
			if _, err := normalizeValue(k, spec, v); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkRange(name string, f float64, s paramSpec) error {
	if math.IsNaN(f) || math.IsInf(f, 0) || f > s.max || f < s.min || (s.minOpen && f == s.min) {
		return fmt.Errorf("qe: %s=%v out of range", name, f)
	}
	return nil
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case float64:
		return t, true
	case string: // canonical decimal string
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	return 0, false
}

func toInt(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int64:
		return t, true
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1<<53 {
			return int64(t), true
		}
	}
	return 0, false
}

// formatFloat is the canonical decimal form of a float parameter.
func formatFloat(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

func keys(m map[string]bool) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}

// Adapter implements job.Adapter for Quantum ESPRESSO.
type Adapter struct{}

var _ job.Adapter = Adapter{}

func (Adapter) Kind() string       { return Kind }
func (Adapter) SolverName() string { return SolverName }

func (Adapter) NormalizeCalculation(c job.Calculation) (job.CanonicalCalculation, error) {
	if !calcTypes[c.Type] {
		return job.CanonicalCalculation{}, fmt.Errorf("qe: calculation.type must be one of scf, relax, vc-relax")
	}
	p, err := normalizeParams(c.Type, c.Parameters)
	if err != nil {
		return job.CanonicalCalculation{}, err
	}
	return job.CanonicalCalculation{Type: c.Type, Parameters: p}, nil
}

func (Adapter) ValidateCanonical(c job.CanonicalCalculation) error {
	if !calcTypes[c.Type] {
		return fmt.Errorf("qe: invalid calculation type %q", c.Type)
	}
	return validateCanonicalParams(c.Type, c.Parameters)
}

func (Adapter) ValidateSystem(sys job.CanonicalSystem, files map[string][]byte) error {
	data, ok := files[sys.Structure.Filename]
	if !ok {
		return fmt.Errorf("qe: structure file missing from bundle")
	}
	st, err := ParseCIF(data)
	if err != nil {
		return err
	}
	return checkSpecies(st, sys)
}

func checkSpecies(st *Structure, sys job.CanonicalSystem) error {
	if len(st.Atoms) > MaxAtoms {
		return fmt.Errorf("qe: structure has %d atoms; the current limit is %d", len(st.Atoms), MaxAtoms)
	}
	used := map[string]bool{}
	for _, a := range st.Atoms {
		used[a.Element] = true
		if _, ok := sys.Pseudopotentials[a.Element]; !ok {
			return fmt.Errorf("qe: no pseudopotential for element %s", a.Element)
		}
	}
	for el := range sys.Pseudopotentials {
		if !used[el] {
			return fmt.Errorf("qe: pseudopotential for %s is not used by the structure", el)
		}
	}
	return nil
}
