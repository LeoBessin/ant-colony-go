#!/usr/bin/env bash
#
# One command, the whole evidence pack for the audit report.
#
#   bash run_benchmarks.sh            # everything
#   bash run_benchmarks.sh env test   # just those stages
#
# Stages, in order:
#   env       bench-machine specification            -> docs/env/
#   test      CORRECTNESS GATE (aborts on failure)
#   bench     go test -bench + benchstat             -> bench/results/
#   build     trimmed binaries                       -> bin/
#   hyper     hyperfine whole-binary timings         -> bench/results/
#   profile   pprof CPU + heap, top + svg            -> bench/profiles/
#   report    concatenate everything                 -> docs/report/generated-tables.md
#
# Runs under Git Bash on Windows 11 or a POSIX shell on macOS/Linux; stage_env
# detects `uname` and picks the matching capture path. Missing optional tools
# (hyperfine, graphviz) produce a warning and a skipped stage, never a failed run.

set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"
ROOT="$(pwd)"

# --- configuration ----------------------------------------------------------

BENCH_SCENARIO="${BENCH_SCENARIO:-medium}"   # hyperfine + pprof target
BENCH_COUNT="${BENCH_COUNT:-6}"              # -count for the slow engine benchmarks
MICRO_COUNT="${MICRO_COUNT:-8}"              # -count for the cheap micro benchmarks
# Two -benchtime policies, because one value cannot serve both scales. Engine
# benchmarks take SECONDS per iteration, so they get a small fixed count.
# Micro benchmarks take NANOSECONDS, so they get a duration: at b.N=1 they
# would report the clock's resolution instead of the code's cost.
#
# 3s is not arbitrary. At 1s/count 6 these benchmarks showed +/-30-43% spread,
# enough for GridLookupMap to read as FASTER than the KeySprintf it contains --
# an ordering that is physically impossible and was pure scheduling noise.
# At 3s/count 8 the spread collapses to +/-1-2% and the ordering is coherent.
ENGINE_BENCHTIME="${ENGINE_BENCHTIME:-3x}"
MICRO_BENCHTIME="${MICRO_BENCHTIME:-3s}"
ENGINE_RE='^Benchmark(Engine|TickRate|Scaling)'
MICRO_RE='^Benchmark(Key|GridLookup|Rng)'
HYPERFINE_RUNS="${HYPERFINE_RUNS:-10}"
HYPERFINE_WARMUP="${HYPERFINE_WARMUP:-3}"
CPUS="${CPUS:-1,2,4,8,12}"                   # Apple M4 Pro: 12 physical cores, no SMT

STAMP="$(date +%Y-%m-%d_%H%M%S)"
RESULTS="bench/results"
PROFILES="bench/profiles"
ENVDIR="docs/env"

# --- plumbing ---------------------------------------------------------------

# Colour only on a terminal. Piped into a file or a grep, escape sequences
# would sit in front of every line and defeat any pattern anchored with ^.
if [ -t 1 ]; then
  RED=$'\033[31m'; GRN=$'\033[32m'; YEL=$'\033[33m'; BLD=$'\033[1m'; OFF=$'\033[0m'
else
  RED=''; GRN=''; YEL=''; BLD=''; OFF=''
fi
say()  { printf '%s==>%s %s\n' "$BLD" "$OFF" "$*"; }
ok()   { printf '%s  ok%s %s\n' "$GRN" "$OFF" "$*"; }
warn() { printf '%s  !!%s %s\n' "$YEL" "$OFF" "$*"; }
die()  { printf '%s ERR%s %s\n' "$RED" "$OFF" "$*" >&2; exit 1; }

# Go is often installed after the shell started, so look for it explicitly
# rather than trusting PATH.
if ! command -v go >/dev/null 2>&1; then
  for d in "/c/Program Files/Go/bin" "$LOCALAPPDATA/Programs/Go/bin"; do
    [ -x "$d/go.exe" ] && { export PATH="$d:$PATH"; break; }
  done
fi
command -v go >/dev/null 2>&1 || die "Go is not installed. winget install GoLang.Go"

# go install puts tools in GOBIN/GOPATH/bin, which is rarely on PATH on Windows.
GOBIN="$(go env GOBIN)"; [ -z "$GOBIN" ] && GOBIN="$(go env GOPATH)/bin"
export PATH="$PATH:$GOBIN"

have() { command -v "$1" >/dev/null 2>&1; }

mkdir -p "$RESULTS" "$PROFILES" "$ENVDIR" bin docs/report

# --- stages -----------------------------------------------------------------

stage_env() {
  say "environment"
  local out="$ENVDIR/${STAMP}.txt"
  local iso_date
  iso_date="$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date)"
  {
    echo "=== bench machine specification ==="
    echo "captured: $iso_date"
    echo
    echo "--- runtime ---"
    go version
    go env GOOS GOARCH GOAMD64 GOMAXPROCS CGO_ENABLED
    echo
    echo "--- cpu / cache / memory / os ---"
    case "$(uname -s)" in
      Darwin)
        sysctl -n machdep.cpu.brand_string 2>/dev/null
        echo "physical cores: $(sysctl -n hw.physicalcpu 2>/dev/null)"
        echo "logical cores:  $(sysctl -n hw.logicalcpu 2>/dev/null)"
        echo "base freq (Hz): $(sysctl -n hw.cpufrequency 2>/dev/null || echo 'n/a (Apple Silicon does not expose this)')"
        echo "L1 icache: $(sysctl -n hw.l1icachesize 2>/dev/null) bytes"
        echo "L1 dcache: $(sysctl -n hw.l1dcachesize 2>/dev/null) bytes"
        echo "L2 cache:  $(sysctl -n hw.l2cachesize 2>/dev/null) bytes"
        echo "L3 cache:  $(sysctl -n hw.l3cachesize 2>/dev/null || echo 'n/a (not exposed on this model)')"
        echo "memory:    $(sysctl -n hw.memsize 2>/dev/null) bytes"
        echo
        sw_vers 2>/dev/null
        ;;
      Linux)
        lscpu 2>/dev/null
        echo
        grep -E '^(MemTotal|MemFree)' /proc/meminfo 2>/dev/null
        echo
        cat /etc/os-release 2>/dev/null
        ;;
      *)
        powershell -NoProfile -Command '
          Get-CimInstance Win32_Processor |
            Format-List Name,NumberOfCores,NumberOfLogicalProcessors,MaxClockSpeed,L2CacheSize,L3CacheSize
          Get-CimInstance Win32_CacheMemory |
            Format-Table Purpose,Level,InstalledSize,MaxCacheSize -AutoSize
          Get-CimInstance Win32_PhysicalMemory |
            Format-Table Manufacturer,Capacity,Speed,ConfiguredClockSpeed -AutoSize
          [System.Environment]::OSVersion.VersionString
          (Get-CimInstance Win32_OperatingSystem).Caption
        ' 2>/dev/null
        ;;
    esac
    echo
    echo "--- power plan (turbo/idle behaviour affects variance) ---"
    case "$(uname -s)" in
      Darwin) pmset -g 2>/dev/null | head -20 ;;
      Linux)  cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor 2>/dev/null ;;
      *)      powercfg /getactivescheme 2>/dev/null ;;
    esac
  } > "$out" 2>&1
  ok "$out"
}

# The correctness gate. Everything downstream is meaningless without it: a
# faster engine that computes something else has not been optimized, it has
# been broken. Nothing is published until this passes.
stage_test() {
  say "correctness gate"
  go test ./... || die "tests failed — NO benchmark number from this tree is publishable"
  ok "all engines agree with the golden files"
}

stage_bench() {
  local out="$RESULTS/bench_${STAMP}.txt"
  : > "$out"

  say "go test -bench: whole engines (ns/op, B/op, allocs/op)"
  go test -run '^$' -bench "$ENGINE_RE" -benchmem \
      -benchtime "$ENGINE_BENCHTIME" -count "$BENCH_COUNT" ./bench/... 2>&1 | tee -a "$out"

  say "go test -bench: micro (cell addressing, grid lookup, PRNG)"
  go test -run '^$' -bench "$MICRO_RE" -benchmem \
      -benchtime "$MICRO_BENCHTIME" -count "$MICRO_COUNT" ./bench/... 2>&1 | tee -a "$out"
  ok "$out"

  # latest.txt is the moving head; baseline.txt is pinned to v0 and must be
  # committed, because the report's "before" column comes from it.
  cp "$out" "$RESULTS/latest.txt"
  if [ ! -f "$RESULTS/baseline.txt" ]; then
    cp "$out" "$RESULTS/baseline.txt"
    warn "no baseline yet — pinned this run as the v0 baseline"
  fi

  say "scalability sweep (-cpu $CPUS)"
  local sc="$RESULTS/scaling_${STAMP}.txt"
  go test -run '^$' -bench BenchmarkScaling -benchmem \
      -benchtime "$ENGINE_BENCHTIME" -count "$BENCH_COUNT" -cpu "$CPUS" ./bench/... 2>&1 | tee "$sc"
  ok "$sc"

  if have benchstat; then
    benchstat "$RESULTS/baseline.txt" "$RESULTS/latest.txt" \
      | tee "$RESULTS/benchstat.txt"
    ok "$RESULTS/benchstat.txt"
  else
    warn "benchstat missing: go install golang.org/x/perf/cmd/benchstat@latest"
  fi
}

stage_build() {
  say "build"
  # -trimpath keeps absolute paths out of the binary so a rebuild on another
  # machine is byte-comparable.
  go build -trimpath -o bin/antsim.exe ./cmd/antsim || die "build failed"
  go build -trimpath -o bin/antweb.exe ./cmd/antweb || die "build failed"
  ok "bin/antsim.exe bin/antweb.exe"
}

stage_hyper() {
  say "hyperfine (whole-binary wall time)"
  if ! have hyperfine; then
    warn "hyperfine missing: winget install sharkdp.hyperfine — skipping"
    return
  fi
  local engines
  engines="$(printf '%s,' $(./bin/antsim.exe -list) | sed 's/,$//')"
  hyperfine \
    --warmup "$HYPERFINE_WARMUP" \
    --runs "$HYPERFINE_RUNS" \
    --export-json "$RESULTS/hyperfine_${STAMP}.json" \
    --export-markdown "$RESULTS/hyperfine.md" \
    -L engine "$engines" \
    "./bin/antsim.exe -quiet -config internal/config/scenarios/${BENCH_SCENARIO}.json -engine {engine}"
  ok "$RESULTS/hyperfine.md"
}

stage_profile() {
  say "pprof (CPU + heap)"
  local svg=1
  have dot || { warn "graphviz missing: winget install Graphviz.Graphviz — no .svg callgraphs"; svg=0; }

  for eng in $(./bin/antsim.exe -list); do
    local cpu="$PROFILES/${eng}.cpu.pprof"
    local mem="$PROFILES/${eng}.mem.pprof"
    ./bin/antsim.exe -quiet \
      -config "internal/config/scenarios/${BENCH_SCENARIO}.json" \
      -engine "$eng" -cpuprofile "$cpu" -memprofile "$mem" >/dev/null || {
        warn "profiling $eng failed"; continue; }

    go tool pprof -top -nodecount=25 "$cpu" > "$PROFILES/${eng}.cpu.top.txt"  2>/dev/null
    go tool pprof -top -nodecount=25 -sample_index=alloc_objects "$mem" \
                                     > "$PROFILES/${eng}.mem.top.txt" 2>/dev/null
    if [ "$svg" = 1 ]; then
      go tool pprof -svg "$cpu" > "$PROFILES/${eng}.cpu.svg" 2>/dev/null
      go tool pprof -svg -sample_index=alloc_objects "$mem" \
                                     > "$PROFILES/${eng}.mem.svg" 2>/dev/null
    fi
    ok "$eng: $(ls "$PROFILES/${eng}".* 2>/dev/null | wc -l) artifacts"
  done

  echo
  say "reminder: pprof -http=:8081 <profile> opens the interactive flamegraph"
}

stage_report() {
  say "report tables"
  local out="docs/report/generated-tables.md"
  {
    echo "<!-- GENERATED by run_benchmarks.sh on ${STAMP}. Do not edit by hand. -->"
    echo
    echo "# Mesures générées — ${STAMP}"
    echo
    echo '## Banc d'"'"'essai'
    echo
    echo '```'
    # The newest capture, not this invocation's stamp: `report` is routinely
    # run on its own, long after `env`, and keying on STAMP silently emitted an
    # empty spec block.
    local envfile
    envfile="$(ls -1t "$ENVDIR"/*.txt 2>/dev/null | head -1)"
    if [ -n "$envfile" ]; then sed -n '1,60p' "$envfile"
    else echo 'Aucune capture — lancer: make env'; fi
    echo '```'
    echo
    echo '## Temps total du binaire (hyperfine)'
    echo
    if [ -f "$RESULTS/hyperfine.md" ]; then cat "$RESULTS/hyperfine.md"
    else echo '_hyperfine non installé — stage ignoré._'; fi
    echo
    echo '## Comparaison baseline vs courant (benchstat)'
    echo
    echo '```'
    if [ -f "$RESULTS/benchstat.txt" ]; then cat "$RESULTS/benchstat.txt"
    else echo 'benchstat non installé — stage ignoré.'; fi
    echo '```'
    echo
    echo '## Hot path (pprof -top)'
    echo
    for f in "$PROFILES"/*.cpu.top.txt; do
      [ -e "$f" ] || continue
      echo "### $(basename "$f" .cpu.top.txt)"
      echo '```'
      sed -n '1,30p' "$f"
      echo '```'
      echo
    done
  } > "$out"
  ok "$out"
}

# --- driver -----------------------------------------------------------------

ALL_STAGES="env test bench build hyper profile report"
STAGES="${*:-$ALL_STAGES}"

printf '%s\n' "$BLD== ant colony — benchmark suite ==$OFF"
echo "   scenario : $BENCH_SCENARIO"
echo "   stages   : $STAGES"
echo "   stamp    : $STAMP"
echo

START=$SECONDS
for s in $STAGES; do
  case "$s" in
    env|test|bench|build|hyper|profile|report) "stage_$s" ;;
    *) die "unknown stage '$s' (have: $ALL_STAGES)" ;;
  esac
  echo
done

say "done in $((SECONDS - START))s"
echo "   results  : $RESULTS/"
echo "   profiles : $PROFILES/"
echo "   report   : docs/report/generated-tables.md"
