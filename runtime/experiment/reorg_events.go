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
