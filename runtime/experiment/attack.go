package experiment

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

const (
	// ExperimentAttackStartEpoch controls when the experiment starts.
	// Epochs before this are treated as baseline epochs.
	ExperimentAttackStartEpoch = uint64(2)

	// Default fraction used only when no malicious validator file is provided.
	ExperimentMaliciousFraction = 0.333
)

func AttackMode() string {
	mode := strings.TrimSpace(os.Getenv("EXPERIMENT_ATTACK_MODE"))
	if mode == "" {
		return "none"
	}
	return mode
}

func IsReorgMode() bool {
	return AttackMode() == "reorg"
}

func ReorgK() uint64 {
	raw := strings.TrimSpace(os.Getenv("EXPERIMENT_REORG_K"))
	if raw == "" {
		return 2
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || v == 0 {
		return 2
	}
	return v
}

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
		"[EXPERIMENT]\nmode=%s\nvalidators=%d\nmalicious_fraction=%s\nmalicious_validators=%d\nreorg_k=%d",
		AttackMode(),
		totalValidators,
		MaliciousFractionString(),
		MaliciousValidatorCut(totalValidators),
		ReorgK(),
	)
}
