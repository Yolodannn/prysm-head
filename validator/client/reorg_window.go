package client

import (
	"sync"

	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v7/runtime/experiment"
	"github.com/OffchainLabs/prysm/v7/time/slots"
	"github.com/sirupsen/logrus"
)

type reorgWindow struct {
	Epoch primitives.Epoch

	StartSlot          primitives.Slot
	PrivateSlot1       primitives.Slot
	PrivateSlot2       primitives.Slot
	IsolatedHonestSlot primitives.Slot
	ReleaseSlot        primitives.Slot

	ByzProposer1           primitives.ValidatorIndex
	ByzProposer2           primitives.ValidatorIndex
	IsolatedHonestProposer primitives.ValidatorIndex
}

var reorgWindows = struct {
	sync.RWMutex
	bySlot  map[primitives.Slot]reorgWindow
	byStart map[primitives.Slot]reorgWindow
}{
	bySlot:  make(map[primitives.Slot]reorgWindow),
	byStart: make(map[primitives.Slot]reorgWindow),
}

func reorgWindowForSlot(slot primitives.Slot) (reorgWindow, bool) {
	reorgWindows.RLock()
	defer reorgWindows.RUnlock()

	w, ok := reorgWindows.bySlot[slot]
	return w, ok
}

func reorgPhaseForSlot(slot primitives.Slot) string {
	w, ok := reorgWindowForSlot(slot)
	if !ok {
		return ""
	}

	switch slot {
	case w.PrivateSlot1:
		return "private_slot_1"
	case w.PrivateSlot2:
		return "private_slot_2"
	case w.IsolatedHonestSlot:
		return "isolated_honest_slot"
	case w.ReleaseSlot:
		return "release_slot"
	default:
		return "inside_window"
	}
}

func recordReorgWindow(w reorgWindow) bool {
	reorgWindows.Lock()
	defer reorgWindows.Unlock()

	if _, exists := reorgWindows.byStart[w.StartSlot]; exists {
		return false
	}

	for slot := w.StartSlot; slot <= w.ReleaseSlot; slot++ {
		if _, overlaps := reorgWindows.bySlot[slot]; overlaps {
			return false
		}
	}

	reorgWindows.byStart[w.StartSlot] = w
	for slot := w.StartSlot; slot <= w.ReleaseSlot; slot++ {
		reorgWindows.bySlot[slot] = w
	}

	return true
}

func reorgWindowLogFields(w reorgWindow) logrus.Fields {
	return logrus.Fields{
		"epoch":                  w.Epoch,
		"startSlot":              w.StartSlot,
		"privateSlot1":           w.PrivateSlot1,
		"privateSlot2":           w.PrivateSlot2,
		"isolatedHonestSlot":     w.IsolatedHonestSlot,
		"releaseSlot":            w.ReleaseSlot,
		"byzProposer1":           w.ByzProposer1,
		"byzProposer2":           w.ByzProposer2,
		"isolatedHonestProposer": w.IsolatedHonestProposer,
	}
}

// detectNaturalReorgWindows scans the cached proposer duty schedule and records
// every non-overlapping natural B,B,H pattern:
//
//	slot s     : Byzantine proposer
//	slot s+1   : Byzantine proposer
//	slot s+2   : Honest proposer to be isolated
//	slot s+3   : release slot
//
// This function only schedules candidate windows.
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

		if !b0 || !b1 || b2 {
			continue
		}

		w := reorgWindow{
			Epoch:                  epoch,
			StartSlot:              s,
			PrivateSlot1:           s,
			PrivateSlot2:           s + 1,
			IsolatedHonestSlot:     s + 2,
			ReleaseSlot:            s + 3,
			ByzProposer1:           p0,
			ByzProposer2:           p1,
			IsolatedHonestProposer: p2,
		}

		if recordReorgWindow(w) {
			log.WithFields(reorgWindowLogFields(w)).Warn("[REORG] Natural B,B,H attack window scheduled")
		} else {
			log.WithFields(reorgWindowLogFields(w)).Warn("[REORG] Natural B,B,H attack window skipped due to overlap")
		}
	}
}
