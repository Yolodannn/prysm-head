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
	return uint64(math.Floor(float64(totalValidators) * ExperimentMaliciousFraction))
}

func IsMaliciousValidator(validatorIndex, totalValidators uint64) bool {
	// EXPERIMENT: interleaved Byzantine validator set.
	// For N=1000, this selects indices 1,4,7,...,997, i.e. 333 validators.
	return validatorIndex%3 == 1
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
