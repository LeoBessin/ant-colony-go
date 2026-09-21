# 01 — Profilage & identification du Hot Path

> Correspond au §2 du barème (5 pts) : preuves empiriques et identification
> formelle du goulot d'étranglement initial.

Reproduction : `make profile` → `bench/profiles/naive.cpu.pprof`,
`naive.mem.pprof`, `naive.cpu.top.txt`, `naive.mem.top.txt`.
Flamegraph interactif : `go tool pprof -http=:8081 bench/profiles/naive.cpu.pprof`.

Cible profilée : scénario `medium` (128×128, 400 fourmis, 400 ticks),
durée 6,52 s, 6790 ms d'échantillons.

> Un profil CPU est **échantillonné** : deux captures du même binaire diffèrent
> de quelques points. Les blocs ci-dessous sont copiés tels quels des artefacts
> versionnés (`bench/profiles/naive.cpu.top.txt`, `naive.mem.top.txt`) pour
> être vérifiables. Les conclusions ne reposent que sur des écarts d'un ordre
> de grandeur, bien au-delà de cette dispersion.

## Profil CPU — `pprof -top`

```
      flat  flat%   sum%        cum   cum%
     850ms 12.52% 12.52%     1380ms 20.32%  fmt.(*fmt).fmtInteger
     610ms  8.98% 21.50%     1530ms 22.53%  runtime.mapaccess2_faststr
     450ms  6.63% 28.13%     2500ms 36.82%  fmt.(*pp).doPrintf
     390ms  5.74% 33.87%      390ms  5.74%  internal/runtime/maps.ctrlGroup.matchH2
     330ms  4.86% 38.73%      330ms  4.86%  internal/runtime/maps.memHashAES
     330ms  4.86% 43.59%      440ms  6.48%  runtime.mallocgcTinySC2
     310ms  4.57% 48.16%     1900ms 27.98%  fmt.(*pp).printArg
     260ms  3.83% 51.99%      260ms  3.83%  runtime.memmove
     210ms  3.09% 55.08%     1590ms 23.42%  fmt.(*pp).fmtInteger
     200ms  2.95% 58.03%      370ms  5.45%  fmt.(*buffer).write
     190ms  2.80% 60.82%     4200ms 61.86%  fmt.Sprintf
```

## Profil mémoire — `pprof -top -sample_index=alloc_objects`

```
      flat  flat%   sum%        cum   cum%
  26612698 99.91% 99.91%   26618949 99.93%  fmt.Sprintf
       175 0.00066% 99.91%  26324208 98.83%  naive.(*World).decayGrid
         0     0% 99.91%    21536561 80.85%  naive.(*World).level
         0     0% 99.91%      294916  1.11%  naive.(*World).chooseMove
         0     0% 99.91%      163842  0.62%  naive.(*World).passable
```

## Identification formelle du Hot Path

Le goulot est **l'adressage des cellules de la grille**, pas la logique de
simulation.

**`fmt.Sprintf` représente 61,86 % du temps CPU cumulé et 99,91 % des
allocations.** La règle du cours — *on ne devine jamais le Hot Path, on le
mesure* — est ici vérifiée dans le bon sens : l'intuition disait « le
déplacement des fourmis », la mesure dit « la construction de chaînes ».

Mécaniquement, `key(x, y)` coûte trois choses par appel :

1. **Formatage** — `doPrintf` → `printArg` → `fmtInteger` parcourt la chaîne de
   format et le slice d'arguments. ~37 % du CPU cumulé à lui seul.
2. **Allocation** — chaque clé est une `string` neuve sur le tas.
   `mallocgcTinySC2` à 6,5 % flat, et une pression GC qui suit les 66 M
   d'objets par exécution.
3. **Accès map** — `mapaccess2_faststr` (22,53 % cum) hache la chaîne
   (`memHashAES`), sonde le groupe de contrôle (`ctrlGroup.matchH2`) puis
   déréférence le bucket. C'est une chaîne de dépendances avec défauts de cache,
   là où une grille plate ne demande qu'un `multiply-add` et un chargement
   indexé.

**Localisation précise :** 98,83 % des allocations passent par `decayGrid`, et
**80,85 % par `level`** — la lecture des 4 voisins. C'est arithmétiquement
cohérent : la phase C visite `W*H` cellules et effectue ~5 adressages par
cellule et par grille (la cellule + 4 voisins), sur 2 grilles.
Soit 128 × 128 × 5 × 2 × 400 ≈ **65,5 M** appels à `key()` — ce qui correspond
aux 66,2 M d'allocations mesurées.

Autrement dit : **le coût dominant est proportionnel à la surface de la grille,
pas au nombre de fourmis.** `chooseMove` ne pèse que 1,11 % des allocations.

## Confirmation micro-benchmark

Les micro-benchmarks isolent le mécanisme, hors simulation
(`-benchtime 3s -count 8`, médianes `benchstat`, source `bench/results/baseline.txt`) :

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| `GridLookupMap` (128×128) | 88,34 ± 0 % | 6 | 1 |
| `KeySprintf` | 70,98 ± 1 % | 6 | 1 |
| `KeyStrconv` | 29,08 ± 1 % | 7 | 1 |
| `KeyFlatIndex` | **0,4502 ± 1 %** | 0 | 0 |
| `GridLookupFlat` (128×128) | **0,4484 ± 1 %** | 0 | 0 |

**Rapport mesuré : 197× entre l'accès map et l'accès indexé**
(88,34 / 0,4484) — un ordre de grandeur de 10².

Décomposition, cohérente à la nanoseconde près :

- construction de la chaîne seule (`Strconv`) : **29,1 ns**
- surcoût de la machinerie `fmt` (`Sprintf` − `Strconv`) : **41,9 ns**, soit
  59 % du coût de la clé — parcours du format, `printArg` sur le slice
  d'arguments, réflexion
- accès map seul (`GridLookupMap` − `KeySprintf`) : **≈ 17,4 ns** — hachage,
  sondage du groupe de contrôle, déréférencement du bucket
- accès indexé (`GridLookupFlat`) : **0,45 ns**, soit moins de 2 cycles

Conclusion : accélérer le formatage ne sert à rien. Même en remplaçant
`Sprintf` par `strconv` — le meilleur cas avec une clé chaîne — il resterait
29,1 + 17,4 = 46,5 ns, soit **104× le coût de l'indexation**. Il faut supprimer la
chaîne, pas l'optimiser.

### Note de métrologie — la variance a failli produire un faux résultat

À `-benchtime 1s -count 6`, ces mêmes micro-benchmarks affichaient un écart de
**± 30 à 43 %**, et `GridLookupMap` (89,96 ns) ressortait *plus rapide* que le
`KeySprintf` (164,3 ns) qu'il contient pourtant — un ordre physiquement
impossible. Il s'agissait de bruit d'ordonnancement Windows sur des mesures
trop courtes, pas d'un résultat.

À `-benchtime 3s -count 8`, l'écart retombe à **± 1-2 %** et l'ordre redevient
cohérent. Les valeurs par défaut du harnais ont été corrigées en conséquence.
Un benchmark dont l'écart-type dépasse l'effet recherché ne mesure rien :
c'est précisément ce que `benchstat` sert à détecter, et c'est la raison de
l'exigence `-count ≥ 6`.

### PRNG

Justification du xoshiro256\*\* maison plutôt que `math/rand`, tiré 2× par
fourmi et par tick, donc sur le chemin critique :

| Benchmark | ns/op | allocs/op |
|---|---|---|
| `RngXoshiro` (Next) | 1,784 ± 1 % | 0 |
| `RngXoshiroIntn` | 2,749 ± 1 % | 0 |
| `RngMathRandLocal` | 4,824 ± 0 % | 0 |
| `RngMathRandGlobal` | 8,837 ± 0 % | 0 |

`rand.Rand` encapsule une *interface* `Source` : un appel dynamique par tirage,
non inlinable. La version globale ajoute un mutex — d'où le facteur 3,2×. Le
gain absolu est modeste face à `Sprintf`, mais le choix est aussi structurel :
un générateur à état de valeur, au flux binaire figé, est ce qui rend les
fichiers golden stables d'une version de Go à l'autre.

## Conséquence — plan d'attaque

Loi d'Amdahl : `Sprintf` + accès map pèsent ≈ 84 % du temps cumulé. Le gain
maximal atteignable en les supprimant est donc borné à ≈ 6,3× sur cette
fraction seule, avant que le prochain goulot n'apparaisse.

**Étage suivant (v1 `flatgrid`) :** remplacer les quatre `map[string]uint32`
par des `[]uint32` plats indexés `y*W + x`. Hypothèse : `allocs/op` 66 M → ~0,
`fmt.Sprintf` absent du top 25, et le prochain profil devrait faire remonter
soit `decayGrid` elle-même (bande passante mémoire), soit `chooseMove`.

C'est cette dernière prédiction qu'il faudra vérifier, pas seulement le gain :
savoir *où le goulot se déplace* vaut autant que savoir de combien il recule.
