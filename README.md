# Ethereum PoS Incentive and Fork-Choice Attack Experiments

This repository contains our experimental implementation and evaluation of
incentive and fork-choice attacks in Ethereum Proof-of-Stake.

The experiments are implemented by extending the Prysm Ethereum consensus client.

## Repository Structure

### Baseline

`baseline/prysm`

Prysm baseline used in our experiments.

### Attack Experiments

- `attack/delay-4s`  
  4-second proposer block-delay attack.

- `attack/reorg-k2`  
  Reorganization attack with \(k=2\).

- `attack/reorg-k3`  
  Reorganization attack with \(k=3\).

- `attack/reorg-k4`  
  Reorganization attack with \(k=4\).

### Mitigation Experiments

- `mitigation/delay-4s`
- `mitigation/reorg-k2`
- `mitigation/reorg-k3`
- `mitigation/reorg-k4`

These branches contain the corresponding experiments with our mitigation mechanism enabled.

### Reproducible Artifact

`artifact/delay-4s-mitigation`

This branch provides a complete runnable experiment, including:

- experiment configuration
- beacon chain startup
- validator startup
- attack / mitigation setup
- reward data collection
- reproduction instructions

## Experimental Platform

The experiments run on a local Ethereum Proof-of-Stake devnet based on Prysm.

## Reproduction

Please refer to the README file in each experiment branch for experiment-specific setup and execution instructions.
