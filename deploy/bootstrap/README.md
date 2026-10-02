# Bootstrap / relay node

A bootstrap node helps new peers join the DHT; a relay node lets peers behind
NAT reach each other when hole punching fails. Neither schedules jobs, sees
job data, or authorizes execution (spec §9.1). Both are the normal `pumat`
binary with a different configuration, and anyone can run one.

Requirements: a small VM with a static public IP and UDP/TCP 4001 open
(on AWS Lightsail: attach a static IP, and add TCP 4001 and UDP 4001 to both
the IPv4 and the IPv6 firewall).

```bash
curl -fsSL https://raw.githubusercontent.com/chaeeundad/PFCN/main/scripts/install.sh | sudo sh
sudo -u pumat PUMAT_HOME=/var/lib/pumat pumat init
sudo cp config.example.yaml /var/lib/pumat/config.yaml   # then edit
sudo chown pumat:pumat /var/lib/pumat/config.yaml && sudo chmod 600 /var/lib/pumat/config.yaml
sudo systemctl enable --now pumat-agent
sudo -u pumat PUMAT_HOME=/var/lib/pumat pumat status   # copy the public /p2p/ address
```

Cloud VMs behind 1:1 NAT (AWS, Lightsail, most clouds) only see their
private address. Set `network.announce` to the public addresses and
`network.reachability: public`; without the latter the relay service does
not start until AutoNAT confirms reachability, which can take a long time on
a small network.

Do not run `pumat on`: bootstrap nodes do not need a container engine and
should not execute jobs.

Give the public address to users, who add it under `network.bootstrap`
(and, for relays, `network.staticRelays`) in their `config.yaml`.

## Project bootstrap set (DNS)

Default configs use `/dnsaddr/pfcn.pumat.org`: nodes read the bootstrap set
from the `_dnsaddr.pfcn.pumat.org` TXT records at startup, so servers can be
added or replaced in DNS without a release. Each record is one address:

```text
_dnsaddr.pfcn.pumat.org.  TXT  "dnsaddr=/dns4/pfcn.pumat.org/udp/4001/quic-v1/p2p/<peer-id>"
_dnsaddr.pfcn.pumat.org.  TXT  "dnsaddr=/dns4/pfcn.pumat.org/tcp/4001/p2p/<peer-id>"
```

Give every server its own A record (e.g. `pfcn.pumat.org`, `pfcn2.pumat.org`)
and add its two lines to the TXT set. Back up each server's
`/var/lib/pumat/identity/node.key`: restoring it keeps the peer ID when a
server is rebuilt. Bootstrap peers are not trusted for anything but
connectivity (§9.1), so DNS only affects availability.

Relay limits use go-libp2p's Circuit Relay v2 defaults (bounded reservations,
duration and bytes per relayed connection), so relays carry signalling and
small transfers; bulk data should use direct connections (§9.4).

To also run the public explorer on this node, set `indexer.enabled: true`
and put a TLS reverse proxy (e.g. Caddy) in front of `indexer.http`.
