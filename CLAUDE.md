# CLAUDE.md — Simulation de colonie de fourmis

Consignes pour Claude Code (et tout autre assistant) travaillant sur ce dépôt.
À lire avant d'écrire du code. Lire `constitution.md` avant de proposer une
optimisation.

> Le code, les commentaires et les identifiants sont en **anglais** (usage Go).
> La documentation et le rapport sont en **français**.

---

## 1. Ce qu'est ce projet

Une **simulation de colonie de fourmis écrite en Go**, réalisée comme projet
noté pour *Sup de Vinci — RNCP Bloc 4, « Optimisations & Performances
Backend »*.

La simulation n'est pas le but. **C'est une charge de travail.** Son rôle est
d'être un calcul backend réaliste et mesurable, optimisable pas à pas à mesure
que le cours introduit chaque levier, chaque étape étant prouvée par des
chiffres.

### Ce qui est réellement noté

Extrait de `docs/bareme-evaluation-performance-for-backend.pdf` :

> Le code source déposé sert **exclusivement de pièce à conviction**. Le code en
> tant que tel **n'est pas noté**. Seul le document final (Rapport d'Audit) fait
> foi pour la notation.

Le livrable est **`docs/report/audit.md`**, un rapport d'audit de performance en
français, noté sur 20 points + 2 bonus. Le code existe pour rendre ses mesures
vraies et reproductibles.

| § | Points | Exigence |
|---|---|---|
| 1 | 3 | Spécification du banc d'essai (CPU, cœurs, L1/L2/L3, RAM, OS, runtime) + protocole hyperfine rigoureux (warmup, itérations, moyenne/médiane/écart-type/variance) |
| 2 | 5 | Captures pprof/flamegraph réelles + identification formelle du **Hot Path** initial |
| 3 | 5 | Journal d'optimisation sur 4 axes : mémoire & localité de cache ; concurrence & scalabilité CPU ; I/O & persistance |
| 4 | 3 | Au moins une **optimisation contre-productive documentée**, avec analyse chiffrée de la régression |
| 5 | 4 | Reproductibilité en **une commande** + tableau comparatif final (benchstat / hyperfine) |
| 6 | +2 | `constitution.md` — fichier de gouvernance IA |

Supports de cours : `docs/support-performance-for-backend-j1_am.pdf`
(fondamentaux, Grand O, hot path, représentation binaire) et `docs/..._j1_pm.pdf`
(CPU, registres, mur de la mémoire, L1/L2/L3, lignes de cache 64 octets,
localité spatiale et temporelle).

---

## 2. Les quatre règles

**1. Le code démarre bête à dessein, et évolue avec le cours.**
`internal/engine/naive` est délibérément lent. Ne **pas** le « nettoyer », ne
pas l'accélérer, ne pas corriger ses structures de données. Son inefficacité est
la baseline de mesure et la source de chaque gain du rapport. La qualité
architecturale n'est explicitement *pas* encore un objectif — la conception
s'améliore à mesure que chaque séance introduit le concept qui la justifie.

**2. Chaque optimisation est un nouveau moteur, documenté et mesuré.**
Ne jamais modifier un moteur existant pour l'accélérer. Le copier dans un
nouveau package, changer *une seule* chose, l'enregistrer sous un nouveau nom,
puis mesurer. Les anciens moteurs doivent continuer à compiler et à s'exécuter
pour que le harnais puisse comparer v0 et v5 côte à côte. Voir §5 pour la
procédure complète.

**3. Le déterminisme est l'invariant de correction.**
Même configuration + même graine ⇒ sortie identique au bit près, sur tous les
moteurs, pour toujours. C'est ce qui rend l'affirmation « c'est plus rapide »
vérifiable plutôt qu'invérifiable. Les règles qui protègent cet invariant sont
en §4 et ne sont pas négociables.

**4. Mesurer, jamais deviner.**
La règle du cours : *make it work → make it right → make it fast*, et *on ne
devine jamais le Hot Path, on le mesure*. Toute proposition doit venir avec une
hypothèse matérielle et la commande de profilage qui la vérifie.

---

## 3. Organisation

```
cmd/antsim/        exécution headless — LA CIBLE DE MESURE (hyperfine, pprof)
cmd/antweb/        interface web du harnais (seul consommateur de net/http)

internal/
  config/          données d'entrée : Config, scénarios JSON, validation, CanonicalHash
    scenarios/     tiny / small / medium / large (également go:embed pour l'UI)
  simcore/         CONTRATS UNIQUEMENT — Engine, Result, Snapshot, Observer, Rng, hash
  engine/
    naive/         baseline v0 — délibérément lente
    gradient/      VARIANTE sémantique de v0, pas un étage : golden à part
    register.go    blank-import de chaque moteur : un binaire les contient tous
  uiserver/        HTTP + SSE + front-end canvas

test/              tests golden de déterminisme + test d'hygiène des imports
bench/             suites testing.B ; artefacts results/ et profiles/
docs/report/       LE LIVRABLE NOTÉ
docs/journal/      journal d'optimisation, une entrée par séance
docs/env/          spécifications du banc d'essai capturées
```

### Données en entrée, données en sortie

```
Config (JSON : grille, murs, nourriture, fourmis, ticks, graine, phéromones)
   ↓  Engine.Run(ctx, cfg, observer)
Result (nourriture collectée/restante, fourmis chargées, total phéromones,
        AntPosHash, GridHash, StateChecksum   ← déterministe
        WallNS, Allocs, HeapBytes             ← observationnel, HORS checksum)
```

Le temps et les compteurs d'allocation sont délibérément exclus de
`StateChecksum` : les y inclure ferait échouer les fichiers golden au hasard.

### Pourquoi des packages et non des build tags

Les build tags donnent un moteur par binaire. Tout le harnais repose sur
l'exécution de v0 et v5 **dans le même processus** pour les comparer.
L'enregistrement à `init()` est ce qui rend cela possible. Ne pas le remplacer
par des build tags.

---

## 4. Règles de déterminisme — à ne pas casser

`internal/engine/naive` les respecte toutes. Tout nouveau moteur doit faire de
même.

1. **PRNG par fourmi.** Chaque fourmi possède un `simcore.Rng` initialisé par
   `SplitMix64(cfg.Seed ^ f(antID))`. Un flux unique partagé consommé dans
   l'ordre des fourmis serait plus simple et casserait silencieusement le
   moteur parallèle.
2. **Exactement deux tirages PRNG par fourmi et par tick, sans condition.** Les
   deux sont tirés avant tout branchement, même quand le second est inutilisé,
   pour que la position dans le flux reste une fonction pure du numéro de tick.
3. **Phéromones en virgule fixe (`uint32`, milli-unités). Aucun flottant dans le
   tick.** L'addition flottante n'est pas associative ; l'addition entière l'est.
   Les ratios sont des paires d'entiers `Num`/`Den` dans la configuration.
4. **Grille en double tampon.** Lire dans l'un, écrire dans l'autre, échanger.
   Une mise à jour en place rendrait le résultat dépendant de l'ordre de
   parcours.
5. **Ne jamais itérer une map Go pour quoi que ce soit affectant l'état.** Go
   randomise l'ordre d'itération. Les maps ne servent qu'à la consultation ; les
   murs, la nourriture et les fourmis sont toujours construits depuis des slices
   de configuration ordonnées.
6. **Ordre de balayage des directions fixe**, tout droit en premier, égalités
   résolues au plus petit indice. Cet ordre fait partie de la *définition* de la
   simulation, pas d'un détail d'implémentation — c'est aussi ce qui empêche les
   fourmis exploratrices de tourner en rond sur place.
7. **Aucun `time.Now()`, aucune variable d'environnement, aucun ordre
   d'achèvement de goroutine** dans la simulation. La graine est la seule source
   d'entropie.
8. **Les phases parallèles se découpent par plages d'indices fixes**, jamais par
   vol de travail.

### Le tick, en trois phases

```
A. SENSE   lecture seule de la grille précédente → intents[i]    (parallélisable)
B. COMMIT  strictement série, indice croissant : déplacement,
           collision, ramassage, dépôt au nid, phéromone         (série à jamais)
C. DECAY   évaporation + diffusion, puis échange du double tampon (parallélisable)
```

Le découpage existe dès v0 bien que v0 soit mono-thread. La phase B reste série
pour toujours : elle arbitre de vrais conflits (deux fourmis atteignant la
dernière unité de nourriture) et son résultat dépend légitimement de l'ordre des
fourmis.

---

## 5. Ajouter une optimisation

Suivre exactement cette procédure. Les étapes 1 et 6 sont celles qu'on saute, et
ce sont celles dont dépend la note.

1. **Profiler d'abord.** `make profile`, puis lire
   `bench/profiles/<engine>.cpu.top.txt`. Si vous ne pouvez pas pointer une
   ligne de ce fichier, vous n'avez pas encore d'optimisation justifiée.
2. **Copier** le meilleur moteur actuel vers `internal/engine/<nouveaunom>/`.
3. **Changer une seule chose.** Un levier par moteur, pour qu'un chiffre
   s'attribue à une cause.
4. `simcore.Register("<nouveaunom>", ...)` dans son `init()`, et blank-import
   depuis `internal/engine/register.go`.
5. **`make test`.** Le test golden doit passer pour le nouveau moteur. Un
   checksum différent signifie que vous avez changé la simulation, pas optimisé —
   la vitesse est sans objet tant que le checksum ne correspond pas.
6. **Mesurer et rédiger** : `make bench` (benchstat, avant/après) et
   `make hyper` (binaire complet), puis une nouvelle entrée dans
   `docs/journal/` contenant l'hypothèse, la commande, les chiffres et
   l'explication mécanique.

### Feuille de route — planifiée, PAS encore construite

Aucun étage d'optimisation n'existe encore : seuls `naive` (la baseline) et
`gradient` (une variante sémantique, voir §9) sont construits. Chaque étage
ci-dessous représente une séance de cours, réalisée profilage en main. Ne pas
les construire avant le profilage qui les justifie.

**Le barème note le cheminement, pas le nombre d'étages.** §3 exige 3 axes
(mémoire/localité, concurrence, I/O) et §4 un échec constructif — soit
**4 moteurs suffisent** si chacun est mesuré et expliqué correctement. Ajouter
des étages optionnels sans les documenter au même niveau de rigueur dilue la
note ; un chemin court et bien prouvé vaut mieux qu'une collection.

**Chemin retenu (couvre les 4 axes notés) :**

| Étage | Package | Changement | Axe du barème |
|---|---|---|---|
| v0 | `naive` | baseline | — |
| v1 | `flatgrid` | maps à clé chaîne → `[]uint32` plat, `idx = y*W+x` | 3a mémoire & localité |
| v4 | `parallel` | worker pool dimensionné aux **12 cœurs physiques** (banc actuel, §8), phases A et C en bandes de lignes — construit sur v1, la map interdit tout découpage propre | 3b concurrence |
| F1 | `failsharing` | **échec constructif planifié**, construit sur v4 : compteurs par worker non rembourrés partageant une ligne de 64 o, puis le rembourrage `[64]byte` qui corrige — publier les deux chiffres | §4, lignes de cache 64 o |
| v6 | `codec` | gob/protobuf vs JSON pour Result et le flux de snapshots ; éventuellement journal SQLite + index + `EXPLAIN QUERY PLAN` | 3c I/O |

**Optionnels — seulement si le chemin retenu est fini et prouvé avant la
deadline :**

| Étage | Package | Changement | Axe du barème |
|---|---|---|---|
| v2 | `soa` | `[]*Ant` → `[]Ant` → SoA ; alignement des champs, `int32`/`uint8` | 3a, ligne de cache 64 o |
| v3 | `nogc` | tampons préalloués, suppression de `Ant.Trail`, `sync.Pool` pour les snapshots ; assertion `allocs/op == 0` | 3a / 3c |
| v5 | `tuned` | compteurs atomiques, arrêt précoce, taille de bande calée sur L2 | 3b |

Chaque étage, retenu ou optionnel, suit exactement la procédure §5 (profiler
d'abord, un seul changement, `make test` avant de publier un chiffre) et le
gabarit de rédaction en tête de `docs/report/audit.md` §3.

---

## 6. Commandes

```bash
make all           # preuves complètes : env, test, bench, build, hyperfine, pprof, report
make test          # gate de correction uniquement
make bench         # go test -bench + benchstat (avant/après)
make profile       # profils pprof CPU + tas → bench/profiles/
make web           # interface du harnais sur http://localhost:8080
make run           # une exécution headless
```

`run_benchmarks.sh` est la source de vérité ; le Makefile ne fait que
l'envelopper, pour que l'exigence « une seule commande » tienne avec ou sans
`make` installé.

```bash
./bin/antsim.exe -config internal/config/scenarios/medium.json -engine naive \
                 -repeat 3 -cpuprofile cpu.pprof
go tool pprof -http=:8081 bench/profiles/naive.cpu.pprof   # flamegraph interactif
```

**Piège :** `cmd/antweb` embarque les scénarios via `go:embed`. Modifier un
scénario impose de reconstruire `antweb`, sinon l'UI continue de simuler
l'ancien.

### Prérequis

Go 1.23+ requis (construit et mesuré sur **go1.26.4 darwin/arm64**). Les
outils optionnels dégradent en étape ignorée avec un avertissement, jamais en
échec :

```bash
brew install hyperfine                                    # temps du binaire complet
go install golang.org/x/perf/cmd/benchstat@latest         # comparaison statistique
brew install graphviz                                      # callgraphs pprof -svg
```

---

## 7. Contraintes strictes

- **Bibliothèque standard uniquement** dans `simcore`, `config` et chaque
  moteur. Une mesure sans dépendance est une mesure défendable. Protobuf, quand
  l'étage v6 arrivera, sera confiné dans `internal/codec` et jamais importé par
  un moteur.
- **`cmd/antsim` ne doit jamais importer `net/http`** ni rien de ce que
  `uiserver` entraîne. `test/imports_test.go` le vérifie : l'initialisation HTTP
  dans le binaire mesuré corromprait tous les chiffres du rapport.
- **Ne jamais régénérer les fichiers golden pour faire passer un test en
  échec.** `make golden` accepte une *nouvelle définition* de la simulation.
  L'utiliser pour faire taire une optimisation cassée détruit la seule preuve
  que les optimisations étaient sûres.
- **Ne jamais publier une mesure depuis un arbre où `make test` échoue.**
  `run_benchmarks.sh` abandonne pour exactement cette raison.
- L'interface est **secondaire**. Elle ne doit jamais influencer la simulation :
  l'observateur est consulté une fois avant la boucle de tick, les envois de
  snapshots sont non bloquants et abandonnent si le tampon est plein, et
  `TestObserverDoesNotChangeOutcome` prouve que le résultat est inchangé.

---

## 8. Banc d'essai

Toutes les mesures de ce dépôt sont prises sur :

```
Apple M4 Pro — 12 cœurs physiques / 12 threads (pas de SMT)
L1 128 Ko icache + 64 Ko dcache (par cœur) · L2 4 Mo · L3 non exposé (SLC système)
24 Go RAM
macOS 27.0 (26A428) · go1.26.4 darwin/arm64
```

`bench/results/baseline.txt` est pinné sur cette machine : un moteur ne se
compare qu'à un autre moteur mesuré ici, jamais à un chiffre pris ailleurs.

Les tailles de scénario sont choisies en regard de cette hiérarchie. Une fois
l'étage v1 la grille devenue un `[]uint32` plat, une grille coûte
`Width*Height*4` octets :

| scénario | grille | par grille | tient dans |
|---|---|---|---|
| `small` | 64×64 | 16 Ko | L1/L2 |
| `medium` | 128×128 | 64 Ko | L2 |
| `large` | 256×256 | 256 Ko | quatre grilles dépassent L2, tiennent en L3 |

Le palier `medium` → `large` est l'endroit où le travail de localité doit
commencer à payer visiblement. Capturer la spécification machine avec
`make env` avant chaque session de mesure ; elle atterrit dans `docs/env/`.

---

## 9. Variantes de simulation

Un moteur qui change la **vitesse** doit reproduire `naive` bit pour bit : c'est
la prémisse de tout le rapport. Un moteur qui change ce que la simulation
**calcule** est une *variante*, et ne peut pas être tenu au checksum de `naive`
— mais le sortir du test golden le priverait de toute garantie de déterminisme.

- `simcore.Variant` est une interface **optionnelle**. Ne pas l'implémenter
  signifie « simulation de référence ». Ce défaut est porteur : un étage
  d'optimisation hérite de l'obligation de reproduire `naive` **en ne disant
  rien**, et ne peut donc pas y échapper par oubli. Un étage v1→v6 n'implémente
  jamais `Variant`.
- Chaque variante a son fichier de référence,
  `test/testdata/golden/<variante>/<scénario>.json`, et subit exactement le même
  test dans sa propre famille. `familyReference` (dans `test/golden_test.go`)
  nomme le moteur qui *définit* la vérité de chaque famille ; c'est une
  décision, écrite à la main, vérifiée par `TestGoldenFamilies`.
- L'UI groupe checksum de référence et base de speedup par variante : deux
  simulations différentes ne sont pas des alternatives l'une de l'autre, et
  annoncer l'une comme une accélération de l'autre n'aurait aucun sens.

`gradient` est la seule variante à ce jour. Elle corrige un défaut de
navigation de `naive` — les phéromones y encodent une densité de passage et non
une distance, de sorte qu'une fourmi chargée n'a aucun gradient exploitable pour
rentrer. Voir `docs/journal/03-moteur-gradient.md` pour les mesures, et le
commentaire de paquet de `internal/engine/gradient` pour le mécanisme.

Une variante n'est **pas** une échappatoire. Elle se justifie par une mesure qui
montre que le comportement de référence est cassé, jamais par une préférence.
