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
| Référence IPC | INSEE — Série 001763852 (IPC France entière) |
| Fréquence IPC | Trimestrielle (gouvernable par vote) |

---

## Step 1 — Découverte

- Site public : **etalon-etl.fr**
- Code source : **GitHub public** (comme Satoshi)
- Manifeste fondateur disponible sur le site et GitHub
- Deux versions : complète (MANIFESTE_FONDATEUR.md) et courte (MANIFESTE_COURT.md)

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
    Amount    float64   // montant en ETL
    Fee       float64   // 0.1% du montant
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
- Toutes les transactions sont chiffrées et signées (règle immuable n°8)

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
- **INSEE — Série 001763852**
- IPC France entière (panier complet : alimentation, logement, transports/carburants, santé...)
- URL : `https://www.insee.fr/fr/statistiques/serie/001763852`
- Gravée dans le code — immuable

### Mécanisme
- Chaque nœud lit l'INSEE **indépendamment**
- Chaque nœud soumet sa valeur lue au réseau
- **2/3 supermajorité** requise pour valider la nouvelle valeur
- Mise à jour automatique à 23:00 UTC le jour de vérification

```
Nœud A → soumet IPC = 120.4
Nœud B → soumet IPC = 120.4
Nœud C → soumet IPC = 120.4
             ↓
2/3 accord → IPC = 120.4 gravé dans le bloc
```

### Fréquence
- **Trimestrielle** par défaut
- Gouvernable par vote des nœuds

### Si l'INSEE ne publie pas
- La valeur précédente est maintenue (règle immuable n°11)

### Page de vote IPC (dashboard + site web)

```
Section "IPC en cours"
├── Valeur actuelle : 120.4 (validée le 01/10/2026)
├── Nouvelle proposition : 121.1 (source INSEE, publiée le 15/10/2026)
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
