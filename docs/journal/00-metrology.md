# 00 — Métrologie & baseline v0

> Séance 1. Objectif : banc d'essai spécifié, protocole de mesure établi,
> temps de référence consigné. Correspond au §1 du barème (3 pts).

## Banc d'essai

Capture automatique : `make env` → `docs/env/<horodatage>.txt`.

| Élément | Valeur |
|---|---|
| CPU | AMD Ryzen 5 5600X, 6 cœurs physiques / 12 threads |
| Fréquence de base | 3701 MHz |
| Cache L1 | 384 Ko |
| Cache L2 | 3072 Ko (6 × 512 Ko, privé par cœur) |
| Cache L3 | 32768 Ko (partagé) |
| RAM | 16 Go DDR4, 3200 MHz (configuré 3200) |
| OS | Windows 11 Pro 10.0.26200 |
| Runtime | go1.27.1 windows/amd64, CGO_ENABLED=0, GOAMD64=v1 |

## Protocole

Deux instruments, deux questions distinctes — le rapport doit le dire
explicitement :

| Instrument | Question | Ce qu'il inclut |
|---|---|---|
| `go test -bench` (testing.B) | *Quelle instruction / allocation a diminué ?* | `ns/op`, `B/op`, `allocs/op`, courbe `-cpu`. Exclut le démarrage du process. |
| `hyperfine` | *Qu'attend réellement l'opérateur ?* | Process complet : démarrage, parsing de config, sortie JSON. Moyenne, médiane, écart-type. |

Paramètres :

- **Warmup** : `hyperfine --warmup 3` ; `testing.B` fait son propre rodage.
- **Itérations** : `-count 6` minimum (benchstat exige ≥ 6 échantillons pour un
  intervalle de confiance à 95 %) ; `hyperfine --runs 10`.
- **Deux politiques `-benchtime`** : `3x` pour les moteurs complets (plusieurs
  secondes par itération), `3s` pour les micro-benchmarks. Une valeur unique ne
  peut pas servir les deux échelles — à `b.N = 1`, un micro-benchmark rapporte
  la résolution de l'horloge (100 ns) et non le coût du code.
  La durée `3s` (et `-count 8`) est elle-même un résultat expérimental : à
  `1s`/`-count 6`, l'écart atteignait ± 30-43 % et produisait un classement
  physiquement impossible. Voir la note de métrologie dans
  [`01-profiling.md`](01-profiling.md).
- **Isolation du bruit** : applications fermées, machine sur secteur, plan
  d'alimentation relevé (`powercfg /getactivescheme`), `-trimpath` à la
  compilation. La variance résiduelle est rapportée, jamais masquée.
- **Significativité** : `benchstat` seul décide. Une exécution unique est une
  anecdote, pas une mesure.

## Invariant de correction

Toute optimisation doit préserver : **même config + même graine ⇒
`StateChecksum` identique**, sur tous les moteurs.

`run_benchmarks.sh` exécute `go test ./...` avant toute mesure et **abandonne**
en cas d'échec. Un moteur plus rapide qui calcule autre chose n'a pas été
optimisé, il a été cassé ; sa vitesse ne compte pas.

## Baseline v0 (`naive`)

Scénario `medium` : grille 128×128, 400 fourmis, 400 ticks, graine 20260921.

| Mesure | Valeur |
|---|---|
| Temps mur | **6,451 s ± 1 %** |
| Débit | **61,5 ticks/s** |
| Allocations | **66,23 M allocs/op** |
| Mémoire allouée | **399,1 Mio/op** |
| Nourriture collectée | 203 |
| Checksum | `0xd8184bf93c56fa26` |

Scénario `small` : 64×64, 120 fourmis, 200 ticks → **799,3 ms ± 1 %**,
8,244 M allocs/op, 41,60 Mio/op.

Source : `bench/results/baseline.txt` (`-count 6`, médianes `benchstat`).

### Scalabilité (`-cpu 1,2,6,12`)

| GOMAXPROCS | ns/op |
|---|---|
| 1 | 846 663 800 |
| 2 | 790 215 033 |
| 6 | 806 784 867 |
| 12 | 803 521 567 |

**Courbe plate** — écart total de 7 % entre 1 et 12 cœurs, sans tendance.
C'est le résultat attendu et il est utile : il établit formellement que v0 est
mono-thread. C'est la référence contre laquelle
l'étage v4 (worker pool) sera mesuré. Les écarts observés sont du bruit
d'ordonnancement, pas du parallélisme.

## Ce que la baseline est délibérément

`internal/engine/naive` est lent **par construction**. Chaque choix vise à
préparer un levier du cours :

| Choix naïf | Levier préparé |
|---|---|
| `map[string]uint32` + clé `fmt.Sprintf("%d,%d")` | grille plate `[]uint32`, `idx = y*W+x` |
| `[]*Ant` (chaînage de pointeurs) | AoS puis SoA |
| Champs `int64`, deux `bool` intercalés (~96 o) | alignement + `int32`/`uint8` |
| `Trail []string` en `append` à chaque tick | suppression, pression GC |
| Allocations par tick | tampons préalloués, zéro-allocation |
| Boucle de tick mono-thread | worker pool sur 6 cœurs physiques |

Ce qu'elle n'est **pas**, c'est incorrecte : PRNG par fourmi, virgule fixe,
double tampon et phases séparées sont présents dès v0, car ce sont précisément
les propriétés qui rendront chaque optimisation ultérieure *prouvablement*
équivalente.
