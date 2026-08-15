# Reorganization Experiments

This directory contains the reproduction scripts for the reorganization experiments with:

- `k = 2`
- `k = 3`
- `k = 4`
- `no-attack` control

The experiments are based on Prysm v7.1.4 at commit:

```text
1756380c2e84e90004df0a6268f8c28d832f5ab6
```

## Run

From the repository root, run one of the following commands.

### k = 2

```bash
bash experiments/reorg/scripts/run.sh k2
```

### k = 3

```bash
bash experiments/reorg/scripts/run.sh k3
```

### k = 4

```bash
bash experiments/reorg/scripts/run.sh k4
```

### No-attack control

```bash
bash experiments/reorg/scripts/run.sh no-attack
```

The script automatically prepares a clean Prysm worktree, applies the corresponding experiment patch, builds Prysm, generates the genesis state, and starts the beacon node and validator clients.

## Output

By default, experiment outputs are stored in:

```text
../reorg-artifacts/<mode>/
```

The main reward output is:

```text
rewards.csv
```

Reorganization experiments additionally generate:

```text
reorg_events.csv
reorg_windows.csv
reorg_results.csv
reorg_private_roots.csv
```
