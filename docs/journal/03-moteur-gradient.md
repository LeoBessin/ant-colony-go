# 03 — Moteur `gradient` : des phéromones qui encodent une distance

> **Ce n'est pas un étage d'optimisation.** Aucun gain de vitesse n'est
> revendiqué. C'est une *variante sémantique* : une seconde définition de la
> simulation, avec ses propres fichiers golden, construite parce que `naive`
> a un défaut de navigation qu'aucun réglage ne corrige.

Date : 2026-09-22. Nouveau paquet : `internal/engine/gradient`.

## Symptôme

Après épuisement de la première source de nourriture, les fourmis chargées ne
retrouvent plus le nid. Mesuré sur `medium`, moteur `naive`, 1600 ticks :

| tick | tas restant | livrées | en charge | ancienneté médiane de la charge |
|---|---|---|---|---|
| 800 | 105 | 687 | 112 | 87 |
| 1000 | **0** | 760 | 144 | 201 |
| 1200 | 0 | 796 | 108 | **402** |
| 1400 | 0 | 817 | 87 | **601** |
| 1600 | 0 | 831 | 73 | **797** |

L'ancienneté médiane croît **au même rythme que l'horloge** (201, 402, 601,
797) : signature d'une population figée. Personne ne quitte l'ensemble des
porteuses. Entre 73 et 144 fourmis gardent leur charge indéfiniment.

Sur l'ensemble des ramassages : 12 % ne sont jamais livrés, et le rapport
détour (ticks ÷ distance à vol d'oiseau) vaut 1,7× en médiane, 5,3× au p90,
13× au maximum.

## Diagnostic

Test décisif : sur le champ final, depuis chaque case, monter répétitivement
vers le voisin de plus fort `PheroHome` (+ biais nid, choix parmi les 8
directions, sans tirage aléatoire — donc *meilleur* que ce dont dispose une
vraie fourmi). Arrive-t-on au nid ?

```
'.' = atteint le nid   'X' = boucle ou maximum local   '#' = mur   N = nid   F = nourriture
  XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
  XXFXXXXXXXXXXXXXXXXXXXXXXXXXXFXX
  XXXXXXXXXXXXXXXXFXXXXXXXXXXXXXXX
  XXXXXXXX#######XXX#######XXXXXXX
  XXXXXXXX#XXXXXXXXXXXXXXX#XXXXXXX
  XXXXXXXX#XXXXXXXXXXXXX..#XXXXXXX
  XXXXXFXX#XXXXXXXNXXXX...#XXFXXXX
  XXXXXXXX#XXXXXX...XX....#XXXXXXX
  XXXXXXXX#XXXX...........#XXXXXXX
  XXXXXXXX#XX.............#XXXXXXX
  XXXXXXXX#...............#XXXXXXX
  XXXXXXXX#################XXXXXXX
  XXXXXXXXXXXXXXXXFXXXXXXXXXXXXXXX
  XXXFXXXXXXXXXXXXXXXXXXXXXXXXXXXX
```

**Aucune case hors de l'enceinte n'atteint le nid.** Les huit tas sont hors de
l'enceinte : toute fourmi chargée démarre donc, par construction, dans une zone
d'où la règle de retour ne mène nulle part. Les livraisons qui ont lieu sont
dues au tirage d'errance de 12 %, pas à la navigation.

Deux causes indépendantes :

**1. `PheroHome` mesure une densité, pas une distance.** Dans `naive`, chaque
fourmi en recherche dépose le même `DepositHome` où qu'elle soit. Le champ
enregistre donc *où les fourmis s'attroupent* : ses maxima sont les couloirs et
les bouchons. Sur une carte 128×128 on compte **57 maxima locaux**, dont le nid
n'est qu'un parmi d'autres. Seul, ce champ ramène au nid depuis 3 % de la carte.

**2. Le biais nid est une attraction en ligne droite** (distance de Chebyshev)
et ne contourne pas un obstacle concave. Balayage sur un champ figé :

| `nestBias` | carte d'où le retour aboutit |
|---|---|
| 0 | 3 % |
| 250 000 (`Max/4`, valeur actuelle) | 9 % |
| 500 000 | 46 % |
| ∞ (ignorer la phéromone) | **46 %** |

Il sature à 46 %, entièrement à l'intérieur de l'enceinte. Aucune intensité ne
fait contourner un mur, puisque « réduire la distance au nid » pointe droit
dans sa face extérieure.

## Le changement — un seul

Le dépôt est divisé par deux tous les `falloffHalfLife` pas parcourus depuis
l'**événement d'ancrage** de la fourmi : quitter le nid pour le champ `Home`,
ramasser de la nourriture pour le champ `Food`.

```go
func falloff(base uint32, steps int64) uint32 {
    sh := uint64(steps) / falloffHalfLife
    ...
    return uint32(uint64(base) >> sh)
}
```

`Ant.Steps` existait déjà dans `naive`, incrémenté à chaque tick et **jamais
lu nulle part**. Il est ici remis à zéro à chaque ancrage et devient l'entrée
du dépôt.

Point essentiel : `Steps` compte une **longueur de chemin réellement parcourue**,
pas une distance à vol d'oiseau. Une fourmi qui a dû contourner un mur rapporte
le trajet le plus long, donc le champ encode une distance *géodésique* et
contourne les obstacles gratuitement — exactement la propriété que le biais en
ligne droite ne peut pas avoir.

La décroissance doit être **exponentielle**. Le niveau d'équilibre d'une case
est proportionnel au trafic qui la traverse, et le trafic varie de plus d'un
ordre de grandeur entre un couloir et le terrain libre : une décroissance douce
est simplement battue par l'encombrement. Mesuré, une courbe hyperbolique ne
fait passer la navigabilité que de 9 % à 28 %.

Balayage de la demi-vie sur `medium`, 800 ticks :

| demi-vie | collectées | carte d'où le retour aboutit |
|---|---|---|
| — (`naive`) | 687 | 9 % |
| 4 | 886 | 53 % |
| 8 | 902 | 53 % |
| **16 (retenue)** | **904** | **58 %** |
| 32 | 901 | 29 % |
| 64 | 901 | 29 % |

## Résultat

`medium`, 1600 ticks, `naive` → `gradient` :

| | `naive` | `gradient` |
|---|---|---|
| ramassages | 904 | **1376** |
| livraisons | 831 | **1192** |
| durée de portage p50 / p90 | 76 / 233 | **48 / 165** |
| rapport détour p50 / p90 | 1,7× / 5,3× | **1,1× / 3,3×** |
| ancienneté médiane de la charge, ticks 1000→1600 | 201 → 797 (croissance 1:1) | 117 → 176 (stable) |

La croissance 1:1 disparaît : plus aucune fourmi n'est échouée
définitivement. Les 184 charges non livrées à l'arrêt correspondent exactement
aux 184 fourmis en transit à cet instant — rien n'est perdu.

Les livraisons continuent après l'épuisement du premier tas (899 → 1192 entre
les ticks 600 et 1600), ce qui n'arrivait pas avec `naive` : `PheroFood` culmine
désormais sur les sources de nourriture et non sur le trafic, ce qui améliore
aussi l'exploration.

Sur le scénario tel quel (400 ticks) : **288 → 561 collectées**.

## Conséquences sur le harnais

Un moteur qui change la *définition* de la simulation ne peut pas être tenu au
checksum de `naive`, mais le sortir du test golden le priverait de toute
garantie de déterminisme. D'où la notion de **variante** :

- `simcore.Variant` — interface optionnelle. Ne pas l'implémenter signifie
  « simulation de référence ». Ce défaut est porteur : un étage d'optimisation
  hérite de l'obligation de reproduire `naive` **en ne disant rien**, et ne peut
  donc pas y échapper par oubli.
- `test/testdata/golden/<variante>/<scénario>.json` — chaque variante a son
  fichier de référence et subit le même test, dans sa propre famille.
- `TestGoldenFamilies` vérifie qu'aucun moteur ne déclare une variante sans
  fichier de référence, ce qui le ferait sortir du contrôle en silence.
- Le tableau comparatif de l'UI groupe désormais checksum de référence **et**
  base de speedup par variante : deux simulations différentes ne sont pas des
  alternatives l'une de l'autre.

Les fichiers golden de la famille de référence sont **inchangés au bit près**
après cette opération, ce qui confirme que `naive` n'a pas été touché.

## Portée

`gradient` reste la baseline « bête » : mêmes `map[string]uint32`, même
`fmt.Sprintf`, même `[]*Ant`, même `Ant.Trail`. C'est délibéré — l'échelle
d'optimisation v1→v6 s'applique telle quelle à cette variante, et le travail de
performance n'est en rien affecté par ce changement de comportement.
