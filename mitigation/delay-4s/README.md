# Delay-4s Mitigation Experiment

This directory contains the mitigation version of the 4-second delayed proposer attack.

The mitigation is implemented on top of the original delay-4s attack. Therefore, the attack patch must be applied first, followed by the mitigation patch.

## Reproduction

Starting from the baseline code in the repository root:

```bash
git apply experiments/delay-4s/attack.patch
git apply mitigation/delay-4s/mitigation.patch
```

Then build Prysm:

```bash
bazel build //cmd/prysmctl:prysmctl //cmd/beacon-chain:beacon-chain //cmd/validator:validator
```

Run the mitigation experiment:

```bash
bash mitigation/delay-4s/run.sh
```

## Mitigation

The mitigation enables partial head reward through:

```bash
EXPERIMENT_PARTIAL_HEAD_REWARD=1
```

The corresponding implementation modifies the Altair reward-processing logic.

## Experimental Setting

* Total validators: 1000
* Byzantine validators: 333
* Honest validators: 667
* Fork: Altair
* Delayed proposer release: 4 seconds

The Byzantine validators are indices `0-332`, while honest validators are indices `333-999`.

## Files

* `mitigation.patch`: mitigation changes relative to the delay-4s attack implementation.
* `run.sh`: script for reproducing the mitigation experiment.
* `333byz-partial.env`: mitigation-specific environment variables.

The attack configuration and validator group files are shared with:

```text
experiments/delay-4s/
```

## Outputs

The experiment generates files including:

```text
rewards.csv
private_blocks.csv
beacon.log
validator-byzantine.log
validator-honest.log
```

Large generated logs and raw experimental outputs are not committed to this repository.
