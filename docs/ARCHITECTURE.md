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
| Frais de transaction | max(0.1%, 0,0001 ETL) au proposant du bloc |
| Taille de bloc | 100 000 transactions maximum (≈ 14 Mo) — débit quotidien, gouvernable |
| Récompense | 10 ETL/jour, partagée entre tous les participants (défaut gouvernable) |
| Halving | Aucun — émission constante |
| Admission | Parrainage (1/30 j, 6 mois d'ancienneté) + observation 5/7 jours |
| Genesis | ≥ 10 fondateurs indépendants ; aucune récompense si < 10 nœuds actifs |
| Consensus | Supermajorité 2/3 des nœuds |
| Format de config | .toml |
| Pruning | Activé dès le départ : 90 derniers blocs gardés, en-têtes toujours (mode archive possible) |
| Stockage | bbolt (pur Go, transactions ACID) |
| Racine d'état | `StateRoot` dans chaque en-tête, validé par vote 2/3 |
| Dashboard local | localhost:3001 |
| Site public | etalon-etl.com |
| Explorateur | etalon-etl.com (onglet Explorateur du site public) |
| Référence IPC | Configurable — défaut : INSEE Série 011814630 (IPC base 2025, France entière), via l'API SDMX |
| Fréquence IPC | Trimestrielle (passage au mensuel : objectif futur, par vote) |
| Protocole | Forkable par toute communauté nationale |

---

## ETL comme protocole universel

ETL est conçu pour être **repris par n'importe quelle communauté nationale**.
Chaque fork est un réseau à part : son propre genesis, sa propre communauté, son propre indice des prix.

```
ETL-FR  →  IPC France (INSEE)                         ← implémentation de référence
ETL-DE  →  IPC Allemagne (Destatis)                   ← fork communauté allemande
ETL-JP  →  IPC Japon (Statistics Bureau of Japan)     ← fork communauté japonaise
ETL-CN  →  IPC Chine (NBS)                            ← fork communauté chinoise
```

**Ce qu'un fork change** — tout le reste du protocole est identique :

| Élément | Où |
|---|---|
| Identifiant du réseau (`ETL-DE`) et préfixe des adresses (`etlde`) | Définition du réseau **dans le logiciel** (`internal/node/networks.go`) : c'est une modification du code, pas de la configuration. Une configuration locale ne peut pas créer un autre réseau (règles de consensus hors de `config.toml`) |
| Genesis : fondateurs, IPC de référence, jour de lancement | `genesis.json` produit par `etl genesis` ; son empreinte est gravée dans le logiciel du fork |
| Source de l'indice des prix | `ipc_url` et `ipc_country` dans `config.toml` |

**Limite actuelle de l'oracle** : il lit les séries publiées au format SDMX-ML 2.1 (« structure-specific »),
celui de l'INSEE. Une série mensuelle publiée dans ce format se lit en changeant `ipc_url` ; tout autre
format demande d'écrire un lecteur adapté dans `internal/oracle`.

Les adresses et les signatures d'un réseau sont refusées sur un autre : l'identifiant du réseau entre
dans chaque signature, et le préfixe dans chaque adresse.

Comme Linux — une base commune, des adaptations locales.

---

## Step 1 — Découverte

- Site public : **etalon-etl.com**
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

### Portefeuille du navigateur — parcours et protections

- **Création en 3 étapes** : phrase de 24 mots, vérification de 3 mots tirés au hasard, phrase de
  passe (12 caractères au moins). Le coffre est chiffré dans le navigateur ; sauvegarde = les 24 mots
- **Phrase de passe oubliée** : elle ne se récupère pas. L'écran verrouillé propose **« Utiliser mes
  24 mots »**, qui remplace le coffre de ce navigateur par un nouveau. Si les mots donnent une **autre
  adresse** que le portefeuille présent, les deux adresses sont affichées côte à côte et il faut taper
  REMPLACER : l'ancien portefeuille ne se retrouvera qu'avec ses propres 24 mots
- **Envoi en 3 étapes** : saisir (frais et solde après l'envoi calculés pendant la saisie), vérifier
  (adresse complète par groupes de 4 caractères, avertissement au premier envoi vers une adresse),
  envoyé (inscription au bloc de 23:00 UTC). Le solde affiché après l'envoi n'est jamais négatif
- **Recevoir** : QR code de l'adresse, en **majuscules** (mode alphanumérique du QR : code plus petit,
  plus facile à lire ; les adresses tout en majuscules sont acceptées, jamais la casse mixte). Il ne
  contient que l'adresse ; un montant intégré au QR serait une décision de format, pas encore prise.
  Bibliothèque : uqr 0.1.3 (MIT, dérivée du générateur de Project Nayuki), vendorisée avec SRI
- **Contacts** : un nom choisi pour une adresse, **dans ce navigateur seulement**, chiffré en
  AES-256-GCM avec une clé dérivée de la clé privée du portefeuille (HKDF-SHA256) et lié à son adresse.
  Lisibles seulement portefeuille déverrouillé ; retirés avec le portefeuille. Ils ne font pas partie
  des 24 mots : effacer les données du navigateur les perd
- **Motif visuel** : chaque adresse a un petit dessin (grille 5 × 5 symétrique, 12 teintes) tiré de son
  empreinte BLAKE3. Il aide à reconnaître un correspondant ; il **ne prouve rien** (une adresse au motif
  voisin se fabrique sans grand effort) : l'adresse complète reste affichée avant toute signature
- **Pseudonymes publics inscrits dans la chaîne** (« @nom » visible de tous) : non retenus à ce stade.
  Ce serait un changement de consensus (attribution, accaparement des noms, coût, anonymat), à décider
  séparément

---

## Step 3 — Lancement du nœud

### Logiciel
- Langage : **Go**
- Tests unitaires : obligatoires
- **Un seul binaire, `etl`** : nœud, réseau, consensus, oracle IPC et dashboard (interface embarquée)
- Mise en route pas à pas : [guide de l'opérateur](fr/GUIDE_OPERATEUR.md)

| Commande | Rôle |
|---|---|
| `etl init` | crée `~/.etl/config.toml` et la clé du nœud ; affiche la phrase de récupération de 24 mots |
| `etl restore` | recrée la clé du nœud à partir de sa phrase de récupération |
| `etl run` | démarre le nœud (commande par défaut) |
| `etl address` | affiche l'adresse du nœud |
| `etl passphrase` | change la phrase de passe qui chiffre la clé du nœud |
| `etl genesis` | prépare le `genesis.json` d'un réseau (cérémonie de lancement) |
| `etl version` | affiche la version |

**Phrase de passe de la clé du nœud** : saisie au clavier sans affichage, ou lue dans un fichier
(`-passphrase-file`, permissions 0600 exigées) pour un serveur sans surveillance. **Jamais** en variable
d'environnement ni en argument de commande (visibles par les autres processus et dans l'historique).

### Configuration (.toml) — réglages locaux seulement

Le fichier `~/.etl/config.toml` ne contient que ce qui ne regarde que l'opérateur : réseau rejoint,
répertoires, ports, pruning, pairs d'amorçage, limites du mempool, source de l'IPC, dashboard.

```toml
[node]
network = "ETL-FR"
datadir = "~/.etl/chain"
wallet  = "~/.etl/wallet.key"     # "" : nœud passif (ni vote, ni récompense)
genesis = "~/.etl/genesis.json"
port    = 3000
pruning = true

[network]
bootstrap = [
  "etl1.etalon-etl.com:3000",
]
```

**Les règles de consensus n'y figurent pas** — identifiant du réseau, préfixe des adresses, récompense,
difficulté, frais, taille et heure du bloc. Elles viennent de la définition du réseau gravée dans le
logiciel et du genesis, puis n'évoluent que par vote on-chain (plus de 2/3, application 30 jours après).
Un fichier qui tente de les régler est **refusé au démarrage**, avec une explication : un opérateur ne
peut pas se séparer de la chaîne commune par une simple modification de configuration.

Toute clé inconnue est aussi refusée, avec son numéro de ligne : une configuration ambiguë n'est jamais
lue « à peu près ».

### Réseau — transport et diffusion
- **Bibliothèque standard Go uniquement** (TCP + TLS 1.3) : aucune dépendance réseau tierce à auditer.
  Un nœud derrière une box sans redirection de port ne fait que des connexions sortantes — suffisant pour participer
- **Identité de transport jetable**, sans lien avec l'adresse ETL : les pairs voient une IP, jamais l'adresse ETL
  qui s'y cache (pseudonymité, règle n°5). TLS chiffre ; l'authenticité vient des signatures ETL de chaque message
- Poignée de main : même version de protocole, même `chain_id`, **même genesis** — sinon refus ; connexion à
  soi-même détectée
- Pairs : bootstrap (IP + DNS), puis échange d'adresses ; 8 connexions sortantes, 64 entrantes au plus
- **Diffusion par inondation** : chaque message (transaction, participation, vote, vote IPC, vote d'exclusion,
  bloc proposé, bloc finalisé + certificat) est **vérifié avant d'être relayé**, puis relayé **immédiatement**
  à tous les pairs ; dédoublonnage (chaque nœud traite chaque message une fois). Mesuré : moins d'une seconde
  sur 5 sauts — la tolérance de 5 s (23:00:25) couvre la propagation
- Deux niveaux de vérification : sans contexte par le réseau (décodage canonique, signatures), puis selon
  l'état par le moteur de consensus (jour, registre, solde…) — qui décide : relayer, ignorer ou rejeter
- **Défense** : données invalides → déconnexion et bannissement 24 h ; taille de message 32 Mo max (refus
  avant allocation) ; débit limité par pair (100 msg/s, rafale 500) ; pair trop lent déconnecté ;
  aucun relais sans validation (le réseau ne sert pas d'amplificateur)
- À venir : Tor (SOCKS5) en option pour les connexions sortantes

### Réseau — synchronisation
- Un nœud qui démarre ou revient **rejoue la chaîne depuis le genesis** : blocs finalisés + certificats, chacun
  revérifié (bloc, certificat, `StateRoot`) — un pair ne peut pas inventer de blocs
- Requêtes point à point : `GetTip` → `Tip` (hauteur), `GetBlocks` → `Blocks` (lots de 100 blocs ou 16 Mo)
  ou `NotAvailable` (blocs élagués) ; délai de 30 s avant de passer à un autre pair ; 4 demandes servies à la fois
- Les nœuds **archive** l'annoncent dans la poignée de main et sont interrogés en priorité ;
  **le bootstrap OVH est un nœud archive**
- Un pair qui fournit un bloc invalide est banni ; la synchronisation continue avec un autre
- Un bloc finalisé diffusé avec plusieurs hauteurs d'avance déclenche la synchronisation ;
  le bloc suivant est appliqué directement

### Attaque « longue portée » — défenses
Au genesis, il n'y a que 10 fondateurs : si 7 de leurs clés fuitaient un jour, leurs détenteurs pourraient
fabriquer une histoire alternative complète depuis le genesis, avec des certificats valides.
- **Points de contrôle** : chaque version du logiciel embarque des couples (hauteur, hash) de blocs récents.
  Toute chaîne qui les contredit est refusée ; une chaîne locale qui les contredit arrête le nœud au démarrage.
  **La liste doit être mise à jour à chaque version**
- **Détection des conflits** : deux blocs finalisés à la même hauteur → les votants communs aux deux certificats
  ont voté deux fois. Le nœud **s'arrête et alerte son opérateur** plutôt que de choisir ; les doubles votes
  sont extraits comme preuves d'exclusion ; le bloc conflictuel n'est pas relayé

### Horloge
- **Exigence opérateur : horloge synchronisée par NTP.** La fenêtre de participation (30 s), la tolérance
  d'inclusion (23:00:25) et les tours de proposition (tolérance ±5 s) supposent des horloges à l'heure
- **Contrôle** : chaque nœud annonce son heure à la poignée de main. Le nœud calcule l'écart médian avec
  ses pairs ; au-delà de **2 s** (avec au moins 2 pairs), il avertit l'opérateur dans son journal et sur
  l'accueil du dashboard. Ce n'est qu'un avertissement : l'heure des pairs n'est jamais utilisée pour
  corriger la sienne (un groupe de pairs malveillants pourrait sinon la déplacer)

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
| **Parrain** | Nœud admis et actif, **≥ 6 mois d'ancienneté** (fondateurs dispensés), **1 parrainage par 30 jours** maximum (fondateurs compris). Le panneau opérateur affiche ces conditions et la date d'éligibilité, calculées par **la même fonction que le consensus** (`Node.CanSponsor`) |
| **Observation** | Les 7 jours suivant la candidature : le candidat doit participer (PoW valide) **au moins 5 jours**. Pas de récompense, pas de vote, pas de ticket |
| **Admission** | Automatique à la fin de l'observation si 5/7 atteint ; sinon le candidat est retiré (il peut recandidater avec un autre parrain ; le quota du premier parrain reste consommé) |
| **Inactivité** | **30 jours** sans participation → inactif : plus de part, ne compte plus dans le dénominateur des 2/3. Retour : nouvelle observation (sans parrain) ; l'ancienneté repart de zéro (un fondateur réactivé perd sa dispense) ; en cas d'échec, reste inactif |
| **Exclusion automatique** | Double participation prouvée (deux participations signées du même jour, incluses dans un bloc) → exclu définitivement |
| **Exclusion par vote 2/3** | Faux nœuds avérés → exclu ; **le parrain perd son droit de parrainage pendant 1 an** (il reste admis). Chaque nœud signe `{cible, jour}` (usage `exclusion-vote`), vote valable 30 jours ; plus de 2/3 des nœuds actifs → l'exclusion et ses votes sont inscrits dans un bloc |

Les jours sont des jours UTC (horodatage du bloc ÷ 86 400) : un jour sans bloc compte quand même.

### Genesis
- Le genesis liste **au moins 10 membres fondateurs indépendants**, annoncés publiquement avant le lancement
- Les fondateurs sont admis dès le genesis et **peuvent parrainer immédiatement** (dispense d'ancienneté) ; le quota de 1 parrainage par 30 jours s'applique
- Le genesis s'engage sur le `chain_id`, le message **et la liste des fondateurs** : les changer change toute la chaîne
- **Aucune récompense tant que moins de 10 nœuds sont actifs** — personne ne peut accumuler d'ETL seul au démarrage
- **Fichier `genesis.json`** publié avec chaque version : `chain_id`, message, horodatage (23:00 UTC du
  jour de lancement), IPC de référence (valeur, période, empreinte de la source) et adresses des
  fondateurs. Le nœud en reconstruit le bloc genesis et compare son empreinte à **celle gravée dans le
  logiciel** : un fichier différent, même d'un seul octet, est refusé
- **Avant le lancement**, aucune empreinte n'est gravée pour ETL-FR : le logiciel refuse de le rejoindre.
  La cérémonie (date et liste des fondateurs : à décider) produit le fichier avec `etl genesis`, puis
  l'empreinte est gravée dans la version publiée
- **Réseau de test** (`network = "test"`) : identifiant `ETL-TEST`, adresses `etlt1q…`, 4 fondateurs
  suffisent, preuve de travail allégée. Aucune adresse ni signature ne peut être confondue avec ETL-FR ;
  il accepte n'importe quel genesis et ne sert qu'aux essais

### Dashboard
- **localhost:3001** pour l'opérateur
- **etalon-etl.com** pour les visiteurs : même interface en mode « site public » (voir *Site public* ci-dessous)
- Un seul codebase — deux contextes de déploiement
- UX moderne, interactive
- Servi par le nœud lui-même : interface embarquée dans le binaire, API JSON locale
- La clé personnelle de l'utilisateur reste dans son navigateur (coffre chiffré) ; la clé du nœud ne quitte jamais le nœud
- Interface en **français, anglais, japonais et chinois (simplifié)**, langue choisie d'après le navigateur
  puis dans le menu ; dates et nombres aux conventions de chaque langue. Les traductions japonaise et
  chinoise n'ont pas encore été relues par des locuteurs natifs : les corrections sont bienvenues
- Polices embarquées pour l'alphabet latin seulement ; le japonais et le chinois utilisent les polices du
  système (attribut `lang` de la page : `ja`, `zh-Hans`)
- Phrase de récupération : listes de mots française et anglaise seulement ; en japonais et en chinois, les
  24 mots sont en anglais (une liste japonaise ou chinoise serait un nouveau format de phrase)

#### Identité visuelle et accueil

- **Logo « E gradué »** : un E dont les branches sont les traits d'une règle graduée, la lettre du nom et
  l'idée de mesure de référence en un seul signe. Icône d'onglet en SVG, icône 180 px en PNG (Safari)
- **Polices embarquées dans le binaire** : Gloock (titres et grands chiffres), Public Sans (texte),
  IBM Plex Mono (adresses), licence libre OFL, jeu latin (≈ 80 Ko). Aucun CDN : la politique de sécurité
  du contenu n'autorise que le nœud lui-même. Leurs empreintes sont listées dans
  `web/dashboard/fonts/integrity.json` et vérifiées par les tests
- **Accueil** : la valeur de 1 ETL en euros et la variation des prix depuis le lancement ; le prochain
  bloc sur une règle de 24 h (fenêtre de lecture de l'IPC, bloc de 23:00 UTC) ; l'état du nœud et du
  réseau
- **Graphique pédagogique** (100 € contre 100 ETL sur 10 ans) : c'est une **simulation**, pas une donnée
  historique. Il porte en gros « Simulation — inflation à 2 %/an » et « SIMULATION » en filigrane, pour
  qu'aucune capture ne puisse le faire passer pour un chiffre réel

**« Réseau en bonne santé »** : le nœud le calcule et le transmet au dashboard (`/api/status`, champ
`health`). Les quatre critères doivent être remplis ; chaque critère manquant s'affiche avec son
explication.

| Critère | Seuil | Si non rempli |
|---|---|---|
| Pairs connectés | au moins 2 | vérifier la connexion Internet et le port P2P |
| Horloge | à moins de 2 s de celle des pairs | synchroniser l'heure du système (NTP) |
| Nœud | pas arrêté par un conflit de finalité | intervention humaine (journal du nœud) |
| Dernier bloc | il y a moins de 25 heures | le nœud n'est peut-être plus synchronisé |

**Panneau opérateur** (session à jeton) : quatre onglets — paramètres, parrainage, arbitrages,
exclusion. Chaque paramètre gouvernable se lit sur une règle graduée : bornes immuables, plage permise
pour un vote, valeur actuelle, changement en attente et proposition, choisie au curseur sans pouvoir
sortir de la plage permise ; les votes en cours s'affichent avec le seuil des 2/3. Les paramètres sont
regroupés par thème (économie, blocs, admission des nœuds, parrainage).

**Mise en page vérifiée** avant chaque modification de l'interface : `web/tools/layoutcheck.mjs` ouvre
chaque page dans Chrome à six largeurs (1440 à 360 px), en thème clair et sombre, et signale tout
débordement, texte coupé, barre de défilement interne ou erreur de la console, ainsi qu'un texte de
graphique trop petit à l'écran (moins de 9,5 px) ou superposé à un autre. Le texte des graphiques
s'ajuste à leur largeur réelle pour rester à 11 px, du téléphone à l'écran large. Le contrôle suit le mode
annoncé par le nœud : sur le site public, il vérifie aussi l'explorateur avec un vrai bloc, une adresse,
une transaction et un échange pris dans la chaîne, et les recherches sans résultat ; avant le lancement,
l'accueil et « Rejoindre ». Le serveur de développement (non livré) sert le site public derrière un proxy
HTTPS local qui imite celui du serveur (`etl-dev -public`, `-prelaunch`).

**Page IPC** : IPC en vigueur, au lancement et valeur de 1 ETL ; courbe de l'IPC adopté trimestre par
trimestre ; vote en cours face au seuil des 2/3 (« il manque n voix ») ; lecture de ce nœud. Le lien
« Vérifier sur insee.fr » est déduit de la source configurée (`ipc_url`) et n'apparaît que pour une série
INSEE.

#### Sécurité de l'API locale

Le navigateur de l'opérateur visite aussi d'autres sites : n'importe quelle page peut tenter d'envoyer des requêtes à `localhost:3001`. L'API s'en protège ainsi :

| Menace | Parade |
|---|---|
| Accès depuis le réseau | écoute sur `127.0.0.1` uniquement |
| Rebond DNS (un domaine hostile qui pointe vers 127.0.0.1) | en-tête `Host` limité à `localhost:3001` et `127.0.0.1:3001` |
| Requête forgée depuis un autre site (CSRF) | `Origin` vérifié et corps JSON obligatoire pour toute modification ; aucun en-tête CORS |
| Action de l'opérateur déclenchée à son insu | jeton aléatoire régénéré à chaque démarrage, dans `~/.etl/dashboard.token` (lisible par l'opérateur seul), exigé pour voter (paramètres, exclusions) ou relire la source IPC |
| Script injecté ou ressource externe | politique de sécurité du contenu stricte : scripts, styles et connexions vers le nœud seulement ; aucune intégration dans une autre page |

- Les transactions et opérations d'escrow sont **signées dans le navigateur** : l'API ne fait que les transmettre au réseau.
- Format JSON : montants en chaînes décimales d'unités (jamais de nombre à virgule), adresses `etl1q…`, identifiants en hexadécimal.

#### Site public (etalon-etl.com)

Le site public est servi par un nœud Étalon lancé avec la section `[public]` de `config.toml`, derrière
un proxy HTTPS sur la même machine (Caddy, certificat Let's Encrypt). Le nœud écoute toujours sur
`127.0.0.1` seulement ; le proxy seul est joignable depuis Internet.

- **Nœud archive et passif obligatoire** (`pruning = false`, `wallet = ""`) : aucune clé privée sur une
  machine exposée. Une configuration contraire est refusée au démarrage.
- **Ce qui change par rapport au dashboard** :

| | Dashboard (localhost) | Site public |
|---|---|---|
| En-tête `Host` accepté | `localhost:3001`, `127.0.0.1:3001` | le nom du site seulement (`host`) |
| `Origin` accepté pour une modification | `http://localhost:3001` | `https://` + nom du site |
| HSTS | non | oui (deux ans) |
| Jeton et routes de l'opérateur | oui | **ni jeton écrit, ni routes enregistrées** |
| Transferts et échanges signés dans le navigateur | oui | oui |
| Mises à jour en direct | SSE | relecture de l'API toutes les 30 secondes |
| Limitation de débit | non | par adresse IP, lectures et soumissions séparées |

- **Limitation de débit** : seau à jetons par adresse IP (par défaut 300 lectures et 6 soumissions par
  minute), sur l'API seulement ; au-delà, réponse 429 avec le délai d'attente. L'adresse vient de
  l'en-tête `X-Forwarded-For` ajouté par le proxy, cru seulement si la connexion arrive de la boucle
  locale. Une adresse IPv6 compte par bloc /64. Mémoire bornée (100 000 clients, puis un seau commun).
- **Aucun journal d'adresse IP** : les seaux restent en mémoire et s'effacent après une minute
  d'inactivité ; le journal d'erreurs du serveur HTTP, qui cite l'adresse du client, est coupé. Le proxy
  est configuré sans journal d'accès.
- **Avant le lancement d'ETL-FR** : `etl run` sert seulement les pages de présentation et un statut
  `{"launched": false}` — ni chaîne, ni réseau, ni portefeuille, ni explorateur.

**Interface du site public.** C'est la même page et le même code que le dashboard : l'interface lit
`/api/status` et choisit sa navigation.

| Mode | Navigation |
|---|---|
| Dashboard de l'opérateur (localhost) | Accueil · Portefeuille · Échanges · IPC · Opérateur |
| Site public, réseau lancé | Accueil · Portefeuille · Échanges · Explorateur · IPC · Rejoindre |
| Site public, avant le lancement | Accueil · Rejoindre, avec le bandeau « réseau pas encore lancé » |

- Une page absente du mode (Opérateur sur le site public) ramène à l'accueil.
- **Accueil visiteur** : la valeur de 1 ETL, la simulation, le prochain bloc et les chiffres du réseau ;
  ni la carte du nœud, ni l'indicateur de santé (qui décrit le nœud qui sert le site), remplacé par
  « dernier bloc il y a X h ». Avant le lancement : la promesse, sans valeur du jour, et trois principes.
- **Portefeuille** : le même, avec un avertissement permanent — son code vient du site ; pour une sécurité
  maximale, utiliser le portefeuille de son propre nœud. Historique complet, page par page.
- **Échanges** : l'outil d'escrow (ouvrir, compléter, suivre) sur les deux interfaces. Le tableau
  d'annonces, quand il existera, ne sera visible que sur le dashboard local tant que la vérification MiCA
  n'est pas faite.
- **IPC** : la même page, sans la relecture manuelle de la source (action de l'opérateur).
- **Rejoindre** : les trois étapes (comprendre, essayer, devenir fondateur) et la documentation dans les
  quatre langues, lue sur le dépôt GitHub (aucune copie à tenir à jour).
- **Mises à jour** : relecture de l'état toutes les 30 secondes ; à chaque nouveau bloc, la page se met à
  jour et le visiteur est averti des transactions de son portefeuille.

---

## Step 4 — Première transaction

### Structure Go

```go
type Transaction struct {
    ID        crypto.Hash      // Blake3 de l'encodage binaire canonique
    From      crypto.Address   // adresse ETL de l'expéditeur
    To        crypto.Address   // adresse ETL du destinataire
    Amount    uint64           // montant en unités ETL (1 ETL = 10^8 unités) — jamais float64
    Fee       uint64           // max(Amount * fee_bps / 10000, MinFee) — exactement
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
- **Tours de proposition** : si le bloc n'est pas finalisé, le ticket suivant propose au tour suivant

| Tour | Proposant | Début (UTC) |
|---|---|---|
| 0 | 1er ticket | 23:00:30 |
| 1 | 2e ticket | 23:02:30 |
| 2 | 3e ticket | 23:04:30 |
| 3 | 4e ticket | 23:06:30 |
| 4 | 5e ticket | 23:08:30 |

  - Un tour est identifié par **(jour, tour)**. Tant que la hauteur n'est pas finalisée, **les tours continuent
    le lendemain** : (J1, 0) … (J1, 4), puis (J2, 0)… Un jour sans bloc ne bloque jamais la chaîne
  - **Un seul proposant légitime par tour** : au tour (jour, r), le r-ième ticket parmi les participations de
    **ce jour-là**. La proposition joint cette liste (la « nomination »), contrôlée par chaque votant
    comme les participants d'un bloc (complétude avant 23:00:25)
  - Le proposant propose : **le bloc qu'il sait verrouillé/valide** (avec la preuve de plus de 2/3 de pré-votes
    à un tour antérieur), sinon **un nouveau bloc**, daté du jour, dont les participants sont la nomination
  - Un bloc finalisé en retard **garde sa date et ses participants** (récompensés) ; les participations du jour
    du rattrapage ne sont pas récompensées — cas rare, réservé aux incidents réseau
  - Les participations d'un bloc restent les mêmes d'un tour à l'autre : un proposant défaillant garde sa part ;
    seuls les frais vont au proposant du bloc
  - **Aucune sanction** contre un proposant défaillant : la non-livraison n'est pas prouvable (panne réseau possible)
  - Un nœud **refuse une proposition reçue avant le début de son tour** (tolérance d'horloge ±5 s) : sinon le
    proposant d'un tour suivant pourrait devancer le proposant légitime

**Phase 3 — Vote en deux temps avec verrouillage** (Tendermint, adapté au rythme quotidien)

| Étape | Quand | Effet |
|---|---|---|
| **Pré-vote** | à réception d'une proposition valide, dans son tour | « ce bloc me convient » |
| **Pré-engagement** | quand le nœud voit **plus de 2/3 de pré-votes** pour un bloc **dans le tour en cours** | le nœud se **verrouille** sur ce bloc (sur disque), puis signe |
| **Finalité** | **plus de 2/3 de pré-engagements** pour le même bloc **au même tour** | certificat, stockage, diffusion |

- Votent : les nœuds **admis et actifs** selon le registre **avant** le bloc. Pas de vote « nul » : les tours
  avancent à l'horloge, l'absence de vote suffit
- **Seuil : strictement plus de 2/3** des nœuds actifs — 7 sur 10, 7 sur 9, 667 sur 1 000
- **Règles de pré-vote** :
  - nouveau bloc : si le nœud n'est pas verrouillé, ou verrouillé sur ce bloc ;
  - re-proposition avec preuve au tour vr : si son verrou est antérieur ou égal à vr, ou porte sur ce bloc
- Un nœud honnête ne pré-vote en outre que si la proposition est valide (bloc, `StateRoot`, nomination), et si
  elle contient **toutes les participations valides qu'il a reçues avant 23:00:25 UTC** et n'inclut aucun nœud
  qu'il a vu participer deux fois avant cette heure (tolérance réseau : le proposant a pu ne pas les recevoir)
- **Pourquoi c'est sûr** : un bloc finalisé a été pré-engagé par plus de 2/3 des nœuds, tous verrouillés sur lui ;
  ils ne pré-voteront aucun autre bloc sans preuve plus récente, qui ne peut pas exister. Tant que moins d'un
  tiers trichent, **jamais deux blocs finalisés à la même hauteur**
- **Pourquoi la chaîne repart toujours** : un nœud ne se verrouille qu'avec une preuve (plus de 2/3 de pré-votes).
  Après une coupure sans preuve, personne n'est verrouillé ; après une coupure avec preuve, le bloc verrouillé
  est re-proposé jusqu'à sa finalisation
- **Sur disque, avant toute diffusion** : chaque vote signé, par (hauteur, jour, tour, type) — jamais deux votes
  différents pour la même combinaison ; et le **verrou avec sa preuve** — un nœud redémarré ne l'oublie pas
- **Double vote** = deux pré-votes (ou deux pré-engagements) **du même tour** pour deux blocs différents :
  faute prouvable → **exclusion automatique**. Voter différemment à des tours différents est normal
- **Certificat de finalité** = les pré-engagements d'un même tour, stockés avec le bloc ; quiconque se synchronise
  vérifie lui-même la finalité. Certificats élagués à 90 jours, **sauf le dernier**
- **Vérifié par simulation** : attaque ciblée (« amnésie ») qui produit deux blocs finalisés si le verrou est
  désactivé, et simulation aléatoire avec 3 nœuds malveillants sur 10, pertes, désordre et coupures

### Récompense
- **Émission constante : 10 ETL par jour**, sans halving. La stabilité vient de l'IPC, pas de la rareté
- **Partagée à parts égales** entre tous les nœuds ayant soumis une participation valide : `10 ETL ÷ N`
  - Chaque part est arrondie à l'unité inférieure ; le reste (moins de N unités, soit < N × 0,00000001 ETL) n'est pas émis
- **Les frais de transaction vont au proposant** du bloc
- Un nœud **exclu par le bloc** (exclusion votée, double participation, double vote) peut figurer parmi ses
  participants — le rang du proposant en dépend — mais **ne touche pas sa part** ce jour-là
- **Aucune récompense tant que moins de 10 nœuds sont admis et actifs** (compté au début du bloc)
- Genesis : aucune récompense (zéro pré-mine)
- **ETL en circulation** = somme des soldes + fonds retenus dans les échanges en cours. Rien n'étant
  détruit (les frais vont au proposant), c'est exactement le total émis depuis le genesis
- **Part de chaque bloc enregistrée pour l'affichage** : la part dépend du nombre de nœuds actifs
  *avant* le bloc, que l'état ne conserve pas. Le nœud enregistre donc, au moment où il applique un bloc,
  la part versée à chaque participant récompensé (table `rewards`, 8 octets par bloc). Cette donnée est
  **hors du `StateRoot`** : elle ne participe pas au consensus et deux nœuds ne peuvent pas diverger à
  cause d'elle. Elle alimente « part du dernier bloc » et les récompenses de l'historique ; pour un bloc
  appliqué par une version antérieure, la part est inconnue et n'est pas affichée (jamais estimée)

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
- **Frais minimum : 0,0001 ETL** par transaction — `frais = max(0.1 %, 0,0001 ETL)`, montant exact exigé.
  Sans minimum, les micro-transactions seraient gratuites et un attaquant pourrait remplir chaque bloc.
  Un paiement de 0,1 ETL ou plus n'est pas concerné ; saturer un bloc coûte 10 ETL par jour
- **Taille de bloc : 100 000 transactions maximum** (gouvernable) — avec 1 bloc/jour, c'est le débit quotidien

### Mempool (salle d'attente des transactions)
- **Admission**, contre l'état du dernier bloc : transaction valide ; nonce ni passé ni trop en avance ;
  solde suffisant pour **toutes** les transactions en attente de l'expéditeur (sans dépassement de capacité) ;
  à nonce égal, la première arrivée gagne (frais fixes : pas d'enchère) ; ni datée du futur, ni de plus de 7 jours
- **Sélection** par le proposant : premier arrivé, premier servi, dans l'ordre des nonces de chaque compte,
  horodatage ≤ 23:00:00 ; simulation des soldes : le bloc s'applique toujours sans erreur
- **Après chaque bloc** : retrait des transactions incluses, au nonce dépassé, expirées, ou que le nouveau solde
  ne couvre plus (avec celles qui en dépendent)
- En mémoire uniquement ; limites locales (non consensus) : 16 par compte, 200 000 au total, 7 jours

---

### Racine d'état (StateRoot)
- Chaque en-tête contient la **racine de Merkle de l'état** (soldes + registre des nœuds) **après** application du bloc
- Validée par le vote 2/3 comme le reste du bloc : un bloc dont la racine ne correspond pas est rejeté
- Permet de vérifier n'importe quel instantané de l'état sans rejouer la chaîne depuis le genesis :
  *« Ne faites pas confiance. Vérifiez. »* — même quand les anciens blocs ont été élagués
- Calcul : feuilles triées par adresse (`A | adresse | solde | nonce`, `N | adresse | entrée du registre`), Blake3

### Stockage et pruning
- **bbolt** : un fichier, transactions ACID. Un bloc, son index, les changements d'état et la tête de chaîne
  sont écrits dans **une seule transaction** : une coupure de courant ne laisse jamais une chaîne à moitié écrite
- L'état est écrit sur disque **avant** d'être modifié en mémoire : un échec d'écriture ne change rien
- **Seuls les blocs finalisés** (vote 2/3) sont stockés : pas de réorganisation de chaîne
- Au démarrage, l'état rechargé est vérifié contre le `StateRoot` du dernier bloc : une base altérée est refusée
- Chaque bloc relu est revérifié contre son en-tête : une corruption du disque est détectée
- Fichiers en `0600`, répertoire en `0700`

| Donnée | Conservation |
|---|---|
| En-têtes (~250 o/jour) | Toujours |
| Genesis complet | Toujours |
| Contenu des blocs | 90 derniers blocs (`prune_keep_blocks`), puis élagué |
| État (soldes + registre) | Toujours, à jour au dernier bloc |
| Part de récompense par bloc (8 o/jour, hors `StateRoot`) | Toujours |
| Index de l'explorateur (site public seulement, hors `StateRoot`) | Toujours, reconstructible à partir des blocs |

- **Mode archive** (`pruning = false`) : garde tout — pour l'explorateur et le nœud de bootstrap
- Le pruning est un choix local à chaque nœud, pas une règle de consensus
- **Index de l'explorateur** (nœud archive avec `[public]` activé) : adresse → blocs où son solde
  bouge (transferts, échanges, récompenses), transaction → bloc, échange → bloc d'ouverture et parties.
  Il ne sert qu'à l'affichage : hors `StateRoot`, hors consensus. Activé après coup, ou après une
  interruption, il est reconstruit au démarrage à partir des blocs, à l'identique

### Encodage binaire
- Format canonique unique pour le stockage **et** le réseau : un objet = une seule suite d'octets possible
- Décodeurs durcis contre les entrées hostiles (tronquées, trop longues, tailles de liste démesurées) et testés par fuzzing
- L'identifiant d'une transaction n'est jamais transmis : il est recalculé

## Step 5 — Confirmation de transaction

### Finalité
- **Un bloc finalisé est définitif** dès sa finalisation (certificat de plus de 2/3 de pré-engagements) :
  pas de confirmations supplémentaires à attendre, quel que soit le montant (règle immuable n°4)

### Notifications
- **Server-Sent Events (SSE)** — notification temps réel dans le dashboard local (le site public relit l'API toutes les 30 secondes) : le nœud pousse un événement par bloc finalisé, le navigateur garde ce qui le concerne (protocole HTTP standard, sans dépendance, reconnexion automatique)
- À 23:00 UTC : l'expéditeur et le destinataire reçoivent la confirmation

```
✅ Transaction confirmée — Bloc #142
   5 ETL envoyés à ETL1pAuL...
   Frais : 0.005 ETL
```

### Explorateur public

Onglet Explorateur du site public (etalon-etl.com). Chaque page a sa propre adresse, que l'on peut
partager :

| Page | Adresse | Contenu |
|---|---|---|
| Derniers blocs | `#/explorer` | hauteur, date, proposant, nombre de transactions, d'opérations d'échange et de participants, IPC adopté ; pages plus anciennes à la demande |
| Bloc | `#/explorer/block/412` | empreinte, bloc précédent, proposant, partage de la récompense, IPC, tour de vote ; transactions, opérations d'échange, participants |
| Adresse | `#/explorer/address/etl1q…` | solde, transactions envoyées, statut de nœud, échanges menés à terme ; historique complet, page par page |
| Transaction | `#/explorer/tx/…` | montant, expéditeur, destinataire, bloc, frais, numéro d'envoi, heure de signature |
| Échange | `#/explorer/escrow/…` | montant, parties et leur réputation, frise des étapes avec le numéro de bloc de chacune, rémunération de l'arbitre (versée ou rendue) |

- Accessible à tous — membres ou non
- Tout identifiant mène à sa page ; une empreinte s'affiche en entier sur sa propre page
- **Recherche** : une seule case pour une hauteur, une adresse (majuscules acceptées) ou une empreinte de
  bloc, de transaction ou d'échange. Sans résultat, la page dit pourquoi :

| Motif | Exemple | Ce que la page dit |
|---|---|---|
| empreinte incomplète | `3f7a91c2e8` | une empreinte complète compte 64 caractères ; celle-ci en a 10 |
| empreinte inconnue | 64 caractères | une transaction n'apparaît qu'après le prochain bloc (heure donnée) |
| autre réseau | `etlt1q…` | adresse du réseau de test (ou d'un autre préfixe) ; préfixe de ce réseau |
| bloc futur | `99999` | le dernier bloc est le N ; heure du prochain bloc |
| non reconnu | `bonjour` | ni hauteur, ni adresse de ce réseau, ni empreinte |

- **Échanges archivés** : un échange réglé depuis plus de 90 jours n'est plus dans l'état des nœuds ; sa
  page est reconstituée à partir de son bloc d'ouverture et de son bloc de règlement (index de
  l'explorateur), et le dit.
- Historique complet grâce à l'index de l'explorateur du nœud archive (voir *Stockage et pruning*)
- UX moderne et interactive
- Principe manifeste : *"Ne faites pas confiance. Vérifiez."*

---

## Step 6 — Oracle IPC (mise à jour trimestrielle)

### Source officielle
- **INSEE — Série 011814630** (défaut ETL-FR)
- IPC base 2025 — Ensemble des ménages — France — Coicop 00 « Ensemble » (panier complet, tabac inclus)
- Lecture : **API SDMX de l'INSEE** — `https://api.insee.fr/series/BDM/V1/data/SERIES_BDM/011814630`
- Page lisible par un humain : `https://www.insee.fr/fr/statistiques/serie/011814630`
- **Configurable** dans `config.toml` (`ipc_url`, `ipc_country`) — jamais codée en dur
- Lecture **une fois par jour**, à une minute tirée au hasard entre 20:00 et 22:30 UTC (3 tentatives au
  plus) : l'INSEE voit l'adresse IP du nœud ; `HTTPS_PROXY` est respecté (voir le guide de l'opérateur)
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
- Un nœud honnête ne signe (usage `ipc-vote`) que le triplet qu'il a lu lui-même à la source
- **Strictement plus de 2/3** des nœuds actifs doivent signer un **triplet identique**
  (votants admis et seuil comptés au moment de l'inclusion, sur le registre avant le bloc)
- Dès le seuil atteint, le proposant du jour inscrit le triplet dans l'en-tête et les votes dans le bloc ;
  tous les nœuds revérifient les votes. N'importe qui peut re-télécharger la source et vérifier le hash
- **Période strictement postérieure** à la période courante : pas de retour en arrière, pas de rejeu.
  Un trimestre peut manquer (l'INSEE n'a pas publié) : le suivant est accepté
- **Pas de garde-fou automatique** sur l'ampleur d'une variation : une limite arbitraire pourrait bloquer
  une vraie crise inflationniste. La protection est le vote 2/3 et la vérification de la source par chacun

### L'IPC dans l'état
- L'état garde l'**IPC de référence** (genesis, immuable) et l'**IPC courant** ; les deux sont dans le `StateRoot`
- Taux affiché : `1 ETL = IPC courant ÷ IPC de référence` euro — vérifiable par tous
- **Historique** : chaque IPC adopté est inscrit dans l'en-tête du bloc qui l'adopte, et les en-têtes sont
  gardés à vie. Le nœud reconstruit la courbe trimestre par trimestre en relisant ses en-têtes au
  démarrage, puis la complète à chaque bloc : aucune donnée supplémentaire stockée

```
Nœud A → { 103.07, "2026-Q3", 9f86d0... }
Nœud B → { 103.07, "2026-Q3", 9f86d0... }
Nœud C → { 103.07, "2026-Q3", 9f86d0... }
Nœud D → { 103.07 }                          ← rejeté (incomplet)
             ↓
2/3 accord sur le triplet → IPC 2026-Q3 = 103070 (× 1000) gravé dans le bloc
```

### Fréquence
- **Trimestrielle** : seule fréquence acceptée par le logiciel actuel (`ipc_frequency = "quarterly"` ;
  toute autre valeur est refusée au démarrage d'un nœud avec clé)
- Un passage au mensuel décidé par vote des nœuds reste un **objectif futur** : il fera l'objet d'une étape
  dédiée si la communauté le demande

### Si l'INSEE ne publie pas
- La valeur précédente est maintenue (règle immuable n°11)

### Page de vote IPC (dashboard + site web)

```
Section "IPC en cours"
├── Valeur actuelle : 102.85 — 2026-Q2 (validée le 01/07/2026)
├── Nouvelle proposition : 103.07 — 2026-Q3 (source INSEE, publiée le 16/10/2026)
├── Votes : 4/7 nœuds ont voté ✅ (2/3 requis = 5/7)
├── Dernière lecture de ce nœud : 20/10/2026 21:14 UTC — vote émis
└── [Relire la source maintenant] ← bouton pour l'opérateur de nœud
```

- Le vote est **automatique** : chaque nœud lit lui-même la source chaque jour (voir ci-dessus).
- Le bouton ne fait que déclencher une lecture immédiate (au plus une toutes les 5 minutes). **Aucune valeur ne peut être saisie à la main** : un nœud ne vote que ce qu'il a lu à la source.

---

## Gouvernance par vote 2/3 — engagement et limites

- Votes IPC et exclusions Sybil sont engagés par l'en-tête (`VotesRoot`), comme tout le contenu du bloc
- Même seuil partout : **strictement plus de 2/3** des nœuds actifs, comptés sur le registre avant le bloc
- Changements de paramètres engagés de la même façon (voir ci-dessous)

### Gouvernance des paramètres
- Les paramètres gouvernables sont **dans l'état on-chain** (couverts par le `StateRoot`)
- Chaque nœud signe `{paramètre, nouvelle valeur, jour}` (usage `param-vote`), vote valable 30 jours ;
  plus de 2/3 des nœuds actifs pour **la même valeur** → le changement est inscrit dans un bloc
- **Délai d'activation : 30 jours** — le changement reste en attente, puis s'applique à tous au même moment.
  Le mempool écarte alors les transactions devenues invalides (frais périmés)
- **Un seul changement en attente par paramètre**
- Garde-fous **immuables** : bornes absolues, et variation par vote limitée à ×2 / ÷2 (frais, frais minimum,
  taille de bloc, récompense) ou ±2 bits (difficulté)

| Paramètre | Défaut | Bornes |
|---|---|---|
| Frais (`fee_bps`) | 0,1 % | 0 à 1 % |
| Frais minimum | 0,0001 ETL | 0 à 0,01 ETL |
| Taille de bloc | 100 000 tx | 10 000 à 1 000 000 |
| Récompense quotidienne | 10 ETL | 1 à 100 ETL |
| Difficulté | 21 bits | 16 à 28 bits |
| Durée d'observation | 7 jours | 3 à 30 jours |
| Présences exigées pendant l'observation | 5 | 1 à 30 (≤ durée d'observation) |
| Inactivité | 30 jours | 7 à 365 jours |
| Ancienneté du parrain | 180 jours | 30 à 730 jours |
| Délai entre parrainages | 30 jours | 7 à 365 jours |
| Interdiction de parrainage (Sybil) | 365 jours | 30 à 1 825 jours |
| Seuil de récompense (nœuds actifs) | 10 | 3 à 100 |

- **Restent fixes** (nouvelle version du logiciel nécessaire) : le calendrier du consensus (23:00, fenêtre de 30 s,
  tolérances, tours), l'identité du réseau (`chain_id`, préfixe, genesis, fondateurs) et les règles immuables
- La fréquence de l'IPC relève de l'oracle (le protocole accepte toute période postérieure)

### Limite assumée du modèle 2/3
Une coalition de **plus de 2/3 des nœuds** pourrait exclure des nœuds honnêtes, imposer une valeur IPC
ou finaliser des blocs qui l'arrangent. C'est l'hypothèse de confiance de tout le consensus :
ETL est sûr tant que **moins d'un tiers** des nœuds trichent.

La protection n'est pas technique mais **la transparence** : chaque vote est signé et inscrit dans la chaîne.
Chacun voit qui a voté quoi, quand. Une capture serait publique, prouvable, et la communauté honnête
pourrait forker à partir du dernier état sain — le code est libre (GPL v3) et l'état vérifiable (`StateRoot`).

---

## Step 7 — Échange P2P euros/ETL

### Principe
ETL ne touche pas au système bancaire. ETL sécurise uniquement **la partie ETL** de l'échange. La partie euros est hors-chaîne, libre choix des membres.

### Tableau d'annonces P2P
- Disponible sur **etalon-etl.com/echange** et **GitHub**
- Les membres publient leurs offres d'achat/vente
- Contact direct entre membres — pas d'intermédiaire

### Escrow automatique

La chaîne voit les ETL, pas les euros : elle ne peut pas savoir seule qui ment en cas de désaccord.
L'escrow bloque donc les ETL du vendeur dans l'état, et le règlement exige l'accord des bonnes personnes.

```
1. Marie publie : "Vends 50 ETL — 1 ETL = 1 €" ; Paul accepte
2. Marie OUVRE l'échange : ses 50 ETL sont bloqués on-chain pour Paul
   (délai choisi par Marie entre 1 et 30 jours ; arbitre facultatif)
3. Paul envoie 50 € à Marie (virement, cash, Lydia... son choix) et signe « PAYÉ »
4. Marie LIBÈRE les 50 ETL vers le wallet de Paul
```

**Qui peut régler l'échange (règle « 2 sur 3 »)**

| Issue | Peut la décider |
|---|---|
| Libération (ETL → acheteur) | le vendeur seul, **ou** l'acheteur + l'arbitre |
| Remboursement (ETL → vendeur) | l'acheteur seul, **ou** le vendeur + l'arbitre, **ou** le vendeur seul si l'échéance est passée sans « payé » |

- **Arbitre** : choisi par les deux parties au cas par cas, il doit être un **nœud admis** du registre (identifiable, et exclu s'il triche) et **cosigner l'ouverture**. Seul, il ne peut rien. Un arbitre exclu du registre perd son pouvoir d'arbitrage. Sa rémunération, fixée à l'ouverture, ne lui est versée que s'il cosigne le règlement ; sinon elle revient au vendeur.
- **Mode simple (sans arbitre)** : sans « payé », l'échéance permet au vendeur de récupérer ses ETL. **Après « payé », seuls la libération par le vendeur ou le remboursement accordé par l'acheteur restent possibles : sans accord, les fonds restent bloqués indéfiniment.** À choisir uniquement entre personnes qui se font confiance, ou avec une bonne réputation de part et d'autre.
- **« Payé »** : signé par l'acheteur, une seule fois, **avant l'échéance** (il ne peut pas bloquer tardivement le remboursement dû au vendeur).
- **Litige** : ouvert par le vendeur ou l'acheteur, une fois par échange — alerte publique, visible de tous.

**Frais**
- À l'ouverture, le vendeur paie les frais d'un transfert du montant (0,1 %, minimum 0,0001 ETL), versés au proposant du bloc.
- Toutes les opérations suivantes (« payé », litige, règlement) sont **gratuites** : l'acheteur, souvent un nouveau venu, n'a pas encore d'ETL. Au plus trois opérations par échange, donc un spam borné par le coût de l'ouverture.

**Sécurité du protocole**
- Chaque opération est signée (usage `escrow`) et porte la **séquence de l'échange** (nombre d'opérations déjà appliquées) : elle ne peut être ni rejouée ni appliquée dans le désordre, sans dépendre du nonce d'un compte.
- Une ouverture signée n'est valable que **7 jours** (anti-rejeu, comme les candidatures).
- Les opérations forment une section du bloc (`EscrowOps`, racine `EscrowRoot` dans l'en-tête) et comptent dans la taille maximale du bloc ; échanges et réputation sont couverts par le **StateRoot**.
- Un échange réglé est élagué **90 jours** après son règlement ; la réputation, elle, reste.

**Dans le dashboard**
- **Suivi fidèle au protocole** : Ouvert → Euros payés → Réglé (ETL libérés à l'acheteur, ou rendus au
  vendeur) ; un litige s'affiche en rouge. Il n'y a pas d'étape « livré » : la chaîne ne connaît que le
  paiement déclaré et le règlement
- **Échéance** affichée telle qu'elle s'applique : l'acheteur doit déclarer le paiement avant la date ;
  passée sans « payé », le vendeur peut récupérer ses ETL seul
- **Prochaine action** indiquée selon le rôle (« À vous : libérer les ETL », « En attente du paiement de
  l'acheteur »…) ; chaque signature est précédée de ce qu'elle produit (« 50 ETL iront à l'acheteur.
  C'est définitif. »)
- **Réputation en chiffres** (terminés, remboursés, litiges ouverts, litiges perdus), jamais en étoiles :
  la chaîne ne connaît aucune note
- **Libellé local** (« vélo », « cours de guitare ») : la chaîne ne connaît aucun titre ; l'utilisateur
  peut nommer un échange dans son navigateur, chiffré comme ses contacts (clé distincte)
- **Arbitrage** depuis le panneau opérateur du nœud arbitre : libérer à l'acheteur **ou** rembourser le
  vendeur, rien entre les deux (le protocole ne prévoit pas de répartition). La partie concernée
  contresigne avec le texte transmis

> L'ouverture du tableau d'annonces fera l'objet d'une vérification réglementaire préalable (règlement européen MiCA sur les services sur crypto-actifs).

### Moyens de paiement euros
- Virement bancaire, cash, PayPal, Lydia, Revolut...
- ETL ne dicte rien — liberté totale

### Système de réputation

```
Wallet etl1qxk9... — Réputation on-chain
├── 12 échanges menés à terme
├── 1 remboursement
├── 1 litige ouvert
└── ⚠️ 1 litige perdu (réglé contre lui avec l'arbitre)
```

- Compteurs tenus par la chaîne pour chaque adresse, vendeur comme acheteur : échanges menés à terme, remboursements, litiges ouverts, litiges perdus (règlement cosigné par l'arbitre en sa défaveur)
- Lié au wallet pseudonyme (pas à l'identité réelle)
- Gravé on-chain — personne ne peut l'effacer
- **Alerte** dès qu'un litige est ouvert, visible sur le tableau d'annonces

---

## Récapitulatif des pages / interfaces

| Interface | URL | Audience |
|---|---|---|
| Site principal | etalon-etl.com | Tous |
| Explorateur blocs | etalon-etl.com (onglet Explorateur) | Tous |
| Échange P2P | etalon-etl.com/echange | Membres |
| Vote IPC | dashboard (section IPC) | Opérateurs nœuds |
| Dashboard local | localhost:3001 | Opérateurs nœuds |
| Code source | GitHub public | Tous |

---

## Prochaine étape

Cérémonie de lancement d'ETL-FR : date et liste des fondateurs (à décider).

---

**ETL — Étalon**
*Architecture v1.0 — octobre 2026*
