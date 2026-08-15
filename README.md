# Ethereum PoS Incentive and Fork-Choice Attack Experiments

This repository contains the experimental implementation and reproduction artifacts for incentive and fork-choice attacks in Ethereum Proof-of-Stake.

The experiments are implemented by extending the Prysm Ethereum consensus client.

## Repository Structure

### Attack Experiments

- `experiments/delay-4s/`  
  4-second proposer block-delay attack.

- `experiments/reorg/`  
  Reorganization attacks with `k = 2`, `k = 3`, and `k = 4`, together with a no-attack control.

### Mitigation Experiments

- `mitigation/delay-4s/`  
  Delay attack with the mitigation mechanism enabled.

- `mitigation/reorg/`  
  Reorganization attacks with `k = 2`, `k = 3`, and `k = 4` with the mitigation mechanism enabled.

## Reproduction

All experiments can be launched directly from the repository root.

### Delay Attack

```bash
bash experiments/delay-4s/run.sh
```

### Delay Attack with Mitigation

```bash
bash mitigation/delay-4s/run.sh
```

### Reorganization Attacks

```bash
bash experiments/reorg/scripts/run.sh k2
bash experiments/reorg/scripts/run.sh k3
bash experiments/reorg/scripts/run.sh k4
```

No-attack control:

```bash
bash experiments/reorg/scripts/run.sh no-attack
```

### Reorganization Attacks with Mitigation

```bash
bash mitigation/reorg/scripts/run.sh k2
bash mitigation/reorg/scripts/run.sh k3
bash mitigation/reorg/scripts/run.sh k4
```

Each launcher automatically prepares the corresponding Prysm baseline, applies the experiment patch, builds Prysm, generates the devnet genesis state, and starts the beacon node and validator clients.

For experiment-specific details, see the README file in the corresponding directory.

