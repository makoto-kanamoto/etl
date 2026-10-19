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
| Frais de transaction | 0.1% au proposant du bloc |
| Récompense | 10 ETL/jour, partagée entre tous les participants (défaut gouvernable) |
| Halving | Aucun — émission constante |
| Admission | Parrainage (1/30 j, 6 mois d'ancienneté) + observation 5/7 jours |
| Genesis | ≥ 10 fondateurs indépendants ; aucune récompense si < 10 nœuds actifs |
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
- Format d'adresse : **bech32m** (BIP-350, comme les adresses Bitcoin récentes), préfixe réseau `etl`
  - Corps : version (0) + 20 premiers octets de Blake3(clé publique compressée)
  - Détecte les fautes de frappe, pas de casse mixte, lisible à voix haute, compact en QR code
  - Chaque fork a son préfixe (`etl` pour ETL-FR, `etlde` pour ETL-DE…) : une adresse d'un fork est refusée sur un autre
- La clé privée **ne quitte jamais le client** — jamais envoyée au serveur
- Stockage clé privée opérateur nœud : `~/.etl/wallet.key` (chiffré)

```
Exemple d'adresse : etl1qnwxsgqx9c4phyx8vjt6650fl5zgxzcaav8qcw0
                    (clé privée = 1 — vecteur de référence, ne jamais utiliser)
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

### Admission des nœuds — registre on-chain

La règle « 1 membre = 1 nœud » repose sur ce registre : **c'est la sécurité économique principale**,
puisque chaque nœud admis reçoit une part de la récompense et une voix dans les votes 2/3.
Une PoW ne coûte que ~2 s : sans registre, une machine de 12 cœurs ferait ~180 « nœuds ».

Valeurs par défaut — gouvernables par vote 2/3 (critères d'admission).

| Étape | Règle |
|---|---|
| **Candidature** | Enregistrement on-chain signé par le candidat **et** contresigné par un parrain admis. Valable 7 jours après signature (anti-rejeu) |
| **Parrain** | Nœud admis et actif, **≥ 6 mois d'ancienneté** (fondateurs dispensés), **1 parrainage par 30 jours** maximum (fondateurs compris) |
| **Observation** | Les 7 jours suivant la candidature : le candidat doit participer (PoW valide) **au moins 5 jours**. Pas de récompense, pas de vote, pas de ticket |
| **Admission** | Automatique à la fin de l'observation si 5/7 atteint ; sinon le candidat est retiré (il peut recandidater avec un autre parrain ; le quota du premier parrain reste consommé) |
| **Inactivité** | **30 jours** sans participation → inactif : plus de part, ne compte plus dans le dénominateur des 2/3. Retour : nouvelle observation (sans parrain) ; l'ancienneté repart de zéro (un fondateur réactivé perd sa dispense) ; en cas d'échec, reste inactif |
| **Exclusion automatique** | Double participation prouvée (deux participations signées du même jour, incluses dans un bloc) → exclu définitivement |
| **Exclusion par vote 2/3** | Faux nœuds avérés → exclu ; **le parrain perd son droit de parrainage pendant 1 an** (il reste admis) |

Les jours sont des jours UTC (horodatage du bloc ÷ 86 400) : un jour sans bloc compte quand même.

### Genesis
- Le genesis liste **au moins 10 membres fondateurs indépendants**, annoncés publiquement avant le lancement
- Les fondateurs sont admis dès le genesis et **peuvent parrainer immédiatement** (dispense d'ancienneté) ; le quota de 1 parrainage par 30 jours s'applique
- Le genesis s'engage sur le `chain_id`, le message **et la liste des fondateurs** : les changer change toute la chaîne
- **Aucune récompense tant que moins de 10 nœuds sont actifs** — personne ne peut accumuler d'ETL seul au démarrage

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
    ID        crypto.Hash      // Blake3 de l'encodage binaire canonique
    From      crypto.Address   // adresse ETL de l'expéditeur
    To        crypto.Address   // adresse ETL du destinataire
    Amount    uint64           // montant en unités ETL (1 ETL = 10^8 unités) — jamais float64
    Fee       uint64           // Amount * fee_bps / 10000 (fee_bps = 10 → 0.1%)
    Nonce     uint64           // compteur par compte — protection anti-rejeu
    Timestamp int64            // heure Unix de création
    Signature crypto.Signature // ECDSA récupérable, 65 octets
}
```

- **Octets en interne**, texte (hex, bech32m) uniquement à l'affichage et dans l'API
- Ce qui est signé et hashé est un **encodage binaire fixe**, jamais du JSON
- **Signature récupérable** : la clé publique se déduit de la signature ; le nœud vérifie qu'elle correspond à `From`
- Nonce déterministe (RFC 6979), signatures **low-S** uniquement : une transaction n'a qu'une seule signature valide

### Séparation des signatures
Toute signature porte sur `Blake3(chain_id | usage | message)` :
- `chain_id` (`"ETL-FR"`, fixé au genesis) — une transaction ETL-FR ne peut pas être rejouée sur ETL-DE
- `usage` (`tx`, `participation`, `block-proposal`, `block-vote`, `ipc-vote`) — une transaction ne peut pas passer pour un vote, et inversement

### Flux

```
1. Marie crée la transaction dans le navigateur
2. Elle signe avec sa clé privée (côté client, SRI obligatoire)
3. La transaction signée est envoyée au réseau
4. Elle entre dans le mempool des nœuds
5. Après 23:00:30 UTC : le proposant du jour l'intègre dans le bloc
6. Les frais (0.1%) vont au proposant
```

### Production du bloc quotidien

**Phase 1 — Participation (23:00:00 → 23:00:30 UTC)**
- À 23:00 UTC, **tous les nœuds minent** leur PoW du jour (Blake3)
- La PoW porte sur `prev_hash | heure du bloc | adresse du nœud | nonce` : elle ne vaut que pour ce jour et ce nœud
- **La PoW est un ticket d'entrée** : valide ou non. Un hash plus bas ne donne aucun avantage
- **1 seule participation par nœud**, soumise immédiatement dès qu'une PoW valide est trouvée.
  Un nœud qui soumet deux participations différentes est exclu du jour (un doublon réseau identique ne compte pas)
- **Chaque participation est signée par le nœud** (usage `participation`) : personne ne peut participer au nom d'un autre
- **Seuls les nœuds admis participent.** Sans cette règle, n'importe qui pourrait générer
  des milliers d'adresses hors ligne et choisir celle qui a le ticket le plus bas
- Toute participation reçue hors fenêtre est ignorée

**Phase 2 — Proposition (après 23:00:30 UTC)**
- **Proposant** = parmi les participants, celui dont `Blake3(hash du bloc précédent | adresse du nœud)` est **le plus bas**
  - Ce tirage ne dépend que de l'identité du nœud et du bloc précédent : **miner plus longtemps ne sert à rien**
  - Imprévisible avant la publication du bloc précédent, vérifiable par tous après
- Le proposant construit le bloc : transactions du mempool + **liste de toutes les participations reçues**,
  triées par ticket croissant (la sienne en premier), et le signe (usage `block-proposal`)
- Si son bloc n'est pas accepté, le suivant dans l'ordre des tickets prend le relais

**Phase 3 — Vote**
- Les nœuds **votent** sur ce bloc — **supermajorité 2/3** requise
- Un nœud vote contre un bloc qui **omet une participation valide** qu'il a reçue dans la fenêtre
  (le proposant aurait intérêt à écarter des participants pour grossir sa part)
  - **Tolérance réseau** : seules les participations reçues **avant 23:00:25 UTC** sont contrôlées.
    Une participation arrivée entre 23:00:25 et 23:00:30 a pu ne pas atteindre le proposant à temps :
    son absence ne justifie pas un vote contre
- Simple, déterministe, pas d'élection

### Récompense
- **Émission constante : 10 ETL par jour**, sans halving. La stabilité vient de l'IPC, pas de la rareté
- **Partagée à parts égales** entre tous les nœuds ayant soumis une participation valide : `10 ETL ÷ N`
  - Chaque part est arrondie à l'unité inférieure ; le reste (moins de N unités, soit < N × 0,00000001 ETL) n'est pas émis
- **Les frais de transaction vont au proposant** du bloc
- **Aucune récompense tant que moins de 10 nœuds sont admis et actifs** (compté au début du bloc)
- Genesis : aucune récompense (zéro pré-mine)

### Difficulté
- **Cible fixe**, calibrée pour ~2 secondes sur un CPU standard : **21 bits de tête à zéro**
  (mesuré : ~1,2 M hash/s sur un cœur d'Apple M3 Pro → ~1,7 s en théorie, 2,4 s mesurées sur 20 essais)
- Modifiable uniquement par **vote 2/3** (règle gouvernable)
- Pas d'ajustement automatique : avec 1 bloc/jour à heure fixe, aucune mesure fiable du temps de minage n'existe

### Sécurité
- Signature ECDSA côté client
- La clé privée ne quitte jamais le navigateur
- Toutes les transactions sont signées (règle immuable n°8) — pas chiffrées : la chaîne est publique et chaque nœud doit pouvoir vérifier les soldes

### Montants
- Tous les montants sont des **uint64** en unités : 1 ETL = 10^8 unités
- La config (`default.toml`) garde des décimaux lisibles (`daily_reward = 10.0`) ; le nœud Go les convertit en uint64 au chargement
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
├── Proposant : etl1q... (frais du bloc)
└── Récompense : 10 ETL partagés entre 42 participants (0.23809523 ETL chacun)
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
