# Guide de l'opérateur de nœud Étalon (ETL)

Ce guide explique comment installer, démarrer et surveiller un nœud. L'architecture d'ensemble est
décrite dans [ARCHITECTURE.md](../ARCHITECTURE.md).

*English version: [Node operator guide](../en/OPERATOR_GUIDE.md).*

> **État actuel** : le réseau ETL-FR n'est pas encore lancé. Tant que l'empreinte de son genesis n'est
> pas gravée dans le logiciel, `etl run` refuse de le rejoindre. Pour essayer, utilisez le
> [réseau de test](#réseau-de-test).

---

## 1. Ce qu'il faut

- Une machine allumée en permanence (serveur, mini-PC), Linux ou macOS.
- **L'heure exacte, synchronisée par NTP.** Le consensus en dépend : la fenêtre de participation dure
  30 secondes et le bloc est produit chaque jour à 23:00 UTC.
  - Linux (systemd) : `timedatectl status` doit afficher `System clock synchronized: yes` ; sinon,
    `sudo timedatectl set-ntp true`.
  - macOS : Réglages système → Général → Date et heure → « Régler automatiquement ».
- Le port **3000** (TCP) ouvert vers l'extérieur si possible. Sans port ouvert, le nœud fonctionne
  quand même en ne se connectant qu'à d'autres nœuds ; mettez alors `max_inbound = 0`.
- Pour compiler : Go (version indiquée dans `go.mod`).

```sh
go build -o etl ./cmd/etl
```

## 2. Première mise en route

```sh
etl init
```

`init` crée le répertoire `~/.etl`, la configuration `~/.etl/config.toml`, puis la **clé du nœud**
(`~/.etl/wallet.key`), chiffrée par une phrase de passe que vous choisissez (12 caractères au moins).

Il affiche ensuite **24 mots** : c'est la seule sauvegarde de la clé du nœud.

- Notez-les sur papier, dans l'ordre, et gardez-les en lieu sûr.
- Ne les photographiez pas et ne les copiez pas dans un fichier.
- Ils ne sont **pas** compatibles avec les portefeuilles Bitcoin ou Ethereum : ne les saisissez jamais
  ailleurs que dans Étalon.

Options : `etl init -lang en` pour des mots anglais ; `etl -home RÉPERTOIRE init` pour un autre
répertoire que `~/.etl`.

Placez ensuite dans `~/.etl/` le fichier **`genesis.json` publié avec la version** du logiciel. Le nœud
vérifie son empreinte : un fichier modifié est refusé.

## 3. Démarrer le nœud

```sh
etl run        # ou simplement : etl
```

La phrase de passe est demandée au clavier, sans affichage. Le journal indique `nœud démarré`, avec
l'adresse du nœud et le lien du dashboard. **Ctrl-C** arrête le nœud proprement (`nœud arrêté`).

### Serveur sans surveillance

Pour un démarrage automatique (gestionnaire de services comme systemd), la phrase de passe peut être lue
dans un fichier :

```sh
chmod 600 ~/.etl/pass
etl run -passphrase-file ~/.etl/pass
```

Le fichier est refusé s'il est lisible par d'autres utilisateurs que vous. Sachez qu'il protège moins
bien que la saisie au clavier : quiconque lit ce fichier **et** `wallet.key` peut utiliser la clé du
nœud. La phrase de passe n'est **jamais** acceptée en variable d'environnement ni en argument de
commande, car les autres processus et l'historique du shell pourraient la voir.

### Nœud passif

Avec `wallet = ""` dans la configuration, le nœud suit et vérifie la chaîne sans clé : pas de vote, pas
de lecture de l'IPC, pas de récompense.

## 4. Le dashboard

Ouvrez **http://localhost:3001** dans un navigateur, sur la machine du nœud.

- Le dashboard n'écoute que sur `127.0.0.1` : il n'est jamais joignable depuis le réseau.
- Sur un serveur distant, passez par un tunnel SSH, sans ouvrir le port 3001 :
  `ssh -L 3001:localhost:3001 utilisateur@serveur`, puis ouvrez http://localhost:3001 sur votre poste.
- Les actions de l'opérateur (votes de paramètres, exclusion, parrainage, arbitrage, relecture de l'IPC)
  demandent le **jeton opérateur**, contenu dans `~/.etl/dashboard.token`. Ce jeton change à chaque
  démarrage du nœud.

### L'indicateur de santé de l'accueil

Sur l'accueil, la carte « Prochain bloc » affiche **« Réseau en bonne santé »** quand les quatre
conditions ci-dessous sont remplies, sinon **« À surveiller »** avec la liste de ce qui ne va pas.

| Condition | Si elle n'est pas remplie |
|---|---|
| Au moins 2 pairs connectés | Vérifiez la connexion Internet, le port P2P (3000 par défaut) et la liste `bootstrap`. |
| Horloge à moins de 2 secondes de celle des pairs | Synchronisez l'heure du système par NTP (section 1) : le consensus l'exige. |
| Nœud non arrêté | Un conflit de finalité a arrêté le nœud : ne le redémarrez pas à l'aveugle, gardez le journal et prévenez les autres opérateurs. |
| Dernier bloc il y a moins de 25 heures | Un bloc est produit chaque jour à 23:00 UTC : au-delà, le nœud n'est peut-être plus synchronisé (pairs, journal). |

Un nœud seul, sans pair, affiche donc « À surveiller » : c'est normal tant qu'il n'a rejoint aucun
réseau.

La carte « Ce nœud » indique aussi sa participation aux 30 derniers blocs et la part de récompense
touchée au dernier bloc.

### Site public (nœud exposé sur Internet)

Un nœud peut servir le site public (portefeuille web et explorateur) **à la place** du dashboard, derrière
un proxy HTTPS installé sur la même machine (Caddy, par exemple). Section `[public]` de `config.toml` :

```toml
[node]
wallet  = ""        # obligatoire : nœud passif, aucune clé sur une machine exposée
pruning = false     # obligatoire : nœud archive (historique complet de l'explorateur)

[public]
enabled = true
host    = "etalon-etl.fr"   # nom du site seul, sans https:// ni port
read_per_minute  = 300      # lectures de l'API par adresse IP et par minute
write_per_minute = 6        # transferts et échanges soumis par adresse IP et par minute
```

- Le nœud écoute toujours sur `127.0.0.1` (port `[dashboard] port`) : seul le proxy est joignable depuis
  Internet. Il doit transmettre le nom du site dans `Host` et ajouter l'adresse du visiteur dans
  `X-Forwarded-For` (comportement par défaut de Caddy).
- Pas de jeton, pas de panneau de l'opérateur : ces routes n'existent pas en mode public.
- Les adresses IP des visiteurs ne servent qu'à la limitation de débit, en mémoire ; le nœud n'en écrit
  aucune. Configurez le proxy sans journal d'accès.
- Au premier démarrage avec `[public]`, le nœud construit l'index de l'explorateur à partir des blocs
  (quelques secondes). Un nœud déjà élagué ne peut pas le construire : il faut repartir d'une chaîne
  complète.
- **Avant le lancement d'ETL-FR**, `etl run` ne refuse pas de démarrer : il sert seulement les pages de
  présentation (journal : `réseau pas encore lancé : site public de présentation seul`).

## 5. Autres commandes

| Commande | Rôle |
|---|---|
| `etl address` | affiche l'adresse du nœud |
| `etl passphrase` | change la phrase de passe (l'ancienne est demandée, la nouvelle deux fois) |
| `etl restore` | recrée la clé du nœud à partir des 24 mots (après une panne, sur une nouvelle machine) |
| `etl version` | affiche la version |
| `etl genesis` | prépare un `genesis.json` (cérémonie de lancement, réseau de test) |

Options générales : `-home RÉPERTOIRE` (défaut `~/.etl`) et `-config FICHIER` (défaut
`HOME/config.toml`).

## 6. Configuration

`~/.etl/config.toml` ne contient que des **réglages locaux** : réseau rejoint, répertoires, ports,
pruning, pairs d'amorçage, limites du mempool, source de l'IPC, dashboard. Chaque réglage y est commenté.

- **Pruning** (`pruning = true`, défaut) : le nœud garde le contenu des 90 derniers blocs, plus tous les
  en-têtes. `pruning = false` fait un nœud archive (utile pour l'amorçage et l'explorateur).
- **Les règles de consensus ne se règlent pas ici** (récompense, frais, difficulté, heure du bloc…).
  Elles évoluent uniquement par vote on-chain. Un fichier qui tente de les modifier est refusé au
  démarrage.
- Une clé inconnue ou mal écrite est refusée, avec son numéro de ligne.

## 7. Vie privée

- **Les pairs voient l'adresse IP de votre nœud**, comme dans tout réseau pair à pair.
- **L'INSEE voit aussi l'adresse IP du nœud.** Un nœud avec clé lit l'IPC sur l'API de l'INSEE une fois
  par jour, à une minute tirée au hasard entre 20:00 et 22:30 UTC (3 tentatives au plus), plus vos
  relectures manuelles depuis le dashboard (une toutes les 5 minutes au plus).
- Pour faire passer cette lecture par un proxy, définissez `HTTPS_PROXY` avant de démarrer le nœud :
  `HTTPS_PROXY=http://proxy.exemple:3128 etl run`. Ce réglage ne concerne que la lecture de l'IPC, pas
  les connexions entre nœuds.

## 8. Sauvegardes

- **Les 24 mots** suffisent pour recréer la clé du nœud (`etl restore`). C'est la seule sauvegarde
  indispensable.
- `wallet.key` est chiffré par la phrase de passe : le copier ailleurs ne sert que si vous connaissez
  aussi cette phrase.
- La chaîne (`~/.etl/chain`) n'a pas besoin de sauvegarde : un nœud neuf la retélécharge auprès de
  ses pairs.

## 9. Messages et que faire

| Message | Cause | Que faire |
|---|---|---|
| `ce réseau n'est pas encore lancé` | ETL-FR n'a pas encore de genesis dans cette version | attendre la version de lancement, ou essayer le réseau de test |
| `genesis.json ne correspond pas à l'empreinte gravée` | fichier modifié ou d'une autre version | reprendre le `genesis.json` publié avec votre version |
| `clé du nœud introuvable` | `wallet.key` absent | `etl init`, `etl restore`, ou `wallet = ""` pour un nœud passif |
| `phrase de passe incorrecte ou coffre altéré` | mauvaise phrase de passe | ressaisir ; en cas d'oubli, `etl restore` avec les 24 mots |
| `coffre créé pour un autre réseau` | la clé a été créée pour un autre réseau (test ou ETL-FR) | vérifier `network` dans la configuration, ou utiliser un autre `-home` |
| `permissions trop ouvertes` | fichier de phrase de passe lisible par d'autres | `chmod 600 FICHIER` |
| `pas de terminal pour la saisie` | démarrage sans clavier (service) | utiliser `-passphrase-file` |
| `les règles de consensus ne se règlent pas ici` | règle de consensus dans `config.toml` | retirer la ligne indiquée |
| `site public : nœud passif seulement` | `wallet` n'est pas vide alors que `[public]` est activé | `wallet = ""` |
| `site public : nœud archive seulement` | `pruning = true` alors que `[public]` est activé | `pruning = false` |
| `l'index de l'explorateur exige un nœud archive` | la chaîne locale a déjà été élaguée | supprimer `~/.etl/chain` : le nœud retélécharge la chaîne complète |
| `horloge décalée par rapport aux pairs` | l'heure de la machine s'écarte de plus de 2 s de celle des pairs | vérifier la synchronisation NTP (section 1) |
| `ARRÊT DU NŒUD : conflit de finalité` | deux blocs finalisés à la même hauteur : situation anormale grave | ne pas redémarrer à l'aveugle ; conserver le journal et prévenir les autres opérateurs |

## Réseau de test

Le réseau de test sert aux essais. Il est sans valeur et ne peut pas être confondu avec ETL-FR :
identifiant `ETL-TEST`, adresses `etlt1q…`.

1. Créez un répertoire par nœud et sa configuration avec `network = "test"` (avant `etl init`, car la
   clé est liée au réseau) ; changez `port` et `[dashboard] port` si plusieurs nœuds tournent sur la
   même machine, et indiquez les autres nœuds dans `bootstrap`.
2. `etl -home RÉPERTOIRE init` sur chaque nœud, puis notez les adresses (`etl -home RÉPERTOIRE address`).
3. Écrivez les adresses des fondateurs (4 au moins), une par ligne, dans un fichier, puis :

   ```sh
   etl genesis -network test -date 2026-10-20 -founders fondateurs.txt \
     -ipc-value 103070 -ipc-period 2026-Q3 -ipc-sha256 <64 caractères hexadécimaux> \
     -out genesis.json
   ```

4. Copiez ce `genesis.json` dans le répertoire de chaque nœud et lancez `etl -home RÉPERTOIRE run`.
