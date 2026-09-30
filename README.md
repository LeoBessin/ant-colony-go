# Ant Colony — banc d'optimisation backend

Simulation de colonie de fourmis sur grille en Go (fourmis, nourriture, murs,
pistes de phéromones), conçue comme une charge de travail optimisable pour
*Sup de Vinci — RNCP Bloc 4, « Optimisations & Performances Backend »*.

La simulation est un moyen, pas une fin. Son rôle est d'être un calcul backend
réaliste qui démarre **délibérément lent**, puis s'optimise étape par étape,
chacune documentée et mesurée. Le livrable noté est le rapport d'audit dans
`docs/report/`, pas le code.

## Démarrage rapide

Nécessite Go 1.23+ (développé et mesuré sur 1.27.1).

```bash
go run ./cmd/antweb            # interface du harnais sur http://localhost:8080
go run ./cmd/antweb -unlimited # idem, sans plafonds (local uniquement, voir « Mode limité et mode illimité »)
```

```bash
bash run_benchmarks.sh         # toutes les preuves, en une commande
```

## Ce que ça fait

**Données en entrée, données en sortie.** Une exécution prend un scénario JSON
et produit un résultat déterministe — même configuration + même graine donne un
checksum identique, à chaque fois, sur tous les moteurs. C'est cet invariant qui
transforme « cette optimisation est sûre » en affirmation démontrable plutôt
qu'en pari.

```bash
./bin/antsim.exe -config internal/config/scenarios/medium.json -engine naive
```

```json
{
  "engine": "naive",
  "seed": 20260921,
  "ticks_run": 400,
  "food_collected": 203,
  "state_checksum": 15571279245464762918,
  "wall_ns": 6374823800,
  "allocs": 66228925
}
```

L'interface web affiche la colonie en temps réel via Server-Sent Events pendant
l'exécution, et lance les moteurs en headless côte à côte pour les comparer —
en vérifiant qu'ils produisent toujours le même checksum.

## Organisation

| Chemin | Rôle |
|---|---|
| `cmd/antsim` | exécution headless — la cible de mesure pour hyperfine et pprof |
| `cmd/antweb` | interface web du harnais |
| `internal/simcore` | contrats : `Engine`, `Result`, `Snapshot`, `Observer`, PRNG |
| `internal/config` | données d'entrée, scénarios, validation |
| `internal/engine/naive` | baseline v0 — lente à dessein |
| `internal/engine/flatgrid` | v1 — grille en tableau indexé (adressage) |
| `internal/engine/parallel` | v4 — worker pool sur `GOMAXPROCS` (concurrence) |
| `internal/engine/nogc` | v3 — suppression de `Ant.Trail` (zéro allocation) |
| `internal/engine/gradient`, `.../scent` | variantes sémantiques, golden séparé (voir §9 de `CLAUDE.md`) |
| `test/` | tests golden de déterminisme |
| `bench/` | suites `testing.B`, résultats et profils |
| `docs/report/` | **le livrable noté** |
| `docs/journal/` | journal d'optimisation, une entrée par séance |

## Commandes

```bash
make test        # gate de correction (tests golden de déterminisme)
make bench       # go test -bench + benchstat avant/après
make profile     # pprof CPU + tas -> bench/profiles/
make hyper       # temps du binaire complet via hyperfine
make all         # tout, plus la spécification machine et les tableaux du rapport
```

Les outils optionnels manquants produisent un avertissement et une étape
ignorée, jamais un échec :

```bash
brew install hyperfine
go install golang.org/x/perf/cmd/benchstat@latest
brew install graphviz
```

### Harnais web en conteneur

```bash
docker compose up --build -d   # ou : make compose-up — http://localhost:8080
docker compose down            # ou : make compose-down
```

Image multi-étage (`golang:1.26-alpine` → `distroless/static:nonroot`,
~17 Mo). Le serveur applique les leviers réseau de la séance J4 :

- **HTTP/2** en plus d'HTTP/1.1, y compris en clair (h2c) pour les clients qui
  le parlent d'emblée (`curl --http2-prior-knowledge`, `vegeta -h2c`). Les
  navigateurs ne négocient HTTP/2 qu'en TLS : `antweb -cert … -key …`.
- **Keep-alive** (`IdleTimeout` 120 s) : les sockets sont réutilisées au lieu
  de repayer la poignée de main TCP à chaque requête.
- **gzip** sur le JSON et les fichiers statiques, jamais sur le flux SSE.
- Délais de lecture/écriture, `/healthz` pour le healthcheck, et arrêt propre
  sur SIGTERM (flux SSE fermés, benchs annulés).

#### Mode limité (par défaut) et mode illimité

`antweb` démarre en **mode limité** : c'est le mode d'un harnais exposé, borné
pour qu'une seule requête ne puisse pas épuiser l'hôte (OOM du conteneur, cœur
bloqué indéfiniment). Les plafonds sont définis dans
`internal/uiserver/limits.go` et appliqués à `/api/run` comme à `/api/bench` :

| Plafond | Valeur | Pourquoi |
|---|---|---|
| Cellules de la grille | ≤ 256 × 256 (65 536) | deux grilles par moteur + 256 snapshots en tampon (~34 Mo) |
| Fourmis | ≤ 5000 | |
| Ticks | ≤ 5000 | |
| Fourmis × ticks | ≤ 2 000 000 | les moteurs avant `nogc` gardent `Ant.Trail` (fuite de l'optimisation n°4, ~39 o par fourmi et par tick) : ~80 Mo au pire |
| Murs + tas de nourriture | ≤ 4096 | `naive` les parcourt à chaque tick |
| `repeat` d'un bench | ≤ 5 | |
| Benchs simultanés | 1 | les suivants reçoivent `429` : `parallel` occupe déjà tous les cœurs |
| Durée d'un bench | 60 s | |
| Durée d'une exécution en direct | 5 min | arrêt même sans clic sur *Stop* |

Tous les plafonds sont nettement au-dessus du plus grand scénario embarqué
(`large` : 256×256, 2000 fourmis, 300 ticks) : l'UI ne refuse jamais ses propres
scénarios. Une requête qui dépasse un plafond reçoit `400` avec le plafond en
cause dans le message. Le conteneur est en plus plafonné à 2 cœurs et 512 Mo
(`docker-compose.yml`, avec `GOMEMLIMIT=400MiB`).

Le **mode illimité** lève tous ces plafonds, la sérialisation des benchs et les
deux délais, pour qu'un test de charge local (vegeta, hey) mesure les moteurs et
non les refus du serveur :

```bash
make web-unlimited             # équivaut à : go run ./cmd/antweb -unlimited
```

Garde-fous :

- **Loopback uniquement.** `-unlimited` n'est accepté qu'avec une adresse
  d'écoute locale (`localhost:…`, `127.0.0.1:…`, `[::1]:…`). Toute autre adresse,
  y compris `:8080` (toutes les interfaces), fait sortir `antweb` avec le code 2.
  Le conteneur, qui écoute sur `:8080`, ne peut donc pas tourner en mode
  illimité.
- `antweb` affiche `WARNING: limits disabled (-unlimited)` au démarrage.
- Le corps de requête reste limité à 1 Mo dans les deux modes.

`cmd/antsim`, la cible de mesure, n'a aucun de ces plafonds : ils ne concernent
que le serveur web. Seul `config.Validate` s'y applique, et il ne rejette que
les configurations incohérentes, sans borne supérieure.

En déploiement, le harnais est protégé par une authentification HTTP Basic
appliquée par `antweb` lui-même (quel que soit le proxy devant, et sur tous les
domaines). Renseigner `BASIC_AUTH_USER` et `BASIC_AUTH_PASSWORD` dans Coolify →
Environment Variables ; rien n'est versionné. Sans mot de passe, l'auth est
désactivée (usage local) et `antweb` l'annonce au démarrage. `/healthz` reste
ouvert pour le healthcheck.

Les chiffres affichés par l'UI conteneurisée sont **indicatifs** : les mesures
du rapport viennent de `cmd/antsim` sous hyperfine. Les scénarios étant
embarqués (`go:embed`), en modifier un impose de relancer avec `--build`.

### Commandes hors `run_benchmarks.sh`, citées dans les rapports

`make all` couvre `test`/`bench`/`hyper`/`profile`. Ce qui suit ne l'est
pas — lancé à la main pour `docs/report/auditfinal.md`.

```bash
# Compteurs matériels du processeur (Optimisation n°1, vérification indépendante de pprof)
/usr/bin/time -l ./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine naive
/usr/bin/time -l ./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine flatgrid

# Parallélisation et mémoire, scénario large (Optimisations n°3/n°4, Annexe)
hyperfine --warmup 3 --runs 10 -L e flatgrid,parallel,nogc -L p 1,4,12 \
    "./bin/antsim.exe -quiet -config internal/config/scenarios/large.json -engine {e} -gomaxprocs {p}"

# Allocations avant/après la correction de la fuite mémoire (Optimisation n°4)
./bin/antsim.exe -quiet -config internal/config/scenarios/large.json -engine parallel -ticks 300 | grep allocs
./bin/antsim.exe -quiet -config internal/config/scenarios/large.json -engine nogc -ticks 300 | grep allocs

# Mémoire du processus échantillonnée pendant une exécution longue (graphique memleak.png)
./bin/antsim.exe -quiet -config internal/config/scenarios/large.json -engine {parallel,nogc} -ticks 60000 -gomaxprocs 12 &
# puis, en boucle toutes les 0,25 s jusqu'à la fin du process :
ps -o rss= -p <PID>

# Disponibilité/latence sous charge HTTP réelle (Annexe) — nécessite `antweb` lancé (make web) et vegeta installé
echo "GET http://localhost:8080/" | vegeta attack -rate=50 -duration=30s -timeout=10s | vegeta report
```

## État

Construits et mesurés : `naive` (baseline), `flatgrid` (v1, adressage),
`parallel` (v4, concurrence), `nogc` (v3, zéro allocation), `gradient` et
`scent` (variantes).

Hot Path v0 mesuré : `naive.go:105` (`fmt.Sprintf`) à **76 %** du temps CPU
total (construction de clé + recherche `map`). Voir `docs/report/auditfinal.md`
(« Analyse — cause racine et résolution ») et `docs/journal/01-profiling.md`.

## Règles de contribution

- `CLAUDE.md` — règles du projet, invariants de déterminisme, comment ajouter un
  moteur.
- `constitution.md` — contraintes strictes pour les assistants IA.

Les deux qui comptent le plus : ne jamais modifier un moteur existant pour
l'accélérer (le copier dans un nouveau package et changer une seule chose), et
ne jamais régénérer les fichiers golden pour faire passer un test en échec.

> **Note sur la langue.** La documentation et le rapport sont en français ; le
> code, les commentaires et les identifiants sont en anglais, comme il est
> d'usage en Go.
