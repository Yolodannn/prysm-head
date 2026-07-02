package client

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/OffchainLabs/go-bitfield"
	"github.com/OffchainLabs/prysm/v7/async"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/signing"
	"github.com/OffchainLabs/prysm/v7/config/features"
	fieldparams "github.com/OffchainLabs/prysm/v7/config/fieldparams"
	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v7/encoding/bytesutil"
	"github.com/OffchainLabs/prysm/v7/monitoring/tracing"
	"github.com/OffchainLabs/prysm/v7/monitoring/tracing/trace"
	ethpb "github.com/OffchainLabs/prysm/v7/proto/prysm/v1alpha1"
	validatorpb "github.com/OffchainLabs/prysm/v7/proto/prysm/v1alpha1/validator-client"
	"github.com/OffchainLabs/prysm/v7/runtime/experiment"
	prysmTime "github.com/OffchainLabs/prysm/v7/time"
	"github.com/OffchainLabs/prysm/v7/time/slots"
	"github.com/OffchainLabs/prysm/v7/validator/client/iface"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var failedAttLocalProtectionErr = "attempted to make slashable attestation, rejected by local slashing protection"

func reorgDecodeRoot(root string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(root, "0x"))
	if err != nil {
		return nil, err
	}
	if len(b) != fieldparams.RootLength {
		return nil, fmt.Errorf("invalid reorg root length %d", len(b))
	}
	return b, nil
}

func reorgIsPrivateVotePhase(phase string) bool {
	return phase == "private_slot_1" ||
		phase == "private_slot_2" ||
		phase == "private_slot_3" ||
		phase == "private_slot_4" ||
		phase == "isolated_honest_slot_1" ||
		phase == "isolated_honest_slot_2" ||
		phase == "isolated_honest_slot_3"
}

// SubmitAttestation completes the validator client's attester responsibility at a given slot.
// It fetches the latest beacon block head along with the latest canonical beacon state
// information in order to sign the block and include information about the validator's
// participation in voting on the block.
func (v *validator) SubmitAttestation(ctx context.Context, slot primitives.Slot, pubKey [fieldparams.BLSPubkeyLength]byte) {
	ctx, span := trace.StartSpan(ctx, "validator.SubmitAttestation")
	defer span.End()
	span.SetAttributes(trace.StringAttribute("validator", fmt.Sprintf("%#x", pubKey)))

	v.waitUntilAttestationDueOrValidBlock(ctx, slot)

	var b strings.Builder
	if err := b.WriteByte(byte(iface.RoleAttester)); err != nil {
		log.WithError(err).Error("Could not write role byte for lock key")
		tracing.AnnotateError(span, err)
		return
	}
	_, err := b.Write(pubKey[:])
	if err != nil {
		log.WithError(err).Error("Could not write pubkey bytes for lock key")
		tracing.AnnotateError(span, err)
		return
	}
	lock := async.NewMultilock(b.String())
	lock.Lock()
	defer lock.Unlock()

	fmtKey := fmt.Sprintf("%#x", pubKey[:])
	log := log.WithField("pubkey", fmt.Sprintf("%#x", bytesutil.Trunc(pubKey[:]))).WithField("slot", slot)
	duty, err := v.duty(pubKey)
	if err != nil {
		log.WithError(err).Error("Could not fetch validator assignment")
		if v.emitAccountMetrics {
			ValidatorAttestFailVec.WithLabelValues(fmtKey).Inc()
		}
		tracing.AnnotateError(span, err)
		return
	}
	if duty.CommitteeLength == 0 {
		log.Debug("Empty committee for validator duty, not attesting")
		return
	}

	if experiment.IsReorgMode() && !experiment.ShouldRunValidatorDuty(uint64(duty.ValidatorIndex)) {
		log.WithField("validatorIndex", duty.ValidatorIndex).Debug("EXPERIMENT: skipping attestation for validator role")
		return
	}

	postElectra := slots.ToEpoch(slot) >= params.BeaconConfig().ElectraForkEpoch

	data, err := v.getAttestationData(ctx, slot, duty.CommitteeIndex)
	if err != nil {
		log.WithError(err).Error("Could not request attestation to sign at slot")
		if v.emitAccountMetrics {
			ValidatorAttestFailVec.WithLabelValues(fmtKey).Inc()
		}
		tracing.AnnotateError(span, err)
		return
	}

	reorgWithholdAttestation := false
	reorgPrivateVoteRoot := ""
	reorgPrivateVotePhase := ""
	var reorgPrivateVoteWindow experiment.ReorgWindow

	if experiment.IsReorgMode() && experiment.IsMaliciousValidator(uint64(duty.ValidatorIndex), 0) {
		privateRoot, phase, window, ok, err := experiment.ReorgPrivateVoteRootForSlot(uint64(slot))
		if err != nil {
			log.WithError(err).WithField("validatorIndex", duty.ValidatorIndex).Warn("[REORG] Failed to read private vote root")
			return
		}

		if reorgIsPrivateVotePhase(phase) {
			if !ok || privateRoot == "" {
				log.WithFields(logrus.Fields{
					"validatorIndex": duty.ValidatorIndex,
					"phase":          phase,
				}).Warn("[REORG] Missing private vote root, withholding byzantine attestation instead of public vote")
				return
			}

			privateRootBytes, err := reorgDecodeRoot(privateRoot)
			if err != nil {
				log.WithError(err).WithFields(logrus.Fields{
					"validatorIndex": duty.ValidatorIndex,
					"phase":          phase,
					"privateRoot":    privateRoot,
				}).Warn("[REORG] Invalid private vote root")
				return
			}

			publicRoot := fmt.Sprintf("%#x", data.BeaconBlockRoot)
			data.BeaconBlockRoot = privateRootBytes

			reorgWithholdAttestation = true
			reorgPrivateVoteRoot = privateRoot
			reorgPrivateVotePhase = phase
			reorgPrivateVoteWindow = window

			log.WithFields(logrus.Fields{
				"validatorIndex": duty.ValidatorIndex,
				"phase":          phase,
				"publicRoot":     publicRoot,
				"privateRoot":    privateRoot,
				"startSlot":      window.StartSlot,
				"releaseSlot":    window.ReleaseSlot,
			}).Warn("[REORG] Byzantine attester voting for private root and withholding attestation")
		}
	}

	experiment.WriteReorgEvent(
		"attestation_data",
		"attestation",
		uint64(slot),
		uint64(duty.ValidatorIndex),
		experiment.IsMaliciousValidator(uint64(duty.ValidatorIndex), 0),
		fmt.Sprintf("%#x", data.BeaconBlockRoot),
		"",
		fmt.Sprintf("%#x", data.BeaconBlockRoot),
		"",
		0,
	)

	sig, _, err := v.signAtt(ctx, pubKey, data, slot)
	if err != nil {
		log.WithError(err).Error("Could not sign attestation")
		if v.emitAccountMetrics {
			ValidatorAttestFailVec.WithLabelValues(fmtKey).Inc()
		}
		tracing.AnnotateError(span, err)
		return
	}

	var indexedAtt ethpb.IndexedAtt
	if postElectra {
		indexedAtt = &ethpb.IndexedAttestationElectra{
			AttestingIndices: []uint64{uint64(duty.ValidatorIndex)},
			Data:             data,
			Signature:        sig,
		}
	} else {
		indexedAtt = &ethpb.IndexedAttestation{
			AttestingIndices: []uint64{uint64(duty.ValidatorIndex)},
			Data:             data,
			Signature:        sig,
		}
	}

	_, signingRoot, err := v.domainAndSigningRoot(ctx, indexedAtt.GetData())
	if err != nil {
		log.WithError(err).Error("Could not get domain and signing root from attestation")
		if v.emitAccountMetrics {
			ValidatorAttestFailVec.WithLabelValues(fmtKey).Inc()
		}
		tracing.AnnotateError(span, err)
		return
	}

	// Send the attestation to the beacon node.
	if err := v.db.SlashableAttestationCheck(ctx, indexedAtt, pubKey, signingRoot, v.emitAccountMetrics, ValidatorAttestFailVec); err != nil {
		log.WithError(err).Error("Failed attestation slashing protection check")
		log.WithFields(
			attestationLogFields(pubKey, indexedAtt),
		).Debug("Attempted slashable attestation details")
		tracing.AnnotateError(span, err)
		return
	}

	var aggregationBitfield bitfield.Bitlist
	var attestation ethpb.Att
	var attResp *ethpb.AttestResponse
	if postElectra {
		sa := &ethpb.SingleAttestation{
			Data:          data,
			AttesterIndex: duty.ValidatorIndex,
			CommitteeId:   duty.CommitteeIndex,
			Signature:     sig,
		}
		attestation = sa
		if !reorgWithholdAttestation {
			attResp, err = v.validatorClient.ProposeAttestationElectra(ctx, sa)
		}
	} else {
		aggregationBitfield = bitfield.NewBitlist(duty.CommitteeLength)
		aggregationBitfield.SetBitAt(duty.ValidatorCommitteeIndex, true)
		a := &ethpb.Attestation{
			Data:            data,
			AggregationBits: aggregationBitfield,
			Signature:       sig,
		}
		attestation = a
		if !reorgWithholdAttestation {
			attResp, err = v.validatorClient.ProposeAttestation(ctx, a)
		}
	}
	if reorgWithholdAttestation {
		releaseAt, err := slots.StartTime(v.genesisTime, primitives.Slot(reorgPrivateVoteWindow.IsolatedHonestSlot3))
		if err != nil {
			log.WithError(err).WithFields(logrus.Fields{
				"validatorIndex": duty.ValidatorIndex,
				"phase":          reorgPrivateVotePhase,
				"startSlot":      reorgPrivateVoteWindow.StartSlot,
			}).Warn("[REORG] Failed to compute attestation release time")
			return
		}
		releaseAt = releaseAt.Add(9*time.Second + 300*time.Millisecond)
		delay := max(time.Until(releaseAt), 0)

		log.WithFields(logrus.Fields{
			"validatorIndex": duty.ValidatorIndex,
			"phase":          reorgPrivateVotePhase,
			"privateRoot":    reorgPrivateVoteRoot,
			"delay":          delay.String(),
			"startSlot":      reorgPrivateVoteWindow.StartSlot,
			"releaseSlot":    reorgPrivateVoteWindow.ReleaseSlot,
		}).Warn("[REORG] Scheduled withheld Byzantine attestation release at chain release time")

		go func() {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			<-timer.C

			submitCtx := context.Background()
			var submitErr error

			if postElectra {
				sa, ok := attestation.(*ethpb.SingleAttestation)
				if !ok {
					log.WithField("validatorIndex", duty.ValidatorIndex).Warn("[REORG] Withheld attestation type mismatch for Electra")
					return
				}
				_, submitErr = v.validatorClient.ProposeAttestationElectra(submitCtx, sa)
			} else {
				a, ok := attestation.(*ethpb.Attestation)
				if !ok {
					log.WithField("validatorIndex", duty.ValidatorIndex).Warn("[REORG] Withheld attestation type mismatch")
					return
				}
				_, submitErr = v.validatorClient.ProposeAttestation(submitCtx, a)
			}

			if submitErr != nil {
				log.WithError(submitErr).WithFields(logrus.Fields{
					"validatorIndex": duty.ValidatorIndex,
					"phase":          reorgPrivateVotePhase,
					"privateRoot":    reorgPrivateVoteRoot,
				}).Warn("[REORG] Failed to release withheld Byzantine attestation")
				return
			}

			if err := v.saveSubmittedAtt(attestation, pubKey[:], false); err != nil {
				log.WithError(err).WithField("validatorIndex", duty.ValidatorIndex).Warn("[REORG] Could not save released Byzantine attestation")
				return
			}

			if v.emitAccountMetrics {
				ValidatorAttestSuccessVec.WithLabelValues(fmtKey).Inc()
				ValidatorAttestedSlotsGaugeVec.WithLabelValues(fmtKey).Set(float64(slot))
			}

			log.WithFields(logrus.Fields{
				"validatorIndex": duty.ValidatorIndex,
				"phase":          reorgPrivateVotePhase,
				"privateRoot":    reorgPrivateVoteRoot,
				"startSlot":      reorgPrivateVoteWindow.StartSlot,
				"releaseSlot":    reorgPrivateVoteWindow.ReleaseSlot,
			}).Warn("[REORG] Released withheld Byzantine attestation")
		}()

		return
	}

	if err != nil {
		log.WithError(err).Error("Could not submit attestation to beacon node")
		if v.emitAccountMetrics {
			ValidatorAttestFailVec.WithLabelValues(fmtKey).Inc()
		}
		tracing.AnnotateError(span, err)
		return
	}

	if err := v.saveSubmittedAtt(attestation, pubKey[:], false); err != nil {
		log.WithError(err).Error("Could not save validator index for logging")
		if v.emitAccountMetrics {
			ValidatorAttestFailVec.WithLabelValues(fmtKey).Inc()
		}
		tracing.AnnotateError(span, err)
		return
	}

	span.SetAttributes(
		trace.Int64Attribute("slot", int64(slot)), // lint:ignore uintcast -- This conversion is OK for tracing.
		trace.StringAttribute("attestationHash", fmt.Sprintf("%#x", attResp.AttestationDataRoot)),
		trace.StringAttribute("blockRoot", fmt.Sprintf("%#x", data.BeaconBlockRoot)),
		trace.Int64Attribute("justifiedEpoch", int64(data.Source.Epoch)),
		trace.Int64Attribute("targetEpoch", int64(data.Target.Epoch)),
	)
	if postElectra {
		span.SetAttributes(trace.Int64Attribute("attesterIndex", int64(duty.ValidatorIndex)))
		span.SetAttributes(trace.Int64Attribute("committeeIndex", int64(duty.CommitteeIndex)))
	} else {
		span.SetAttributes(trace.StringAttribute("aggregationBitfield", fmt.Sprintf("%#x", aggregationBitfield)))
		span.SetAttributes(trace.Int64Attribute("committeeIndex", int64(data.CommitteeIndex)))
	}

	if v.emitAccountMetrics {
		ValidatorAttestSuccessVec.WithLabelValues(fmtKey).Inc()
		ValidatorAttestedSlotsGaugeVec.WithLabelValues(fmtKey).Set(float64(slot))
	}
}

// Given the validator public key, this gets the validator assignment.
func (v *validator) duty(pubKey [fieldparams.BLSPubkeyLength]byte) (*ethpb.ValidatorDuty, error) {
	v.dutiesLock.RLock()
	defer v.dutiesLock.RUnlock()
	if !v.duties.IsInitialized() {
		return nil, errors.New("no duties for validators")
	}
	d, ok := v.duties.CurrentDuty(pubKey)
	if !ok {
		return nil, fmt.Errorf("pubkey %#x not in duties", bytesutil.Trunc(pubKey[:]))
	}
	return d, nil
}

// Given validator's public key, this function returns the signature of an attestation data and its signing root.
func (v *validator) signAtt(ctx context.Context, pubKey [fieldparams.BLSPubkeyLength]byte, data *ethpb.AttestationData, slot primitives.Slot) ([]byte, [32]byte, error) {
	ctx, span := trace.StartSpan(ctx, "validator.signAtt")
	defer span.End()

	domain, root, err := v.domainAndSigningRoot(ctx, data)
	if err != nil {
		return nil, [32]byte{}, err
	}
	sig, err := v.km.Sign(ctx, &validatorpb.SignRequest{
		PublicKey:       pubKey[:],
		SigningRoot:     root[:],
		SignatureDomain: domain.SignatureDomain,
		Object:          &validatorpb.SignRequest_AttestationData{AttestationData: data},
		SigningSlot:     slot,
	})
	if err != nil {
		return nil, [32]byte{}, err
	}

	return sig.Marshal(), root, nil
}

func (v *validator) domainAndSigningRoot(ctx context.Context, data *ethpb.AttestationData) (*ethpb.DomainResponse, [32]byte, error) {
	domain, err := v.domainData(ctx, data.Target.Epoch, params.BeaconConfig().DomainBeaconAttester[:])
	if err != nil {
		return nil, [32]byte{}, err
	}
	root, err := signing.ComputeSigningRoot(data, domain.SignatureDomain)
	if err != nil {
		return nil, [32]byte{}, err
	}
	return domain, root, nil
}

// highestSlot returns the highest slot with a valid block seen by the validator
func (v *validator) highestSlot() primitives.Slot {
	v.highestValidSlotLock.Lock()
	defer v.highestValidSlotLock.Unlock()
	return v.highestValidSlot
}

// setHighestSlot sets the highest slot with a valid block seen by the validator
func (v *validator) setHighestSlot(slot primitives.Slot) {
	v.highestValidSlotLock.Lock()
	defer v.highestValidSlotLock.Unlock()
	if slot > v.highestValidSlot {
		v.highestValidSlot = slot
		v.slotFeed.Send(slot)
	}
}

// waitUntilAttestationDueOrValidBlock waits until (a) or (b) whichever comes first:
//
//	(a) the validator has received a valid block that is the same slot as input slot
//	(b) the configured attestation due time has transpired (as basis points of the slot duration)
func (v *validator) waitUntilAttestationDueOrValidBlock(ctx context.Context, slot primitives.Slot) {
	ctx, span := trace.StartSpan(ctx, "validator.waitUntilAttestationDueOrValidBlock")
	defer span.End()

	// Don't need to wait if requested slot is the same as highest valid slot.
	if slot <= v.highestSlot() {
		return
	}

	cfg := params.BeaconConfig()
	component := cfg.AttestationDueBPS
	if slots.ToEpoch(slot) >= cfg.GloasForkEpoch {
		component = cfg.AttestationDueBPSGloas
	}
	finalTime, err := v.slotComponentDeadline(slot, component)
	if err != nil {
		log.WithError(err).WithField("slot", slot).Error("Slot overflows, unable to wait for attestation deadline")
		return
	}
	wait := prysmTime.Until(finalTime)
	if wait <= 0 {
		return
	}
	t := time.NewTimer(wait)
	defer t.Stop()

	ch := make(chan primitives.Slot, 1)
	sub := v.slotFeed.Subscribe(ch)
	defer sub.Unsubscribe()

	for {
		select {
		case s := <-ch:
			if features.Get().AttestTimely {
				if slot <= s {
					return
				}
			}
		case <-ctx.Done():
			tracing.AnnotateError(span, ctx.Err())
			return
		case <-sub.Err():
			log.Error("Subscriber closed, exiting goroutine")
			return
		case <-t.C:
			return
		}
	}
}

func attestationLogFields(pubKey [fieldparams.BLSPubkeyLength]byte, indexedAtt ethpb.IndexedAtt) logrus.Fields {
	return logrus.Fields{
		"pubkey":         fmt.Sprintf("%#x", pubKey),
		"slot":           indexedAtt.GetData().Slot,
		"committeeIndex": indexedAtt.GetData().CommitteeIndex,
		"blockRoot":      fmt.Sprintf("%#x", indexedAtt.GetData().BeaconBlockRoot),
		"sourceEpoch":    indexedAtt.GetData().Source.Epoch,
		"sourceRoot":     fmt.Sprintf("%#x", indexedAtt.GetData().Source.Root),
		"targetEpoch":    indexedAtt.GetData().Target.Epoch,
		"targetRoot":     fmt.Sprintf("%#x", indexedAtt.GetData().Target.Root),
		"signature":      fmt.Sprintf("%#x", indexedAtt.GetSignature()),
	}
}
