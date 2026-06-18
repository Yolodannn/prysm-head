# Delay-4s private-head experiment

This branch implements the 4-second delayed proposer attack with asymmetric block visibility.

## Setup

The experiment uses one beacon node and two validator clients connected to the same beacon node.

- Honest validator client: `EXPERIMENT_VALIDATOR_ROLE=honest`
- Byzantine validator client: `EXPERIMENT_VALIDATOR_ROLE=byzantine`

## Attack behavior

When the proposer is Byzantine:

1. the validator client builds and signs the block normally;
2. the block root is recorded into `private_blocks.csv`;
3. the proposer waits for 4 seconds;
4. the block is submitted to the beacon node.

When an attester is Byzantine:

1. the validator client requests normal attestation data from the beacon node;
2. if a private block exists for the current slot, the attester replaces `BeaconBlockRoot` with the private block root before signing;
3. the attestation is submitted after the delayed block is publicly released.

This simulates a private view for Byzantine validators while keeping a single canonical beacon chain.

## Expected result

Honest validators:

- source reward remains normal;
- target reward remains normal;
- head reward loss increases.

Byzantine validators:

- source reward remains normal;
- target reward remains normal;
- head reward is mostly preserved.

## Important files

- `validator/client/propose.go`
- `validator/client/attest.go`
- `validator/client/experiment_private_blocks.go`
- `validator/client/BUILD.bazel`
- `beacon-chain/rpc/prysm/v1alpha1/validator/proposer.go`
- `experiments/delay-4s/devnet_config.yaml`

## Environment variables

```bash
EXPERIMENT_WRITE_REWARDS=1
EXPERIMENT_VALIDATOR_ROLE=honest
EXPERIMENT_VALIDATOR_ROLE=byzantine
EXPERIMENT_PRIVATE_BLOCKS_PATH=/home/suliudan2001/workspace/devnet/private_blocks.csv
```

## Build

```bash
bazel build //cmd/validator:validator //cmd/beacon-chain:beacon-chain
```

Large output files such as raw reward CSVs and log archives are not committed to this branch.
