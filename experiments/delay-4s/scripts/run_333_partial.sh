#!/usr/bin/env bash
set -euo pipefail

# ============================================================
# Delay-4s / 333 Byzantine Validators / Partial Head Reward
#
# Validators:
#   Byzantine: 0-332   (333 validators)
#   Honest:    333-999 (667 validators)
#
# Total validators: 1000
# Fork: Altair
# ============================================================

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

BASE="${BASE:-$HOME/workspace/devnet/delay-4s-333-partial}"

CFG="$ROOT/experiments/delay-4s/devnet_config.yaml"
GROUP="$ROOT/experiments/delay-4s/groups/byz_333_first.txt"

PRYSMCTL="$ROOT/bazel-bin/cmd/prysmctl/prysmctl_/prysmctl"
BEACON_BIN="$ROOT/bazel-bin/cmd/beacon-chain/beacon-chain_/beacon-chain"
VALIDATOR_BIN="$ROOT/bazel-bin/cmd/validator/validator_/validator"

echo "============================================================"
echo " Delay-4s 333-Byzantine Partial-Head-Reward Experiment"
echo "============================================================"
echo "ROOT  = $ROOT"
echo "BASE  = $BASE"
echo "CFG   = $CFG"
echo "GROUP = $GROUP"
echo

mkdir -p "$BASE"

# ------------------------------------------------------------
# Check required files/binaries
# ------------------------------------------------------------

for f in "$CFG" "$GROUP"; do
    if [[ ! -f "$f" ]]; then
        echo "ERROR: missing file: $f"
        exit 1
    fi
done

for f in "$PRYSMCTL" "$BEACON_BIN" "$VALIDATOR_BIN"; do
    if [[ ! -x "$f" ]]; then
        echo "ERROR: missing executable: $f"
        echo "Build Prysm first. See experiments/delay-4s/README.md"
        exit 1
    fi
done

# ------------------------------------------------------------
# 1. Generate Altair genesis
# ------------------------------------------------------------

echo
echo "===== [1/5] Generate genesis ====="

"$PRYSMCTL" testnet generate-genesis \
  --fork=altair \
  --genesis-time-delay=120 \
  --num-validators=1000 \
  --chain-config-file="$CFG" \
  --output-ssz="$BASE/genesis.ssz"

ls -lh "$BASE/genesis.ssz"

# ------------------------------------------------------------
# 2. Start beacon node
# ------------------------------------------------------------

echo
echo "===== [2/5] Start beacon node ====="

nohup env \
  EXPERIMENT_MALICIOUS_VALIDATORS_FILE="$GROUP" \
  EXPERIMENT_TOTAL_VALIDATORS=1000 \
  EXPERIMENT_WRITE_REWARDS=1 \
  EXPERIMENT_PARTIAL_HEAD_REWARD=1 \
  EXPERIMENT_REWARDS_CSV="$BASE/rewards.csv" \
  EXPERIMENT_PRIVATE_BLOCKS_PATH="$BASE/private_blocks.csv" \
  "$BEACON_BIN" \
    --datadir="$BASE/beacon-data" \
    --chain-config-file="$CFG" \
    --genesis-state="$BASE/genesis.ssz" \
    --interop-eth1data-votes \
    --min-sync-peers=0 \
    --bootstrap-node= \
    --force-clear-db \
    --accept-terms-of-use \
  > "$BASE/beacon.log" 2>&1 &

echo $! > "$BASE/beacon.pid"

echo "Beacon PID: $(cat "$BASE/beacon.pid")"

sleep 5

tail -30 "$BASE/beacon.log"

# ------------------------------------------------------------
# 3. Start Byzantine validators
# ------------------------------------------------------------

echo
echo "===== [3/5] Start Byzantine validators ====="

nohup env \
  EXPERIMENT_VALIDATOR_ROLE=byzantine \
  EXPERIMENT_MALICIOUS_VALIDATORS_FILE="$GROUP" \
  EXPERIMENT_TOTAL_VALIDATORS=1000 \
  EXPERIMENT_PRIVATE_BLOCKS_PATH="$BASE/private_blocks.csv" \
  "$VALIDATOR_BIN" \
    --datadir="$BASE/validator-byzantine-data" \
    --chain-config-file="$CFG" \
    --beacon-rpc-provider=127.0.0.1:4000 \
    --beacon-rest-api-provider=http://127.0.0.1:3500 \
    --interop-num-validators=333 \
    --interop-start-index=0 \
    --monitoring-port=8082 \
    --accept-terms-of-use \
  > "$BASE/validator-byzantine.log" 2>&1 &

echo $! > "$BASE/validator-byzantine.pid"

echo "Byzantine validator PID: $(cat "$BASE/validator-byzantine.pid")"

# ------------------------------------------------------------
# 4. Start honest validators
# ------------------------------------------------------------

echo
echo "===== [4/5] Start honest validators ====="

nohup env \
  EXPERIMENT_VALIDATOR_ROLE=honest \
  EXPERIMENT_MALICIOUS_VALIDATORS_FILE="$GROUP" \
  EXPERIMENT_TOTAL_VALIDATORS=1000 \
  EXPERIMENT_PRIVATE_BLOCKS_PATH="$BASE/private_blocks.csv" \
  "$VALIDATOR_BIN" \
    --datadir="$BASE/validator-honest-data" \
    --chain-config-file="$CFG" \
    --beacon-rpc-provider=127.0.0.1:4000 \
    --beacon-rest-api-provider=http://127.0.0.1:3500 \
    --interop-num-validators=667 \
    --interop-start-index=333 \
    --monitoring-port=8081 \
    --accept-terms-of-use \
  > "$BASE/validator-honest.log" 2>&1 &

echo $! > "$BASE/validator-honest.pid"

echo "Honest validator PID: $(cat "$BASE/validator-honest.pid")"

# ------------------------------------------------------------
# 5. Basic health check
# ------------------------------------------------------------

echo
echo "===== [5/5] Wait and check experiment ====="

sleep 90

echo
echo "===== Running processes ====="

pgrep -af "beacon-chain_/beacon-chain|validator_/validator" || true

echo
echo "===== Byzantine validator log ====="
tail -30 "$BASE/validator-byzantine.log"

echo
echo "===== Honest validator log ====="
tail -30 "$BASE/validator-honest.log"

echo
echo "===== Beacon head ====="

curl -s http://127.0.0.1:3500/eth/v1/beacon/headers/head \
  | python3 -c 'import sys,json; print("slot="+json.load(sys.stdin)["data"]["header"]["message"]["slot"])'

echo
echo "============================================================"
echo " Experiment started"
echo
echo " Results directory:"
echo "   $BASE"
echo
echo " Important outputs:"
echo "   $BASE/rewards.csv"
echo "   $BASE/private_blocks.csv"
echo "   $BASE/beacon.log"
echo "   $BASE/validator-byzantine.log"
echo "   $BASE/validator-honest.log"
echo "============================================================"

