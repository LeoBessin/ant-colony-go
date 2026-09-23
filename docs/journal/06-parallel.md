# 06 — v4 `parallel` : worker pool sur les phases A et C

> Deuxième étage du chemin retenu (CLAUDE.md §5). Axe 3b (concurrence &
> scalabilité CPU). Construit sur v1 `flatgrid`.

Date : 2026-09-23. Nouveau paquet : `internal/engine/parallel`.

> ⚠️ **Brouillon mesuré sur la machine de développement, pas sur le banc de
> référence.** Tous les chiffres ci-dessous viennent d'un **AMD Ryzen AI 7 350
> (8 cœurs / 16 threads, Linux)**, alors que le banc pinné de CLAUDE.md §8 et
> de l'audit §1.1 est l'Apple M4 Pro. Les ratios `flatgrid` → `parallel` sont
> cohérents entre eux (même machine, même session), mais **ne doivent pas être
> comparés** aux chiffres de `04-flatgrid.md`. À re-mesurer sur le banc de
> référence avant de citer dans l'audit.

## 1. Hypothèse — ce que le profil montre

Profil CPU de `flatgrid` sur `large` (256×256, 2 000 fourmis, 300 ticks,
10 répétitions, 5,80 s d'échantillons) :

| Fonction | cum % | Phase | Parallélisable ? |
|---|---:|---|---|
| `flatgrid.(*World).decayGrid` | **75,5 %** | C | oui, par bandes de lignes (double tampon) |
| `flatgrid.(*World).commit` | 10,2 % | B | **non**, série à jamais (CLAUDE.md §4) |
| `flatgrid.(*World).sense` | 4,8 % | A | oui, par plages de fourmis (lecture seule) |

Fraction parallélisable p ≈ 0,803. Borne d'Amdahl S(n) = 1 / (0,197 + 0,803/n) :
**1,67× à 2 workers, 2,51× à 4, 3,36× à 8**.

**Hypothèse matérielle** : `decayGrid` est un stencil à 4 voisins sur deux
grilles de 256 Ko. Chaque cellule ne dépend que des tampons du tick
*précédent*, donc N bandes de lignes sont indépendantes et peuvent tourner sur
N cœurs sans verrou. La phase A est en lecture seule sur la grille.

## 2. Changement, un seul

Les phases A et C sont distribuées à un **pool fixe de goroutines**, démarré
une fois par `Run` :

- worker k traite toujours les fourmis `[k·N/n, (k+1)·N/n)` en phase A et les
  lignes `[k·H/n, (k+1)·H/n)` des deux grilles en phase C (`span()` dans
  `pool.go`) ;
- **un canal par worker**, pas un canal partagé : le découpage est par plages
  d'indices fixes, jamais par vol de travail (CLAUDE.md §4 règle 8) ;
- un `sync.WaitGroup` sert de barrière en fin de phase. L'envoi sur le canal
  et le `Wait` sont les relations *happens-before* entre phases ;
- n = `runtime.GOMAXPROCS(0)`, borné à H. `go test -cpu` et
  `antsim -gomaxprocs` font donc varier le nombre de workers sans modifier le
  code, et ce nombre n'intervient jamais dans le résultat.

Inchangés : `commit` (série, indice croissant), les deux tirages PRNG, la
virgule fixe, le double tampon, et les `clear()` de fin de phase C. Ces
derniers restent en série : une bande voisine peut encore lire `dep` tant que
tous les workers n'ont pas fini, donc les paralléliser demanderait une
troisième barrière par tick, ce qui serait un autre levier.

Fichiers : `internal/engine/parallel/{parallel,tick,pool,output}.go`.

## 3. Correction

- **`make test` : PASS.** Golden `tiny`/`small` identique à `naive`
  (famille par défaut, pas de `Variant`).
- **`TestParallelIndependentOfWorkerCount`** (nouveau, `test/parallel_test.go`) :
  même checksum que `flatgrid` avec GOMAXPROCS = 1, 2, 3, 7 et 16 sur `tiny`,
  `small` et `medium`. Les valeurs impaires placent les bords de bande sur
  d'autres lignes : c'est là qu'un décalage d'indice dans `span()` ou une
  écriture hors bande apparaîtrait.
- **`go test -race`** sur ce test et sur `TestRepeatable` : aucune course
  détectée.

## 4. Mesures (hyperfine, binaire complet, Ryzen AI 7 350)

```bash
go build -o bin/antsim.exe ./cmd/antsim
hyperfine -N -w 3 -r 15 \
  "./bin/antsim.exe -quiet -config internal/config/scenarios/large.json -engine flatgrid" \
  -L p 1,2,4,8,16 \
  "./bin/antsim.exe -quiet -config internal/config/scenarios/large.json -engine parallel -gomaxprocs {p}"
```

### `large` (256×256)

| Moteur | workers | Moyenne ± σ | Speedup vs `flatgrid` | Amdahl |
|---|---:|---:|---:|---:|
| `flatgrid` | — | 530,7 ± 5,4 ms | 1,00× | — |
| `parallel` | 1 | 544,6 ± 8,6 ms | **0,97×** | 1,00× |
| `parallel` | 2 | 362,2 ± 18,8 ms | 1,47× | 1,67× |
| `parallel` | 4 | 245,3 ± 9,2 ms | 2,16× | 2,51× |
| `parallel` | 8 | 217,6 ± 14,5 ms | 2,44× | 3,36× |
| `parallel` | 16 (SMT) | 204,6 ± 19,5 ms | **2,59×** | 4,05× |

### `medium` (128×128)

| Moteur | workers | Moyenne ± σ | Speedup vs `flatgrid` |
|---|---:|---:|---:|
| `flatgrid` | — | 175,0 ± 2,5 ms | 1,00× |
| `parallel` | 1 | 179,0 ± 2,4 ms | 0,98× |
| `parallel` | 2 | 113,5 ± 3,9 ms | 1,54× |
| `parallel` | 4 | 94,0 ± 5,5 ms | 1,86× |
| `parallel` | 8 | 86,8 ± 4,6 ms | **2,02×** |
| `parallel` | 16 (SMT) | 89,7 ± 12,9 ms | 1,95× |

Contrôle involontaire mais utile : la commande a aussi lancé `flatgrid` une
fois par valeur de `p`. `flatgrid` n'utilise pas de goroutines, et son temps
reste plat (531 → 550 ms sur `large`) quel que soit GOMAXPROCS. C'est la
preuve que le gain vient du pool, pas de la seule valeur de GOMAXPROCS.

## 5. Explication mécanique — pourquoi on reste sous la borne d'Amdahl

1. **1 worker coûte 2 à 3 % de plus que `flatgrid`.** C'est le prix fixe du
   pool : 2 barrières par tick (envoi canal + `WaitGroup`), soit 600
   réveils de goroutine par run, sans aucun parallélisme à la clé.
2. **La phase B série devient le plafond.** Profil `parallel`, `large`,
   8 workers : `commit` = 0,83 s de CPU sur 2,31 s de temps mural
   (10 répétitions), soit **≈ 36 % du temps mural**, contre 10 % dans
   `flatgrid`. Amdahl en action : la fraction série n'a pas bougé en valeur
   absolue, mais elle pèse désormais plus du tiers.
3. **Le travail parallèle lui-même coûte plus de CPU.** `decayGrid` consomme
   **6,21 s** de CPU cumulé à 8 workers, contre **4,38 s** en série, pour
   exactement le même calcul (+42 %). Hypothèses, non encore vérifiées :
   (a) la fréquence turbo baisse quand 8 cœurs sont actifs au lieu d'un
   seul ; (b) les 8 bandes se partagent la bande passante mémoire et le L3,
   et quatre grilles de 256 Ko dépassent le L2 d'un seul cœur. À vérifier
   avec `perf stat -e cycles,instructions,cache-misses` à 1 et 8 workers :
   un IPC stable avec moins de cycles par seconde désignerait (a), un IPC en
   baisse avec plus de défauts de cache désignerait (b).
4. **16 threads (SMT) n'apportent presque rien** (+6 % sur `large`, léger
   recul sur `medium`) pour un écart-type qui double. Deux threads du même
   cœur se partagent ses unités de calcul et son L1/L2 : c'est ce qu'on
   attend d'une charge qui sature déjà la mémoire.
5. **`medium` plafonne plus tôt** (≈ 2,0× dès 8 workers) : 128 lignes / 8
   = 16 lignes par bande, soit ≈ 0,3 ms de travail par phase. Le coût des
   barrières y pèse proportionnellement plus.

## 6. Pistes suivantes (non construites)

- **F1 `failsharing`** (§4 du barème) : construit sur ce moteur, avec des
  compteurs par worker non rembourrés sur une même ligne de 64 o, puis le
  rembourrage `[64]byte`.
- Le nouveau plafond est `commit`, dont une bonne part vient de `Ant.Trail`
  (`concatstrings`, `mallocgc`, `FormatInt` dans le profil). C'est le levier
  v3 `nogc`, et non un levier de concurrence.
