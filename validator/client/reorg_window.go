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

	StartSlot           primitives.Slot
	PrivateSlot1        primitives.Slot
	PrivateSlot2        primitives.Slot
	PrivateSlot3        primitives.Slot
	IsolatedHonestSlot1 primitives.Slot
	IsolatedHonestSlot2 primitives.Slot
	ReleaseSlot         primitives.Slot

	ByzProposer1            primitives.ValidatorIndex
	ByzProposer2            primitives.ValidatorIndex
	ByzProposer3            primitives.ValidatorIndex
	IsolatedHonestProposer1 primitives.ValidatorIndex
	IsolatedHonestProposer2 primitives.ValidatorIndex
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
	case w.PrivateSlot3:
		return "private_slot_3"
	case w.IsolatedHonestSlot1:
		return "isolated_honest_slot_1"
	case w.IsolatedHonestSlot2:
		return "isolated_honest_slot_2"
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
		"epoch":                   w.Epoch,
		"startSlot":               w.StartSlot,
		"privateSlot1":            w.PrivateSlot1,
		"privateSlot2":            w.PrivateSlot2,
		"privateSlot3":            w.PrivateSlot3,
		"isolatedHonestSlot1":     w.IsolatedHonestSlot1,
		"isolatedHonestSlot2":     w.IsolatedHonestSlot2,
		"releaseSlot":             w.ReleaseSlot,
		"byzProposer1":            w.ByzProposer1,
		"byzProposer2":            w.ByzProposer2,
		"byzProposer3":            w.ByzProposer3,
		"isolatedHonestProposer1": w.IsolatedHonestProposer1,
		"isolatedHonestProposer2": w.IsolatedHonestProposer2,
	}
}

// detectNaturalReorgWindows scans the cached proposer duty schedule and records
// every non-overlapping natural B,B,B,H,H pattern:
//
//	slot s     : Byzantine proposer
//	slot s+1   : Byzantine proposer
//	slot s+2   : Byzantine proposer
//	slot s+3   : Honest proposer to be isolated
//	slot s+4   : Honest proposer to be isolated
//	slot s+5   : release slot
func (v *validator) detectNaturalReorgWindows(epochStartSlot primitives.Slot) {
	schedule := v.duties.ProposerSchedule()
	if len(schedule) == 0 {
		return
	}

	slotsPerEpoch := params.BeaconConfig().SlotsPerEpoch
	epoch := slots.ToEpoch(epochStartSlot)

	for offset := primitives.Slot(0); offset+5 < slotsPerEpoch; offset++ {
		s := epochStartSlot + offset

		p0, ok0 := schedule[s]
		p1, ok1 := schedule[s+1]
		p2, ok2 := schedule[s+2]
		p3, ok3 := schedule[s+3]
		p4, ok4 := schedule[s+4]
		if !ok0 || !ok1 || !ok2 || !ok3 || !ok4 {
			continue
		}

		b0 := experiment.IsMaliciousValidator(uint64(p0), 0)
		b1 := experiment.IsMaliciousValidator(uint64(p1), 0)
		b2 := experiment.IsMaliciousValidator(uint64(p2), 0)
		b3 := experiment.IsMaliciousValidator(uint64(p3), 0)
		b4 := experiment.IsMaliciousValidator(uint64(p4), 0)

		if !b0 || !b1 || !b2 || b3 || b4 {
			continue
		}

		w := reorgWindow{
			Epoch:                   epoch,
			StartSlot:               s,
			PrivateSlot1:            s,
			PrivateSlot2:            s + 1,
			PrivateSlot3:            s + 2,
			IsolatedHonestSlot1:     s + 3,
			IsolatedHonestSlot2:     s + 4,
			ReleaseSlot:             s + 5,
			ByzProposer1:            p0,
			ByzProposer2:            p1,
			ByzProposer3:            p2,
			IsolatedHonestProposer1: p3,
			IsolatedHonestProposer2: p4,
		}

		if recordReorgWindow(w) {
			if experiment.ShouldWriteReorgWindows() {
				if err := experiment.AppendReorgWindow(experiment.ReorgWindow{
					Epoch:                   uint64(w.Epoch),
					StartSlot:               uint64(w.StartSlot),
					PrivateSlot1:            uint64(w.PrivateSlot1),
					PrivateSlot2:            uint64(w.PrivateSlot2),
					PrivateSlot3:            uint64(w.PrivateSlot3),
					IsolatedHonestSlot1:     uint64(w.IsolatedHonestSlot1),
					IsolatedHonestSlot2:     uint64(w.IsolatedHonestSlot2),
					ReleaseSlot:             uint64(w.ReleaseSlot),
					ByzProposer1:            uint64(w.ByzProposer1),
					ByzProposer2:            uint64(w.ByzProposer2),
					ByzProposer3:            uint64(w.ByzProposer3),
					IsolatedHonestProposer1: uint64(w.IsolatedHonestProposer1),
					IsolatedHonestProposer2: uint64(w.IsolatedHonestProposer2),
				}); err != nil {
					log.WithError(err).WithFields(reorgWindowLogFields(w)).Warn("[REORG] Failed to write B,B,B,H,H attack window")
				}
			}
			log.WithFields(reorgWindowLogFields(w)).Warn("[REORG] Natural B,B,B,H,H attack window scheduled")
		} else {
			log.WithFields(reorgWindowLogFields(w)).Warn("[REORG] Natural B,B,B,H,H attack window skipped due to overlap")
		}
	}
}
