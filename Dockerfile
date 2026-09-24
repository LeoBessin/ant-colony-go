# Image du harnais web (cmd/antweb).
#
# Ce conteneur n'est PAS une cible de mesure : les chiffres affichés par l'UI
# viennent d'un processus qui sert aussi du HTTP, dans un conteneur. Les
# chiffres du rapport d'audit viennent de cmd/antsim sous hyperfine
# (run_benchmarks.sh), sur le banc d'essai décrit dans CLAUDE.md §8.

FROM golang:1.26-alpine AS build
WORKDIR /src
# go.mod seul d'abord : la couche reste en cache tant que le module ne change
# pas (aucune dépendance externe aujourd'hui, mais le réflexe est gratuit).
COPY go.mod ./
RUN go mod download
COPY . .
# Scénarios et front-end sont embarqués via go:embed : le binaire se suffit.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /antweb ./cmd/antweb

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /antweb /antweb
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/antweb"]
CMD ["-addr", ":8080"]
