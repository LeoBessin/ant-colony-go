# Enveloppe mince autour de run_benchmarks.sh. Le script est la source de
# verite, pour que la commande reproductible unique fonctionne avec ou sans
# make installe.
#
# Note Windows : ces cibles passent par Git Bash.

SHELL := bash
BENCH_SCENARIO ?= medium

.PHONY: all help run web test bench build hyper profile report env clean golden fmt vet

help:
	@echo "make all       - suite complete (env, test, bench, build, hyperfine, pprof, report)"
	@echo "make test      - gate de correction seul (tests golden de determinisme)"
	@echo "make bench     - go test -bench + benchstat"
	@echo "make hyper     - temps du binaire complet via hyperfine"
	@echo "make profile   - profils pprof CPU + tas"
	@echo "make report    - regenere docs/report/generated-tables.md"
	@echo "make env       - capture la specification du banc d essai"
	@echo "make run       - une execution headless (BENCH_SCENARIO=$(BENCH_SCENARIO))"
	@echo "make web       - lance l interface du harnais sur http://localhost:8080"
	@echo "make golden    - REGENERE les fichiers golden (seulement si la definition a change)"
	@echo "make clean     - supprime les artefacts de build et de mesure"

all:      ; bash run_benchmarks.sh
env:      ; bash run_benchmarks.sh env
test:     ; bash run_benchmarks.sh test
bench:    ; bash run_benchmarks.sh test bench
build:    ; bash run_benchmarks.sh build
hyper:    ; bash run_benchmarks.sh build hyper
profile:  ; bash run_benchmarks.sh build profile
report:   ; bash run_benchmarks.sh report

run: build
	./bin/antsim.exe -config internal/config/scenarios/$(BENCH_SCENARIO).json

web:
	go run ./cmd/antweb

fmt:
	gofmt -l -w .

vet:
	go vet ./...

# Regenerer les golden accepte une NOUVELLE definition de la simulation. Ne
# jamais lancer ceci pour faire passer une optimisation cassee : c est
# exactement ce que les fichiers golden existent pour empecher.
golden:
	UPDATE_GOLDEN=1 go test ./test/... -run TestGolden -v

clean:
	rm -rf bin
	rm -f bench/results/*.txt bench/results/*.json bench/results/*.md
	rm -f bench/profiles/*.pprof bench/profiles/*.svg bench/profiles/*.txt
