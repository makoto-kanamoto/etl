# Deploying the Étalon public site

This guide installs, on a Linux server, an Étalon node that serves the public site — web wallet,
explorer, presentation pages — behind Caddy, which provides HTTPS. The same server is also a bootstrap
node of the network: new nodes connect to it to find their first peers. The files to install are in the
repository's [`deploy/`](../../deploy) folder. Public mode is described in
[ARCHITECTURE.md](../ARCHITECTURE.md) (in French, *Site public* section) and in the
[operator guide](OPERATOR_GUIDE.md) (*Public site* section).

*This is a translation of the [French guide](../fr/GUIDE_DEPLOIEMENT.md). In case of discrepancy, the
French version prevails.*

```
Internet ──443──▶ Caddy (Let's Encrypt certificate) ──▶ 127.0.0.1:3001  Étalon node (archive, passive)
Internet ──3000─▶ Étalon node (peer-to-peer network)
```

For the site, the node never listens on the Internet directly: only Caddy can be reached, on port 443.
The node is an **archive** node (it keeps the whole chain, for the explorer) and a **passive** one (no
key): a machine exposed to the Internet holds no private key.

Throughout the guide, **replace the examples with your real values**:

| Placeholder | Value | Example |
|---|---|---|
| `<HOSTNAME>` | your real domain name | `etalon-etl.fr` |
| `<IP_VPS>` | your server's public IPv4 address | `203.0.113.7` |

`etalon-etl.fr` and `etl1.etalon-etl.fr` are the names planned for ETL-FR, not required values: the
guide works with any domain name. The `deploy/` files contain the `<HOSTNAME>` and `<IP_VPS>`
placeholders, replaced during installation by the commands below.

---

## 1. Requirements

- A server (VPS) running **Debian 12** or **Ubuntu 24.04**, with key-based SSH access and a user who can
  run `sudo`.
- The **domain name** pointing to the server: at the registrar, three DNS `A` records, all pointing to
  `<IP_VPS>`:

  | Name | Role |
  |---|---|
  | `<HOSTNAME>` | the site |
  | `www.<HOSTNAME>` | redirected to the site |
  | `etl1.<HOSTNAME>` | the bootstrap node of the peer-to-peer network (section 6) |

  The HTTPS certificate can only be obtained once these records are visible: `dig +short <HOSTNAME>` must
  return `<IP_VPS>`.
- **Accurate time** on the server: `timedatectl status` must show `System clock synchronized: yes`
  (otherwise `sudo timedatectl set-ntp true`).
- On your own computer: Go **1.27.2 or later** and a copy of the repository, to build the software.

The choice of hosting provider and registrar is not covered here.

## 2. Build the software for the server

On your computer, at the root of the repository. First check the server's architecture with
`uname -m`: `x86_64` means `GOARCH=amd64`, `aarch64` means `GOARCH=arm64`.

```sh
GOOS=linux GOARCH=amd64 go build -trimpath -o etl-linux ./cmd/etl
```

**`-trimpath` is mandatory.** Without it, the binary contains the paths of the files on your computer,
including your user name.

Then copy the binary and the `deploy/` files to the server:

```sh
scp etl-linux deploy/config.toml deploy/etl.service deploy/Caddyfile deploy/firewall.sh <IP_VPS>:/tmp/
```

## 3. Install the node

On the server, first set your two values; the commands in sections 3 and 4 reuse them (in the same SSH
session, otherwise set them again):

```sh
SITE=etalon-etl.fr   # replace with your real domain name
IP=203.0.113.7       # replace with your server's public IPv4 address
```

```sh
# Dedicated system user, no shell; its data in /var/lib/etl.
sudo useradd --system --home-dir /var/lib/etl --shell /usr/sbin/nologin etl
sudo install -d -m 0700 -o etl -g etl /var/lib/etl

# Software and configuration.
sudo install -m 0755 /tmp/etl-linux /usr/local/bin/etl
sudo install -d -m 0755 /etc/etl
sudo install -m 0644 /tmp/config.toml /etc/etl/config.toml
sudo sed -i "s/<HOSTNAME>/$SITE/" /etc/etl/config.toml

# Service.
sudo install -m 0644 /tmp/etl.service /etc/systemd/system/etl.service
sudo sed -i "s/<HOSTNAME>/$SITE/" /etc/systemd/system/etl.service
sudo systemctl daemon-reload
sudo systemctl enable --now etl
```

As long as ETL-FR has not launched, `journalctl -u etl -n 20` must show: `réseau pas encore lancé : site public de présentation seul`
(network not launched yet: presentation site only).

The configuration (`/etc/etl/config.toml`) holds no secret. It is only valid for a passive node
(`wallet = ""`) that is also an archive node (`pruning = false`): the node refuses to start otherwise.

## 4. Install Caddy

Official Caddy package:

```sh
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https curl gnupg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update
sudo apt install -y caddy
```

Then its configuration:

```sh
sudo install -m 0644 /tmp/Caddyfile /etc/caddy/Caddyfile
sudo sed -i "s/<HOSTNAME>/$SITE/g; s/<IP_VPS>/$IP/g" /etc/caddy/Caddyfile
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

Caddy obtains the certificate on the first visit to the site, then renews it on its own.

What this Caddyfile does:

- it forwards every request to the node (`127.0.0.1:3001`), with the bare site name (no port);
- it redirects `www.<HOSTNAME>` and `http://` to `https://<HOSTNAME>`;
- it replaces the `X-Forwarded-For` header with the visitor's address: a visitor cannot pose as someone
  else to the node's rate limiting;
- it writes **no visitor IP address** (see section 8).

The security headers (CSP, HSTS…) and rate limiting come from the node itself.

## 5. Firewall

```sh
sudo apt install -y ufw
sudo sh /tmp/firewall.sh
```

Inbound ports open: 22 (SSH), 80 (certificate and redirection), 443 (HTTPS, TCP and UDP for HTTP/3),
3000 (the node's peer-to-peer network, bootstrap included). Everything else is closed. If your SSH listens on another port,
edit `firewall.sh` **before** running it, or you will lose access to the server.

## 6. Bootstrap node (etl1)

A new node has to find its first peers. The software contains a list of bootstrap nodes (`bootstrap` in
`config.toml`), which starts with `etl1.etalon-etl.fr:3000`: this server. Nothing more to install — the
site's node already listens on port 3000, opened by the firewall. The name `etl1.<HOSTNAME>` only has to
point to `<IP_VPS>` (section 1).

Check, from your computer:

```sh
dig +short etl1.<HOSTNAME>            # <IP_VPS>
nc -vz etl1.<HOSTNAME> 3000           # connection accepted
```

**If your domain name is not `etalon-etl.fr`**, the software's default bootstrap list does not point to
your server: it has to be changed in the code (`config/default.toml` and the `Default` function in
`internal/config`) and a new version published; otherwise new nodes must add `etl1.<HOSTNAME>:3000` by
hand to their `config.toml`.

## 7. Check

From your computer:

```sh
curl -sI https://<HOSTNAME>/ | grep -i strict-transport-security   # HSTS present
curl -s https://<HOSTNAME>/api/status                                # {"launched":false,…} before launch
curl -s -o /dev/null -w "%{http_code}\n" -X POST -H "Content-Type: application/json" -d '{}' \
  https://<HOSTNAME>/api/operator/ipc-read                           # 404: no operator route
```

Then open `https://<HOSTNAME>` in a browser: before launch, the home page and “Join” with the banner
“The ETL-FR network has not launched yet”.

On the server:

```sh
systemctl status etl caddy
journalctl -u caddy | grep -c remote_ip   # 0: no visitor address
```

## 8. Visitors' privacy

**No visitor IP address is written anywhere:**

- **The node** logs no address. Rate limiting keeps addresses in memory only, and forgets them after one
  minute of inactivity.
- **Caddy** has no access log. The logs that quote the client's address (request, proxy and TLS
  handshake errors) are filtered out by the Caddyfile; the certificate logs remain.
- **The firewall** does not log refused connections (`ufw logging off`).

Other server logs do quote addresses, but not those of the site's visitors: SSH connections (yours, and
login attempts by strangers) in the system log.

## 9. When ETL-FR launches

On launch day, a new version of the software carries the fingerprint of the ETL-FR genesis, and the
`genesis.json` file is published with it.

```sh
# On your computer: build the launch version (always with -trimpath), copy.
scp etl-linux genesis.json <IP_VPS>:/tmp/

# On the server.
sudo install -m 0755 /tmp/etl-linux /usr/local/bin/etl
sudo install -m 0644 -o etl -g etl /tmp/genesis.json /var/lib/etl/genesis.json
sudo systemctl restart etl
journalctl -u etl -f        # « nœud démarré » (node started), then the height goes up
```

The node joins ETL-FR, downloads the chain and builds the explorer index. The site then shows the
wallet, exchanges, explorer and CPI pages by itself: nothing to change in Caddy or in the configuration.

## 10. Update the software

Build the new version with `-trimpath`, copy it to the server, then:

```sh
sudo install -m 0755 /tmp/etl-linux /usr/local/bin/etl
sudo systemctl restart etl
```

## 11. Backups

The server holds **no secret**: no key, no passphrase. Its configuration is in the repository
(`deploy/`), and the chain is downloaded again from the other nodes. A lost server is reinstalled by
following this guide.
