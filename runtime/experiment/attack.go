package experiment

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"time"
)

const (
	// EXPERIMENT: proposer delay attack hook. Local devnet only.
	ExperimentDelay = 4 * time.Second

	// ExperimentAttackStartEpoch controls when proposer-delay attack starts.
	// Epochs before this are treated as normal baseline epochs.
	ExperimentAttackStartEpoch = uint64(2)

	// Experiment settings. Change this single value between experiment runs.
	ExperimentMaliciousFraction = 0.333
)

// MaliciousValidatorCut returns floor(totalValidators * ExperimentMaliciousFraction).
func MaliciousValidatorCut(totalValidators uint64) uint64 {
	if count, ok := MaliciousValidatorCountFromFile(); ok {
		return count
	}
	return uint64(math.Floor(float64(totalValidators) * ExperimentMaliciousFraction))
}

func IsMaliciousValidator(validatorIndex uint64, totalValidators uint64) bool {
	if configured, isMalicious := IsMaliciousValidatorFromFile(validatorIndex); configured {
		return isMalicious
	}

	if totalValidators == 0 {
		totalValidators = 1000
	}

	return validatorIndex < MaliciousValidatorCut(totalValidators)
}

func ShouldRunValidatorDuty(validatorIndex uint64) bool {
	role := os.Getenv("EXPERIMENT_VALIDATOR_ROLE")

	switch role {
	case "", "all":
		return true
	case "honest":
		return !IsMaliciousValidator(validatorIndex, 0)
	case "byzantine":
		return IsMaliciousValidator(validatorIndex, 0)
	default:
		return true
	}
}

func MaliciousFractionString() string {
	if fraction, ok := MaliciousFractionStringFromFile(); ok {
		return fraction
	}
	return strconv.FormatFloat(ExperimentMaliciousFraction, 'f', -1, 64)
}

func StartupLog(totalValidators uint64) string {
	return fmt.Sprintf(
		"[EXPERIMENT]\nvalidators=%d\nmalicious_fraction=%s\nmalicious_validators=%d\ndelay=%s",
		totalValidators,
		MaliciousFractionString(),
		MaliciousValidatorCut(totalValidators),
		ExperimentDelay,
	)
}
