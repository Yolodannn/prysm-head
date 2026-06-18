package client

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	fieldparams "github.com/OffchainLabs/prysm/v7/config/fieldparams"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

var experimentPrivateBlockFileLock sync.Mutex

func experimentIsByzantineRole() bool {
	role := strings.ToLower(strings.TrimSpace(os.Getenv("EXPERIMENT_VALIDATOR_ROLE")))
	return role == "byzantine" || role == "byz" || role == "malicious"
}

func experimentPrivateBlocksPath() string {
	if p := strings.TrimSpace(os.Getenv("EXPERIMENT_PRIVATE_BLOCKS_PATH")); p != "" {
		return p
	}
	return "/home/suliudan2001/workspace/devnet/private_blocks.csv"
}

func experimentRecordPrivateBlock(slot primitives.Slot, root [fieldparams.RootLength]byte, releaseTime time.Time) error {
	experimentPrivateBlockFileLock.Lock()
	defer experimentPrivateBlockFileLock.Unlock()

	f, err := os.OpenFile(experimentPrivateBlocksPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	needHeader := false
	if st, statErr := f.Stat(); statErr != nil {
		if closeErr := f.Close(); closeErr != nil {
			return closeErr
		}
		return statErr
	} else if st.Size() == 0 {
		needHeader = true
	}

	if needHeader {
		if _, err := fmt.Fprintln(f, "slot,block_root,release_unix_millis"); err != nil {
			if closeErr := f.Close(); closeErr != nil {
				return closeErr
			}
			return err
		}
	}

	_, err = fmt.Fprintf(f, "%d,%s,%d\n", uint64(slot), hexutil.Encode(root[:]), releaseTime.UnixMilli())
	if closeErr := f.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	return err
}

func experimentLookupPrivateBlock(slot primitives.Slot) ([fieldparams.RootLength]byte, time.Time, bool) {
	var root [fieldparams.RootLength]byte

	experimentPrivateBlockFileLock.Lock()
	defer experimentPrivateBlockFileLock.Unlock()

	f, err := os.Open(experimentPrivateBlocksPath())
	if err != nil {
		return root, time.Time{}, false
	}

	var foundRoot [fieldparams.RootLength]byte
	var foundRelease time.Time
	found := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "slot,") {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 3 {
			continue
		}

		s, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
		if err != nil || primitives.Slot(s) != slot {
			continue
		}

		decoded, err := hexutil.Decode(strings.TrimSpace(parts[1]))
		if err != nil || len(decoded) != fieldparams.RootLength {
			continue
		}

		ms, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
		if err != nil {
			continue
		}

		copy(foundRoot[:], decoded)
		foundRelease = time.UnixMilli(ms)
		found = true
	}

	if err := scanner.Err(); err != nil {
		if closeErr := f.Close(); closeErr != nil {
			return root, time.Time{}, false
		}
		return root, time.Time{}, false
	}

	if err := f.Close(); err != nil {
		return root, time.Time{}, false
	}

	return foundRoot, foundRelease, found
}
