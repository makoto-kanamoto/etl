# ÉTALON — ETL
## Architecture technique — Version 1.0

---

## Vue d'ensemble

| Paramètre | Valeur |
|---|---|
| Langage nœud | Go |
| Algorithme PoW | Blake3 (~2 secondes CPU standard) |
| Modèle de compte | Account (pas UTXO) |
| Cryptographie | ECDSA secp256k1 |
| Fréquence des blocs | 1 bloc/jour à 23:00 UTC |
| Frais de transaction | 0.1% distribués aux nœuds validateurs |
| Récompense par bloc | 10 ETL (défaut gouvernable) |
| Halving | Tous les 2 ans (défaut gouvernable) |
| Consensus | Supermajorité 2/3 des nœuds |
| Format de config | .toml |
| Pruning | Activé dès le départ (pas en V2) |
| Dashboard local | localhost:3001 |
| Site public | etalon-etl.fr |
| Explorateur | explorer.etalon-etl.fr |
| Référence IPC | Configurable — défaut : INSEE Série 011814630 (IPC base 2025, France entière), via l'API SDMX |
| Fréquence IPC | Trimestrielle (gouvernable par vote) |
| Protocole | Forkable par toute communauté nationale |

---

## ETL comme protocole universel

ETL est conçu pour être **forké par n'importe quelle communauté nationale**.
Chaque fork a son propre genesis, sa propre communauté, son propre ancrage IPC.

```
ETL-FR  →  IPC France (INSEE)                         ← implémentation de référence
ETL-DE  →  IPC Allemagne (Destatis)                   ← fork communauté allemande
ETL-JP  →  IPC Japon (Statistics Bureau of Japan)     ← fork communauté japonaise
ETL-CN  →  IPC Chine (NBS)                            ← fork communauté chinoise
```

**Principe :** l'oracle IPC n'est pas codé en dur. Il est configurable dans default.toml.
Un fork change uniquement `ipc_url` et `ipc_country`. Tout le reste du protocole est identique.

Comme Linux — une base commune, des adaptations locales.

---

## Step 1 — Découverte

- Site public : **etalon-etl.fr**
- Code source : **GitHub public** (comme Satoshi)
- Manifeste disponible en FR / EN / JA / ZH (docs/fr/ docs/en/ docs/ja/ docs/zh/)
- Deux versions : complète et courte

---

## Step 2 — Création du wallet

- Génération des clés **dans le navigateur** (JavaScript)
- **Subresource Integrity (SRI) obligatoire** sur tous les scripts JS web
- Algorithme : ECDSA secp256k1 (même que Bitcoin)
- Format d'adresse : inspiré Bitcoin (ETL1...)
- La clé privée **ne quitte jamais le client** — jamais envoyée au serveur
- Stockage clé privée opérateur nœud : `~/.etl/wallet.key` (chiffré)

```
Exemple d'adresse : ETL1xK9f3mP2qR8vL7nT4uW6sA5dF1gH3jK
```

---

## Step 3 — Lancement du nœud

### Logiciel
- Langage : **Go**
- Tests unitaires : obligatoires

### Configuration (.toml)

```toml
[node]
wallet  = "ETL1xK9f3..."
port    = 3000
datadir = "~/.etl/chain"
pruning = true

[network]
bootstrap = [
  "etl1.etalon-etl.fr:3000",
  "51.178.xx.xx:3000",
]
```

### Bootstrap réseau
- **IP + DNS** (les deux) pour les nœuds de bootstrap
- Premier nœud bootstrap : OVH (fondateur)
- Les nouveaux nœuds se connectent aux bootstraps au démarrage

### Règle 1 membre = 1 nœud
- Confiance sociale de la communauté
- Coût PoW dissuasif pour nœuds multiples
- Période d'observation 7 jours avant admission complète
- Vote de la communauté en cas de suspicion

### Admission des nouveaux nœuds
- Automatique après 7 jours d'observation sans incident

### Dashboard
- **localhost:3001** pour l'opérateur
- **etalon-etl.fr** pour les utilisateurs web
- Un seul codebase — deux contextes de déploiement
- UX moderne, interactive

---

## Step 4 — Première transaction

### Structure Go

```go
type Transaction struct {
    ID        string    // hash unique de la transaction
    From      string    // adresse ETL de l'expéditeur
    To        string    // adresse ETL du destinataire
    Amount    uint64    // montant en unités ETL (1 ETL = 10^8 unités) — jamais float64
    Fee       uint64    // Amount * fee_bps / 10000 (fee_bps = 10 → 0.1%)
    Nonce     uint64    // compteur par compte — protection anti-rejeu
    Timestamp int64     // heure Unix de création
    Signature string    // signature ECDSA de l'expéditeur
}
```

### Flux

```
1. Marie crée la transaction dans le navigateur
2. Elle signe avec sa clé privée (côté client, SRI obligatoire)
3. La transaction signée est envoyée au réseau
4. Elle entre dans le mempool des nœuds
5. À 23:00 UTC : le nœud validateur l'intègre dans le bloc
6. Les frais (0.1%) sont distribués au nœud validateur
```

### Sécurité
- Signature ECDSA côté client
- La clé privée ne quitte jamais le navigateur
- Toutes les transactions sont signées (règle immuable n°8) — pas chiffrées : la chaîne est publique et chaque nœud doit pouvoir vérifier les soldes

### Montants
- Tous les montants sont des **uint64** en unités : 1 ETL = 10^8 unités
- La config (`default.toml`) garde des décimaux lisibles (`reward = 10.0`) ; le nœud Go les convertit en uint64 au chargement
- Les frais sont exprimés en **points de base** (`fee_bps = 10` → 0.1%) pour un calcul 100% entier

---

## Step 5 — Confirmation de transaction

### Finalité
- Petits montants (< 10 ETL) : **1 confirmation** = final
- Grands montants (> 10 ETL) : **2 confirmations** = final
- Seuils gouvernables par vote

### Notifications
- **WebSocket** — notification temps réel dans le dashboard
- À 23:00 UTC : l'expéditeur et le destinataire reçoivent la confirmation

```
✅ Transaction confirmée — Bloc #142
   5 ETL envoyés à ETL1pAuL...
   Frais : 0.005 ETL
```

### Explorateur public

```
explorer.etalon-etl.fr

Bloc #142 — 17 octobre 2026 à 23:00 UTC
├── Transaction tx_3f7a... : ETL1xK9... → ETL1pAuL... : 5 ETL
├── Transaction tx_8b2c... : ETL1aB3... → ETL1cD4... : 12.5 ETL
└── Reward : ETL1xK9... (nœud validateur) : 10 ETL
```

- Accessible à tous — membres ou non
- UX moderne et interactive
- Principe manifeste : *"Ne faites pas confiance. Vérifiez."*

---

## Step 6 — Oracle IPC (mise à jour trimestrielle)

### Source officielle
- **INSEE — Série 011814630** (défaut ETL-FR)
- IPC base 2025 — Ensemble des ménages — France — Coicop 00 « Ensemble » (panier complet, tabac inclus)
- Lecture : **API SDMX de l'INSEE** — `https://api.insee.fr/series/BDM/V1/data/SERIES_BDM/011814630`
- Page lisible par un humain : `https://www.insee.fr/fr/statistiques/serie/011814630`
- **Configurable** dans `default.toml` (`ipc_url`, `ipc_country`) — jamais codée en dur
- Pour un réseau donné, la source est fixée au genesis

> L'ancienne série 001763852 (base 2015, hors tabac) est **arrêtée** depuis janvier 2026 (dernière valeur : 2025-12). Elle ne doit pas être utilisée.

### Série mensuelle, vote trimestriel
- La série INSEE est **mensuelle** (`TIME_PERIOD="2026-09"`)
- Un trimestre est représenté par **l'observation de son dernier mois** : `2026-Q1` → `2026-03`, `Q2` → `06`, `Q3` → `09`, `Q4` → `12`
- La valeur est stockée en **entier × 1000** (`103.07` → `103070`), comme les montants : aucun float dans le consensus

### Soumission d'un vote IPC — format obligatoire

Chaque vote contient **trois éléments**, tous obligatoires :

| Champ | Exemple | Rôle |
|---|---|---|
| `value` | `103.07` | Valeur de l'IPC lue (`OBS_VALUE`) |
| `period` | `"2026-Q3"` | Période de référence (format `AAAA-Qn`) |
| `source_sha256` | `"9f86d081..."` | Empreinte SHA256 de l'observation dans la réponse de l'API SDMX (voir ci-dessous) |

### Ce que couvre `source_sha256`

Le hash porte sur **l'élément `<Obs>` de la période votée**, octets bruts tels que renvoyés par l'API :

```xml
<Obs TIME_PERIOD="2026-09" OBS_VALUE="103.07" OBS_STATUS="A" OBS_QUAL="DEF" OBS_TYPE="A" DATE_JO="2026-10-16"/>
```

Pourquoi pas la réponse complète :
- L'en-tête SDMX (`<message:Header>`) contient un `ID` et un horodatage `Prepared` **différents à chaque appel** — deux nœuds honnêtes obtiendraient deux hash différents
- Le reste de la réponse change dès que l'INSEE publie un nouveau mois ou révise un mois ancien (`OBS_REV`) — deux nœuds lisant à quelques heures d'écart divergeraient

L'élément `<Obs>` seul est stable et reste vérifiable par n'importe qui : il suffit d'interroger l'API et de hasher la même ligne.

```go
type IPCVote struct {
    Value        string // OBS_VALUE lu, ex. "103.07" (converti en entier × 1000 : 103070)
    Period       string // "2026-Q3"
    SourceSHA256 string // hex, 64 caractères
    Node         string // adresse ETL du nœud votant
    Signature    string // signature ECDSA du nœud
}
```

**Règles de validation :**
- Toute soumission à laquelle il manque `value`, `period` ou `source_sha256` est **rejetée** par les nœuds
- `period` doit respecter le format `AAAA-Qn` et correspondre au trimestre attendu
- `source_sha256` doit faire 64 caractères hexadécimaux
- Le vote est signé par le nœud (ECDSA)

### Mécanisme
- Chaque nœud lit la source **indépendamment**
- Chaque nœud soumet le triplet `{ value, period, source_sha256 }` signé
- **2/3 supermajorité** requise sur un **triplet identique** pour valider la nouvelle valeur
- Le triplet validé est gravé dans le bloc — n'importe qui peut re-télécharger la source et vérifier le hash
- Mise à jour automatique à 23:00 UTC le jour de vérification

```
Nœud A → { 103.07, "2026-Q3", 9f86d0... }
Nœud B → { 103.07, "2026-Q3", 9f86d0... }
Nœud C → { 103.07, "2026-Q3", 9f86d0... }
Nœud D → { 103.07 }                          ← rejeté (incomplet)
             ↓
2/3 accord sur le triplet → IPC 2026-Q3 = 103070 (× 1000) gravé dans le bloc
```

### Fréquence
- **Trimestrielle** par défaut
- Gouvernable par vote des nœuds

### Si l'INSEE ne publie pas
- La valeur précédente est maintenue (règle immuable n°11)

### Page de vote IPC (dashboard + site web)

```
Section "IPC en cours"
├── Valeur actuelle : 102.85 — 2026-Q2 (validée le 01/07/2026)
├── Nouvelle proposition : 103.07 — 2026-Q3 (source INSEE, publiée le 16/10/2026)
├── Votes : 4/7 nœuds ont voté ✅ (2/3 requis = 5/7)
└── [Voter] ← bouton pour l'opérateur de nœud
```

---

## Step 7 — Échange P2P euros/ETL

### Principe
ETL ne touche pas au système bancaire. ETL sécurise uniquement **la partie ETL** de l'échange. La partie euros est hors-chaîne, libre choix des membres.

### Tableau d'annonces P2P
- Disponible sur **etalon-etl.fr/echange** et **GitHub**
- Les membres publient leurs offres d'achat/vente
- Contact direct entre membres — pas d'intermédiaire

### Escrow automatique

```
1. Marie publie : "Vends 50 ETL — 1 ETL = 1 €"
2. Paul accepte l'offre
3. Les 50 ETL de Marie sont verrouillés on-chain (escrow)
4. Paul envoie 50 € à Marie (virement, cash, Lydia... son choix)
5. Marie confirme la réception des euros
6. Les 50 ETL sont libérés vers le wallet de Paul
7. En cas de litige → vote des nœuds
```

### Moyens de paiement euros
- Virement bancaire, cash, PayPal, Lydia, Revolut...
- ETL ne dicte rien — liberté totale

### Système de réputation

```
Wallet ETL1xK9... — Réputation
├── 12 échanges réussis ✅
├── Note communauté : 4.9/5
└── ⚠️ 1 litige signalé (résolu contre lui)
```

- Lié au wallet pseudonyme (pas à l'identité réelle)
- Gravé on-chain — personne ne peut l'effacer
- **Alerte automatique** à la communauté dès qu'un litige est ouvert
- Visible sur le tableau d'annonces

---

## Récapitulatif des pages / interfaces

| Interface | URL | Audience |
|---|---|---|
| Site principal | etalon-etl.fr | Tous |
| Explorateur blocs | explorer.etalon-etl.fr | Tous |
| Échange P2P | etalon-etl.fr/echange | Membres |
| Vote IPC | dashboard (section IPC) | Opérateurs nœuds |
| Dashboard local | localhost:3001 | Opérateurs nœuds |
| Code source | GitHub public | Tous |

---

## Prochaine étape

Structure du dépôt GitHub + début du code Go.

---

**ETL — Étalon**
*Architecture v1.0 — octobre 2026*
