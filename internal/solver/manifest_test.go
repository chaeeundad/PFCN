package solver

import (
	"strings"
	"testing"
	"time"

	"github.com/chaeeundad/PFCN/internal/identity"
)

const manifestYAML = `schema: pumat.solver.v1
name: quantum-espresso
version: "7.4.1"
source: {repository: https://gitlab.com/QEF/q-e, commit: qe-7.4.1, license: GPL-2.0-or-later}
artifacts:
  - platform: linux/arm64
    reference: localhost:5000/pumat-solvers/quantum-espresso
    mediaType: application/vnd.oci.image.manifest.v1+json
    digest: sha256:2222222222222222222222222222222222222222222222222222222222222222
entrypoints:
  pw.x: {launcher: mpi, network: deny, maxProcesses: 512, maxWalltime: 24h}
security: {rootFilesystem: readOnly, network: deny, privileged: false, hostMounts: false}
numerics: {blas: openblas, blasVersion: "0.3.21", cpuDispatch: generic}
parser:
  name: pumat-qe-parser
  version: "0.1.0"
  format: wasm
  reference: builtin
  digest: sha256:3333333333333333333333333333333333333333333333333333333333333333
attestations: {sbom: "", buildProvenance: ""}
`

func TestSignVerify(t *testing.T) {
	key, _ := identity.Generate(t.TempDir())
	other, _ := identity.Generate(t.TempDir())
	m, err := FromYAML([]byte(manifestYAML), time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	env, err := Sign(m, key)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Policy{TrustedSigners: []string{key.PeerID.String()}}.Verify(env)
	if err != nil {
		t.Fatal(err)
	}
	if v.Digest[:7] != "sha256:" || v.Manifest.Entrypoints["pw.x"].MaxWalltimeSeconds != 86400 {
		t.Fatalf("%+v", v)
	}
	if _, err := (Policy{TrustedSigners: []string{other.PeerID.String()}}).Verify(env); err == nil {
		t.Fatal("untrusted signer must be rejected")
	}
	if _, err := (Policy{}).Verify(env); err == nil {
		t.Fatal("empty policy must trust nothing")
	}
	if _, err := (Policy{TrustedSigners: []string{key.PeerID.String()}, AllowSolvers: []string{"lammps"}}).Verify(env); err == nil {
		t.Fatal("allowlist must apply")
	}
	if _, ok := v.Manifest.ArtifactFor("linux/amd64"); ok {
		t.Fatal("no amd64 artifact expected")
	}
}

func TestSecurityFloor(t *testing.T) {
	for _, bad := range []string{
		strings.Replace(manifestYAML, "network: deny, privileged", "network: allow, privileged", 1),
		strings.Replace(manifestYAML, "privileged: false", "privileged: true", 1),
		strings.Replace(manifestYAML, "pw.x: {launcher: mpi, network: deny", "pw.x: {launcher: mpi, network: allow", 1),
		strings.Replace(manifestYAML, "format: wasm", "format: native", 1),
	} {
		if _, err := FromYAML([]byte(bad), time.Now()); err == nil {
			t.Error("expected security floor violation")
		}
	}
}
