# 04 — v1 `flatgrid` : de la map à l'indexation directe

> Premier étage du chemin retenu (CLAUDE.md §5). Axe 3a (mémoire & localité
> de cache). Correspond au gabarit de rédaction en tête de
> `docs/report/audit.md` §3.

Date : 2026-09-23. Nouveau paquet : `internal/engine/flatgrid`.

## 1. Optimisation appliquée

**Hypothèse** (§2.1/§2.3 de l'audit, `docs/journal/01-profiling.md`) :
`naive.go:105` — `key(x, y) string { return fmt.Sprintf("%d,%d", x, y) }` —
consomme **56,41 % du CPU total** à elle seule, et `runtime.mapaccess1_faststr`
19,74 % de plus. Remplacer les maps par des `[]T` indexés `y*W+x` doit
supprimer l'allocation, le hachage et le parcours de bucket sur les deux
lignes qui dominent le profil.

**Changement, un seul** : chaque `map[string]T` du `World` devient un `[]T`
adressé par `idx(x,y) = y*W+x` :

| Champ | avant | après |
|---|---|---|
| `Walls` | `map[string]bool` | `[]bool` |
| `Food` | `map[string]int` | `[]int` |
| `PheroFood`, `PheroHome`, `depFood`, `depHome`, `nextFood`, `nextHome` | `map[string]uint32` | `[]uint32` |

Tout le reste est copié verbatim de `naive` : les deux tirages PRNG
inconditionnels, l'arithmétique en virgule fixe, le double tampon, l'ordre de
balayage des directions, la struct `Ant` non alignée, le slice de pointeurs
`[]*Ant`. Ce sont les leviers de v2/v3, pas de celui-ci.

**Cas particulier — `Ant.Trail`** : ce champ (`[]string`, l'historique de
positions que personne ne lit) utilisait aussi `key()` pour construire sa
chaîne. Il est **hors périmètre** de v1 (sa suppression est le levier prévu
pour v3), donc son contenu doit rester inchangé — mais `key()` disparaît du
paquet. Solution : une fonction `trailKey(x,y)` équivalente à base de
`strconv.FormatInt`, qui produit le même texte (`"x,y"`) sans `fmt.Sprintf`.
Zéro `fmt.Sprintf` restant dans `flatgrid`, zéro changement de comportement
de `Trail`.

Fichiers : `internal/engine/flatgrid/{flatgrid,tick,output}.go`.

## 2. Métriques de référence (v0 `naive`, machine §1.1 de l'audit)

| Métrique | Valeur |
|---|---:|
| `Engine/medium` sec/op | 4,029 s ± 1 % |
| `Engine/medium` allocs/op | 66 302 084 |
| `Engine/medium` B/op | 417 160 573 (397,8 Mi) |
| `TickRate` ticks/s | 96,47 |
| Temps binaire complet (hyperfine, `medium`) | 4,060 s ± 0,096 s |
| Checksum (`medium`) | `0xa6b0a8c6451d55e4` |

## 3. Comparatif benchmark

```bash
go test -run '^$' -bench '^Benchmark(Engine|TickRate)' -benchmem \
    -benchtime 3x -count 6 ./bench/...
```

| Métrique | v0 `naive` | v1 `flatgrid` | Gain |
|---|---:|---:|---:|
| `Engine/medium` sec/op | 4,029 s ± 1 % | **37,88 ms ± 1 %** | **≈ 106×** |
| `Engine/small` sec/op | 470,8 ms ± 2 % | **5,314 ms ± 40 %** | **≈ 89×** ¹ |
| `allocs/op` (`medium`) | 66 302 084 | **165 315** | **≈ 401×** |
| `B/op` (`medium`) | 417 160 573 | **8 990 112** | **≈ 46×** |
| `TickRate` ticks/s | 96,47 | **≈ 9 944** | **≈ 103×** |
| Temps binaire complet (hyperfine, `medium`) | 4,060 s ± 0,096 s | **43,2 ms ± 0,8 ms** | **94,07× ± 2,83** |
| Checksum (`medium`) | `0xa6b0a8c6451d55e4` | `0xa6b0a8c6451d55e4` | **identique** |

¹ `Engine/small` sur `flatgrid` tombe à 5,3 ms : à cette échelle, le bruit
d'ordonnancement (démarrage de goroutine, scheduling du process) devient une
fraction non négligeable du temps mesuré, d'où le ±40 %. Une politique
`-benchtime`/`-count` plus agressive pour les scénarios devenus "rapides"
serait à envisager avant de citer ce chiffre isolément — le `medium`
(± 1 %) reste la mesure de référence.

**`make test` : PASS.** Le golden test confirme le checksum identique à
`naive` sur `tiny` et `small` (famille par défaut, §9 de CLAUDE.md — cet
engin ne déclare pas de `Variant`) ; vérifié en plus manuellement sur
`medium`, hors golden : `food_collected=288`, `state_checksum` identique au
bit près entre les deux moteurs.

## 4. Commande

```bash
# build + comparaison rapide
go build -trimpath -o bin/antsim.exe ./cmd/antsim
./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine naive    | grep state_checksum
./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine flatgrid | grep state_checksum

# benchmarks + stats
go test -run '^$' -bench '^Benchmark(Engine|TickRate)' -benchmem -benchtime 3x -count 6 ./bench/...

# profil du nouveau hot path
./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine flatgrid \
    -cpuprofile bench/profiles/flatgrid.cpu.pprof -memprofile bench/profiles/flatgrid.mem.pprof
go tool pprof -top -nodecount=20 bench/profiles/flatgrid.cpu.pprof
go tool pprof -top -nodecount=10 -sample_index=alloc_objects bench/profiles/flatgrid.mem.pprof

# temps binaire complet
hyperfine --warmup 3 --runs 10 -L engine naive,flatgrid \
    "./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine {engine}"
```

## Où le goulot s'est déplacé — la prédiction vérifiée

Le journal de profilage (`01-profiling.md`) pariait sur deux candidats pour
le prochain goulot : `decayGrid` elle-même, ou la pression GC résiduelle.
Profil CPU de `flatgrid` sur `medium` :

```
50,00 %  flatgrid.(*World).level          (flat)
75,00 %  flatgrid.(*World).decayGrid      (cum)
25,00 %  runtime.madvise                  (GC résiduel)
```

`fmt.Sprintf` et `mapaccess1_faststr` ont **disparu du top 20**. C'est
exactement `decayGrid`/`level` — la lecture des 4 voisins, désormais un
`multiply-add` et un chargement indexé — qui domine, comme prédit. Le premier
candidat était le bon ; le second (GC) reste présent mais résiduel (25 % d'un
temps total désormais 106× plus court, donc négligeable en valeur absolue).

Profil mémoire : ce qui reste d'allocations (93,43 %) vient de `trailKey` —
c'est-à-dire `Ant.Trail`. Prédiction pour v3, confirmée par construction
plutôt que par mesure : c'est le seul générateur d'allocation qu'on a
délibérément laissé en place.

## Note de métrologie — la borne d'Amdahl était juste sur la direction, prudente sur l'ampleur

`docs/journal/01-profiling.md` §"Conséquence" bornait le gain atteignable à
**≈ 4,0×**, en ne comptant que la fraction CPU directement attribuée à
`Sprintf` + `mapaccess1_faststr` dans un seul échantillon (74,9 %). Le gain
mesuré est **≈ 106×** sur `Engine/medium` — plus de 25× la borne.

Ce n'est pas une erreur de mesure, c'est une hypothèse implicite fausse dans
le calcul de la borne : elle traitait le "reste" (25,1 % du profil) comme un
temps fixe, indépendant de l'optimisation. Or une bonne partie de ce reste
(`mallocgcTiny`, `madvise`, `tryDeferToSpanScan`, `sync.Pool.Get/Put` — tous
présents dans le profil v0, `01-profiling.md`) est elle-même **causée** par
les 66 M allocations par run, pas indépendante d'elles. Supprimer
l'allocation n'a donc pas seulement retiré les 74,9 % mesurés sur un seul
échantillon : ça a aussi fait disparaître le travail de GC en arrière-plan
que l'échantillonnage CPU sous-représente structurellement (le GC tourne sur
des goroutines séparées, peu visibles dans un profil mono-thread comme celui
de v0), plus l'amélioration de localité de cache sur `level`/`decayGrid`
elles-mêmes, qui n'était pas dans le calcul du tout.

**Leçon pour la suite** : une borne d'Amdahl calculée sur une fraction de
profil CPU échantillonné est un *minorant* du gain réel dès que la fraction
retirée était aussi la cause principale de la pression GC. Elle reste utile
pour l'ordre de grandeur et la direction, pas pour le chiffre final — c'est
le rôle de la mesure post-optimisation, pas de la borne a priori. À garder en
tête avant de citer une borne d'Amdahl pour v4/F1/v6.
