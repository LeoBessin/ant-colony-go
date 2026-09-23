# 02 — Correction de définition : fourmis coincées contre les obstacles

> **Ce n'est pas une optimisation.** Aucun chiffre de performance n'est
> revendiqué ici. C'est une correction de la *définition* de la simulation,
> consignée parce qu'elle a entraîné une régénération des fichiers golden —
> l'acte que `CLAUDE.md` §7 exige de ne jamais commettre en silence.

Date : 2026-09-22. Moteur touché : `internal/engine/naive` (v0).

## Symptôme rapporté

Dans l'interface web, toutes les fourmis apparaissaient en mode « transport »
dès la première image, et les deux couches de phéromones ne s'affichaient pas.

Le premier symptôme est purement front-end (voir la note en fin d'entrée). Le
second était réel : le champ de phéromones *était* corrompu, mais par une cause
située dans le tick.

## Cause — un demi-tour appliqué deux fois

`chooseMove` inversait le cap lorsque `a.Blocked` était vrai :

```go
dir := a.Dir
if a.Blocked {
    dir = (dir + 4) & 7 // turn around rather than grind against the wall
}
```

Or les deux chemins d'échec, `step()` et la branche `best < 0`, stockaient déjà
le cap inversé dans l'intent : `Dir: (d + 4) & 7`. Deux inversions successives
valent l'identité. Une fourmi acculée retrouvait donc exactement son état
précédent et re-testait les trois mêmes cases infranchissables au tick suivant.

Trace d'une fourmi déposée dans le coin inférieur gauche de `tiny` :

```
t0  ( 0,23) dir=5 blocked=false
t1  ( 0,23) dir=1 blocked=true
t2  ( 0,23) dir=1 blocked=true     <- etat identique, re-derive chaque tick
...                                   seul le tirage d'errance (12 %) en sort
t18 ( 0,23) dir=3 blocked=true
t19 ( 0,22) dir=0 blocked=false    <- 18 ticks perdus
```

## Pourquoi cela corrompait le champ de phéromones

Le dépôt en phase B a lieu sur la case de la fourmi, qu'elle se soit déplacée ou
non. Une fourmi bloquée devient donc un émetteur permanent, et le maximum global
du champ « retour au nid » se retrouvait dans un coin de grille au lieu du nid :

| scénario | ant-ticks immobiles | max `PheroHome` | niveau au nid |
|---|---|---|---|
| `tiny` | **41,6 %** | 652 161 en (23,0) — un coin | 51 817 (**12,6×** moins) |
| `small` | 14,8 % | 391 877 en (6,0) — un bord | 306 083 |
| `medium` | 11,6 % | 1 000 000 en (63,26) | 529 854 |

Une fourmi chargée suivant ce gradient était donc attirée *vers les coins*.

**La mécanique des phéromones elle-même est saine** et n'a pas été touchée :
`decayGrid` calcule `v - 0,12·v + 0,03·Σvoisins` puis `×0,97`, ce qui est un
stencil de diffusion conservatif correct ; le double tampon est échangé puis
vidé dans le bon ordre ; le parcours reste row-major (jamais un ordre de map).
Le champ était faux parce que ses *entrées* l'étaient.

## Correction

Suppression de l'inversion dans `chooseMove` : `a.Dir` porte déjà le cap
retourné quand le tick précédent a échoué à bouger. Une seule ligne de
comportement change ; aucune règle de déterminisme (§4) n'est affectée — toujours
deux tirages PRNG inconditionnels, ordre de balayage inchangé, phase B sérielle.

## Effet mesuré

| | nourriture collectée | ant-ticks immobiles | max `PheroHome` |
|---|---|---|---|
| `tiny` | 40 → **80** (la totalité) | 41,6 % → **2,3 %** | coin → (5,5) |
| `small` | 77 → **127** | 14,8 % → **1,1 %** | bord → **exactement le nid** |
| `medium` | 203 → **288** | 11,6 % → **0,6 %** | → adjacent au nid |

## Conséquences sur les preuves

1. **Fichiers golden régénérés** (`make golden`). `config_hash` est inchangé —
   les entrées n'ont pas bougé, seule la définition du tick a changé :

   | | `food_collected` | `state_checksum` |
   |---|---|---|
   | `tiny` | 40 → 80 | `0x784a2bed0af266d5` → `0x58cbbb3ec5a8d61d` |
   | `small` | 77 → 127 | `0xe78a64bd5bded181` → `0x3bd2e12c9b6e9b22` |

2. **`bench/results/baseline.txt` était périmé, re-mesuré depuis.** L'ancienne
   définition avait ~12 à 42 % des ant-ticks qui ne déplaçaient rien ; le
   travail par tick n'était donc pas le même. La colonne « avant » de toute
   comparaison a été re-capturée sur le banc d'essai (§8) après cette
   correction — voir `docs/report/audit.md` §1.1/§2. Les profils
   `bench/profiles/*.top.txt` restent qualitativement valables — `fmt.Sprintf`
   et les accès map dominent toujours — leurs pourcentages exacts viennent de
   cette recapture.

3. `Ant.Blocked` n'est plus lu par la décision. Le champ est **conservé**
   volontairement : la disposition mémoire hostile de `Ant` (deux `bool`
   intercalés entre des champs de 8 octets) est la baseline que l'étage v2
   `soa` doit corriger, et la supprimer maintenant fausserait cette démonstration.

## Note annexe — le bug d'affichage

`Snapshot.AntFlags`, `PheroFood` et `PheroHome` sont des `[]uint8`, et
`encoding/json` sérialise `[]uint8` en **chaîne base64**, pas en tableau. Côté
navigateur, `s.af[i]` valait donc un caractère (`"A"`), toujours *truthy* — d'où
toutes les fourmis en orange — et `7 + "A" * 0.85` vaut `NaN`, qu'un
`Uint8ClampedArray` stocke en `0` — d'où les couches de phéromones entièrement
noires. Corrigé dans `app.js` par un décodage unique en tête de `draw()`. Le
base64 est plus compact qu'un tableau de nombres JSON : le format de transport
était bon, c'est le front-end qui le lisait mal. Aucun impact sur la simulation.
