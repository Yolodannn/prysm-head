package altair

import (
	"bytes"
	"os"
	"sync"

	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/helpers"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/state"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	ethpb "github.com/OffchainLabs/prysm/v7/proto/prysm/v1alpha1"
)

const experimentPartialHeadRewardDenominatorBps uint64 = 10000

type experimentPartialHeadRewardKey struct {
	epoch primitives.Epoch
	index primitives.ValidatorIndex
}

var (
	experimentPartialHeadRewardMu      sync.Mutex
	experimentPartialHeadRewardWeights = make(map[experimentPartialHeadRewardKey]uint64)
)

func experimentPartialHeadRewardEnabled() bool {
	return os.Getenv("EXPERIMENT_PARTIAL_HEAD_REWARD") == "1"
}

func experimentHeadWeightBpsForDistance(distance uint64) uint64 {
	switch distance {
	case 0:
		return 10000
	case 1:
		return 7200
	case 2:
		return 5100
	case 3:
		return 1400
	default:
		return 0
	}
}

func experimentPartialHeadWeightBpsForData(
	beaconState state.ReadOnlyBeaconState,
	data *ethpb.AttestationData,
) (uint64, bool, error) {
	if data == nil {
		return 0, false, nil
	}

	for distance := uint64(0); distance <= 3; distance++ {
		if data.Slot < primitives.Slot(distance) {
			continue
		}
		root, err := helpers.BlockRootAtSlot(beaconState, data.Slot-primitives.Slot(distance))
		if err != nil {
			return 0, false, err
		}
		if bytes.Equal(data.BeaconBlockRoot, root) {
			return experimentHeadWeightBpsForDistance(distance), true, nil
		}
	}

	return 0, false, nil
}

func experimentRecordPartialHeadReward(
	epoch primitives.Epoch,
	index primitives.ValidatorIndex,
	weightBps uint64,
) {
	if !experimentPartialHeadRewardEnabled() || weightBps == 0 {
		return
	}

	k := experimentPartialHeadRewardKey{
		epoch: epoch,
		index: index,
	}

	experimentPartialHeadRewardMu.Lock()
	defer experimentPartialHeadRewardMu.Unlock()

	if prev, ok := experimentPartialHeadRewardWeights[k]; !ok || weightBps > prev {
		experimentPartialHeadRewardWeights[k] = weightBps
	}
}

func experimentLookupPartialHeadReward(
	epoch primitives.Epoch,
	index primitives.ValidatorIndex,
) (uint64, bool) {
	if !experimentPartialHeadRewardEnabled() {
		return 0, false
	}

	k := experimentPartialHeadRewardKey{
		epoch: epoch,
		index: index,
	}

	experimentPartialHeadRewardMu.Lock()
	defer experimentPartialHeadRewardMu.Unlock()

	weightBps, ok := experimentPartialHeadRewardWeights[k]
	return weightBps, ok
}
