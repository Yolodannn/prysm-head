# Reorganization Mitigation Experiments

This directory contains the mitigation experiments for the reorganization attacks with:

- `k = 2`
- `k = 3`
- `k = 4`

The experiments are based on Prysm v7.1.4 at commit:

```text
1756380c2e84e90004df0a6268f8c28d832f5ab6
```

## Run

From the repository root:

### k = 2

```bash
bash mitigation/reorg/scripts/run.sh k2
```

### k = 3

```bash
bash mitigation/reorg/scripts/run.sh k3
```

### k = 4

```bash
bash mitigation/reorg/scripts/run.sh k4
```

The launcher automatically applies the corresponding mitigation patch, builds Prysm, generates the genesis state, and starts the beacon node and validator clients.

## Output

By default, results are stored in:

```text
../reorg-mitigation-artifacts/<mode>/
```

The main reward output is:

```text
rewards.csv
```

Reorganization-specific outputs include:

```text
reorg_events.csv
reorg_windows.csv
reorg_results.csv
reorg_private_roots.csv
```
