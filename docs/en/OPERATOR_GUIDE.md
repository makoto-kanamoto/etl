# Étalon (ETL) node operator guide

This guide explains how to install, start and monitor a node. The overall architecture is described in
[ARCHITECTURE.md](../ARCHITECTURE.md) (in French).

> **Current status**: the ETL-FR network has not launched yet. Until its genesis hash is built into the
> software, `etl run` refuses to join it. To try things out, use the [test network](#test-network).

> **Language**: the `etl` command and its log currently print their messages in French. The
> [messages table](#9-messages-and-what-to-do) below quotes them as printed, with an English explanation.
> The dashboard is available in English (language menu, top right).

*This is a translation of the [French guide](../fr/GUIDE_OPERATEUR.md). In case of discrepancy, the
French version prevails.*

---

## 1. Requirements

- A machine that stays on all the time (server, mini-PC), running Linux or macOS.
- **Accurate time, synchronised by NTP.** Consensus depends on it: the participation window lasts
  30 seconds and the block is produced every day at 23:00 UTC.
  - Linux (systemd): `timedatectl status` must show `System clock synchronized: yes`; otherwise run
    `sudo timedatectl set-ntp true`.
  - macOS: System Settings → General → Date & Time → "Set time and date automatically".
- Port **3000** (TCP) open to the outside if possible. Without an open port the node still works, by
  connecting out to other nodes only; in that case set `max_inbound = 0`.
- To build: Go (version given in `go.mod`).

```sh
go build -o etl ./cmd/etl
```

## 2. First start

```sh
etl init
```

`init` creates the `~/.etl` directory, the configuration `~/.etl/config.toml`, then the **node key**
(`~/.etl/wallet.key`), encrypted with a passphrase you choose (at least 12 characters).

It then displays **24 words**: they are the only backup of the node key.

- Write them down on paper, in order, and keep them somewhere safe.
- Do not photograph them or copy them into a file.
- They are **not** compatible with Bitcoin or Ethereum wallets: never type them anywhere but in Étalon.

Options: `etl init -lang en` for English words; `etl -home DIRECTORY init` for a directory other than
`~/.etl`.

Then put the **`genesis.json` file published with your version** of the software into `~/.etl/`. The
node checks its hash: a modified file is refused.

## 3. Starting the node

```sh
etl run        # or simply: etl
```

The passphrase is asked for at the keyboard, without being displayed. The log shows `nœud démarré`
(node started), with the node's address and the dashboard link. **Ctrl-C** stops the node cleanly
(`nœud arrêté`, node stopped).

### Unattended server

For automatic start-up (a service manager such as systemd), the passphrase can be read from a file:

```sh
chmod 600 ~/.etl/pass
etl run -passphrase-file ~/.etl/pass
```

The file is refused if users other than you can read it. Be aware that it offers less protection than
typing at the keyboard: anyone who can read this file **and** `wallet.key` can use the node key. The
passphrase is **never** accepted in an environment variable or a command-line argument, because other
processes and the shell history could see it.

### Passive node

With `wallet = ""` in the configuration, the node follows and verifies the chain without a key: no
voting, no reading of the CPI, no reward.

## 4. The dashboard

Open **http://localhost:3001** in a browser, on the node's machine.

- The dashboard listens on `127.0.0.1` only: it can never be reached from the network.
- On a remote server, use an SSH tunnel rather than opening port 3001:
  `ssh -L 3001:localhost:3001 user@server`, then open http://localhost:3001 on your own computer.
- Operator actions (parameter votes, exclusion, sponsorship, arbitration, re-reading the CPI) require
  the **operator token**, stored in `~/.etl/dashboard.token`. The token changes every time the node
  starts.

### The health indicator on the home page

On the home page, the “Next block” card shows **“Network healthy”** when the four conditions below are
met, otherwise **“Needs attention”** with the list of what is wrong.

| Condition | If it is not met |
|---|---|
| At least 2 connected peers | Check the Internet connection, the P2P port (3000 by default) and the `bootstrap` list. |
| Clock within 2 seconds of the peers' clocks | Synchronise the system time with NTP (section 1): consensus requires it. |
| Node not halted | A finality conflict has halted the node: do not restart it blindly; keep the log and warn the other operators. |
| Last block less than 25 hours ago | A block is produced every day at 23:00 UTC: beyond that, the node may no longer be in sync (peers, log). |

A lone node with no peers therefore shows “Needs attention”: this is expected as long as it has not
joined any network.

The “This node” card also shows its participation in the last 30 blocks and the reward share received
in the last block.

### Public site (node exposed to the Internet)

A node can serve the public site (web wallet and explorer) **instead of** the dashboard, behind an HTTPS
proxy installed on the same machine (Caddy, for example). Step-by-step installation on a server, with the
ready-made files of the `deploy/` folder: [deployment guide](DEPLOYMENT_GUIDE.md). `[public]` section of
`config.toml`:

```toml
[node]
wallet  = ""        # required: passive node, no key on an exposed machine
pruning = false     # required: archive node (full explorer history)

[public]
enabled = true
host    = "etalon-etl.fr"   # site name only, without https:// or a port
read_per_minute  = 300      # API reads per IP address per minute
write_per_minute = 6        # transfers and exchanges submitted per IP address per minute
```

- The node still listens on `127.0.0.1` only (port `[dashboard] port`): only the proxy can be reached
  from the Internet. The proxy must pass the site name in `Host` and add the visitor's address to
  `X-Forwarded-For` (Caddy's default behaviour).
- No token, no operator panel: these routes do not exist in public mode.
- Visitors' IP addresses are used only for rate limiting, in memory; the node writes none of them.
  Configure the proxy without an access log.
- On the first start with `[public]`, the node builds the explorer index from the blocks (a few seconds).
  A node that has already pruned its chain cannot build it: start again from a full chain.
- **Before ETL-FR launches**, `etl run` does not refuse to start: it serves the presentation pages only
  (log: `réseau pas encore lancé : site public de présentation seul`, network not launched yet:
  presentation site only).

## 5. Other commands

| Command | Purpose |
|---|---|
| `etl address` | shows the node's address |
| `etl passphrase` | changes the passphrase (the old one is asked for, the new one twice) |
| `etl restore` | recreates the node key from the 24 words (after a failure, on a new machine) |
| `etl version` | shows the version |
| `etl genesis` | prepares a `genesis.json` (launch ceremony, test network) |

General options: `-home DIRECTORY` (default `~/.etl`) and `-config FILE` (default `HOME/config.toml`).

## 6. Configuration

`~/.etl/config.toml` contains **local settings only**: network joined, directories, ports, pruning,
bootstrap peers, mempool limits, CPI source, dashboard. Each setting is commented in the file (in
French).

- **Pruning** (`pruning = true`, the default): the node keeps the content of the last 90 blocks, plus
  every header. `pruning = false` makes an archive node (useful for bootstrapping and the explorer).
- **Consensus rules are not set here** (reward, fees, difficulty, block time…). They change only by
  on-chain vote. A file that tries to change them is refused at start-up.
- An unknown or misspelt key is refused, with its line number.

## 7. Privacy

- **Peers see your node's IP address**, as in any peer-to-peer network.
- **INSEE (the French statistics institute) also sees the node's IP address.** A node with a key reads
  the CPI from INSEE's API once a day, at a random minute between 20:00 and 22:30 UTC (at most 3
  attempts), plus any manual re-reads you trigger from the dashboard (at most one every 5 minutes).
- To route this request through a proxy, set `HTTPS_PROXY` before starting the node:
  `HTTPS_PROXY=http://proxy.example:3128 etl run`. This setting only affects reading the CPI, not
  connections between nodes.

## 8. Backups

- **The 24 words** are enough to recreate the node key (`etl restore`). They are the only backup you
  cannot do without.
- `wallet.key` is encrypted with the passphrase: copying it elsewhere is only useful if you also know
  that passphrase.
- The chain (`~/.etl/chain`) needs no backup: a fresh node downloads it again from its peers.

## 9. Messages and what to do

| Message (as printed) | Cause | What to do |
|---|---|---|
| `ce réseau n'est pas encore lancé` | ETL-FR has no genesis in this version yet | wait for the launch version, or try the test network |
| `genesis.json ne correspond pas à l'empreinte gravée` | file modified or from another version | use the `genesis.json` published with your version |
| `clé du nœud introuvable` | `wallet.key` missing | `etl init`, `etl restore`, or `wallet = ""` for a passive node |
| `phrase de passe incorrecte ou coffre altéré` | wrong passphrase | type it again; if forgotten, `etl restore` with the 24 words |
| `coffre créé pour un autre réseau` | the key was created for another network (test or ETL-FR) | check `network` in the configuration, or use another `-home` |
| `permissions trop ouvertes` | passphrase file readable by others | `chmod 600 FILE` |
| `pas de terminal pour la saisie` | started without a keyboard (service) | use `-passphrase-file` |
| `les règles de consensus ne se règlent pas ici` | consensus rule in `config.toml` | remove the line indicated |
| `site public : nœud passif seulement` | `wallet` is not empty while `[public]` is enabled | `wallet = ""` |
| `site public : nœud archive seulement` | `pruning = true` while `[public]` is enabled | `pruning = false` |
| `l'index de l'explorateur exige un nœud archive` | the local chain has already been pruned | delete `~/.etl/chain`: the node downloads the full chain again |
| `horloge décalée par rapport aux pairs` | the machine's clock differs from its peers' by more than 2 s | check NTP synchronisation (section 1) |
| `ARRÊT DU NŒUD : conflit de finalité` | two blocks finalised at the same height: a serious abnormal situation | do not restart blindly; keep the log and warn the other operators |

## Test network

The test network is for trying things out. It has no value and cannot be mistaken for ETL-FR:
identifier `ETL-TEST`, addresses `etlt1q…`.

1. Create one directory per node, with a configuration containing `network = "test"` (before
   `etl init`, because the key is tied to the network); change `port` and `[dashboard] port` if several
   nodes run on the same machine, and list the other nodes under `bootstrap`.
2. Run `etl -home DIRECTORY init` for each node, then note the addresses
   (`etl -home DIRECTORY address`).
3. Write the founders' addresses (at least 4), one per line, into a file, then:

   ```sh
   etl genesis -network test -date 2026-10-20 -founders founders.txt \
     -ipc-value 103070 -ipc-period 2026-Q3 -ipc-sha256 <64 hexadecimal characters> \
     -out genesis.json
   ```

4. Copy this `genesis.json` into each node's directory and run `etl -home DIRECTORY run`.
