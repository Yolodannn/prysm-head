#!/usr/bin/env bash
set -euo pipefail

# ============================================================
# Delay-4s attack experiment launcher
#
# Prysm baseline:
#   e9064111a4c1fb1707446f2e9d19caed1db462f9
# ============================================================

SUBMISSION_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

DELAY_BASE_COMMIT="e9064111a4c1fb1707446f2e9d19caed1db462f9"

CFG="$SUBMISSION_ROOT/experiments/delay-4s/devnet_config.yaml"
GROUP="$SUBMISSION_ROOT/experiments/delay-4s/groups/byz_333_first.txt"
PATCH="$SUBMISSION_ROOT/experiments/delay-4s/attack.patch"
ENV_FILE="$SUBMISSION_ROOT/experiments/delay-4s/env/333byz.env"

WORKTREE="${DELAY_WORKTREE:-${SUBMISSION_ROOT}-delay-4s-attack}"
BASE="${BASE:-$SUBMISSION_ROOT/../delay-4s-artifacts}"

echo "============================================================"
echo " Delay-4s Attack Experiment"
echo "============================================================"
echo "SUBMISSION_ROOT = $SUBMISSION_ROOT"
echo "WORKTREE        = $WORKTREE"
echo "BASE            = $BASE"
echo "CFG             = $CFG"
echo "GROUP           = $GROUP"
echo

for f in "$CFG" "$GROUP" "$PATCH" "$ENV_FILE"; do
  if [[ ! -f "$f" ]]; then
    echo "ERROR: missing required file:"
    echo "  $f"
    exit 1
  fi
done

# ------------------------------------------------------------
# 1. Prepare clean Prysm worktree
# ------------------------------------------------------------

echo "===== [1/7] Prepare Prysm source ====="

if [[ -e "$WORKTREE" ]]; then
  if [[ -f "$WORKTREE/.git" || -d "$WORKTREE/.git" ]]; then
    echo "Existing worktree found; resetting to delay baseline."
    git -C "$WORKTREE" reset --hard "$DELAY_BASE_COMMIT"
    git -C "$WORKTREE" clean -fd
  else
    echo "ERROR: $WORKTREE exists but is not a Git worktree."
    exit 1
  fi
else
  git -C "$SUBMISSION_ROOT" worktree add \
    --detach \
    "$WORKTREE" \
    "$DELAY_BASE_COMMIT"
fi

echo "Applying attack patch:"
echo "  $PATCH"

git -C "$WORKTREE" apply "$PATCH"

# ------------------------------------------------------------
# 2. Build Prysm
# ------------------------------------------------------------

echo
echo "===== [2/7] Build Prysm ====="

cd "$WORKTREE"

bazel build \
  //cmd/prysmctl:prysmctl \
  //cmd/beacon-chain:beacon-chain \
  //cmd/validator:validator

PRYSMCTL="$WORKTREE/bazel-bin/cmd/prysmctl/prysmctl_/prysmctl"
BEACON_BIN="$WORKTREE/bazel-bin/cmd/beacon-chain/beacon-chain_/beacon-chain"
VALIDATOR_BIN="$WORKTREE/bazel-bin/cmd/validator/validator_/validator"

for f in "$PRYSMCTL" "$BEACON_BIN" "$VALIDATOR_BIN"; do
  if [[ ! -x "$f" ]]; then
    echo "ERROR: missing executable:"
    echo "  $f"
    exit 1
  fi
done

# ------------------------------------------------------------
# 3. Clean previous run
# ------------------------------------------------------------

echo
echo "===== [3/7] Clean previous run ====="

mkdir -p "$BASE"

for pidfile in \
  "$BASE/beacon.pid" \
  "$BASE/validator-byzantine.pid" \
  "$BASE/validator-honest.pid"
do
  if [[ -f "$pidfile" ]]; then
    pid="$(cat "$pidfile" 2>/dev/null || true)"
    if [[ -n "$pid" ]]; then
      kill "$pid" 2>/dev/null || true
    fi
  fi
done

sleep 2

rm -rf \
  "$BASE/beacon-data" \
  "$BASE/validator-byzantine-data" \
  "$BASE/validator-honest-data"

rm -f \
  "$BASE/genesis.ssz" \
  "$BASE/rewards.csv" \
  "$BASE/private_blocks.csv" \
  "$BASE/beacon.log" \
  "$BASE/validator-byzantine.log" \
  "$BASE/validator-honest.log" \
  "$BASE"/*.pid

# ------------------------------------------------------------
# 4. Configure experiment
# ------------------------------------------------------------

echo
echo "===== [4/7] Configure experiment ====="

while IFS='=' read -r name _; do
  if [[ "$name" == EXPERIMENT_* ]]; then
    unset "$name"
  fi
done < <(env)

source "$ENV_FILE"

export EXPERIMENT_MALICIOUS_VALIDATORS_FILE="$GROUP"
export EXPERIMENT_TOTAL_VALIDATORS=1000
export EXPERIMENT_WRITE_REWARDS=1
export EXPERIMENT_REWARDS_CSV="$BASE/rewards.csv"
export EXPERIMENT_PRIVATE_BLOCKS_PATH="$BASE/private_blocks.csv"

# Attack experiment must not inherit mitigation.
unset EXPERIMENT_PARTIAL_HEAD_REWARD

# ------------------------------------------------------------
# 5. Generate Altair genesis
# ------------------------------------------------------------

echo
echo "===== [5/7] Generate Altair genesis ====="

"$PRYSMCTL" testnet generate-genesis \
  --fork=altair \
  --genesis-time-delay=120 \
  --num-validators=1000 \
  --chain-config-file="$CFG" \
  --output-ssz="$BASE/genesis.ssz"

# ------------------------------------------------------------
# 6. Start beacon node and validators
# ------------------------------------------------------------

echo
echo "===== [6/7] Start beacon node ====="

nohup "$BEACON_BIN" \
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

sleep 8

echo "Starting Byzantine validators: 0-332"

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

echo "Starting honest validators: 333-999"

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

# ------------------------------------------------------------
# 7. Health check
# ------------------------------------------------------------

echo
echo "===== [7/7] Health check ====="

sleep 15

echo
echo "Running processes:"
pgrep -af "beacon-chain_/beacon-chain|validator_/validator" || true

echo
echo "Beacon log:"
tail -30 "$BASE/beacon.log" || true

echo
echo "Byzantine validator log:"
tail -20 "$BASE/validator-byzantine.log" || true

echo
echo "Honest validator log:"
tail -20 "$BASE/validator-honest.log" || true

echo
echo "============================================================"
echo " Experiment started successfully"
echo
echo " Results:"
echo "   $BASE"
echo
echo " Main outputs:"
echo "   $BASE/rewards.csv"
echo "   $BASE/private_blocks.csv"
echo
echo " Logs:"
echo "   $BASE/beacon.log"
echo "   $BASE/validator-byzantine.log"
echo "   $BASE/validator-honest.log"
echo "============================================================"
