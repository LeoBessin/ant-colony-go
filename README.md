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

Exposé sur un serveur, le harnais est **borné** pour ne pas épuiser l'hôte :
grille ≤ 256×256, ≤ 5000 fourmis, ≤ 5000 ticks, fourmis × ticks ≤ 2 M (tous
les moteurs avant `nogc` gardent `Ant.Trail`, la fuite de l'optimisation n°4 :
~39 o par fourmi et par tick), `repeat` ≤ 5, corps de requête
≤ 1 Mo, un seul bench à la fois (les autres reçoivent `429`), bench coupé à
60 s, exécution en direct coupée à 5 min (`internal/uiserver/limits.go`). Le
conteneur est en plus plafonné à 2 cœurs et 512 Mo (`docker-compose.yml`).

En déploiement Coolify (proxy Caddy), le domaine est protégé par une
authentification HTTP Basic. Le hash n'est pas versionné : le générer, puis
renseigner `BASIC_AUTH_USER` et `BASIC_AUTH_HASH` dans Coolify → Environment
Variables **avant** de redéployer.

```bash
docker run --rm caddy caddy hash-password --plaintext '<mot de passe>'
```

Les chiffres affichés par l'UI conteneurisée sont **indicatifs** : les mesures
du rapport viennent de `cmd/antsim` sous hyperfine. Les scénarios étant
embarqués (`go:embed`), en modifier un impose de relancer avec `--build`.

### Commandes citées dans `docs/report/auditfinal.md`

Reproduction des mesures du rapport de synthèse, dans l'ordre où il les cite.

```bash
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
```

## État

Seule la **baseline v0** existe. Les étages suivants (grille plate, disposition
SoA, zéro allocation, worker pool, codecs binaires, et une régression de faux
partage volontaire) sont planifiés dans `CLAUDE.md` et seront construits
profilage en main — la règle du cours est *on ne devine jamais le Hot Path, on
le mesure*.

Hot Path v0 mesuré : `fmt.Sprintf` à **61,9 %** du CPU cumulé et **99,9 %** des
allocations. Voir `docs/journal/01-profiling.md`.

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
