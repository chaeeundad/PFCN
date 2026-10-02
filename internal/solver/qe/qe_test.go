package qe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chaeeundad/PFCN/internal/job"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

const testManifest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

func writeJob(t *testing.T, dir, params string, extra string) string {
	t.Helper()
	cif, err := os.ReadFile("../../../examples/qe-si-scf/silicon.cif")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "silicon.cif"), cif, 0o644)
	upf := []byte("<UPF fake>")
	os.WriteFile(filepath.Join(dir, "Si.upf"), upf, 0o644)
	y := `apiVersion: pumat.org/v1alpha1
kind: QuantumEspressoJob
metadata:
  name: si-scf
  visibility: public
solver:
  name: quantum-espresso
  version: "7.4.1"
  manifestDigest: "` + testManifest + `"
  entrypoint: pw.x
resources:
  cpu: {cores: 4}
  memory: 2GiB
  walltime: 30m
system:
  structure: {file: silicon.cif}
  pseudopotentials:
    Si: {filename: Si.upf, digest: "` + contentid.SHA256(upf) + `"}
calculation:
  type: scf
  parameters:
` + params + `
publication: {input: true, rawOutput: true, parsedOutput: true, provenance: true, license: CC-BY-4.0}
` + extra
	p := filepath.Join(dir, "job.yaml")
	os.WriteFile(p, []byte(y), 0o644)
	return p
}

func prepare(t *testing.T, params, extra string) (*job.Prepared, error) {
	t.Helper()
	dir := t.TempDir()
	p := writeJob(t, dir, params, extra)
	data, _ := os.ReadFile(p)
	spec, err := job.ParseYAML(data)
	if err != nil {
		return nil, err
	}
	return job.Prepare(spec, dir, job.NewRegistry(Adapter{}), nil)
}

const okParams = "    ecutwfc: 30\n    ecutrho: 240\n    conv_thr: 1.0e-8\n    kpoints: [4, 4, 4]\n"

func TestPrepareAndCalcIDStable(t *testing.T) {
	a, err := prepare(t, okParams, "")
	if err != nil {
		t.Fatal(err)
	}
	// Different formatting/order, same science.
	b, err := prepare(t, "    kpoints: [4,4,4,0,0,0]\n    conv_thr: 0.00000001\n    ecutrho: 240.0\n    ecutwfc: 30\n", "delivery: {mode: detached}\n")
	if err != nil {
		t.Fatal(err)
	}
	ia, _ := a.Canonical.CalcID()
	ib, _ := b.Canonical.CalcID()
	if ia != ib {
		t.Fatalf("equivalent jobs must share calc_id: %s vs %s", ia, ib)
	}
	c, _ := prepare(t, "    ecutwfc: 31\n    ecutrho: 240\n    conv_thr: 1.0e-8\n    kpoints: [4, 4, 4]\n", "")
	ic, _ := c.Canonical.CalcID()
	if ic == ia {
		t.Fatal("different parameters must change calc_id")
	}

	// Canonical roundtrip as a worker would receive it.
	raw, err := a.Canonical.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := job.ParseCanonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := back.Validate(job.NewRegistry(Adapter{})); err != nil {
		t.Fatal(err)
	}
	iback, _ := back.CalcID()
	if iback != ia {
		t.Fatal("calc_id must survive canonical roundtrip")
	}
}

func TestRejections(t *testing.T) {
	cases := map[string][2]string{
		"forbidden field":    {okParams, "command: rm -rf /\n"},
		"adapter controlled": {okParams + "    outdir: /etc\n", ""},
		"not allowlisted":    {okParams + "    lda_plus_u: true\n", ""},
		"unknown field":      {okParams, "extra: 1\n"},
		"relax-only param":   {okParams + "    ion_dynamics: bfgs\n", ""},
		"bad kpoints":        {"    ecutwfc: 30\n    kpoints: [4, 4]\n", ""},
		"anchor":             {"    ecutwfc: &e 30\n    ecutrho: *e\n    kpoints: [4,4,4]\n", ""},
		"custodian":          {okParams, "delivery: {custodian: 12D3KooW}\n"},
	}
	for name, c := range cases {
		if _, err := prepare(t, c[0], c[1]); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestBuildInput(t *testing.T) {
	p, err := prepare(t, okParams+"    nspin: 2\n    starting_magnetization: {Si: 0.5}\n    occupations: smearing\n    smearing: mv\n    degauss: 0.02\n", "")
	if err != nil {
		t.Fatal(err)
	}
	in, err := BuildInput(&p.Canonical, p.Files["silicon.cif"])
	if err != nil {
		t.Fatal(err)
	}
	s := string(in)
	for _, want := range []string{
		"outdir = '/work/scratch'", "pseudo_dir = '/work/pseudo'", "prefix = 'pumat'",
		"ecutwfc = 3.0d1", "conv_thr = 1.0d-8", "starting_magnetization(1) = 5.0d-1",
		"nat = 2", "ntyp = 1", "Si 28.085 Si.upf", "K_POINTS automatic\n  4 4 4 0 0 0",
		"max_seconds = 1740",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("pw.in missing %q:\n%s", want, s)
		}
	}
	if EstimatePools(&p.Canonical) != 4 {
		t.Errorf("pools = %d", EstimatePools(&p.Canonical))
	}
}

func TestCIFSymmetryExpansion(t *testing.T) {
	cif := `data_Fe
_cell_length_a 2.87
_cell_length_b 2.87
_cell_length_c 2.87(1)
_cell_angle_alpha 90
_cell_angle_beta 90
_cell_angle_gamma 90
loop_
_space_group_symop_id
_space_group_symop_operation_xyz
1 x,y,z
2 'x+1/2, y+1/2, z+1/2'
3 -x,-y,-z
loop_
_atom_site_label
_atom_site_fract_x
_atom_site_fract_y
_atom_site_fract_z
Fe1 0 0 0
`
	st, err := ParseCIF([]byte(cif))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Atoms) != 2 || st.Atoms[0].Element != "Fe" {
		t.Fatalf("expected 2 Fe atoms, got %+v", st.Atoms)
	}
	bad := strings.Replace(cif, "Fe1 0 0 0", "Fe1 0 0 0 0.5", 1)
	bad = strings.Replace(bad, "_atom_site_fract_z\n", "_atom_site_fract_z\n_atom_site_occupancy\n", 1)
	if _, err := ParseCIF([]byte(bad)); err == nil {
		t.Fatal("partial occupancy must be rejected")
	}
	noOps := "data_x\n_cell_length_a 1\n_cell_length_b 1\n_cell_length_c 1\n_cell_angle_alpha 90\n_cell_angle_beta 90\n_cell_angle_gamma 90\n_symmetry_space_group_name_H-M 'F m -3 m'\nloop_\n_atom_site_label\n_atom_site_fract_x\n_atom_site_fract_y\n_atom_site_fract_z\nNa 0 0 0\n"
	if _, err := ParseCIF([]byte(noOps)); err == nil {
		t.Fatal("space group without ops must be rejected")
	}
}
