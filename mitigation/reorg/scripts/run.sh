#!/usr/bin/env bash
set -euo pipefail

# ============================================================
# Reorganization experiment launcher
#
# Usage:
#   bash mitigation/reorg/scripts/run.sh k2
#   bash mitigation/reorg/scripts/run.sh k3
#   bash mitigation/reorg/scripts/run.sh k4
#
# Reorg experiments are based on Prysm v7.1.4:
#   1756380c2e84e90004df0a6268f8c28d832f5ab6
# ============================================================

MODE="${1:-}"

case "$MODE" in
  k2|k3|k4)
    ;;
  *)
    echo "Usage: $0 {k2|k3|k4}"
    exit 1
    ;;
esac

# ------------------------------------------------------------
# Paths
# ------------------------------------------------------------

SUBMISSION_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

REORG_BASE_COMMIT="1756380c2e84e90004df0a6268f8c28d832f5ab6"

CFG="$SUBMISSION_ROOT/experiments/reorg/common/devnet_config.yaml"
GROUP="$SUBMISSION_ROOT/experiments/reorg/common/groups/byz_333_first.txt"

# Keep the actual experiment source separate from artifact/submission.
WORKTREE="${REORG_WORKTREE:-${SUBMISSION_ROOT}-reorg-mitigation-${MODE}}"

# Generated chain data and outputs.
BASE="${BASE:-$SUBMISSION_ROOT/../reorg-mitigation-artifacts/${MODE}}"

case "$MODE" in
  k2)
    K=2
    PATCH="$SUBMISSION_ROOT/mitigation/reorg/k2/mitigation.patch"
    ENV_FILE="$SUBMISSION_ROOT/mitigation/reorg/k2/333byz.env"
    ;;
  k3)
    K=3
    PATCH="$SUBMISSION_ROOT/mitigation/reorg/k3/mitigation.patch"
    ENV_FILE="$SUBMISSION_ROOT/mitigation/reorg/k3/333byz.env"
    ;;
  k4)
    K=4
    PATCH="$SUBMISSION_ROOT/mitigation/reorg/k4/mitigation.patch"
    ENV_FILE="$SUBMISSION_ROOT/mitigation/reorg/k4/333byz.env"
    ;;
esac

echo "============================================================"
echo " Reorganization Experiment"
echo "============================================================"
echo "MODE            = $MODE"
echo "SUBMISSION_ROOT = $SUBMISSION_ROOT"
echo "WORKTREE        = $WORKTREE"
echo "BASE            = $BASE"
echo "CFG             = $CFG"
echo "GROUP           = $GROUP"
echo

# ------------------------------------------------------------
# Check artifact files
# ------------------------------------------------------------

for f in "$CFG" "$GROUP" "$PATCH" "$ENV_FILE"; do
  if [[ ! -f "$f" ]]; then
    echo "ERROR: missing required file:"
    echo "  $f"
    exit 1
  fi
done

# ------------------------------------------------------------
# 1. Prepare Prysm v7.1.4 worktree
# ------------------------------------------------------------

echo "===== [1/8] Prepare Prysm source ====="

if [[ -e "$WORKTREE" ]]; then
  if [[ -f "$WORKTREE/.git" || -d "$WORKTREE/.git" ]]; then
    echo "Existing worktree found; resetting to reorg baseline."
    git -C "$WORKTREE" reset --hard "$REORG_BASE_COMMIT"
    git -C "$WORKTREE" clean -fd
  else
    echo "ERROR: $WORKTREE exists but is not a Git worktree."
    echo "Remove it or set REORG_WORKTREE to another path."
    exit 1
  fi
else
  git -C "$SUBMISSION_ROOT" worktree add \
    --detach \
    "$WORKTREE" \
    "$REORG_BASE_COMMIT"
fi

echo "Applying experiment patch:"
echo "  $PATCH"

git -C "$WORKTREE" apply "$PATCH"

# ------------------------------------------------------------
# 2. Build Prysm
# ------------------------------------------------------------

echo
echo "===== [2/8] Build Prysm ====="

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
echo "===== [3/8] Clean previous run ====="

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
  "$BASE/validator-data-byz" \
  "$BASE/validator-data-honest"

rm -f \
  "$BASE/genesis.ssz" \
  "$BASE/beacon.log" \
  "$BASE/validator-byz.log" \
  "$BASE/validator-honest.log" \
  "$BASE/rewards.csv" \
  "$BASE/reorg_events.csv" \
  "$BASE/reorg_windows.csv" \
  "$BASE/reorg_results.csv" \
  "$BASE/reorg_private_roots.csv" \
  "$BASE"/*.pid

# ------------------------------------------------------------
# 4. Configure experiment environment
# ------------------------------------------------------------

echo
echo "===== [4/8] Configure experiment ====="

# Remove variables inherited from an earlier experiment.
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

export EXPERIMENT_REORG_EVENTS_PATH="$BASE/reorg_events.csv"
export EXPERIMENT_REORG_WINDOWS_FILE="$BASE/reorg_windows.csv"
export EXPERIMENT_REORG_RESULTS_CSV="$BASE/reorg_results.csv"
export EXPERIMENT_REORG_PRIVATE_ROOTS_CSV="$BASE/reorg_private_roots.csv"

# Attack experiments must not accidentally inherit mitigation.

echo "EXPERIMENT_ATTACK_MODE=$EXPERIMENT_ATTACK_MODE"

echo "EXPERIMENT_REORG_K=$EXPERIMENT_REORG_K"
echo "EXPERIMENT_PARTIAL_HEAD_REWARD=$EXPERIMENT_PARTIAL_HEAD_REWARD"

# ------------------------------------------------------------
# 5. Generate Altair genesis
# ------------------------------------------------------------

echo
echo "===== [5/8] Generate Altair genesis ====="

"$PRYSMCTL" testnet generate-genesis \
  --fork=altair \
  --genesis-time-delay=120 \
  --num-validators=1000 \
  --chain-config-file="$CFG" \
  --output-ssz="$BASE/genesis.ssz"

ls -lh "$BASE/genesis.ssz"

# ------------------------------------------------------------
# 6. Start beacon node
# ------------------------------------------------------------

echo
echo "===== [6/8] Start beacon node ====="

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

echo "Beacon PID: $(cat "$BASE/beacon.pid")"

sleep 8

# ------------------------------------------------------------
# 7. Start validator clients
# ------------------------------------------------------------

echo
echo "===== [7/8] Start validator clients ====="

  # ----------------------------------------------------------
  # Reorg attack
  #
  # Both clients load all 1000 validators because the reorg
  # implementation needs the complete duty schedule.
  #
  # ShouldRunValidatorDuty() performs the actual split:
  #
  #   role=byzantine -> validators in malicious group
  #   role=honest    -> validators outside malicious group
  # ----------------------------------------------------------

  echo "Starting Byzantine validator client"

  nohup env \
    EXPERIMENT_VALIDATOR_ROLE=byzantine \
    "$VALIDATOR_BIN" \
      --datadir="$BASE/validator-data-byz" \
      --chain-config-file="$CFG" \
      --beacon-rpc-provider=127.0.0.1:4000 \
      --beacon-rest-api-provider=http://127.0.0.1:3500 \
      --interop-num-validators=1000 \
      --interop-start-index=0 \
      --monitoring-port=8082 \
      --accept-terms-of-use \
    > "$BASE/validator-byz.log" 2>&1 &

  echo $! > "$BASE/validator-byzantine.pid"

  echo "Starting honest validator client"

  nohup env \
    EXPERIMENT_VALIDATOR_ROLE=honest \
    "$VALIDATOR_BIN" \
      --datadir="$BASE/validator-data-honest" \
      --chain-config-file="$CFG" \
      --beacon-rpc-provider=127.0.0.1:4000 \
      --beacon-rest-api-provider=http://127.0.0.1:3500 \
      --interop-num-validators=1000 \
      --interop-start-index=0 \
      --monitoring-port=8081 \
      --accept-terms-of-use \
    > "$BASE/validator-honest.log" 2>&1 &

  echo $! > "$BASE/validator-honest.pid"

# ------------------------------------------------------------
# 8. Health check
# ------------------------------------------------------------

echo
echo "===== [8/8] Health check ====="

sleep 15

echo
echo "Running processes:"
pgrep -af "beacon-chain_/beacon-chain|validator_/validator" || true

echo
echo "Beacon log:"
tail -30 "$BASE/beacon.log" || true

echo
echo "Byzantine validator log:"
tail -20 "$BASE/validator-byz.log" || true

echo
echo "Honest validator log:"
tail -20 "$BASE/validator-honest.log" || true

echo
echo "============================================================"
echo " Experiment started successfully"
echo
echo " Mode:"
echo "   $MODE"
echo
echo " Results:"
echo "   $BASE"
echo
echo " Main output:"
echo "   $BASE/rewards.csv"

echo "   $BASE/reorg_events.csv"
echo "   $BASE/reorg_windows.csv"
echo "   $BASE/reorg_results.csv"
echo "   $BASE/reorg_private_roots.csv"

echo
echo " Logs:"
echo "   $BASE/beacon.log"
echo "   $BASE/validator-byz.log"
echo "   $BASE/validator-honest.log"
echo "============================================================"
