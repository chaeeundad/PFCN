package qe

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/chaeeundad/PFCN/internal/job"
)

// Fixed sandbox paths (§39.1). The entrypoint wrapper in the signed image
// mounts the same paths.
const (
	PathInput   = "/work/in"
	PathPseudo  = "/work/pseudo"
	PathScratch = "/work/scratch"
	PathOutput  = "/work/out"
	Prefix      = "pumat"
	InputFile   = "pw.in"
)

// BuildInput renders pw.in from a validated canonical job and its structure.
// Every path and restart-related setting is fixed by the adapter.
func BuildInput(c *job.Canonical, structure []byte) ([]byte, error) {
	if err := (Adapter{}).ValidateCanonical(c.Calculation); err != nil {
		return nil, err
	}
	st, err := ParseCIF(structure)
	if err != nil {
		return nil, err
	}
	if err := checkSpecies(st, c.System); err != nil {
		return nil, err
	}
	p := c.Calculation.Parameters
	species := st.Elements()
	speciesIndex := map[string]int{}
	for i, el := range species {
		speciesIndex[el] = i + 1
	}

	nl := map[string][]string{}
	add := func(list, line string) { nl[list] = append(nl[list], "  "+line) }

	maxSec := c.Resources.WalltimeSeconds - 60
	if maxSec < 30 {
		maxSec = 30
	}
	add("CONTROL", fmt.Sprintf("calculation = '%s'", c.Calculation.Type))
	add("CONTROL", fmt.Sprintf("prefix = '%s'", Prefix))
	add("CONTROL", fmt.Sprintf("outdir = '%s'", PathScratch))
	add("CONTROL", fmt.Sprintf("pseudo_dir = '%s'", PathPseudo))
	add("CONTROL", "disk_io = 'low'")
	add("CONTROL", fmt.Sprintf("max_seconds = %d", maxSec))
	add("CONTROL", "tprnfor = .true.")
	add("CONTROL", "tstress = .true.")
	add("SYSTEM", "ibrav = 0")
	add("SYSTEM", fmt.Sprintf("nat = %d", len(st.Atoms)))
	add("SYSTEM", fmt.Sprintf("ntyp = %d", len(species)))

	names := make([]string, 0, len(p))
	for k := range p {
		names = append(names, k)
	}
	sort.Strings(names)
	var kpts []int64
	for _, k := range names {
		spec := params[k]
		v := p[k]
		switch spec.kind {
		case pKpoints:
			for _, e := range v.([]any) {
				n, _ := toInt(e)
				kpts = append(kpts, n)
			}
		case pSpeciesFloat:
			m := v.(map[string]any)
			els := make([]string, 0, len(m))
			for el := range m {
				els = append(els, el)
			}
			sort.Strings(els)
			for _, el := range els {
				idx, ok := speciesIndex[el]
				if !ok {
					return nil, fmt.Errorf("qe: %s refers to element %s, which is not in the structure", k, el)
				}
				add(spec.namelist, fmt.Sprintf("%s(%d) = %s", k, idx, fortranReal(m[el])))
			}
		case pFloat:
			add(spec.namelist, fmt.Sprintf("%s = %s", k, fortranReal(v)))
		case pInt:
			n, _ := toInt(v)
			add(spec.namelist, fmt.Sprintf("%s = %d", k, n))
		case pBool:
			if v.(bool) {
				add(spec.namelist, k+" = .true.")
			} else {
				add(spec.namelist, k+" = .false.")
			}
		case pEnum:
			add(spec.namelist, fmt.Sprintf("%s = '%s'", k, v.(string)))
		}
	}

	var b strings.Builder
	order := []string{"CONTROL", "SYSTEM", "ELECTRONS"}
	switch c.Calculation.Type {
	case "relax":
		order = append(order, "IONS")
	case "vc-relax":
		order = append(order, "IONS", "CELL")
	}
	for _, list := range order {
		b.WriteString("&" + list + "\n")
		for _, l := range nl[list] {
			b.WriteString(l + "\n")
		}
		b.WriteString("/\n")
	}

	b.WriteString("ATOMIC_SPECIES\n")
	for _, el := range species {
		fmt.Fprintf(&b, "  %s %s %s\n", el, atomicMass[el], c.System.Pseudopotentials[el].Filename)
	}
	b.WriteString("CELL_PARAMETERS angstrom\n")
	for _, v := range st.CellVectors() {
		fmt.Fprintf(&b, "  %.10f %.10f %.10f\n", v[0], v[1], v[2])
	}
	b.WriteString("ATOMIC_POSITIONS crystal\n")
	for _, a := range st.Atoms {
		fmt.Fprintf(&b, "  %s %.10f %.10f %.10f\n", a.Element, a.X, a.Y, a.Z)
	}
	b.WriteString("K_POINTS automatic\n")
	fmt.Fprintf(&b, "  %d %d %d %d %d %d\n", kpts[0], kpts[1], kpts[2], kpts[3], kpts[4], kpts[5])
	return []byte(b.String()), nil
}

// fortranReal renders a canonical decimal string as a Fortran double literal.
func fortranReal(v any) string {
	s, _ := v.(string)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	out := strconv.FormatFloat(f, 'e', -1, 64) // e.g. 1e-08, 6e+01
	mant, exp, _ := strings.Cut(out, "e")
	if !strings.Contains(mant, ".") {
		mant += ".0"
	}
	e, _ := strconv.Atoi(exp)
	return fmt.Sprintf("%sd%d", mant, e)
}

// EstimatePools picks the k-point pool count for -nk: the largest divisor of
// cores that does not exceed half the k-point grid size.
func EstimatePools(c *job.Canonical) int64 {
	cores := c.Resources.CPUCores
	kp, _ := c.Calculation.Parameters["kpoints"].([]any)
	total := int64(1)
	for i := 0; i < 3 && i < len(kp); i++ {
		n, _ := toInt(kp[i])
		total *= n
	}
	limit := max(total/2, 1)
	best := int64(1)
	for d := int64(1); d <= cores; d++ {
		if cores%d == 0 && d <= limit {
			best = d
		}
	}
	return best
}
