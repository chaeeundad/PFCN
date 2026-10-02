package sandbox

import (
	"strings"
	"testing"
	"time"
)

func TestArgsAreHardened(t *testing.T) {
	c := &Container{engine: "docker", path: "docker"}
	args := strings.Join(c.Args(Spec{
		Name: "pumat-x", Image: "r@sha256:abc", Entrypoint: "pw.x",
		InputDir: "/w/in", PseudoDir: "/w/p", ScratchDir: "/w/s", OutputDir: "/w/o",
		CPUs: 2, MemoryBytes: 1 << 30, PIDs: 256, Walltime: time.Minute,
		Env: map[string]string{"PUMAT_NPROCS": "2"},
	}, 1000, 1000), " ")
	for _, want := range []string{
		"--network none", "--read-only", "--cap-drop ALL", "--security-opt no-new-privileges",
		"--pids-limit 256", "--memory 1073741824", "--memory-swap 1073741824", "--cpus 2",
		"--user 1000:1000", "--pull never", "/w/in:/work/in:ro", "/w/p:/work/pseudo:ro",
		"r@sha256:abc pw.x",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	for _, bad := range []string{"--privileged", "--network host", "--pid host"} {
		if strings.Contains(args, bad) {
			t.Errorf("unexpected %q", bad)
		}
	}
}

func TestNormalizePlatform(t *testing.T) {
	if NormalizePlatform("linux/aarch64") != "linux/arm64" || NormalizePlatform("linux/x86_64") != "linux/amd64" {
		t.Fatal("platform normalization")
	}
}
