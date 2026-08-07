# Delay-4s Private-Head Experiment

This artifact implements the 4-second delayed proposer attack with asymmetric block visibility.

## Setup

The experiment uses one beacon node and two validator clients connected to the same beacon node.

- Honest validator client: `EXPERIMENT_VALIDATOR_ROLE=honest`
- Byzantine validator client: `EXPERIMENT_VALIDATOR_ROLE=byzantine`

The experiment contains 1000 validators in total:

- Byzantine validators: indices 0-332 (333 validators)
- Honest validators: indices 333-999 (667 validators)

The experiment starts from the Altair fork.

## Attack behavior

When the proposer is Byzantine:

1. the validator client builds and signs the block normally;
2. the block root is recorded into `private_blocks.csv`;
3. the proposer waits for 4 seconds;
4. the block is submitted to the beacon node.

When an attester is Byzantine:

1. the validator client requests normal attestation data from the beacon node;
2. if a private block exists for the current slot, the attester replaces `BeaconBlockRoot` with the private block root;
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

The experiment configuration is stored in:

    experiments/delay-4s/devnet_config.yaml

The Byzantine validator set is stored in:

    experiments/delay-4s/groups/byz_333_first.txt

The environment configuration for the 333-Byzantine partial-head-reward experiment is:

    experiments/delay-4s/env/333byz-partial.env

The complete experiment launcher is:

    experiments/delay-4s/scripts/run_333_partial.sh

## Build

Run the following commands from the root directory of the repository.

Build `prysmctl`, the beacon node, and the validator client:

    bazel build \
      //cmd/prysmctl:prysmctl \
      //cmd/beacon-chain:beacon-chain \
      //cmd/validator:validator

The experiment launcher expects the generated binaries at:

    bazel-bin/cmd/prysmctl/prysmctl_/prysmctl
    bazel-bin/cmd/beacon-chain/beacon-chain_/beacon-chain
    bazel-bin/cmd/validator/validator_/validator

## Run the experiment

Make the launcher executable:

    chmod +x experiments/delay-4s/scripts/run_333_partial.sh

Start the complete experiment:

    ./experiments/delay-4s/scripts/run_333_partial.sh

The launcher automatically performs the following steps:

1. generates an Altair genesis state with 1000 validators;
2. starts the beacon node;
3. starts the Byzantine validator client controlling validators 0-332;
4. starts the honest validator client controlling validators 333-999;
5. waits for the processes to initialize;
6. checks the running processes and the current beacon-chain head.

## Genesis

The launcher generates the genesis state using:

    --fork=altair
    --genesis-time-delay=120
    --num-validators=1000

The generated genesis state is written to:

    $BASE/genesis.ssz

## Beacon node

The beacon node uses the following experiment settings:

    EXPERIMENT_TOTAL_VALIDATORS=1000
    EXPERIMENT_WRITE_REWARDS=1
    EXPERIMENT_PARTIAL_HEAD_REWARD=1

Reward information is written to:

    $BASE/rewards.csv

Private-block information is written to:

    $BASE/private_blocks.csv

The beacon node exposes:

    gRPC: 127.0.0.1:4000
    REST: http://127.0.0.1:3500

## Byzantine validators

The Byzantine validator client controls validators 0-332:

    --interop-num-validators=333
    --interop-start-index=0
    --monitoring-port=8082

It runs with:

    EXPERIMENT_VALIDATOR_ROLE=byzantine

## Honest validators

The honest validator client controls validators 333-999:

    --interop-num-validators=667
    --interop-start-index=333
    --monitoring-port=8081

It runs with:

    EXPERIMENT_VALIDATOR_ROLE=honest

## Output directory

By default, the launcher stores runtime files under:

    ~/workspace/devnet/delay-4s-333-partial

A different output directory can be specified using `BASE`:

    BASE=/path/to/output \
      ./experiments/delay-4s/scripts/run_333_partial.sh

## Experiment outputs

The main experiment outputs are:

    rewards.csv
    private_blocks.csv
    beacon.log
    validator-byzantine.log
    validator-honest.log

The runtime directory also contains:

    genesis.ssz
    beacon-data/
    validator-byzantine-data/
    validator-honest-data/
    beacon.pid
    validator-byzantine.pid
    validator-honest.pid

The CSV files and logs should be preserved when archiving an experimental run.

The database directories are runtime data and can be regenerated.

## Check experiment status

Check the running Prysm processes:

    pgrep -af "beacon-chain_/beacon-chain|validator_/validator"

Check the current beacon-chain head:

    curl -s http://127.0.0.1:3500/eth/v1/beacon/headers/head \
      | python3 -c 'import sys,json; print("slot="+json.load(sys.stdin)["data"]["header"]["message"]["slot"])'

Check the beacon-node log:

    tail -f ~/workspace/devnet/delay-4s-333-partial/beacon.log

Check the Byzantine-validator log:

    tail -f ~/workspace/devnet/delay-4s-333-partial/validator-byzantine.log

Check the honest-validator log:

    tail -f ~/workspace/devnet/delay-4s-333-partial/validator-honest.log

## Stop the experiment

Stop the beacon node:

    pkill -f "beacon-chain_/beacon-chain"

Stop the validator clients:

    pkill -f "validator_/validator"

Before starting a new experimental run, verify that no old processes remain:

    pgrep -af "beacon-chain_/beacon-chain|validator_/validator"

## Reproducibility

The experiment source code, chain configuration, validator-group definition,
environment configuration, and launcher are version-controlled together.

For reproducibility, use the source-code version associated with this artifact
and run:

    ./experiments/delay-4s/scripts/run_333_partial.sh

