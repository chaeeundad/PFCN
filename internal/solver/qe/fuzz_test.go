package qe

import (
	"os"
	"testing"

	"github.com/chaeeundad/PFCN/internal/job"
)

func FuzzParseCIF(f *testing.F) {
	if b, err := os.ReadFile("../../../examples/qe-si-scf/silicon.cif"); err == nil {
		f.Add(b)
	}
	f.Add([]byte("data_x\nloop_\n_symmetry_equiv_pos_as_xyz\n'-x+1/2,y,z'\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		st, err := ParseCIF(in)
		if err != nil {
			return
		}
		if len(st.Atoms) == 0 || len(st.Atoms) > MaxAtoms {
			t.Fatalf("accepted structure with %d atoms", len(st.Atoms))
		}
	})
}

func FuzzParseJobYAML(f *testing.F) {
	f.Add([]byte("apiVersion: pumat.org/v1alpha1\nkind: QuantumEspressoJob\ncalculation: {type: scf, parameters: {ecutwfc: 30, kpoints: [1,1,1]}}\n"))
	f.Add([]byte("a: &x [*x]\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		s, err := job.ParseYAML(in)
		if err != nil {
			return
		}
		// Parameter normalization must not panic on arbitrary decoded values.
		_, _ = Adapter{}.NormalizeCalculation(s.Calculation)
	})
}
