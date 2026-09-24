| Command | Mean [ms] | Min [ms] | Max [ms] | Relative |
|:---|---:|---:|---:|---:|
| `./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine flatgrid` | 45.2 ± 0.4 | 44.7 | 45.7 | 1.47 ± 0.02 |
| `./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine gradient` | 4059.7 ± 32.4 | 4018.2 | 4114.4 | 131.81 ± 2.01 |
| `./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine naive` | 4191.0 ± 29.9 | 4152.0 | 4241.4 | 136.07 ± 2.02 |
| `./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine nogc` | 30.8 ± 0.4 | 29.9 | 31.3 | 1.00 |
| `./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine parallel` | 41.2 ± 4.5 | 39.5 | 53.9 | 1.34 ± 0.15 |
| `./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine scent` | 46.0 ± 0.7 | 44.9 | 47.5 | 1.49 ± 0.03 |
