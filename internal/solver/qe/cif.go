package qe

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// MaxAtoms bounds the expanded structure size during the alpha (§39).
const MaxAtoms = 200

// Structure is a periodic crystal in fractional coordinates.
type Structure struct {
	A, B, C            float64 // Å
	Alpha, Beta, Gamma float64 // degrees
	Atoms              []Atom
}

type Atom struct {
	Element string
	X, Y, Z float64 // fractional, wrapped to [0,1)
}

// ParseCIF reads a single-block CIF, expands symmetry operations, and
// rejects partial occupancy.
func ParseCIF(data []byte) (*Structure, error) {
	if len(data) > 4<<20 {
		return nil, errors.New("cif: file too large")
	}
	tags, loops, err := tokenizeCIF(data)
	if err != nil {
		return nil, err
	}
	st := &Structure{}
	for _, f := range []struct {
		tag string
		dst *float64
	}{
		{"_cell_length_a", &st.A}, {"_cell_length_b", &st.B}, {"_cell_length_c", &st.C},
		{"_cell_angle_alpha", &st.Alpha}, {"_cell_angle_beta", &st.Beta}, {"_cell_angle_gamma", &st.Gamma},
	} {
		v, ok := tags[f.tag]
		if !ok {
			return nil, fmt.Errorf("cif: missing %s", f.tag)
		}
		x, err := cifNumber(v)
		if err != nil {
			return nil, fmt.Errorf("cif: %s: %w", f.tag, err)
		}
		*f.dst = x
	}
	if st.A <= 0 || st.B <= 0 || st.C <= 0 || st.Alpha <= 0 || st.Beta <= 0 || st.Gamma <= 0 ||
		st.Alpha >= 180 || st.Beta >= 180 || st.Gamma >= 180 {
		return nil, errors.New("cif: invalid cell parameters")
	}

	sites := findLoop(loops, "_atom_site_fract_x")
	if sites == nil {
		return nil, errors.New("cif: no _atom_site loop with fractional coordinates")
	}
	ops, err := symmetryOps(tags, loops)
	if err != nil {
		return nil, err
	}

	var base []Atom
	for _, row := range sites.rows {
		el, err := siteElement(sites, row)
		if err != nil {
			return nil, err
		}
		if occ, ok := sites.get(row, "_atom_site_occupancy"); ok && occ != "." && occ != "?" {
			o, err := cifNumber(occ)
			if err != nil || math.Abs(o-1) > 1e-3 {
				return nil, fmt.Errorf("cif: partial occupancy (%s) is not supported", occ)
			}
		}
		var xyz [3]float64
		for i, t := range []string{"_atom_site_fract_x", "_atom_site_fract_y", "_atom_site_fract_z"} {
			v, _ := sites.get(row, t)
			if xyz[i], err = cifNumber(v); err != nil {
				return nil, fmt.Errorf("cif: %s: %w", t, err)
			}
		}
		base = append(base, Atom{Element: el, X: xyz[0], Y: xyz[1], Z: xyz[2]})
	}

	for _, a := range base {
		for _, op := range ops {
			p := op.apply([3]float64{a.X, a.Y, a.Z})
			cand := Atom{Element: a.Element, X: wrap(p[0]), Y: wrap(p[1]), Z: wrap(p[2])}
			if !containsAtom(st.Atoms, cand) {
				st.Atoms = append(st.Atoms, cand)
				if len(st.Atoms) > MaxAtoms {
					return nil, fmt.Errorf("cif: expanded structure exceeds %d atoms", MaxAtoms)
				}
			}
		}
	}
	if len(st.Atoms) == 0 {
		return nil, errors.New("cif: no atoms")
	}
	return st, nil
}

// CellVectors returns lattice vectors in Å (a along x, b in the xy plane).
func (s *Structure) CellVectors() [3][3]float64 {
	rad := math.Pi / 180
	ca, cb, cg := math.Cos(s.Alpha*rad), math.Cos(s.Beta*rad), math.Cos(s.Gamma*rad)
	sg := math.Sin(s.Gamma * rad)
	cx := s.C * cb
	cy := s.C * (ca - cb*cg) / sg
	cz := math.Sqrt(math.Max(s.C*s.C-cx*cx-cy*cy, 0))
	return [3][3]float64{
		{s.A, 0, 0},
		{s.B * cg, s.B * sg, 0},
		{cx, cy, cz},
	}
}

// Elements returns the distinct elements in sorted order.
func (s *Structure) Elements() []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range s.Atoms {
		if !seen[a.Element] {
			seen[a.Element] = true
			out = append(out, a.Element)
		}
	}
	sortStrings(out)
	return out
}

func wrap(x float64) float64 {
	x -= math.Floor(x)
	if x >= 1-1e-8 {
		x = 0
	}
	if math.Abs(x) < 1e-8 {
		x = 0
	}
	return x
}

func containsAtom(atoms []Atom, a Atom) bool {
	for _, b := range atoms {
		if b.Element == a.Element && fracClose(a.X, b.X) && fracClose(a.Y, b.Y) && fracClose(a.Z, b.Z) {
			return true
		}
	}
	return false
}

func fracClose(a, b float64) bool {
	d := math.Abs(a - b)
	return d < 1e-4 || math.Abs(d-1) < 1e-4
}

// --- symmetry -------------------------------------------------------------

type symOp struct {
	rot   [3][3]float64
	trans [3]float64
}

func (o symOp) apply(p [3]float64) [3]float64 {
	var r [3]float64
	for i := 0; i < 3; i++ {
		r[i] = o.rot[i][0]*p[0] + o.rot[i][1]*p[1] + o.rot[i][2]*p[2] + o.trans[i]
	}
	return r
}

func symmetryOps(tags map[string]string, loops []*cifLoop) ([]symOp, error) {
	var exprs []string
	for _, tag := range []string{"_space_group_symop_operation_xyz", "_symmetry_equiv_pos_as_xyz"} {
		if l := findLoop(loops, tag); l != nil {
			for _, row := range l.rows {
				v, _ := l.get(row, tag)
				exprs = append(exprs, v)
			}
			break
		}
		if v, ok := tags[tag]; ok {
			exprs = append(exprs, v)
			break
		}
	}
	if len(exprs) == 0 {
		hm := strings.ReplaceAll(firstTag(tags, "_symmetry_space_group_name_h-m", "_space_group_name_h-m_alt"), " ", "")
		if hm != "" && hm != "P1" && hm != "?" {
			return nil, fmt.Errorf("cif: space group %q given without explicit symmetry operations; include the symop loop or use a P1 cell", hm)
		}
		exprs = []string{"x,y,z"}
	}
	if len(exprs) > 192 {
		return nil, errors.New("cif: too many symmetry operations")
	}
	ops := make([]symOp, 0, len(exprs))
	for _, e := range exprs {
		op, err := parseSymOp(e)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, nil
}

var termRe = regexp.MustCompile(`([+-]?)([0-9]*\.?[0-9]+(?:/[0-9]+)?)?\*?([xyz])?`)

func parseSymOp(s string) (symOp, error) {
	var op symOp
	parts := strings.Split(strings.ToLower(strings.ReplaceAll(s, " ", "")), ",")
	if len(parts) != 3 {
		return op, fmt.Errorf("cif: invalid symmetry operation %q", s)
	}
	for i, p := range parts {
		if p == "" {
			return op, fmt.Errorf("cif: invalid symmetry operation %q", s)
		}
		rest := p
		for rest != "" {
			m := termRe.FindStringSubmatchIndex(rest)
			if m == nil || m[0] != 0 || m[1] == 0 {
				return op, fmt.Errorf("cif: invalid symmetry operation %q", s)
			}
			sign := 1.0
			if m[2] >= 0 && rest[m[2]:m[3]] == "-" {
				sign = -1
			}
			coef := 1.0
			hasNum := m[4] >= 0
			if hasNum {
				c, err := parseFraction(rest[m[4]:m[5]])
				if err != nil {
					return op, fmt.Errorf("cif: invalid symmetry operation %q", s)
				}
				coef = c
			}
			if m[6] >= 0 {
				axis := map[string]int{"x": 0, "y": 1, "z": 2}[rest[m[6]:m[7]]]
				op.rot[i][axis] += sign * coef
			} else if hasNum {
				op.trans[i] += sign * coef
			} else {
				return op, fmt.Errorf("cif: invalid symmetry operation %q", s)
			}
			rest = rest[m[1]:]
		}
	}
	return op, nil
}

func parseFraction(s string) (float64, error) {
	if n, d, ok := strings.Cut(s, "/"); ok {
		a, err1 := strconv.ParseFloat(n, 64)
		b, err2 := strconv.ParseFloat(d, 64)
		if err1 != nil || err2 != nil || b == 0 {
			return 0, errors.New("bad fraction")
		}
		return a / b, nil
	}
	return strconv.ParseFloat(s, 64)
}

// --- tokenizer ------------------------------------------------------------

type cifLoop struct {
	cols map[string]int
	rows [][]string
}

func (l *cifLoop) get(row []string, tag string) (string, bool) {
	i, ok := l.cols[tag]
	if !ok || i >= len(row) {
		return "", false
	}
	return row[i], true
}

func findLoop(loops []*cifLoop, tag string) *cifLoop {
	for _, l := range loops {
		if _, ok := l.cols[tag]; ok {
			return l
		}
	}
	return nil
}

func firstTag(tags map[string]string, names ...string) string {
	for _, n := range names {
		if v, ok := tags[n]; ok {
			return v
		}
	}
	return ""
}

func tokenizeCIF(data []byte) (map[string]string, []*cifLoop, error) {
	var tokens []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	inText := false
	var text strings.Builder
	for sc.Scan() {
		line := sc.Text()
		if inText {
			if strings.HasPrefix(line, ";") {
				tokens = append(tokens, text.String())
				text.Reset()
				inText = false
			} else {
				text.WriteString(line + "\n")
			}
			continue
		}
		if strings.HasPrefix(line, ";") {
			inText = true
			text.WriteString(line[1:] + "\n")
			continue
		}
		toks, err := splitCIFLine(line)
		if err != nil {
			return nil, nil, err
		}
		tokens = append(tokens, toks...)
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}
	if inText {
		return nil, nil, errors.New("cif: unterminated text field")
	}

	tags := map[string]string{}
	var loops []*cifLoop
	blocks := 0
	for i := 0; i < len(tokens); {
		t := tokens[i]
		lt := strings.ToLower(t)
		switch {
		case strings.HasPrefix(lt, "data_"):
			blocks++
			if blocks > 1 {
				return nil, nil, errors.New("cif: multiple data blocks are not supported")
			}
			i++
		case lt == "loop_":
			i++
			l := &cifLoop{cols: map[string]int{}}
			var n int
			for i < len(tokens) && strings.HasPrefix(tokens[i], "_") {
				l.cols[strings.ToLower(tokens[i])] = n
				n++
				i++
			}
			if n == 0 {
				return nil, nil, errors.New("cif: empty loop")
			}
			var vals []string
			for i < len(tokens) && !isReserved(tokens[i]) {
				vals = append(vals, tokens[i])
				i++
			}
			if len(vals)%n != 0 {
				return nil, nil, errors.New("cif: loop value count does not match its columns")
			}
			for j := 0; j < len(vals); j += n {
				l.rows = append(l.rows, vals[j:j+n])
			}
			loops = append(loops, l)
		case strings.HasPrefix(t, "_"):
			if i+1 >= len(tokens) || isReserved(tokens[i+1]) {
				return nil, nil, fmt.Errorf("cif: tag %s has no value", t)
			}
			tags[strings.ToLower(t)] = tokens[i+1]
			i += 2
		default:
			return nil, nil, fmt.Errorf("cif: unexpected token %q", t)
		}
	}
	return tags, loops, nil
}

func isReserved(t string) bool {
	lt := strings.ToLower(t)
	return strings.HasPrefix(t, "_") || lt == "loop_" || strings.HasPrefix(lt, "data_") ||
		strings.HasPrefix(lt, "save_") || lt == "global_" || lt == "stop_"
}

func splitCIFLine(line string) ([]string, error) {
	var out []string
	i := 0
	for i < len(line) {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '#':
			return out, nil
		case c == '\'' || c == '"':
			// A quote closes only when followed by whitespace or end of line.
			j := i + 1
			for {
				k := strings.IndexByte(line[j:], c)
				if k < 0 {
					return nil, fmt.Errorf("cif: unterminated quoted value in %q", line)
				}
				end := j + k
				if end+1 == len(line) || line[end+1] == ' ' || line[end+1] == '\t' {
					out = append(out, line[i+1:end])
					i = end + 1
					break
				}
				j = end + 1
			}
		default:
			j := i
			for j < len(line) && line[j] != ' ' && line[j] != '\t' {
				j++
			}
			out = append(out, line[i:j])
			i = j
		}
	}
	return out, nil
}

var uncertaintyRe = regexp.MustCompile(`\([0-9]+\)$`)

func cifNumber(s string) (float64, error) {
	s = uncertaintyRe.ReplaceAllString(strings.TrimSpace(s), "")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("invalid number %q", s)
	}
	return f, nil
}

var symbolRe = regexp.MustCompile(`^([A-Z][a-z]?)`)

func siteElement(l *cifLoop, row []string) (string, error) {
	for _, tag := range []string{"_atom_site_type_symbol", "_atom_site_label"} {
		if v, ok := l.get(row, tag); ok && v != "." && v != "?" {
			v = strings.ToUpper(v[:1]) + v[1:]
			m := symbolRe.FindStringSubmatch(v)
			if m == nil {
				continue
			}
			el := m[1]
			if _, ok := atomicMass[el]; ok {
				return el, nil
			}
			if _, ok := atomicMass[el[:1]]; ok && len(el) == 2 && tag == "_atom_site_label" {
				return el[:1], nil
			}
			return "", fmt.Errorf("cif: unknown element in %q", v)
		}
	}
	return "", errors.New("cif: atom site without type symbol or label")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
