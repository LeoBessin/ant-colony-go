# 01 — Profilage & identification du Hot Path

> Correspond au §2 du barème (5 pts) : preuves empiriques et identification
> formelle du goulot d'étranglement initial.

Reproduction : `make profile` → `bench/profiles/naive.cpu.pprof`,
`naive.mem.pprof`, `naive.cpu.top.txt`, `naive.mem.top.txt`.
Flamegraph interactif : `go tool pprof -http=:8080 bench/profiles/naive.cpu.pprof`.

Cible profilée : scénario `medium` (128×128, 400 fourmis, 400 ticks),
durée 4,34 s, 3900 ms d'échantillons (89,84 %).

> Un profil CPU est **échantillonné** : deux captures du même binaire diffèrent
> de quelques points. Les blocs ci-dessous sont copiés tels quels des artefacts
> versionnés (`bench/profiles/naive.cpu.top.txt`, `naive.mem.top.txt`) pour
> être vérifiables. Les conclusions ne reposent que sur des écarts d'un ordre
> de grandeur, bien au-delà de cette dispersion.

## Profil CPU — `pprof -top`

```
      flat  flat%   sum%        cum   cum%
     400ms 10.26% 10.26%      580ms 14.87%  fmt.(*fmt).fmtInteger
     310ms  7.95% 18.21%     1240ms 31.79%  fmt.(*pp).doPrintf
     290ms  7.44% 25.64%      770ms 19.74%  runtime.mapaccess1_faststr
     280ms  7.18% 32.82%      280ms  7.18%  runtime.madvise
     200ms  5.13% 37.95%      200ms  5.13%  runtime.pthread_cond_signal
     180ms  4.62% 42.56%      180ms  4.62%  runtime.memmove
     170ms  4.36% 46.92%      170ms  4.36%  aeshashbody
     150ms  3.85% 50.77%      820ms 21.03%  fmt.(*pp).printArg
     150ms  3.85% 54.62%      300ms  7.69%  runtime.mallocgcTiny
      90ms  2.31% 56.92%      180ms  4.62%  fmt.(*fmt).pad
      90ms  2.31% 59.23%      670ms 17.18%  fmt.(*pp).fmtInteger
      90ms  2.31% 61.54%       90ms  2.31%  internal/runtime/maps.probeSeq.next (inline)
      90ms  2.31% 63.85%       90ms  2.31%  runtime.tryDeferToSpanScan
      80ms  2.05% 65.90%      510ms 13.08%  runtime.slicebytetostring
      70ms  1.79% 67.69%      110ms  2.82%  fmt.(*buffer).writeString (inline)
      70ms  1.79% 69.49%       70ms  1.79%  runtime.kevent
      70ms  1.79% 71.28%      150ms  3.85%  sync.(*Pool).Get
      60ms  1.54% 72.82%     2150ms 55.13%  fmt.Sprintf
      60ms  1.54% 74.36%      210ms  5.38%  fmt.newPrinter
      60ms  1.54% 75.90%       60ms  1.54%  internal/runtime/maps.(*groupReference).key (inline)
      60ms  1.54% 77.44%      390ms 10.00%  runtime.mallocgc
      50ms  1.28% 78.72%     2310ms 59.23%  antcolony/internal/engine/naive.(*World).level
      50ms  1.28% 80.00%      130ms  3.33%  fmt.(*pp).free
      50ms  1.28% 81.28%       50ms  1.28%  runtime.convT64
      50ms  1.28% 82.56%       80ms  2.05%  sync.(*Pool).Put
```

## Profil mémoire — `pprof -top -sample_index=alloc_objects`

```
      flat  flat%   sum%        cum   cum%
  25362815 99.84% 99.84%   25365629 99.85%  fmt.Sprintf
       116 0.00046% 99.84%   24841451 97.79%  naive.(*World).decayGrid
         0     0% 99.84%   25368971 99.87%  naive.(*Engine).Run
         0     0% 99.84%     425989  1.68%  naive.(*World).chooseMove
         0     0% 99.84%   24841451 97.79%  naive.(*World).decay
         0     0% 99.84%   19926060 78.44%  naive.(*World).level
         0     0% 99.84%     196610  0.77%  naive.(*World).passable
         0     0% 99.84%     425989  1.68%  naive.(*World).sense
         0     0% 99.84%   25336203 99.74%  naive.(*World).tick
         0     0% 99.84%   25365629 99.85%  naive.key (inline)
```

## Identification formelle du Hot Path

Le goulot est **l'adressage des cellules de la grille**, pas la logique de
simulation.

**`fmt.Sprintf` représente 55,13 % du temps CPU cumulé et 99,84 % des
allocations.** La règle du cours — *on ne devine jamais le Hot Path, on le
mesure* — est ici vérifiée dans le bon sens : l'intuition disait « le
déplacement des fourmis », la mesure dit « la construction de chaînes ».

Mécaniquement, `key(x, y)` coûte trois choses par appel :

1. **Formatage** — `doPrintf` → `printArg` → `fmtInteger` parcourt la chaîne de
   format et le slice d'arguments. ~32 % du CPU cumulé à lui seul.
2. **Allocation** — chaque clé est une `string` neuve sur le tas.
   `mallocgcTiny` à 7,7 % cumulé, et une pression GC qui suit les 25,4 M
   d'objets alloués sur ce seul profil.
3. **Accès map** — `mapaccess1_faststr` (19,74 % cum) hache la chaîne
   (`aeshashbody`), sonde le groupe de contrôle (`probeSeq.next`) puis
   déréférence le bucket. C'est une chaîne de dépendances avec défauts de cache,
   là où une grille plate ne demande qu'un `multiply-add` et un chargement
   indexé.

**Localisation précise :** 97,79 % des allocations passent par `decayGrid`, et
**78,44 % par `level`** — la lecture des 4 voisins.

### Ligne exacte — `pprof -list`

Le `-top` pointe des fonctions ; `pprof -list` pointe des lignes. Les
binaires de ce projet sont compilés `-trimpath`, donc `pprof` a besoin de
`-trim_path=antcolony/` pour retrouver les fichiers sources sur le disque :

```bash
go tool pprof -list 'naive\.key$' -trim_path=antcolony/ bench/profiles/naive.cpu.pprof
```

```
ROUTINE ======================== antcolony/internal/engine/naive.key in internal/engine/naive/naive.go
         0      2.20s (flat, cum) 56.41% of Total
         .      2.20s    105:func key(x, y int64) string { return fmt.Sprintf("%d,%d", x, y) }
```

**Une seule ligne — `naive.go:105` — consomme 56,41 % du temps CPU de tout le
programme.** Descente dans les deux appelants directs :

```
ROUTINE naive.(*World).level in internal/engine/naive/tick.go
      50ms      2.31s (flat, cum) 59.23% of Total
         .          .    231:func (w *World) level(cur, dep map[string]uint32, x, y int64) uint64 {
      20ms       20ms    232:	if x < 0 || y < 0 || x >= w.W || y >= w.H {
         .      1.70s    235:	k := key(x, y)
      30ms      590ms    236:	return uint64(cur[k]) + uint64(dep[k])

ROUTINE naive.(*World).decayGrid in internal/engine/naive/tick.go
      40ms      3.07s (flat, cum) 78.72% of Total
         .      470ms    204:	k := key(x, y)
         .      190ms    205:	v := uint64(cur[k]) + uint64(dep[k])
      20ms      690ms    207:	in := w.level(cur, dep, x, y-1) +
         .      500ms    208:		w.level(cur, dep, x, y+1) +
         .      590ms    209:		w.level(cur, dep, x-1, y) +
         .      550ms    210:		w.level(cur, dep, x+1, y)
         .       60ms    224:	next[k] = uint32(v)
```

Les quatre appels à `level()` (lignes 207-210, un par voisin) totalisent
2,33 s cumulés à eux seuls — 59,7 % du programme, pour une fonction qui ne
fait qu'un `if` et deux accès map.

Contrôle négatif — `chooseMove`, la logique de déplacement, n'apparaît quasi
pas :

```
ROUTINE naive.(*World).chooseMove in internal/engine/naive/tick.go
         0       40ms (flat, cum)  1.03% of Total
```

**1,03 % du CPU, 1,68 % des allocations.** Vérification arithmétique :
128 × 128 cellules × 5 adressages × 2 grilles × 400 ticks ≈ **65,5 M** appels
à `key()`, à comparer aux **66,3 M** allocations mesurées sur `Engine/medium`.
Le coût dominant est donc proportionnel à la **surface de la grille**, pas au
nombre de fourmis.

## Confirmation micro-benchmark

Les micro-benchmarks isolent le mécanisme, hors simulation
(`-benchtime 3s -count 8`, médianes `benchstat`, source `bench/results/baseline.txt`) :

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| `GridLookupMap` (128×128) | 56,00 ± 1 % | 6 | 1 |
| `KeySprintf` | 45,29 ± 3 % | 6 | 1 |
| `KeyStrconv` | 20,45 ± 1 % | 7 | 1 |
| `KeyFlatIndex` | **0,3161 ± 2 %** | 0 | 0 |
| `GridLookupFlat` | **0,3245 ± 1 %** | 0 | 0 |

**Rapport mesuré : 172,6× entre l'accès map et l'accès indexé**
(56,00 / 0,3245).

Décomposition, cohérente à la nanoseconde près :

- construction de la chaîne seule (`Strconv`) : **20,4 ns**
- surcoût de la machinerie `fmt` (`Sprintf` − `Strconv`) : **24,8 ns**, soit
  55 % du coût de la clé — parcours du format, `printArg` sur le slice
  d'arguments, réflexion
- accès map seul (`GridLookupMap` − `KeySprintf`) : **≈ 10,7 ns** — hachage,
  sondage du groupe de contrôle, déréférencement du bucket
- accès indexé (`GridLookupFlat`) : **0,32 ns**

Conclusion : accélérer le formatage ne sert à rien. Même en remplaçant
`Sprintf` par `strconv` — le meilleur cas avec une clé chaîne — il resterait
20,4 + 10,7 = 31,2 ns, soit **96× le coût de l'indexation**. Il faut supprimer la
chaîne, pas l'optimiser.

### Note de métrologie — la variance a failli produire un faux résultat

À `-benchtime 1s -count 6`, ces mêmes micro-benchmarks affichaient un écart de
**± 30 à 43 %**, et `GridLookupMap` ressortait *plus rapide* que le
`KeySprintf` qu'il contient pourtant — un ordre physiquement impossible. Il
s'agissait de bruit d'ordonnancement, pas d'un résultat.

À `-benchtime 3s -count 8`, l'écart retombe à **± 1-3 %** et l'ordre redevient
cohérent. Les valeurs par défaut du harnais ont été corrigées en conséquence.
Un benchmark dont l'écart-type dépasse l'effet recherché ne mesure rien :
c'est précisément ce que `benchstat` sert à détecter, et c'est la raison de
l'exigence `-count ≥ 6`.

### PRNG

| Benchmark | ns/op | allocs/op |
|---|---|---|
| `RngXoshiro` (Next) | 2,694 ± 1 % | 0 |
| `RngXoshiroIntn` | 2,889 ± 0 % | 0 |
| `RngMathRandLocal` | 2,830 ± 0 % | 0 |
| `RngMathRandGlobal` | 5,864 ± 1 % | 0 |

Sur cette machine, le PRNG maison et `math/rand` (source locale) sont
quasiment à égalité — l'écart entre `RngXoshiroIntn` et `RngMathRandLocal`
(2,889 ns vs 2,830 ns) est dans le bruit. `RngMathRandGlobal` reste ~2×
plus lent, à cause du mutex de la source globale partagée. Le PRNG maison
reste justifié pour le déterminisme — un générateur à état de valeur, au flux
binaire figé, est ce qui rend les fichiers golden stables — mais l'argument
de performance est marginal ici : le gain absolu est de toute façon
négligeable face à `Sprintf`.

## Conséquence — plan d'attaque

Loi d'Amdahl : `Sprintf` (55,13 %) + `mapaccess1_faststr` (19,74 %) ≈ 74,9 %
du temps cumulé. Le gain maximal atteignable en les supprimant est donc borné
à **1 / (1 − 0,749) ≈ 4,0×** sur cette fraction seule, avant que le prochain
goulot n'apparaisse.

**Étage suivant (v1 `flatgrid`) :** remplacer les quatre `map[string]uint32`
par des `[]uint32` plats indexés `y*W + x`. Hypothèse : `allocs/op` 66,3 M →
~0, `fmt.Sprintf` absent du top 25, et le prochain profil devrait faire
remonter soit `decayGrid` elle-même (bande passante mémoire), soit le coût
GC résiduel (`madvise`, `mallocgc`, déjà visibles à 7-10 % chacun dans ce
profil, potentiellement dominants une fois les allocations supprimées).

C'est cette dernière prédiction qu'il faudra vérifier, pas seulement le gain :
savoir *où le goulot se déplace* vaut autant que savoir de combien il recule.
