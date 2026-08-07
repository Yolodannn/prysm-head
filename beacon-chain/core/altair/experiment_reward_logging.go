package altair

import (
	"encoding/csv"
	"os"

	"github.com/OffchainLabs/prysm/v7/io/file"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/epoch/precompute"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/time"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/state"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v7/runtime/experiment"
)

const defaultExperimentRewardCSVPath = "rewards.csv"

func experimentRewardCSVPath() string {
	if p := os.Getenv("EXPERIMENT_REWARDS_CSV"); p != "" {
		return p
	}
	return defaultExperimentRewardCSVPath
}

var experimentRewardStartupLogOnce sync.Once

// EXPERIMENT: per-validator source/target/head reward logging.
// Local devnet only. This logs previous-epoch attestation reward components from
// the Altair-style reward precompute path used by Altair and later forks.
func writeExperimentRewardCSV(beaconState state.ReadOnlyBeaconState, vals []*precompute.Validator, deltas []*AttDelta) {
	if os.Getenv("EXPERIMENT_WRITE_REWARDS") != "1" {
		return
	}
	rewardCSVPath := experimentRewardCSVPath()
	if len(vals) != len(deltas) {
		log.WithFields(map[string]any{
			"validators": len(vals),
			"deltas":     len(deltas),
		}).Error("EXPERIMENT: reward CSV skipped because validator and delta lengths differ")
		return
	}

	totalValidators := uint64(beaconState.NumValidators())
	experimentRewardStartupLogOnce.Do(func() {
		log.Info(experiment.StartupLog(totalValidators))
	})

	if err := file.MkdirAll(filepath.Dir(rewardCSVPath)); err != nil {
		log.WithError(err).Error("EXPERIMENT: could not create reward CSV directory")
		return
	}

	info, statErr := os.Stat(rewardCSVPath)
	newFile := os.IsNotExist(statErr) || (statErr == nil && info.Size() == 0)
	f, err := os.OpenFile(rewardCSVPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.WithError(err).Error("EXPERIMENT: could not open reward CSV")
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.WithError(err).Error("EXPERIMENT: could not close reward CSV")
		}
	}()

	if newFile {
		log.WithField("path", rewardCSVPath).Info("EXPERIMENT: reward CSV created/opened")
	} else {
		log.WithField("path", rewardCSVPath).Info("EXPERIMENT: reward CSV opened")
	}

	w := csv.NewWriter(f)
	if newFile {
		if err := w.Write([]string{"epoch", "validator_index", "is_malicious", "malicious_fraction", "source_reward", "target_reward", "head_reward", "total_reward"}); err != nil {
			log.WithError(err).Error("EXPERIMENT: could not write reward CSV header")
			return
		}
	}

	epoch := time.PrevEpoch(beaconState)
	rows := 0
	for i, val := range vals {
		if !val.IsActivePrevEpoch {
			continue
		}
		idx := primitives.ValidatorIndex(i)
		delta := deltas[i]
		total := delta.SourceReward + delta.TargetReward + delta.HeadReward
		if err := w.Write([]string{
			strconv.FormatUint(uint64(epoch), 10),
			strconv.FormatUint(uint64(idx), 10),
			strconv.FormatBool(experiment.IsMaliciousValidator(uint64(idx), 0)),
			experiment.MaliciousFractionString(),
			strconv.FormatUint(delta.SourceReward, 10),
			strconv.FormatUint(delta.TargetReward, 10),
			strconv.FormatUint(delta.HeadReward, 10),
			strconv.FormatUint(total, 10),
		}); err != nil {
			log.WithError(err).Error("EXPERIMENT: could not write reward CSV row")
			return
		}
		rows++
	}

	w.Flush()
	if err := w.Error(); err != nil {
		log.WithError(err).Error("EXPERIMENT: could not flush reward CSV")
		return
	}

	log.WithFields(map[string]any{
		"path":  rewardCSVPath,
		"epoch": epoch,
		"rows":  rows,
	}).Info("EXPERIMENT: epoch rewards written")
}
