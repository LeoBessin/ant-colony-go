# Rapport d'audit de performance — Simulation de colonie de fourmis

**Sup de Vinci — RNCP Bloc 4 · Optimisations & Performances Backend**
Auteur : Leo Bessin · Langage : Go · Session E42

> **État : squelette.** Les sections 1 et 2 sont renseignées avec les mesures
> réelles de la séance 1. Les sections 3 à 5 se remplissent au fil du cours,
> une entrée de `docs/journal/` par levier.
>
> Les tableaux générés automatiquement sont dans
> `docs/report/generated-tables.md` (`make report`). Ne pas les recopier à la
> main : les régénérer.

---

## 0. Objet et périmètre

Simulation discrète d'une colonie de fourmis sur grille : fourmis, nourriture,
murs, et deux champs de phéromones (piste *food* et piste *home*). Le
programme prend une configuration JSON en entrée et produit un résultat
déterministe en sortie, ce qui rend chaque version mesurable sans interaction.

**Invariant de correction.** Même configuration + même graine ⇒ `StateChecksum`
identique, sur tous les moteurs. Toute optimisation qui change ce checksum n'est
pas une optimisation : c'est une régression fonctionnelle, et sa vitesse n'est
pas reportée.

**Baseline délibérément naïve.** Le moteur v0 (`internal/engine/naive`) utilise
des `map[string]` avec clés `fmt.Sprintf`, un slice de pointeurs et des
allocations par tick. Ce choix est assumé et documenté : il fournit une marge
de progression réelle, et chaque structure naïve prépare un levier précis du
cours. Ce qui est préservé dès v0 — PRNG par fourmi, arithmétique entière en
virgule fixe, double tampon, phases séparées — l'est parce que ce sont les
propriétés qui rendront les optimisations ultérieures *prouvablement*
équivalentes.

---

## 1. Environnement & métrologie (§1 — 3 pts)

→ source : [`docs/journal/00-metrology.md`](../journal/00-metrology.md)

### 1.1 Banc d'essai

| Élément | Valeur |
|---|---|
| CPU | AMD Ryzen 5 5600X — 6 cœurs physiques / 12 threads |
| Fréquence de base | 3701 MHz |
| L1 | 384 Ko |
| L2 | 3072 Ko (6 × 512 Ko, privé par cœur) |
| L3 | 32768 Ko (partagé) |
| RAM | 16 Go DDR4-3200 |
| OS | Windows 11 Pro 10.0.26200 |
| Runtime | go1.27.1 windows/amd64 · CGO_ENABLED=0 · GOAMD64=v1 |

Capture automatique et horodatée : `make env` → `docs/env/`.

### 1.2 Protocole

| Instrument | Question à laquelle il répond |
|---|---|
| `go test -bench` | Quelle instruction / allocation a diminué ? (`ns/op`, `B/op`, `allocs/op`, courbe `-cpu`) |
| `hyperfine` | Qu'attend réellement l'opérateur ? (process complet, moyenne / médiane / écart-type) |

- Warmup : `hyperfine --warmup 3` ; `testing.B` fait son propre rodage.
- Itérations : `-count 6` minimum (seuil `benchstat` pour un IC à 95 %) ;
  `hyperfine --runs 10`.
- Deux politiques `-benchtime` : `3x` pour les moteurs complets, `3s` pour les
  micro-benchmarks (`-count 8`). Une valeur unique ne peut pas servir les deux
  échelles ; voir la note de métrologie en 2.2.
- Isolation : applications fermées, secteur, plan d'alimentation relevé,
  `-trimpath`.
- Significativité : arbitrée par `benchstat`, jamais par une exécution unique.

### 1.3 Dimensionnement des scénarios vis-à-vis des caches

Une fois la grille aplatie en `[]uint32` (étage v1), une grille coûte
`W × H × 4` octets :

| Scénario | Grille | Par grille | Niveau |
|---|---|---|---|
| `small` | 64×64 | 16 Ko | L1/L2 |
| `medium` | 128×128 | 64 Ko | L2 |
| `large` | 256×256 | 256 Ko | 4 tampons dépassent L2, tiennent en L3 |

Le palier `medium` → `large` est l'endroit où le travail de localité doit
devenir visible.

---

## 2. Diagnostic matériel & profilage (§2 — 5 pts)

→ source : [`docs/journal/01-profiling.md`](../journal/01-profiling.md)
→ artefacts : `bench/profiles/naive.cpu.pprof`, `naive.mem.pprof`, `*.top.txt`

### 2.1 Hot Path identifié

Le goulot est **l'adressage des cellules**, pas la logique de simulation.

- `fmt.Sprintf` : **61,86 % du CPU cumulé**, **99,91 % des allocations**
- `runtime.mapaccess2_faststr` : 22,53 % cumulé
- `runtime.mallocgcTinySC2` : 6,48 % flat
- 98,83 % des allocations traversent `decayGrid`, dont **80,85 % via `level`**
  (lecture des 4 voisins)

Vérification arithmétique : 128 × 128 cellules × 5 adressages × 2 grilles ×
400 ticks ≈ **65,5 M** appels à `key()`, à comparer aux **66,2 M** allocations
mesurées. Le coût dominant est donc proportionnel à la **surface de la grille**,
pas au nombre de fourmis (`chooseMove` ne pèse que 1,11 % des allocations).

### 2.2 Confirmation micro-benchmark

`-benchtime 3s -count 8`, médianes `benchstat` :

| Benchmark | ns/op | allocs/op |
|---|---|---|
| `GridLookupMap` (128×128) | 88,34 ± 0 % | 1 |
| `KeySprintf` | 70,98 ± 1 % | 1 |
| `KeyStrconv` | 29,08 ± 1 % | 1 |
| `GridLookupFlat` (128×128) | **0,4484 ± 1 %** | 0 |

**Rapport mesuré : 197×.** Décomposition : 29,1 ns de construction de chaîne,
41,9 ns de machinerie `fmt`, ≈ 17,4 ns d'accès map, contre 0,45 ns pour un
accès indexé. Même le meilleur cas avec clé chaîne (`strconv`) resterait à
104× l'indexation : il faut supprimer la chaîne, pas l'optimiser.

> **Métrologie.** Aux réglages initiaux (`1s`, `-count 6`) ces mesures
> affichaient ± 30-43 % d'écart et `GridLookupMap` ressortait *plus rapide* que
> le `KeySprintf` qu'il contient — un ordre impossible, donc du bruit. Les
> réglages par défaut ont été portés à `3s`/`-count 8`, ramenant l'écart à
> ± 1-2 %. Un benchmark dont la variance dépasse l'effet mesuré ne mesure rien.

### 2.3 Borne d'Amdahl

`Sprintf` + accès map ≈ 84 % du temps cumulé ⇒ le gain maximal atteignable en
les supprimant est borné à ≈ **6,3×** sur cette fraction, avant apparition du
goulot suivant. Cette borne est à confronter au gain réellement obtenu en v1.

---

## 3. Journal d'optimisation (§3 — 5 pts)

### 3.1 Mémoire & localité de cache

- [ ] v1 `flatgrid` — `map[string]uint32` → `[]uint32`, `idx = y*W+x`
- [ ] v2 `soa` — `[]*Ant` → AoS → SoA ; alignement des champs, `int32`/`uint8`
- [ ] v3 `nogc` — tampons préalloués, suppression de `Ant.Trail`, zéro allocation

### 3.2 Concurrence & scalabilité CPU

- [ ] v4 `parallel` — worker pool dimensionné aux 6 cœurs **physiques**,
      phases A et C découpées en bandes de lignes
- [ ] v5 `tuned` — compteurs atomiques, arrêt précoce, taille de bande calée sur L2

Référence : la courbe `-cpu 1,2,6,12` de v0 est **plate** (847 / 790 / 807 /
804 ms, écart 7 % sans tendance). C'est la preuve formelle que v0 est
mono-thread.

### 3.3 I/O réseau & persistance

- [ ] v6 `codec` — gob / Protobuf vs JSON ; `sync.Pool` sur les snapshots
- [ ] SQLite : journal des exécutions, `EXPLAIN QUERY PLAN`, index sur
      `(config_hash, engine)`

---

## 4. Confrontation critique & « échec constructif » (§4 — 3 pts)

- [ ] F1 `failsharing` — **faux partage** : compteurs par worker non rembourrés
      dans une même ligne de 64 octets, puis le rembourrage `[64]byte` qui
      corrige. Publier les deux chiffres et l'explication mécanique.

Candidat retenu parce qu'il est mesurable, explicable en termes matériels, et
qu'il porte directement sur le contenu « lignes de cache 64 octets » du cours.

---

## 5. Reproductibilité & synthèse (§5 — 4 pts)

### 5.1 Commande unique

```bash
bash run_benchmarks.sh
```

Étapes : `env` → `test` (**gate de correction, abandon si échec**) → `bench` +
`benchstat` → `build` → `hyperfine` → `pprof` → `report`.

Aucun chiffre n'est publié depuis un arbre où `go test ./...` échoue.

### 5.2 Tableau comparatif final

_À compléter. Source : `docs/report/generated-tables.md`._

| Étage | ns/op (`medium`) | allocs/op | Gain cumulé |
|---|---|---|---|
| v0 `naive` | 6,451 s ± 1 % | 66,23 M | 1,00× |
| v1 `flatgrid` | — | — | — |
| … | | | |

---

## 6. Bonus — gouvernance IA (§6 — +2 pts)

→ [`constitution.md`](../../constitution.md)

Les quatre directives : rôle système strict (ingénieur contraint par des
métriques physiques réelles) ; contraintes négatives explicites (`fmt.Sprintf`,
maps, flottants, allocations et goroutines non bornées interdits sur le Hot
Path) ; justification empirique obligatoire sous la forme « hypothèse d'impact
matériel / commande de vérification » ; formulation compacte et impérative.
