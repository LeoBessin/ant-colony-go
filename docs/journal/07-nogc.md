# 07 — v3 `nogc` : suppression de `Ant.Trail`

> Troisième étage construit. Axe 3a (mémoire) et, par ricochet, 3b : c'est la
> fraction série de v4 qui est réduite. Construit sur v4 `parallel`, et non
> sur v2 comme le dessinait la feuille de route initiale : les étages sont pris
> dans l'ordre où les profils les justifient, et c'est le profil de v4 qui a
> fait de `Trail` le problème.

Date : 2026-09-23. Nouveau paquet : `internal/engine/nogc`.

> ⚠️ **Brouillon mesuré sur la machine de développement** (AMD Ryzen AI 7 350,
> 8 cœurs / 16 threads, Linux), pas sur le banc de référence M4 Pro de
> CLAUDE.md §8. Même réserve que `06-parallel.md` : ratios valables entre eux,
> à re-mesurer avant d'être cités dans l'audit.

## 1. Hypothèse — ce que les profils montrent

Deux symptômes sur v4 `parallel`, `large` :

- **Mémoire.** Sur une exécution de 6 000 ticks, la RSS croît linéairement
  (≈ 120 Mo/s) jusqu'à **484 Mo**. Le profil de tas (`alloc_space`) attribue
  **99 %** des octets à `Trail` : 650 Mo de croissance de slice (`append`
  dans `commit`) et 128 Mo de chaînes `"x,y"` (`trailKey` +
  `strconv.FormatInt`). Tout est retenu jusqu'à la fin du `Run`, et **rien ne
  lit jamais ce champ**.
- **CPU.** Une fois les phases A et C parallèles, `commit` (série) représente
  ≈ 36 % du temps mural (`06-parallel.md` §5), et l'append de `Trail` est sa
  seule allocation. Le GC (`gcBgMarkWorker`, 4,8 % du CPU) prend en plus du
  temps CPU aux workers.

**Hypothèse** : supprimer `Trail` fait tomber les allocations par tick à zéro,
borne la mémoire, et réduit la fraction série d'Amdahl. Le gain doit donc être
**plus grand avec plusieurs workers qu'avec un seul**.

## 2. Changement, un seul

Le champ `Ant.Trail []string` et la ligne qui l'alimente dans `commit` sont
supprimés, ainsi que `trailKey()` et l'import `strconv`, devenus inutiles.
Tout le reste est copié verbatim de `parallel`.

`Trail` ne fait pas partie du résultat : `result()` hache `ID`, `X`, `Y`,
`Dir` et `HasFood`, jamais `Trail`, et `snapshot()` ne le lit pas. Le
checksum ne peut donc pas bouger, et c'est `make test` qui le prouve.

Fichiers : `internal/engine/nogc/{nogc,tick,pool,output}.go`.

## 3. Correction

- **`make test` : PASS.** Checksum identique à `naive` sur `tiny`/`small`.
  Vérifié en plus hors golden sur `large` : `state_checksum` identique entre
  `parallel` et `nogc`.
- `TestParallelIndependentOfWorkerCount` couvre désormais aussi `nogc`
  (GOMAXPROCS = 1, 2, 3, 7, 16).
- **`TestNogcAllocationsDoNotGrowWithTicks`** (nouveau, `test/nogc_test.go`) :
  multiplier les ticks par 10 ne doit pas augmenter les allocations. Mesuré :
  1 allocation à 200 ticks, 1 à 2 000. Pour vérifier que le test mord : sur
  `parallel`, le même scénario passe de 4 180 à 40 261 allocations, bien
  au-delà de la tolérance (180).
- `go test -race` : aucune course.

## 4. Mesures

### Allocations et mémoire (`large`, 300 ticks sauf mention)

| Métrique | `parallel` | `nogc` | Gain |
|---|---:|---:|---:|
| `allocs` (Result) | 1 506 711 | **48** | ≈ 31 000× |
| `heap_bytes` (TotalAlloc) | 26 116 584 | **42 560** | ≈ 614× |
| RSS max, 6 000 ticks | 484 Mo | **11 Mo** | ≈ 44× |

Les 48 allocations restantes ne dépendent pas du nombre de ticks
(`TestNogcAllocationsDoNotGrowWithTicks`) : ce sont des coûts fixes par `Run`.

### Temps (hyperfine, binaire complet, 3 warmups, 15 runs)

```bash
hyperfine -N -w 3 -r 15 -L e flatgrid,parallel,nogc -L p 1,8,16 \
  "./bin/antsim.exe -quiet -config internal/config/scenarios/large.json -engine {e} -gomaxprocs {p}"
```

`large` (256×256) :

| workers | `flatgrid` | `parallel` | `nogc` | `nogc` vs `parallel` | `nogc` vs `flatgrid` |
|---:|---:|---:|---:|---:|---:|
| 1 | 543,1 ± 6,3 ms | 545,5 ± 8,4 ms | 475,3 ± 5,2 ms | 1,15× | 1,14× |
| 8 | 539,2 ± 6,8 ms | 215,8 ± 12,8 ms | 140,9 ± 5,2 ms | **1,53×** | 3,83× |
| 16 | 547,2 ± 11,8 ms | 196,9 ± 14,3 ms | **125,0 ± 2,9 ms** | **1,58×** | **4,38×** |

`medium` (128×128) :

| workers | `flatgrid` | `parallel` | `nogc` | `nogc` vs `parallel` |
|---:|---:|---:|---:|---:|
| 1 | 180,4 ± 4,0 ms | 178,4 ± 2,8 ms | 169,3 ± 5,5 ms | 1,05× |
| 8 | 180,9 ± 6,3 ms | 86,6 ± 4,9 ms | 63,6 ± 1,5 ms | 1,36× |
| 16 | 180,2 ± 3,7 ms | 86,6 ± 4,0 ms | **62,1 ± 1,0 ms** | 1,39× |

### Profil CPU après (`nogc`, `large`, 16 workers, 10 répétitions)

| Fonction | `parallel` (8 w.) | `nogc` (16 w.) |
|---|---:|---:|
| `commit` (série) | 0,83 s | **0,08 s** |
| `gcBgMarkWorker` | 0,39 s (4,8 %) | absent |
| `decayGrid` cum | 76,9 % | **83,9 %** |
| Utilisation CPU moyenne | 349 % | **701 %** |

## 5. Explication mécanique

1. **Le gain est bien plus grand en parallèle qu'en série** : 1,15× à un
   worker, 1,58× à 16. C'était la prédiction du §1. En série, supprimer
   `Trail` ne retire que le coût de l'append et du GC. En parallèle, c'est de
   la **fraction série d'Amdahl** qu'on retire du temps : `commit` passe de
   0,83 s à 0,08 s de CPU, et c'est pendant ce temps que les 16 workers
   attendaient sans rien faire.
2. **Les workers sont nettement plus occupés** : l'utilisation CPU double
   (349 % → 701 %), soit en moyenne 7 threads au travail sur 16, contre
   3,5 avant.
3. **Moins de variance.** L'écart-type passe de ± 14,3 ms à ± 2,9 ms sur
   `large` à 16 workers. Sans allocation, plus de cycle GC aux moments
   imprévisibles, qui interrompait les workers au milieu d'une phase.
4. **La mémoire devient constante.** La taille du monde ne dépend plus du
   nombre de ticks, seulement de la grille et du nombre de fourmis. C'est
   aussi ce qui corrige la croissance de RAM observée dans l'interface web
   sur les longues exécutions.

## 6. Nouveau hot path et pistes

`decayGrid` + `level` redeviennent le goulot (≈ 84 % du CPU), mais désormais
réparti sur tous les workers. Signes de ce qui reste à gagner :

- `decayGrid` consomme 8,08 s de CPU à 16 workers, contre 4,38 s en série
  pour le même travail. C'est l'effet SMT et turbo déjà vu dans
  `06-parallel.md` §5, en plus marqué avec 16 threads sur 8 cœurs.
- `runtime.memclrNoHeapPointers` (2,5 %) : ce sont les `clear()` série de fin
  de phase C, désormais visibles dans le profil.
- `runtime.futex` (2,6 %) : le coût des barrières (réveil des workers).

Prochaine étape du chemin retenu : **F1 `failsharing`** (§4 du barème),
construite sur ce moteur.
