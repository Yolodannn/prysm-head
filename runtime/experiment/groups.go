package experiment

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

var maliciousGroupOnce sync.Once
var maliciousGroupConfigured bool
var maliciousGroupSet map[uint64]struct{}
var maliciousGroupCount uint64

func maliciousValidatorsFilePath() string {
	return strings.TrimSpace(os.Getenv("EXPERIMENT_MALICIOUS_VALIDATORS_FILE"))
}

func failGroupLoad(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "[EXPERIMENT] "+format+"\n", args...)
	os.Exit(1)
}

func loadMaliciousGroup() {
	path := maliciousValidatorsFilePath()
	if path == "" {
		return
	}

	maliciousGroupConfigured = true
	maliciousGroupSet = make(map[uint64]struct{})

	f, err := os.Open(path)
	if err != nil {
		failGroupLoad("failed to open EXPERIMENT_MALICIOUS_VALIDATORS_FILE=%s: %v", path, err)
	}

	scanner := bufio.NewScanner(f)
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()

		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}

		line = strings.ReplaceAll(line, ",", " ")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		for tok := range strings.FieldsSeq(line) {
			if err := addMaliciousValidatorToken(tok, maliciousGroupSet); err != nil {
				failGroupLoad("invalid validator token %q at %s:%d: %v", tok, path, lineNo, err)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		failGroupLoad("failed to read malicious validator file %s: %v", path, err)
	}

	if err := f.Close(); err != nil {
		failGroupLoad("failed to close malicious validator file %s: %v", path, err)
	}

	maliciousGroupCount = uint64(len(maliciousGroupSet))
}

func addMaliciousValidatorToken(tok string, set map[uint64]struct{}) error {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return nil
	}

	if strings.Contains(tok, "..") {
		parts := strings.SplitN(tok, "..", 2)
		return addMaliciousValidatorRange(parts[0], parts[1], set)
	}

	if strings.Contains(tok, "-") {
		parts := strings.SplitN(tok, "-", 2)
		return addMaliciousValidatorRange(parts[0], parts[1], set)
	}

	v, err := strconv.ParseUint(tok, 10, 64)
	if err != nil {
		return err
	}

	set[v] = struct{}{}
	return nil
}

func addMaliciousValidatorRange(startStr string, endStr string, set map[uint64]struct{}) error {
	start, err := strconv.ParseUint(strings.TrimSpace(startStr), 10, 64)
	if err != nil {
		return err
	}

	end, err := strconv.ParseUint(strings.TrimSpace(endStr), 10, 64)
	if err != nil {
		return err
	}

	if end < start {
		return fmt.Errorf("range end %d is smaller than start %d", end, start)
	}

	for i := start; i <= end; i++ {
		set[i] = struct{}{}
		if i == ^uint64(0) {
			break
		}
	}

	return nil
}

func IsMaliciousValidatorFromFile(validatorIndex uint64) (bool, bool) {
	maliciousGroupOnce.Do(loadMaliciousGroup)

	if !maliciousGroupConfigured {
		return false, false
	}

	_, ok := maliciousGroupSet[validatorIndex]
	return true, ok
}

func MaliciousValidatorCountFromFile() (uint64, bool) {
	maliciousGroupOnce.Do(loadMaliciousGroup)

	if !maliciousGroupConfigured {
		return 0, false
	}

	return maliciousGroupCount, true
}

func MaliciousFractionStringFromFile() (string, bool) {
	count, ok := MaliciousValidatorCountFromFile()
	if !ok {
		return "", false
	}

	totalValidators := uint64(1000)
	if raw := strings.TrimSpace(os.Getenv("EXPERIMENT_TOTAL_VALIDATORS")); raw != "" {
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			failGroupLoad("invalid EXPERIMENT_TOTAL_VALIDATORS=%q: %v", raw, err)
		}
		totalValidators = v
	}

	if totalValidators == 0 {
		return "", false
	}

	return strconv.FormatFloat(float64(count)/float64(totalValidators), 'f', -1, 64), true
}
