package experiment

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const defaultReorgEventsPath = "/home/suliudan2001/workspace/devnet/reorg_events.csv"

var reorgEventsMu sync.Mutex

func ReorgEventsPath() string {
	path := strings.TrimSpace(os.Getenv("EXPERIMENT_REORG_EVENTS_PATH"))
	if path == "" {
		return defaultReorgEventsPath
	}
	return path
}

func WriteReorgEvent(
	event string,
	phase string,
	slot uint64,
	validatorIndex uint64,
	isMalicious bool,
	blockRoot string,
	parentRoot string,
	publicHeadRoot string,
	privateHeadRoot string,
	releaseSlot uint64,
) {
	if !IsReorgMode() {
		return
	}

	reorgEventsMu.Lock()
	defer reorgEventsMu.Unlock()

	path := ReorgEventsPath()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}

	info, statErr := os.Stat(path)
	newFile := os.IsNotExist(statErr) || (statErr == nil && info.Size() == 0)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer func() {
		_ = f.Close()
	}()

	w := csv.NewWriter(f)

	if newFile {
		if err := w.Write([]string{
			"event",
			"phase",
			"slot",
			"validator_index",
			"is_malicious",
			"block_root",
			"parent_root",
			"public_head_root",
			"private_head_root",
			"release_slot",
		}); err != nil {
			return
		}
	}

	if err := w.Write([]string{
		event,
		phase,
		strconv.FormatUint(slot, 10),
		strconv.FormatUint(validatorIndex, 10),
		strconv.FormatBool(isMalicious),
		blockRoot,
		parentRoot,
		publicHeadRoot,
		privateHeadRoot,
		strconv.FormatUint(releaseSlot, 10),
	}); err != nil {
		return
	}

	w.Flush()
}

type ReorgWindow struct {
	Epoch uint64

	StartSlot          uint64
	PrivateSlot1       uint64
	PrivateSlot2       uint64
	IsolatedHonestSlot uint64
	ReleaseSlot        uint64

	ByzProposer1           uint64
	ByzProposer2           uint64
	IsolatedHonestProposer uint64
}

func ReorgWindowsFilePath() string {
	return strings.TrimSpace(os.Getenv("EXPERIMENT_REORG_WINDOWS_FILE"))
}

func ShouldWriteReorgWindows() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("EXPERIMENT_VALIDATOR_ROLE")), "byzantine")
}

func AppendReorgWindow(w ReorgWindow) error {
	path := ReorgWindowsFilePath()
	if path == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	needHeader := false
	if st, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			needHeader = true
		} else {
			return err
		}
	} else if st.Size() == 0 {
		needHeader = true
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	cw := csv.NewWriter(f)
	if needHeader {
		if err := cw.Write([]string{
			"epoch",
			"start_slot",
			"private_slot_1",
			"private_slot_2",
			"isolated_honest_slot",
			"release_slot",
			"byz_proposer_1",
			"byz_proposer_2",
			"isolated_honest_proposer",
		}); err != nil {
			return err
		}
	}

	if err := cw.Write([]string{
		strconv.FormatUint(w.Epoch, 10),
		strconv.FormatUint(w.StartSlot, 10),
		strconv.FormatUint(w.PrivateSlot1, 10),
		strconv.FormatUint(w.PrivateSlot2, 10),
		strconv.FormatUint(w.IsolatedHonestSlot, 10),
		strconv.FormatUint(w.ReleaseSlot, 10),
		strconv.FormatUint(w.ByzProposer1, 10),
		strconv.FormatUint(w.ByzProposer2, 10),
		strconv.FormatUint(w.IsolatedHonestProposer, 10),
	}); err != nil {
		return err
	}

	cw.Flush()
	return cw.Error()
}
