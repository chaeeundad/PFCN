package agent

import (
	"context"
	"testing"

	madns "github.com/multiformats/go-multiaddr-dns"
)

func TestExpandDNSAddr(t *testing.T) {
	const id = "12D3KooWDDtqx4Wx1n4FruMVgNVj2J7UkQUiBZU3FDAiyVL59EcA"
	r, err := madns.NewResolver(madns.WithDefaultResolver(&madns.MockResolver{TXT: map[string][]string{
		"_dnsaddr.boot.example.org": {
			"dnsaddr=/ip4/192.0.2.1/udp/4001/quic-v1/p2p/" + id,
			"dnsaddr=/ip4/192.0.2.2/tcp/4001/p2p/" + id,
		},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	old := dnsResolver
	dnsResolver = r
	defer func() { dnsResolver = old }()

	got := expandDNSAddr(context.Background(), []string{
		"/dnsaddr/boot.example.org",
		"/dnsaddr/missing.example.org", // lookup fails: skipped, not fatal
		"/dns4/pfcn.pumat.org/tcp/4001/p2p/" + id,
	})
	if len(got) != 3 {
		t.Fatalf("expected 2 expanded + 1 passthrough, got %v", got)
	}
	infos, err := parseAddrInfos(got)
	if err != nil || len(infos) != 1 || len(infos[0].Addrs) != 3 {
		t.Fatalf("addresses must merge into one peer: %v %v", infos, err)
	}
}
