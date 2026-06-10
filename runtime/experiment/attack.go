package experiment

import (
	"fmt"
	"math"
	"strconv"
	"time"
)

const (
	// EXPERIMENT: proposer delay attack hook. Local devnet only.
	ExperimentDelay = 4 * time.Second

	// Experiment settings. Change this single value between experiment runs.
	ExperimentMaliciousFraction = 0.333
)

// MaliciousValidatorCut returns floor(totalValidators * ExperimentMaliciousFraction).
func MaliciousValidatorCut(totalValidators uint64) uint64 {
	return uint64(math.Floor(float64(totalValidators) * ExperimentMaliciousFraction))
}

func IsMaliciousValidator(validatorIndex, totalValidators uint64) bool {
	return validatorIndex < MaliciousValidatorCut(totalValidators)
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
