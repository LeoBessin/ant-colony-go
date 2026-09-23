# 05 — Moteur `scent` : les fourmis sentent la nourriture

> **Ce n'est pas un étage d'optimisation.** Aucun gain de vitesse n'est
> revendiqué. C'est une *variante sémantique* construite sur `gradient`, avec
> ses propres fichiers golden (`test/testdata/golden/scent/`).

Date : 2026-09-23. Nouveau paquet : `internal/engine/scent`.

## Symptôme

Dans l'UI, sur `medium`, les fourmis longent les murs et les bords de la
carte et passent à côté des tas sans les ramasser. Mesuré sur `flatgrid`
(identique au bit près à `naive`), 1600 ticks :

| tas | premier ramassage (tick) | unités prises | fourmi-ticks de recherche à ≤ 3 cases |
|---|---|---|---|
| (64,20), face à l'ouverture | 43 | **900 / 900** | 24 740 |
| (10,12) | 319 | 2 | 22 |
| (20,64) | 264 | 1 | 23 |
| (108,64) | 165 | 1 | 14 |
| les 4 autres | jamais | 0 | 7 à 23 |

Un seul tas sur huit est exploité.

## Diagnostic

Trois causes, toutes dans `chooseMove` / `commit` :

1. **Un tas non découvert est invisible.** Le champ `PheroFood` n'est écrit
   que par des fourmis *déjà* chargées. Avant la première découverte, les trois
   cases du cône valent 0, l'égalité va à « tout droit » : la fourmi file en
   ligne droite jusqu'à un mur, puis glisse le long. D'où les bandes collées
   aux murs visibles dans l'UI.
2. **Une piste capture la colonie.** La première piste établie attire tous les
   chercheurs sortant par l'ouverture.
3. **Un tas occupe une seule case.** Il faut y poser la patte exactement ; passer
   à une case ne compte pas.

`gradient` ne traite aucune de ces causes : il corrige le *retour* au nid, pas
la *recherche*.

## Tentative écartée : un halo diffusé

Première idée : chaque tas non vide dépose chaque tick dans `depFood`, et la
diffusion de la phase C étale un halo. Balayage de l'émission (fraction de
`Max`), `medium`, 1600 ticks :

| émission | collectées t400 | collectées t1600 | tas touchés (> 10 unités) |
|---|---|---|---|
| 0 (`gradient`) | 561 | 1192 | 3 |
| 1/100 | 588 | 1178 | 5 |
| 1/20 | 565 | 1455 | 3 |
| 1/4 | 613 | 1094 | 5 |
| 1/1 | 660 | 1226 | 3 |

Aucune tendance. Avec 12 % de diffusion et 3 % d'évaporation par tick, la
longueur caractéristique du halo est d'environ une case : il ne porte pas assez
loin pour qu'une fourmi le croise.

## Le changement retenu — un seul

Une fourmi en recherche sent le tas **non vide** le plus proche dans un rayon
`scentRadius` (distance de Chebyshev). Un pas qui l'en rapproche gagne
`scentBias = Max/4`. C'est le miroir exact du biais nid qu'utilise déjà une
fourmi chargée : même intensité, même distance. Il s'éteint dès que le tas est
vide.

Déterminisme : `nearestScent` parcourt `w.piles`, une slice construite dans
l'ordre de `cfg.Food` (jamais la map `Food`), égalité au premier tas. La
phase A ne fait que *lire* `Food`, dont la phase B est l'unique écrivain :
toutes les fourmis d'un tick voient le même ensemble de tas vivants, quel que
soit l'ordre de la phase A, qui reste donc parallélisable.

## Balayage du rayon

`medium`, graines 1 à 5, 1600 ticks, moyenne des collectées :

| rayon | t400 | t1600 |
|---|---|---|
| 0 (`gradient`) | 549 | 1061 |
| 8 | 660 | 1196 |
| 16 | 706 | 1227 |
| **24 (retenu)** | **786** | **1388** |
| 32 | 749 | 1409 |
| 48 | 691 | **973** |

À 48, la fin de partie s'effondre (57 fourmis chargées à t1600 contre ~200) :
comme le biais nid, l'attraction est en ligne droite. Trop large, elle traverse
l'enceinte et plaque les chercheurs contre sa face intérieure — le même piège
concave que documente `03-moteur-gradient.md`. 24 garde les halos hors de
l'enceinte.

L'intensité à rayon 24 est sans effet significatif (`Max/16` : 759 / 1424,
`Max/4` : 786 / 1388, `Max` : 789 / 1346) : la valeur symétrique du biais nid
est gardée.

## Résultat

Graine du scénario, 1600 ticks :

| scénario | `gradient` t400 | `scent` t400 | `gradient` t1600 | `scent` t1600 |
|---|---|---|---|---|
| tiny | 80 | 80 | 80 | 80 |
| small | 427 | **549** | 964 | **1200** (tout) |
| medium | 561 | **742** | 1192 | — |
| large | 155 | **562** | 2991 | 3059 |

Sur `medium` tel que livré (400 ticks) : **561 → 742 collectées**, checksum
`0x9fa1092df40b9fa4`, stable sur `-repeat`.

## Limite connue

Sur `medium`, les tas situés sous l'enceinte restent quasi intacts. La carte
de densité (ticks 800 à 1600) montre que les chercheurs ne sortent que par
l'ouverture du haut et ne contournent jamais l'enceinte, et qu'une partie des
porteuses se perd à l'extérieur pendant 400 à 1000 ticks. C'est un problème de
géométrie et d'exploration, pas de détection : un rayon plus grand le pire
(voir 48). Hors du périmètre de ce changement.

## Portée

Comme `gradient`, `scent` reste la baseline « bête » : `map[string]`,
`fmt.Sprintf`, `[]*Ant`, `Ant.Trail`. `nearestScent` ajoute 8 lectures de map
par fourmi en recherche et par tick : sans objet pour le rapport, puisqu'une
variante n'est jamais comparée en vitesse à la référence.
