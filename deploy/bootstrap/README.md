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
(and, for relays, `network.staticRelays`) in their `config.yaml`. Once the
project operates `bootstrap1/2.pumat.org`, they will be listed in
`config.DefaultBootstrap`.

Relay limits use go-libp2p's Circuit Relay v2 defaults (bounded reservations,
duration and bytes per relayed connection), so relays carry signalling and
small transfers; bulk data should use direct connections (§9.4).

To also run the public explorer on this node, set `indexer.enabled: true`
and put a TLS reverse proxy (e.g. Caddy) in front of `indexer.http`.
