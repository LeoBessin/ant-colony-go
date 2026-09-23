# 00 — Métrologie & baseline v0

> Séance 1. Objectif : banc d'essai spécifié, protocole de mesure établi,
> temps de référence consigné. Correspond au §1 du barème (3 pts).

## Banc d'essai

Capture automatique : `make env` → `docs/env/<horodatage>.txt` (dernière :
[`docs/env/2026-09-22_164722.txt`](../env/2026-09-22_164722.txt)).

| Élément | Valeur |
|---|---|
| CPU | Apple M4 Pro, 12 cœurs physiques / 12 threads (pas de SMT) |
| Cache L1 | 128 Ko icache + 64 Ko dcache, par cœur |
| Cache L2 | 4096 Ko |
| Cache L3 | non exposé (Apple Silicon utilise un SLC système, pas de L3 classique) |
| RAM | 24 Go |
| OS | macOS 27.0 (build 26A428) |
| Runtime | go1.26.4 darwin/arm64, CGO_ENABLED=0 |

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
  d'alimentation relevé (`pmset -g`), `-trimpath` à la compilation. La
  variance résiduelle est rapportée, jamais masquée.
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
| Temps mur | **4,128 s ± 1 %** |
| Débit | **96,03 ticks/s ± 2 %** |
| Allocations | **66,30 M allocs/op** |
| Mémoire allouée | **397,8 Mio/op** |
| Nourriture collectée | 288 |
| Checksum | `0xa6b0a8c6451d55e4` |

Scénario `small` : 64×64, 120 fourmis, 200 ticks → **490,0 ms ± 5 %**,
8,266 M allocs/op, 41,49 Mio/op, nourriture collectée 127,
checksum `0x3bd2e12c9b6e9b22`.

Source : `bench/results/baseline.txt` (`-count 6`, médianes `benchstat`) pour
les temps/allocations ; `./bin/antsim.exe -config … -engine naive` (sortie
JSON directe) pour la nourriture et le checksum, qui ne sont pas dans
`testing.B`.

### Scalabilité (`-cpu 1,2,4,8,12`)

| GOMAXPROCS | sec/op |
|---|---|
| 1 | 566,4m ± 2 % |
| 2 | 512,6m ± 1 % |
| 4 | 511,9m ± 1 % |
| 8 | 513,6m ± 2 % |
| 12 | 505,3m ± 3 % |

**Courbe plate** — 566 → 505 ms, moins de 12 % d'écart total, sans tendance
monotone au-delà du bruit. C'est le résultat attendu et il est utile : il
établit formellement que v0 est mono-thread, quel que soit `GOMAXPROCS`.
C'est la référence contre laquelle l'étage v4 (worker pool, 12 cœurs
physiques) sera mesuré. Source :
[`scaling_2026-09-22_164722.txt`](../../bench/results/scaling_2026-09-22_164722.txt).

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
| Boucle de tick mono-thread | worker pool sur 12 cœurs physiques |

Ce qu'elle n'est **pas**, c'est incorrecte : PRNG par fourmi, virgule fixe,
double tampon et phases séparées sont présents dès v0, car ce sont précisément
les propriétés qui rendront chaque optimisation ultérieure *prouvablement*
équivalente.
