package client

import (
	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v7/runtime/experiment"
	"github.com/OffchainLabs/prysm/v7/time/slots"
	"github.com/sirupsen/logrus"
)

// detectNaturalReorgWindows scans the cached proposer duty schedule and finds
// natural B,B,H patterns:
//
//	slot s     : Byzantine proposer
//	slot s+1   : Byzantine proposer
//	slot s+2   : Honest proposer to be isolated
//	slot s+3   : release slot
//
// This function only detects and logs candidate windows.
// It does not change block production or networking behavior.
func (v *validator) detectNaturalReorgWindows(epochStartSlot primitives.Slot) {
	schedule := v.duties.ProposerSchedule()
	if len(schedule) == 0 {
		return
	}

	slotsPerEpoch := params.BeaconConfig().SlotsPerEpoch
	epoch := slots.ToEpoch(epochStartSlot)

	for offset := primitives.Slot(0); offset+3 < slotsPerEpoch; offset++ {
		s := epochStartSlot + offset

		p0, ok0 := schedule[s]
		p1, ok1 := schedule[s+1]
		p2, ok2 := schedule[s+2]
		if !ok0 || !ok1 || !ok2 {
			continue
		}

		b0 := experiment.IsMaliciousValidator(uint64(p0), 0)
		b1 := experiment.IsMaliciousValidator(uint64(p1), 0)
		b2 := experiment.IsMaliciousValidator(uint64(p2), 0)

		if b0 && b1 && !b2 {
			log.WithFields(logrus.Fields{
				"epoch":                  epoch,
				"startSlot":              s,
				"privateSlot1":           s,
				"privateSlot2":           s + 1,
				"isolatedHonestSlot":     s + 2,
				"releaseSlot":            s + 3,
				"byzProposer1":           p0,
				"byzProposer2":           p1,
				"isolatedHonestProposer": p2,
			}).Warn("[REORG] Natural B,B,H attack window detected")
		}
	}
}
