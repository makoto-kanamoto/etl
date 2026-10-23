<p align="center"><img src="web/dashboard/logo.svg" width="72" height="72" alt="Étalon"></p>

<h1 align="center">Étalon — ETL</h1>

<h3 align="center">1 ETL = ce que valait 1 € de pouvoir d'achat le jour du premier bloc.</h3>
<p align="center"><em>1 ETL = what €1 could buy on the day of the first block.</em></p>

<p align="center">
Une monnaie communautaire indexée sur les prix réels : quand les prix montent, la valeur de l'ETL en euros monte avec eux.<br>
<em>A community currency indexed to real prices: when prices rise, the euro value of ETL rises with them.</em>
</p>

<p align="center"><a href="#english">English</a> · <a href="docs/ja/SHORT_MANIFESTO.md">日本語</a> · <a href="docs/zh/SHORT_MANIFESTO.md">中文</a></p>

> **Statut : en développement.** Le réseau ETL-FR n'est pas encore lancé. Un réseau de test permet déjà de l'essayer sur son ordinateur.

---

## Pourquoi c'est différent

Depuis l'abandon de l'étalon-or, aucune monnaie ne protège votre pouvoir d'achat : 100 € laissés sur un compte achètent moins chaque année. L'ETL est conçu pour l'inverse.

- **Indexé sur les prix réels.** La valeur de 1 ETL suit l'indice des prix à la consommation de l'INSEE (IPC). Chaque nœud lit lui-même la publication officielle ; une nouvelle valeur n'est adoptée que si plus des 2/3 des nœuds lisent la même. Personne ne la saisit à la main.
- **Aucun pré-minage, aucune banque.** Zéro ETL au lancement. Ensuite, dès que 10 nœuds au moins sont actifs, 10 ETL par jour, partagés à parts égales entre les nœuds qui participent. Pas de halving, pas de spéculation sur la rareté : la stabilité vient de l'IPC.
- **1 membre = 1 nœud.** Chaque nouveau nœud est parrainé par un nœud déjà admis, puis observé pendant 7 jours. Ni course à la puissance de calcul, ni fermes de machines.
- **Gouverné par ses membres.** Frais, récompense, règles d'admission : modifiables seulement par un vote de plus des 2/3 des nœuds, dans des bornes inscrites dans le code.
- **Reproductible dans d'autres pays.** Le protocole est conçu pour être repris par d'autres communautés : ETL-DE, ETL-JP… gardent les mêmes règles, avec leur propre lancement, leur propre préfixe d'adresse et l'indice des prix de leur pays. Une adresse d'un réseau est refusée sur un autre.
- **Vos clés restent chez vous.** Le portefeuille vit dans votre navigateur, chiffré par votre phrase de passe ; sa seule sauvegarde est une phrase de 24 mots, que personne d'autre ne connaît.
- **Échanger des euros sans se faire avoir.** Les ETL du vendeur restent bloqués sur la chaîne jusqu'au règlement, avec un arbitre facultatif et une réputation publique pour chaque adresse.

## Ne faites pas confiance. Vérifiez.

Le code est libre. Chaque bloc et chaque transaction sont vérifiables par tous. Chaque IPC adopté porte l'empreinte de la publication de l'INSEE dont il provient : vous pouvez la comparer vous-même sur [insee.fr](https://www.insee.fr/fr/statistiques/serie/011814630).

## Rejoindre en 3 étapes

1. **Comprendre** — lisez le [manifeste court](docs/fr/MANIFESTE_COURT.md) : cinq minutes.
2. **Essayer** — lancez un réseau de test de 4 nœuds sur votre ordinateur et ouvrez le tableau de bord : [guide de l'opérateur, section « Réseau de test »](docs/fr/GUIDE_OPERATEUR.md#réseau-de-test). Prérequis : [Go](https://go.dev/dl/) **1.27.2 ou plus récent** (Linux ou macOS).
   ```sh
   go build -o etl ./cmd/etl
   ```
3. **Devenir fondateur** — le réseau ETL-FR sera lancé par au moins 10 membres fondateurs indépendants, annoncés publiquement avant le lancement. Pour vous porter candidat : makoto.kanamoto@proton.me.

## Documentation

| | Français | English | 日本語 | 中文 |
|---|---|---|---|---|
| Manifeste fondateur | [Lire](docs/fr/MANIFESTE_FONDATEUR.md) | [Read](docs/en/FOUNDERS_MANIFESTO.md) | [読む](docs/ja/FOUNDERS_MANIFESTO.md) | [阅读](docs/zh/FOUNDERS_MANIFESTO.md) |
| Manifeste court | [Lire](docs/fr/MANIFESTE_COURT.md) | [Read](docs/en/SHORT_MANIFESTO.md) | [読む](docs/ja/SHORT_MANIFESTO.md) | [阅读](docs/zh/SHORT_MANIFESTO.md) |
| Guide de l'opérateur de nœud | [Lire](docs/fr/GUIDE_OPERATEUR.md) | [Read](docs/en/OPERATOR_GUIDE.md) | — | — |
| Architecture technique | [Lire](docs/ARCHITECTURE.md) | — | — | — |
| Tableau de bord du nœud | ✓ | ✓ | ✓ | ✓ |

*En cas de divergence entre les versions, la version française fait foi.*

---

<a id="english"></a>

## In English

**1 ETL = what €1 could buy on the day of the first block.** Étalon is a community currency indexed to the French consumer price index (INSEE CPI). **Status: in development** — the ETL-FR network has not launched yet; a test network already runs on your own computer.

**Why it is different**

- **Indexed to real prices.** Each node reads the official INSEE publication itself; a new value is adopted only if more than 2/3 of the nodes read the same one. Nobody types it in.
- **No pre-mine, no bank.** Zero ETL at launch; then, once at least 10 nodes are active, 10 ETL a day shared equally between participating nodes. No halving: stability comes from the CPI, not from scarcity.
- **One member, one node.** Every new node is sponsored by an admitted node, then observed for 7 days. No race for computing power.
- **Governed by its members.** Fees, reward and admission rules change only by a vote of more than 2/3 of the nodes, within bounds written into the code.
- **Reproducible in other countries.** The protocol is designed to be taken up by other communities: ETL-DE, ETL-JP… keep the same rules, with their own launch, their own address prefix and their own country's price index. An address from one network is refused on another.
- **Your keys stay with you.** The wallet lives in your browser, encrypted; its only backup is a 24-word phrase that nobody else knows.
- **Safe euro exchanges.** The seller's ETL stay locked on the chain until settlement, with an optional arbiter and a public reputation for every address.

**Join in 3 steps**

1. **Understand** — read the [short manifesto](docs/en/SHORT_MANIFESTO.md): five minutes.
2. **Try** — run a 4-node test network on your computer: [operator guide, “Test network”](docs/en/OPERATOR_GUIDE.md#test-network). Requires [Go](https://go.dev/dl/) **1.27.2 or later** (Linux or macOS).
3. **Become a founder** — ETL-FR will be launched by at least 10 independent founding members, announced publicly before launch. To apply: makoto.kanamoto@proton.me.

**Documentation:** see the table above — manifestos in four languages, operator guide in French and English, technical architecture in French, node dashboard in all four languages. *If versions differ, the French version prevails.*

---

## À propos du nom / About the name

*Makoto Kanamoto (誠・金本) — vérité de l'étalon-or / truth of the gold standard.*
*En hommage à Satoshi Nakamoto. / In homage to Satoshi Nakamoto.*

## Contact

makoto.kanamoto@proton.me

## Licence / License

GPL v3 — Le code reste libre, pour toujours. / *The code stays free, forever.*
