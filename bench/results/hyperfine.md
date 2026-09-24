| Command | Mean [ms] | Min [ms] | Max [ms] | Relative |
|:---|---:|---:|---:|---:|
| `./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine flatgrid` | 42.2 ± 0.7 | 41.6 | 43.9 | 1.00 |
| `./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine gradient` | 4007.5 ± 147.9 | 3865.8 | 4391.7 | 94.93 ± 3.81 |
| `./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine naive` | 4213.8 ± 20.1 | 4187.0 | 4236.7 | 99.82 ± 1.64 |
| `./bin/antsim.exe -quiet -config internal/config/scenarios/medium.json -engine scent` | 45.8 ± 0.5 | 45.3 | 46.9 | 1.08 ± 0.02 |
