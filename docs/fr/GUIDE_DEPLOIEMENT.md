# Déployer le site public Étalon

Ce guide installe, sur un serveur Linux, un nœud Étalon qui sert le site public — portefeuille web,
explorateur, pages de présentation — derrière Caddy, qui fournit le HTTPS. Le même serveur est aussi un
nœud d'amorçage du réseau : les nouveaux nœuds s'y connectent pour trouver leurs premiers pairs. Les fichiers à installer sont
dans le dossier [`deploy/`](../../deploy) du dépôt. Le fonctionnement du mode public est décrit dans
[ARCHITECTURE.md](../ARCHITECTURE.md) (section *Site public*) et dans le
[guide de l'opérateur](GUIDE_OPERATEUR.md) (section *Site public*).

*English version: [Deployment guide](../en/DEPLOYMENT_GUIDE.md).*

```
Internet ──443──▶ Caddy (certificat Let's Encrypt) ──▶ 127.0.0.1:3001  nœud Étalon (archive, passif)
Internet ──3000─▶ nœud Étalon (réseau pair à pair)
```

Le nœud n'écoute jamais directement sur Internet pour le site : seul Caddy est joignable sur le port 443.
Le nœud est **archive** (il garde toute la chaîne, pour l'explorateur) et **passif** (aucune clé) : une
machine exposée sur Internet ne porte aucune clé privée.

Dans tout le guide, **remplacez les exemples par vos valeurs réelles** :

| Espace réservé | Valeur | Exemple |
|---|---|---|
| `<HOSTNAME>` | votre nom de domaine réel | `etalon-etl.com` |
| `<IP_VPS>` | l'adresse IPv4 publique de votre serveur | `203.0.113.7` |
| `<ALT_HOSTNAME>` | un second nom de domaine, redirigé vers le site (facultatif) | `etalon-etl.org` |

`etalon-etl.com`, `etl1.etalon-etl.com` et `etalon-etl.org` sont les noms d'ETL-FR, pas des valeurs
imposées : le guide fonctionne avec n'importe quel nom de domaine. Les fichiers de `deploy/` contiennent
les espaces réservés `<HOSTNAME>`, `<IP_VPS>` et `<ALT_HOSTNAME>`, remplacés à l'installation par les
commandes ci-dessous.

---

## 1. Ce qu'il faut

- Un serveur (VPS) sous **Debian 12** ou **Ubuntu 24.04**, avec un accès SSH par clé et un utilisateur
  qui peut lancer `sudo`.
- Le **nom de domaine** pointant vers le serveur : chez le registrar, trois enregistrements DNS `A`, tous
  vers `<IP_VPS>` :

  | Nom | Rôle |
  |---|---|
  | `<HOSTNAME>` | le site |
  | `www.<HOSTNAME>` | redirigé vers le site |
  | `etl1.<HOSTNAME>` | le nœud d'amorçage du réseau pair à pair (section 6) |
  | `<ALT_HOSTNAME>` et `www.<ALT_HOSTNAME>` | second domaine, redirigé vers le site (facultatif) |

  Le certificat HTTPS ne peut être obtenu qu'une fois ces enregistrements visibles :
  `dig +short <HOSTNAME>` doit renvoyer `<IP_VPS>`.
- **L'heure exacte** sur le serveur : `timedatectl status` doit afficher
  `System clock synchronized: yes` (sinon `sudo timedatectl set-ntp true`).
- Sur votre ordinateur : Go **1.27.2 ou plus récent** et une copie du dépôt, pour compiler le logiciel.

Le choix de l'hébergeur et du registrar n'est pas traité ici.

## 2. Compiler le logiciel pour le serveur

Sur votre ordinateur, à la racine du dépôt. Vérifiez d'abord l'architecture du serveur avec `uname -m` :
`x86_64` donne `GOARCH=amd64`, `aarch64` donne `GOARCH=arm64`.

```sh
GOOS=linux GOARCH=amd64 go build -trimpath -o etl-linux ./cmd/etl
```

**`-trimpath` est obligatoire.** Sans lui, le binaire contient les chemins des fichiers sur votre
ordinateur, dont votre nom d'utilisateur.

Copiez ensuite le binaire et les fichiers de `deploy/` sur le serveur :

```sh
scp etl-linux deploy/config.toml deploy/etl.service deploy/Caddyfile deploy/firewall.sh <IP_VPS>:/tmp/
```

## 3. Installer le nœud

Sur le serveur, renseignez d'abord vos valeurs ; les commandes des sections 3 et 4 les reprennent (dans
la même session SSH, sinon redéfinissez-les) :

```sh
SITE=etalon-etl.com   # remplacez par votre nom de domaine réel
IP=203.0.113.7        # remplacez par l'adresse IPv4 publique de votre serveur
ALT=etalon-etl.org    # second domaine redirigé vers le site (facultatif)
```

```sh
# Utilisateur système dédié, sans shell ; ses données dans /var/lib/etl.
sudo useradd --system --home-dir /var/lib/etl --shell /usr/sbin/nologin etl
sudo install -d -m 0700 -o etl -g etl /var/lib/etl

# Logiciel et configuration.
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

`journalctl -u etl -n 20` doit montrer, tant qu'ETL-FR n'est pas lancé : `réseau pas encore lancé : site public de présentation seul`.

La configuration (`/etc/etl/config.toml`) ne contient aucun secret. Elle n'est valable qu'avec un nœud
passif (`wallet = ""`) et archive (`pruning = false`) : le nœud refuse de démarrer autrement.

## 4. Installer Caddy

Paquet officiel de Caddy :

```sh
sudo apt install -y debian-keyring debian-archive-keyring apt-transport-https curl gnupg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update
sudo apt install -y caddy
```

Puis sa configuration :

```sh
sudo install -m 0644 /tmp/Caddyfile /etc/caddy/Caddyfile
sudo sed -i "s/<HOSTNAME>/$SITE/g; s/<IP_VPS>/$IP/g; s/<ALT_HOSTNAME>/$ALT/g" /etc/caddy/Caddyfile
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

Sans second domaine, supprimez le dernier bloc du Caddyfile (`<ALT_HOSTNAME>, www.<ALT_HOSTNAME> { … }`)
avant `caddy validate`. Caddy obtient les certificats au premier accès, puis les renouvelle seul.

Ce que fait ce Caddyfile :

- il transmet toutes les requêtes au nœud (`127.0.0.1:3001`), avec le nom du site seul (sans port) ;
- il redirige `www.<HOSTNAME>`, le second domaine (`<ALT_HOSTNAME>`, avec ou sans `www`) et `http://` vers
  `https://<HOSTNAME>`, en gardant la page demandée ;
- il remplace l'en-tête `X-Forwarded-For` par l'adresse du visiteur : un visiteur ne peut pas se faire
  passer pour un autre auprès de la limitation de débit du nœud ;
- il n'écrit **aucune adresse IP de visiteur** (voir section 8).

Les en-têtes de sécurité (CSP, HSTS…) et la limitation de débit viennent du nœud lui-même.

## 5. Pare-feu

```sh
sudo apt install -y ufw
sudo sh /tmp/firewall.sh
```

Ports ouverts en entrée : 22 (SSH), 80 (certificat et redirection), 443 (HTTPS, TCP et UDP pour
HTTP/3), 3000 (réseau pair à pair du nœud, amorçage compris). Tout le reste est fermé. Si votre SSH écoute sur un autre
port, modifiez `firewall.sh` **avant** de le lancer, sinon vous perdez l'accès au serveur.

## 6. Nœud d'amorçage (etl1)

Un nouveau nœud doit trouver ses premiers pairs. Le logiciel contient une liste de nœuds d'amorçage
(`bootstrap` dans `config.toml`), qui commence par `etl1.etalon-etl.com:3000` : ce serveur. Rien à
installer en plus — le nœud du site écoute déjà sur le port 3000, ouvert par le pare-feu. Il suffit que
le nom `etl1.<HOSTNAME>` pointe vers `<IP_VPS>` (section 1).

Vérification, depuis votre ordinateur :

```sh
dig +short etl1.<HOSTNAME>            # <IP_VPS>
nc -vz etl1.<HOSTNAME> 3000           # connexion acceptée
```

**Si votre nom de domaine n'est pas `etalon-etl.com`**, la liste d'amorçage par défaut du logiciel ne
désigne pas votre serveur : il faut la changer dans le code (`config/default.toml` et la fonction
`Default` de `internal/config`) et publier une nouvelle version, sinon les nouveaux nœuds devront ajouter
`etl1.<HOSTNAME>:3000` à la main dans leur `config.toml`.

## 7. Vérifier

Depuis votre ordinateur :

```sh
curl -sI https://<HOSTNAME>/ | grep -i strict-transport-security   # HSTS présent
curl -s https://<HOSTNAME>/api/status                                # {"launched":false,…} avant le lancement
curl -s -o /dev/null -w "%{http_code}\n" -X POST -H "Content-Type: application/json" -d '{}' \
  https://<HOSTNAME>/api/operator/ipc-read                           # 404 : aucune route de l'opérateur
```

Puis ouvrez `https://<HOSTNAME>` dans un navigateur : avant le lancement, l'accueil et « Rejoindre »
avec le bandeau « Le réseau ETL-FR n'est pas encore lancé ».

Sur le serveur :

```sh
systemctl status etl caddy
journalctl -u caddy | grep -c remote_ip   # 0 : aucune adresse de visiteur
```

## 8. Vie privée des visiteurs

**Aucune adresse IP de visiteur n'est écrite, nulle part :**

- **Le nœud** ne journalise aucune adresse. La limitation de débit garde les adresses en mémoire
  seulement, et les oublie après une minute d'inactivité.
- **Caddy** n'a pas de journal d'accès. Les journaux qui citent l'adresse du client (erreurs de requête,
  du proxy, de poignée de main TLS) sont écartés par le Caddyfile ; restent ceux des certificats.
- **Le pare-feu** n'écrit pas les connexions refusées (`ufw logging off`).

D'autres journaux du serveur citent des adresses, mais pas celles des visiteurs du site : les connexions
SSH (les vôtres, et les tentatives de connexion d'inconnus) dans le journal du système.

## 9. Au lancement d'ETL-FR

Le jour du lancement, une nouvelle version du logiciel porte l'empreinte du genesis d'ETL-FR, et le
fichier `genesis.json` est publié avec elle.

```sh
# Sur votre ordinateur : compiler la version de lancement (toujours avec -trimpath), copier.
scp etl-linux genesis.json <IP_VPS>:/tmp/

# Sur le serveur.
sudo install -m 0755 /tmp/etl-linux /usr/local/bin/etl
sudo install -m 0644 -o etl -g etl /tmp/genesis.json /var/lib/etl/genesis.json
sudo systemctl restart etl
journalctl -u etl -f        # « nœud démarré », puis la hauteur augmente
```

Le nœud rejoint ETL-FR, télécharge la chaîne et construit l'index de l'explorateur. Le site affiche alors
de lui-même le portefeuille, les échanges, l'explorateur et l'IPC : rien à changer dans Caddy ni dans la
configuration.

## 10. Mettre à jour le logiciel

Compiler la nouvelle version avec `-trimpath`, la copier sur le serveur, puis :

```sh
sudo install -m 0755 /tmp/etl-linux /usr/local/bin/etl
sudo systemctl restart etl
```

## 11. Sauvegardes

Le serveur ne porte **aucun secret** : pas de clé, pas de phrase de passe. Sa configuration est dans le
dépôt (`deploy/`), et la chaîne se retélécharge auprès des autres nœuds. Un serveur perdu se réinstalle
en suivant ce guide.
