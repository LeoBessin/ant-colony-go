# Synthèse des optimisations de performance

**Simulation de colonie de fourmis — module de calcul backend**

## Glossaire

| Terme | Nom technique (code) | Définition |
|---|---|---|
| **Module principal** | `naive` → `flatgrid` → `parallel` → `nogc` | Le programme de base : il fait déplacer les fourmis, gère la collecte de nourriture, le retour au nid et le dépôt des traces chimiques. Chaque nom après la flèche est une correction supplémentaire, construite sur la précédente — pas un redémarrage à zéro. |
| **Module « recherche de nourriture »** | `internal/engine/scent` (corrigé en place) | Un comportement ajouté par-dessus le module principal : sans lui, une fourmi ne trouve un tas de nourriture qu'en marchant dessus par hasard. Avec lui, elle « sent » les tas à proximité et s'en approche délibérément — une recherche plus efficace, pas un moteur différent. |
| **Cycle de simulation** | un `tick` | Une unité de temps simulé : à chaque cycle, toutes les fourmis se déplacent une fois. Le programme enchaîne plusieurs centaines de cycles par exécution. |
| **Grille** | `World.PheroFood` / `PheroHome`, désormais des `[]uint32` | Le plateau sur lequel évoluent les fourmis, découpé en cases (128×128 dans les mesures de ce document). Chaque case peut contenir un mur, de la nourriture, ou une trace chimique. |
| **Worker** | une *goroutine* Go | Une unité de travail qui tourne sur son propre cœur de processeur. Le module principal en démarre un nombre fixe (jusqu'à 12 sur la machine de test) au lancement, et les réutilise à chaque cycle plutôt que d'en recréer. |
| **Fuite de mémoire** | `Ant.Trail []string` (supprimé) | Une donnée que le programme continue d'accumuler indéfiniment sans jamais la relire ni la vider — ici, l'historique complet de chaque déplacement de chaque fourmi depuis le début de l'exécution. Plus l'exécution dure, plus elle occupe de mémoire, sans limite. |

Chaque module est un package Go séparé, enregistré dans un même binaire
(`cmd/antsim`). Le module principal a été dupliqué à chaque correction —
`naive`, `flatgrid`, `parallel` et `nogc` existent tous encore dans le code,
chacun disponible pour comparaison avec le suivant ; le module « recherche de
nourriture » a été corrigé directement, n'ayant pas d'équivalent à conserver
pour comparaison. Dans tous les cas, un test de non-régression compare le
résultat au bit près avant/après (voir Méthode).

---

## Diagnostic 

**L'outil** : `pprof`, le profileur intégré au langage Go. Il ne suppose
rien — il interrompt le programme des milliers de fois par seconde pendant
qu'il tourne réellement, et note à chaque fois quelle ligne de code est en
train de s'exécuter. Au bout d'une exécution, on obtient un classement des
fonctions par temps réellement consommé — une commande, un résultat, pas une
intuition.

La grille (murs, nourriture, traces chimiques) était
stockée dans des `map` — la structure de recherche associative de Go, celle
qu'on utilise pour associer une clé à une valeur. Pour lire une case, le
programme devait d'abord construire cette clé sous forme de texte (par
exemple `"42,17"`), puis la donner à la `map` pour qu'elle retrouve la
valeur. `pprof` a chiffré ce coût précisément : **76 % du temps CPU total**
passait dans la construction de cette clé texte et la recherche dans la
`map` qui la lit — plus que toute la logique de déplacement des fourmis
réunie. Chiffre obtenu ligne par ligne (`pprof -list`, pas une lecture
approximative d'un graphique) : la seule ligne qui construit la clé texte
(`naive.go:105`) consomme **55,6 %** du temps CPU total à elle seule ; la
recherche dans la `map` qui la lit en ajoute **20,8 %**.

> ## 76 %
> du temps CPU total consommé par la seule construction de clé texte et la
> recherche dans la `map` qui la lit.

**La preuve visuelle** : `pprof` produit aussi un arbre d'appel («
flamegraph ») — chaque case est une fonction, sa largeur est le temps
qu'elle consomme, mesuré directement, pas déduit du pourcentage ci-dessus :

![Arbre d'appel : la conversion en texte et la recherche dans la map dominent visuellement la largeur du graphique](img/flamegraph.png)

La tour verte, qui descend du centre vers la gauche, est la chaîne complète
de construction de la clé texte (`naive.key` → `fmt.Sprintf` → ses fonctions
internes de formatage) — elle occupe à elle seule plus de la moitié de la
largeur du graphique. Les blocs roses/saumon, à droite et en haut à droite,
sont la recherche dans la `map` elle-même (`mapaccess1_faststr`) et le
nettoyage mémoire automatique qu'elle déclenche (`madvise`, `allocSpan`).
Ensemble, ils forment la quasi-totalité de l'arbre : la logique « utile » de
la simulation (déplacement, décision des fourmis) est trop étroite pour
être visible à cette échelle.

**Troisième confirmation, par chronométrage direct.** Au lieu de mesurer le
programme entier, on peut isoler la seule opération en cause et la
chronométrer des millions de fois de suite (`go test -bench` +
`benchstat`) : lire une case de la grille par `map`, contre lire la même
case par un calcul d'index direct.

| Méthode de lecture d'une case | Temps par lecture |
|---|---:|
| Par `map` (clé texte) | 52,96 ns |
| Par index direct | **0,313 ns** |

**169× plus lent**, mesuré en dehors de toute simulation, sur l'opération
seule. Les trois mesures — pourcentage `pprof` sur le programme complet,
lecture de l'arbre d'appel, chronométrage isolé de l'opération — pointent
la même cause par trois méthodes indépendantes.

**La conclusion, et la décision qui en découle** : le ralentissement n'était
pas dans la logique de simulation, il était dans la façon de ranger et
relire la grille en mémoire. C'est ce diagnostic, chiffré par `pprof`, qui a
directement guidé la correction : **remplacer les `map` par un accès direct
en mémoire**, pour supprimer la construction de clé texte et la recherche
associative — voir Optimisation n°1 ci-dessous.

---

## Optimisation n°1 — Module principal

**Le changement** : chaque `map` de la grille (murs, nourriture, deux traces
chimiques) devient un tableau simple, où une case se trouve par un calcul
d'index direct plutôt que par une recherche associative — l'équivalent de
connaître le numéro de rue directement plutôt que de chercher une maison en
tapant son adresse en toutes lettres à chaque pas. Aucune autre logique n'a
été modifiée — même ordre de traitement, mêmes règles de déplacement, même
hasard contrôlé.

| Métrique | Avant | Après |
|---|---:|---:|
| Temps d'exécution | 4,10 s | **39,3 ms** |
| Opérations mémoire par exécution | 66,3 millions | **165 300** |
| Résultat produit | identique | identique |

Le nombre d'opérations mémoire — qui sollicitent le système de gestion de
la mémoire de Go et ralentissent tout le programme en tâche de fond — a été
divisé par plus de 400.

**Vérification indépendante, par un second outil.** Les chiffres ci-dessus
viennent de `pprof`/`benchstat` (mesure logicielle). Un second instrument,
`time -l` (compteurs matériels du processeur : instructions exécutées,
cycles d'horloge, mémoire réellement occupée), confirme le même résultat par
une voie totalement différente — sans dépendre de l'outillage Go :

| Compteur matériel | Avant | Après | Rapport |
|---|---:|---:|---:|
| Instructions CPU exécutées | 93,06 milliards | 1,28 milliard | ×72,5 |
| Cycles d'horloge consommés | 18,47 milliards | 0,19 milliard | ×96,0 |
| Mémoire occupée au pic | 19,4 Mo | 10,6 Mo | ×1,8 |

Point notable : avant correction, le temps CPU consommé (4,25 s) dépassait
le temps réel écoulé (3,94 s) — plus d'un cœur travaillait en moyenne, alors
que la simulation elle-même n'utilise qu'un seul cœur. C'est le nettoyage
mémoire automatique de Go qui tournait en tâche de fond sur un second cœur,
à cause du volume d'opérations mémoire ci-dessus. Après correction, ce
second cœur n'est plus sollicité.

---

## Optimisation n°2 — Module « recherche de nourriture »

Ce second module ajoute un comportement : une fourmi qui cherche de la
nourriture est attirée par les tas qu'elle peut « sentir » à proximité. Ce
module avait été construit avant la correction n°1 et n'en avait donc pas
bénéficié — il souffrait exactement du même problème.

**Le changement** : la même correction que pour le module principal,
appliquée à ce module. Le nouveau comportement de recherche olfactive n'a
lui-même pas été modifié.

| Métrique | Avant | Après |
|---|---:|---:|
| Temps d'exécution | 3,85 s | **42,3 ms** |
| Opérations mémoire par exécution | 67,1 millions | **165 100** |
| Résultat produit | identique | identique |

---

## Optimisation n°3 — Parallélisation multi-cœurs

Les deux premières corrections ont supprimé le mauvais accès mémoire, mais
laissé intact un autre problème : le programme ne s'exécutait que sur **un
seul cœur du processeur**, alors que la machine de test en propose douze.

**Le changement** : le programme démarre désormais un groupe fixe de
*workers* (jusqu'à 12, un par cœur) une seule fois au lancement, réutilisés à
chaque cycle. Le travail qui peut se faire en parallèle — faire réfléchir les
fourmis à leur prochain déplacement, faire évoluer les traces chimiques —
est réparti entre eux. Une étape reste volontairement **hors de ce
partage** : le dépôt effectif sur le plateau (ramassage de nourriture,
retour au nid, marquage chimique) doit se faire fourmi par fourmi, dans
l'ordre, pour trancher correctement les cas où deux fourmis visent la même
ressource au même instant — c'est le **chemin critique** identifié dans le
diagnostic : lui seul ne peut pas être parallélisé.

| Métrique (scénario `large`, 256×256) | Avant (1 cœur) | Après (12 cœurs) |
|---|---:|---:|
| Temps d'exécution | 134 ms | **87 ms** |
| Résultat produit | identique | identique |

![Temps d'exécution selon le nombre de cœurs utilisés](img/scaling.png)

**Un résultat honnête, pas le maximum théorique.** Le gain plafonne dès 4
cœurs (79 ms) et ne s'améliore plus au-delà — 8 et 12 cœurs font même très
légèrement moins bien que 4. Ce n'est pas un défaut de la mesure : c'est
l'étape non-parallélisable (le chemin critique ci-dessus) qui devient, une
fois le reste accéléré, la nouvelle limite. Plus on va vite sur la partie
partageable, plus la partie qui ne l'est pas pèse, proportionnellement, dans
le total — ajouter des cœurs au-delà de ce point ne fait qu'ajouter de la
coordination entre eux sans travail supplémentaire à leur donner.

---

## Optimisation n°4 — Résolution d'une fuite de mémoire

En laissant tourner le programme sur une exécution longue, la mémoire qu'il
occupait augmentait en continu, sans jamais se stabiliser — le symptôme
classique d'une fuite de mémoire.

**La cause** : chaque fourmi conservait l'historique complet de chaque case
visitée depuis le début de l'exécution, dans une liste qui ne fait que
grandir. Personne ne relit jamais cette liste — elle avait été ajoutée à une
étape antérieure du projet et jamais nettoyée.

**Le changement** : suppression de cette liste. Rien d'autre n'a été modifié.

![Mémoire occupée pendant une exécution longue, avant et après correction](img/memleak.png)

Sur une exécution de 60 000 cycles (scénario `large`) : la mémoire passe de
26 Mo à **4,4 Go** en moins de 19 secondes avant correction, sans aucun signe
de stabilisation — elle aurait continué à grossir indéfiniment sur une
exécution plus longue. Après correction, elle reste **stable à 8 Mo** du
début à la fin.

| Métrique (scénario `large`, 300 cycles) | Avant | Après | Rapport |
|---|---:|---:|---:|
| Opérations mémoire par exécution | 1 506 703 | **19** | ≈79 300× |
| Mémoire allouée au total | 26,1 Mo | **16 Ko** | ≈1 624× |

**Effet secondaire favorable** : en supprimant cette liste, le chemin
critique identifié dans l'optimisation n°3 (l'étape non-parallélisable)
devient lui-même plus court — c'est lui qui accumulait cette liste. Résultat,
à 12 cœurs : **50,6 ms**, contre 87 ms pour la correction précédente et
134 ms pour le point de départ — un gain supplémentaire de ×1,7 sur la
parallélisation seule, ×2,7 depuis le tout premier module.

---

## Résultats consolidés

### Tableau maître — module principal, les 4 étapes, même scénario

Les quatre corrections dans l'ordre où elles ont été appliquées, chacune
construite sur la précédente (pas un redémarrage à zéro), mesurées sur le
**même scénario** (`large`, 256×256, 2 000 fourmis, 300 cycles, 12 cœurs) pour
que les chiffres soient directement comparables d'une ligne à l'autre :

| Étape | Stratégie | Temps d'exécution | Mémoire allouée | Allocations | Δ vs étape précédente |
|---|---|---:|---:|---:|---:|
| `naive` | Référence : clé texte (`fmt.Sprintf`) + `map[string]uint32`, un seul cœur | 12,25 s | 1,52 Go | 200 333 979 | — |
| `flatgrid` | Suppression de la clé texte et de la `map` → tableau indexé | **130,6 ms** | **26,1 Mo** | **1 506 705** | ×93,8 temps, ×133 allocations |
| `parallel` | + worker pool sur 12 cœurs | **87,1 ms** | 26,1 Mo (inchangée) | 1 506 703 (inchangées) | ×1,50 temps |
| `nogc` | + suppression de l'historique inutile (`Ant.Trail`) | **50,6 ms** | **16 Ko** | **19** | ×1,72 temps, ×79 300 allocations |
| **Total cumulé** | `naive` → `nogc` | **×242 plus rapide** | **×95 000 moins de mémoire allouée** | **×10,5 million de fois moins d'allocations** | — |

`parallel` ne change ni la mémoire ni les allocations (attendu : seule la
répartition du travail entre cœurs change, pas la structure de données) —
c'est `nogc` qui règle ce deuxième axe, une fois que la parallélisation a
rendu son coût visible et proportionnellement plus important (voir
Optimisation n°4).

### Optimisations n°1 et n°2, en détail (scénario `medium`)

Le tableau maître ci-dessus suit uniquement le module principal. Les deux
graphiques suivants reviennent sur la première paire de corrections
(module principal et module « recherche de nourriture »), sur `medium` —
le scénario où elles ont été mesurées en premier :

![Temps d'exécution avant / après, échelle logarithmique](img/results.png)

![Opérations mémoire avant / après, échelle logarithmique](img/allocations.png)

Les deux modules affichent un temps d'exécution et un volume d'opérations
mémoire du même ordre de grandeur — la correction s'est généralisée sans
effet de bord, et les deux métriques (temps, mémoire) bougent dans les
mêmes proportions : la cause était bien unique.

---

## Prochaine étape

Une cinquième correction est engagée mais pas encore livrée : documenter une
tentative d'optimisation qui, elle, **dégrade** les performances au lieu de
les améliorer — un scénario connu en calcul multi-cœurs où deux workers se
gênent involontairement en écrivant des données voisines en mémoire, sans
qu'aucune donnée ne soit réellement partagée entre eux. L'objectif est de
provoquer ce problème délibérément, le mesurer, puis le corriger — pour
montrer que la même rigueur de mesure s'applique aussi quand une
optimisation ne fonctionne pas comme prévu.

---

## Méthode

| Outil | Rôle | Ce qu'il a montré ici |
|---|---|---|
| `pprof` | Profilage CPU et mémoire réel — identifie la ligne de code exacte responsable du ralentissement, pas une estimation. | La cible exacte à corriger : `naive.key` (construction de clé) + la `map` qui la lit, **76 % du temps CPU** (Diagnostic). |
| `go test -bench` + `benchstat` | Micro-mesures (temps, mémoire allouée) par fonction, moyennées sur au moins 6 exécutions pour écarter le bruit de mesure. | Le chiffrage précis avant/après pour chaque module (tableaux Optimisation n°1 et n°2, et Annexe). |
| `hyperfine` | Temps du programme complet tel qu'un utilisateur le vivrait (démarrage inclus), moyenné sur 10 exécutions avec période de chauffe. | Confirme le même ordre de grandeur que `benchstat` par une mesure indépendante — le gain n'est pas un artefact de la méthode de mesure (tableau Annexe). |
| `time -l` (compteurs matériels du processeur) | Instructions et cycles CPU réellement exécutés, mémoire réellement occupée — mesure au niveau du processeur, indépendante de l'outillage Go. | Confirme le gain une troisième fois, par une voie qui ne dépend ni de `pprof` ni de `go test` ; a aussi révélé qu'un second cœur travaillait en tâche de fond avant correction, à cause du nettoyage mémoire (Optimisation n°1). |
| Tests de non-régression (`go test`) | Comparaison bit à bit du résultat produit par chaque module, avant/après correction. Un gain de vitesse n'est retenu que si ce test passe. | Les quatre corrections passent ce test : résultat produit identique, seule la vitesse (ou la mémoire) change. `go test -race` en plus pour les deux corrections touchant à la parallélisation : aucune donnée lue et écrite en même temps par deux workers. |
| Échantillonnage de la mémoire du processus (`ps`, une fois par seconde) | Suit la mémoire réellement occupée pendant qu'un programme tourne, seconde par seconde — pas seulement au début et à la fin. | Le graphique de fuite de mémoire (Optimisation n°4) : une croissance continue avant correction, une ligne plate après. |

Machine de test : Apple M4 Pro, 12 cœurs, macOS, Go 1.26.4. Détail complet
du protocole et des mesures brutes dans le rapport technique
(`docs/report/audit.md`).

---

## Annexe : données de mesure complètes

Vue d'ensemble des quatre modules concernés par la correction d'adressage
(optimisations n°1 et n°2), scénario `medium` (128×128 cases, 400 fourmis,
400 cycles) — `parallel` et `nogc` sont traités séparément plus bas, sur
`large`, le scénario où leur effet se voit. `gradient` est une troisième
variante (comportement de retour au nid amélioré), indépendante des
optimisations de ce document — elle n'a pas encore reçu la correction
d'adressage et partage donc le même problème, à titre indicatif :

![Temps d'exécution des quatre modules, échelle logarithmique](img/all_engines.png)

### Temps d'exécution et mémoire — micro-mesures (`go test -bench` + `benchstat`, 6 répétitions)

| Module | Scénario | Temps par exécution | Mémoire allouée | Opérations mémoire |
|---|---|---:|---:|---:|
| naive | small | 475,9 ms | 41,5 Mo | 8,27 M |
| naive | medium | 4,10 s | 397,8 Mo | 66,3 M |
| gradient | small | 473,8 ms | 41,5 Mo | 8,27 M |
| gradient | medium | 3,87 s | 397,4 Mo | 66,3 M |
| flatgrid | small | 5,4 ms | 1,34 Mo | 25 200 |
| flatgrid | medium | 39,3 ms | 8,57 Mo | 165 300 |
| scent | small | 5,6 ms | 1,34 Mo | 25 200 |
| scent | medium | 42,3 ms | 8,57 Mo | 165 100 |
| parallel | medium | 36,3 ms | 8,58 Mo | 165 300 |
| nogc | medium | **28,1 ms** | **592 Ko** | **440** |

Sur `medium` (128×128), `parallel` n'apporte quasiment rien par rapport à
`flatgrid` (36,3 ms contre 39,3 ms) : la grille est trop petite pour que
répartir le travail sur plusieurs cœurs compense le coût de les
coordonner. C'est `large` (256×256, ci-dessous) qui montre l'effet de la
parallélisation — c'est d'ailleurs le scénario choisi pour les optimisations
n°3 et n°4 plus haut, précisément pour cette raison.

### Temps du programme complet, démarrage inclus (`hyperfine`, 10 répétitions, `medium`)

| Module | Temps moyen | Écart-type | Facteur vs le plus rapide |
|---|---:|---:|---:|
| flatgrid | 42,2 ms | ± 0,7 ms | ×1,0 (référence) |
| scent | 45,8 ms | ± 0,5 ms | ×1,08 |
| gradient | 4,007 s | ± 0,148 s | ×94,9 |
| naive | 4,214 s | ± 0,020 s | ×99,8 |

Cette mesure inclut le démarrage du programme et l'écriture du résultat —
c'est le temps que vivrait réellement un utilisateur, contrairement au
tableau précédent qui isole le seul calcul.

### Parallélisation et mémoire — scénario `large` (256×256, celui des optimisations n°3 et n°4)

Commande dans le README (`hyperfine ... -L e flatgrid,parallel,nogc -L p 1,4,12`).

| Module | Cœurs | Temps moyen | Écart-type |
|---|---:|---:|---:|
| flatgrid | 1 (n'utilise jamais plus) | 134,4 ms | ± 0,7 ms |
| parallel | 1 | 140,6 ms | ± 1,2 ms |
| parallel | 4 | 78,8 ms | ± 2,3 ms |
| parallel | 12 | 87,1 ms | ± 1,3 ms |
| nogc | 1 | 117,4 ms | ± 7,2 ms |
| nogc | 4 | 50,1 ms | ± 1,7 ms |
| nogc | 12 | **50,6 ms** | ± 1,3 ms |

Commandes dans le README (`antsim -engine {parallel,nogc} -ticks 300 | grep allocs`).

| Module | Opérations mémoire (300 cycles) | Mémoire totale allouée |
|---|---:|---:|
| parallel | 1 506 703 | 26,1 Mo |
| nogc | **19** | **16 Ko** |

Sources versionnées dans le dépôt : `bench/results/latest.txt`,
`bench/results/hyperfine.md`, `bench/profiles/*.cpu.pprof`,
`bench/profiles/parallel.cpu.top.txt`, `bench/profiles/nogc.cpu.top.txt`,
`docs/journal/06-parallel.md`, `docs/journal/07-nogc.md` (mesures initiales
de ces deux derniers prises sur une autre machine, re-mesurées ici sur le
banc de référence).
