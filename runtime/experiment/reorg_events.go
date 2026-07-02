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

	StartSlot           uint64
	PrivateSlot1        uint64
	PrivateSlot2        uint64
	PrivateSlot3        uint64
	PrivateSlot4        uint64
	IsolatedHonestSlot1 uint64
	IsolatedHonestSlot2 uint64
	IsolatedHonestSlot3 uint64
	ReleaseSlot         uint64

	ByzProposer1            uint64
	ByzProposer2            uint64
	ByzProposer3            uint64
	ByzProposer4            uint64
	IsolatedHonestProposer1 uint64
	IsolatedHonestProposer2 uint64
	IsolatedHonestProposer3 uint64
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
			"private_slot_3",
			"private_slot_4",
			"isolated_honest_slot_1",
			"isolated_honest_slot_2",
			"isolated_honest_slot_3",
			"release_slot",
			"byz_proposer_1",
			"byz_proposer_2",
			"byz_proposer_3",
			"byz_proposer_4",
			"isolated_honest_proposer_1",
			"isolated_honest_proposer_2",
			"isolated_honest_proposer_3",
		}); err != nil {
			return err
		}
	}

	if err := cw.Write([]string{
		strconv.FormatUint(w.Epoch, 10),
		strconv.FormatUint(w.StartSlot, 10),
		strconv.FormatUint(w.PrivateSlot1, 10),
		strconv.FormatUint(w.PrivateSlot2, 10),
		strconv.FormatUint(w.PrivateSlot3, 10),
		strconv.FormatUint(w.PrivateSlot4, 10),
		strconv.FormatUint(w.IsolatedHonestSlot1, 10),
		strconv.FormatUint(w.IsolatedHonestSlot2, 10),
		strconv.FormatUint(w.IsolatedHonestSlot3, 10),
		strconv.FormatUint(w.ReleaseSlot, 10),
		strconv.FormatUint(w.ByzProposer1, 10),
		strconv.FormatUint(w.ByzProposer2, 10),
		strconv.FormatUint(w.ByzProposer3, 10),
		strconv.FormatUint(w.ByzProposer4, 10),
		strconv.FormatUint(w.IsolatedHonestProposer1, 10),
		strconv.FormatUint(w.IsolatedHonestProposer2, 10),
		strconv.FormatUint(w.IsolatedHonestProposer3, 10),
	}); err != nil {
		return err
	}

	cw.Flush()
	return cw.Error()
}

func ReadReorgWindows() ([]ReorgWindow, error) {
	path := ReorgWindowsFilePath()
	if path == "" {
		return nil, nil
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() {
		_ = f.Close()
	}()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}

	windows := make([]ReorgWindow, 0, len(rows))
	for i, row := range rows {
		if i == 0 {
			continue
		}
		if len(row) < 17 {
			continue
		}

		vals := make([]uint64, 17)
		ok := true
		for j := range vals {
			v, err := strconv.ParseUint(strings.TrimSpace(row[j]), 10, 64)
			if err != nil {
				ok = false
				break
			}
			vals[j] = v
		}
		if !ok {
			continue
		}

		windows = append(windows, ReorgWindow{
			Epoch:                   vals[0],
			StartSlot:               vals[1],
			PrivateSlot1:            vals[2],
			PrivateSlot2:            vals[3],
			PrivateSlot3:            vals[4],
			PrivateSlot4:            vals[5],
			IsolatedHonestSlot1:     vals[6],
			IsolatedHonestSlot2:     vals[7],
			IsolatedHonestSlot3:     vals[8],
			ReleaseSlot:             vals[9],
			ByzProposer1:            vals[10],
			ByzProposer2:            vals[11],
			ByzProposer3:            vals[12],
			ByzProposer4:            vals[13],
			IsolatedHonestProposer1: vals[14],
			IsolatedHonestProposer2: vals[15],
			IsolatedHonestProposer3: vals[16],
		})
	}

	return windows, nil
}

func ReorgPhaseForSlot(slot uint64) (string, ReorgWindow, bool, error) {
	windows, err := ReadReorgWindows()
	if err != nil {
		return "", ReorgWindow{}, false, err
	}

	for _, w := range windows {
		switch slot {
		case w.PrivateSlot1:
			return "private_slot_1", w, true, nil
		case w.PrivateSlot2:
			return "private_slot_2", w, true, nil
		case w.PrivateSlot3:
			return "private_slot_3", w, true, nil
		case w.PrivateSlot4:
			return "private_slot_4", w, true, nil
		case w.IsolatedHonestSlot1:
			return "isolated_honest_slot_1", w, true, nil
		case w.IsolatedHonestSlot2:
			return "isolated_honest_slot_2", w, true, nil
		case w.IsolatedHonestSlot3:
			return "isolated_honest_slot_3", w, true, nil
		case w.ReleaseSlot:
			return "release_slot", w, true, nil
		}
	}

	return "", ReorgWindow{}, false, nil
}

type ReorgResult struct {
	Epoch               uint64
	StartSlot           uint64
	PrivateSlot1        uint64
	PrivateSlot2        uint64
	PrivateSlot3        uint64
	PrivateSlot4        uint64
	IsolatedHonestSlot1 uint64
	IsolatedHonestSlot2 uint64
	IsolatedHonestSlot3 uint64
	ReleaseSlot         uint64

	PrivateRoot1        string
	PrivateRoot2        string
	PrivateRoot3        string
	PrivateRoot4        string
	IsolatedHonestRoot1 string
	IsolatedHonestRoot2 string
	IsolatedHonestRoot3 string
	ReleaseBlockRoot    string
	ReleaseParentRoot   string
	Success             string
	Reason              string
}

func ReorgResultsFilePath() string {
	return os.Getenv("EXPERIMENT_REORG_RESULTS_CSV")
}

func ShouldWriteReorgResults() bool {
	return ReorgResultsFilePath() != ""
}

func AppendReorgResult(r ReorgResult) error {
	path := ReorgResultsFilePath()
	if path == "" {
		return nil
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

	w := csv.NewWriter(f)
	defer w.Flush()

	if needHeader {
		if err := w.Write([]string{
			"epoch",
			"start_slot",
			"private_slot_1",
			"private_slot_2",
			"private_slot_3",
			"private_slot_4",
			"isolated_honest_slot_1",
			"isolated_honest_slot_2",
			"isolated_honest_slot_3",
			"release_slot",
			"private_root_1",
			"private_root_2",
			"private_root_3",
			"private_root_4",
			"isolated_honest_root_1",
			"isolated_honest_root_2",
			"isolated_honest_root_3",
			"release_block_root",
			"release_parent_root",
			"success",
			"reason",
		}); err != nil {
			return err
		}
	}

	return w.Write([]string{
		strconv.FormatUint(r.Epoch, 10),
		strconv.FormatUint(r.StartSlot, 10),
		strconv.FormatUint(r.PrivateSlot1, 10),
		strconv.FormatUint(r.PrivateSlot2, 10),
		strconv.FormatUint(r.PrivateSlot3, 10),
		strconv.FormatUint(r.PrivateSlot4, 10),
		strconv.FormatUint(r.IsolatedHonestSlot1, 10),
		strconv.FormatUint(r.IsolatedHonestSlot2, 10),
		strconv.FormatUint(r.IsolatedHonestSlot3, 10),
		strconv.FormatUint(r.ReleaseSlot, 10),
		r.PrivateRoot1,
		r.PrivateRoot2,
		r.PrivateRoot3,
		r.PrivateRoot4,
		r.IsolatedHonestRoot1,
		r.IsolatedHonestRoot2,
		r.IsolatedHonestRoot3,
		r.ReleaseBlockRoot,
		r.ReleaseParentRoot,
		r.Success,
		r.Reason,
	})
}

type ReorgPrivateRoot struct {
	Epoch     uint64
	StartSlot uint64
	Slot      uint64
	Phase     string
	BlockRoot string
}

func ReorgPrivateRootsFilePath() string {
	return os.Getenv("EXPERIMENT_REORG_PRIVATE_ROOTS_CSV")
}

func ShouldWriteReorgPrivateRoots() bool {
	return ReorgPrivateRootsFilePath() != ""
}

func AppendReorgPrivateRoot(r ReorgPrivateRoot) error {
	path := ReorgPrivateRootsFilePath()
	if path == "" {
		return nil
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

	w := csv.NewWriter(f)
	defer w.Flush()

	if needHeader {
		if err := w.Write([]string{
			"epoch",
			"start_slot",
			"slot",
			"phase",
			"block_root",
		}); err != nil {
			return err
		}
	}

	return w.Write([]string{
		strconv.FormatUint(r.Epoch, 10),
		strconv.FormatUint(r.StartSlot, 10),
		strconv.FormatUint(r.Slot, 10),
		r.Phase,
		r.BlockRoot,
	})
}

func ReadReorgPrivateRoots() ([]ReorgPrivateRoot, error) {
	path := ReorgPrivateRootsFilePath()
	if path == "" {
		return nil, nil
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() {
		_ = f.Close()
	}()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}

	out := make([]ReorgPrivateRoot, 0, len(rows))
	for i, row := range rows {
		if i == 0 {
			continue
		}
		if len(row) < 5 {
			continue
		}

		epoch, err := strconv.ParseUint(row[0], 10, 64)
		if err != nil {
			continue
		}
		startSlot, err := strconv.ParseUint(row[1], 10, 64)
		if err != nil {
			continue
		}
		slot, err := strconv.ParseUint(row[2], 10, 64)
		if err != nil {
			continue
		}

		out = append(out, ReorgPrivateRoot{
			Epoch:     epoch,
			StartSlot: startSlot,
			Slot:      slot,
			Phase:     row[3],
			BlockRoot: row[4],
		})
	}

	return out, nil
}

func ReorgPrivateVoteRootForSlot(slot uint64) (string, string, ReorgWindow, bool, error) {
	phase, w, ok, err := ReorgPhaseForSlot(slot)
	if err != nil || !ok {
		return "", "", ReorgWindow{}, false, err
	}

	var voteSlot uint64
	switch phase {
	case "private_slot_1":
		voteSlot = w.PrivateSlot1
	case "private_slot_2":
		voteSlot = w.PrivateSlot2
	case "private_slot_3":
		voteSlot = w.PrivateSlot3
	case "private_slot_4":
		voteSlot = w.PrivateSlot4
	case "isolated_honest_slot_1", "isolated_honest_slot_2", "isolated_honest_slot_3":
		voteSlot = w.PrivateSlot4
	default:
		return "", phase, w, false, nil
	}

	roots, err := ReadReorgPrivateRoots()
	if err != nil {
		return "", phase, w, false, err
	}

	for i := len(roots) - 1; i >= 0; i-- {
		r := roots[i]
		if r.StartSlot == w.StartSlot && r.Slot == voteSlot {
			return r.BlockRoot, phase, w, true, nil
		}
	}

	return "", phase, w, false, nil
}
