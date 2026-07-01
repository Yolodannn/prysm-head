package validator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	builderapi "github.com/OffchainLabs/prysm/v7/api/client/builder"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/blockchain"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/builder"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/cache"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/feed"
	blockfeed "github.com/OffchainLabs/prysm/v7/beacon-chain/core/feed/block"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/feed/operation"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/helpers"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/peerdas"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/core/transition"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/db/kv"
	"github.com/OffchainLabs/prysm/v7/beacon-chain/state"
	fieldparams "github.com/OffchainLabs/prysm/v7/config/fieldparams"
	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/consensus-types/blocks"
	"github.com/OffchainLabs/prysm/v7/consensus-types/interfaces"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v7/monitoring/tracing/trace"
	enginev1 "github.com/OffchainLabs/prysm/v7/proto/engine/v1"
	ethpb "github.com/OffchainLabs/prysm/v7/proto/prysm/v1alpha1"
	"github.com/OffchainLabs/prysm/v7/runtime/experiment"
	"github.com/OffchainLabs/prysm/v7/runtime/version"
	"github.com/OffchainLabs/prysm/v7/time/slots"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	emptypb "github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// eth1DataNotification is a latch to stop flooding logs with the same warning.
var eth1DataNotification bool

func reorgBeaconLogFields(phase string, w experiment.ReorgWindow) logrus.Fields {
	return logrus.Fields{
		"phase":                   phase,
		"epoch":                   w.Epoch,
		"startSlot":               w.StartSlot,
		"privateSlot1":            w.PrivateSlot1,
		"privateSlot2":            w.PrivateSlot2,
		"privateSlot3":            w.PrivateSlot3,
		"isolatedHonestSlot1":     w.IsolatedHonestSlot1,
		"isolatedHonestSlot2":     w.IsolatedHonestSlot2,
		"releaseSlot":             w.ReleaseSlot,
		"byzProposer1":            w.ByzProposer1,
		"byzProposer2":            w.ByzProposer2,
		"byzProposer3":            w.ByzProposer3,
		"isolatedHonestProposer1": w.IsolatedHonestProposer1,
		"isolatedHonestProposer2": w.IsolatedHonestProposer2,
	}
}

type reorgPrivateBlock struct {
	block     interfaces.SignedBeaconBlock
	root      [fieldparams.RootLength]byte
	postState state.BeaconState
}

var reorgPrivateBlocks = struct {
	sync.Mutex
	bySlot           map[uint64]reorgPrivateBlock
	released         map[uint64]bool
	releaseScheduled map[uint64]bool
	isolatedRoots    map[uint64]string
}{
	bySlot:           make(map[uint64]reorgPrivateBlock),
	released:         make(map[uint64]bool),
	releaseScheduled: make(map[uint64]bool),
	isolatedRoots:    make(map[uint64]string),
}

func reorgStorePrivateBlock(slot uint64, block interfaces.SignedBeaconBlock, root [fieldparams.RootLength]byte, postState state.BeaconState) {
	reorgPrivateBlocks.Lock()
	defer reorgPrivateBlocks.Unlock()

	reorgPrivateBlocks.bySlot[slot] = reorgPrivateBlock{
		block:     block,
		root:      root,
		postState: postState,
	}
}

func reorgLoadPrivateBlock(slot uint64) (reorgPrivateBlock, bool) {
	reorgPrivateBlocks.Lock()
	defer reorgPrivateBlocks.Unlock()

	b, ok := reorgPrivateBlocks.bySlot[slot]
	return b, ok
}

func reorgStoreIsolatedRoot(slot uint64, root string) {
	reorgPrivateBlocks.Lock()
	defer reorgPrivateBlocks.Unlock()

	reorgPrivateBlocks.isolatedRoots[slot] = root
}

func reorgLoadIsolatedRoot(slot uint64) string {
	reorgPrivateBlocks.Lock()
	defer reorgPrivateBlocks.Unlock()

	return reorgPrivateBlocks.isolatedRoots[slot]
}

func reorgRootString(root [fieldparams.RootLength]byte) string {
	return fmt.Sprintf("%#x", root)
}

func reorgPrivateParent(slot uint64) (state.BeaconState, [fieldparams.RootLength]byte, bool) {
	reorgPrivateBlocks.Lock()
	defer reorgPrivateBlocks.Unlock()

	b, ok := reorgPrivateBlocks.bySlot[slot]
	if !ok || b.postState == nil {
		return nil, [fieldparams.RootLength]byte{}, false
	}
	return b.postState.Copy(), b.root, true
}

func reorgAlreadyReleased(startSlot uint64) bool {
	reorgPrivateBlocks.Lock()
	defer reorgPrivateBlocks.Unlock()

	return reorgPrivateBlocks.released[startSlot]
}

func reorgMarkReleased(startSlot uint64) {
	reorgPrivateBlocks.Lock()
	defer reorgPrivateBlocks.Unlock()

	reorgPrivateBlocks.released[startSlot] = true
}

func reorgMarkReleaseScheduled(startSlot uint64) bool {
	reorgPrivateBlocks.Lock()
	defer reorgPrivateBlocks.Unlock()

	if reorgPrivateBlocks.released[startSlot] || reorgPrivateBlocks.releaseScheduled[startSlot] {
		return false
	}
	reorgPrivateBlocks.releaseScheduled[startSlot] = true
	return true
}

func (vs *Server) reorgScheduleTimedRelease(w experiment.ReorgWindow, releaseAt time.Time) {
	if !reorgMarkReleaseScheduled(w.StartSlot) {
		return
	}

	delay := max(time.Until(releaseAt), 0)

	log.WithFields(reorgBeaconLogFields("isolated_honest_slot_2", w)).WithFields(logrus.Fields{
		"releaseAt": releaseAt,
		"delay":     delay.String(),
	}).Warn("[REORG] Scheduled timed release at second isolated slot +9s")

	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()

		<-timer.C

		if err := vs.reorgReleasePrivateBlocks(context.Background(), w); err != nil {
			log.WithError(err).WithFields(reorgBeaconLogFields("timed_release", w)).Warn("[REORG] Timed release failed")
			return
		}

		log.WithFields(reorgBeaconLogFields("timed_release", w)).Warn("[REORG] Timed release completed")
	}()
}

func (vs *Server) reorgReleasePrivateBlocks(ctx context.Context, w experiment.ReorgWindow) error {
	if reorgAlreadyReleased(w.StartSlot) {
		return nil
	}

	for _, slot := range []uint64{w.PrivateSlot1, w.PrivateSlot2, w.PrivateSlot3} {
		privateBlock, ok := reorgLoadPrivateBlock(slot)
		if !ok {
			return fmt.Errorf("missing private block for slot %d", slot)
		}

		pb, err := privateBlock.block.Proto()
		if err != nil {
			return err
		}
		if err := vs.P2P.Broadcast(ctx, pb); err != nil {
			return err
		}

		log.WithFields(logrus.Fields{
			"slot":      slot,
			"root":      fmt.Sprintf("%#x", privateBlock.root),
			"startSlot": w.StartSlot,
		}).Warn("[REORG] Released private block")
	}

	reorgMarkReleased(w.StartSlot)
	return nil
}

func (vs *Server) reorgPrivatePreState(
	ctx context.Context,
	phase string,
	w experiment.ReorgWindow,
	block interfaces.SignedBeaconBlock,
	root [fieldparams.RootLength]byte,
) (state.BeaconState, error) {
	if phase == "private_slot_2" || phase == "private_slot_3" {
		parentSlot := w.PrivateSlot1
		if phase == "private_slot_3" {
			parentSlot = w.PrivateSlot2
		}

		privateState, _, ok := reorgPrivateParent(parentSlot)
		if !ok {
			return nil, fmt.Errorf("missing private parent state for slot %d", parentSlot)
		}
		return privateState, nil
	}

	roblock, err := blocks.NewROBlockWithRoot(block, root)
	if err != nil {
		return nil, err
	}
	return vs.BlockReceiver.GetPrestateToPropose(ctx, roblock)
}

func (vs *Server) reorgWithholdPrivateBlock(
	ctx context.Context,
	phase string,
	w experiment.ReorgWindow,
	block interfaces.SignedBeaconBlock,
	root [fieldparams.RootLength]byte,
) (*ethpb.ProposeResponse, error) {
	preState, err := vs.reorgPrivatePreState(ctx, phase, w, block, root)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "could not get private pre-state: %v", err)
	}

	postState, err := transition.ExecuteStateTransition(ctx, preState, block)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "could not execute private state transition: %v", err)
	}

	slot := uint64(block.Block().Slot())
	reorgStorePrivateBlock(slot, block, root, postState)

	if experiment.ShouldWriteReorgPrivateRoots() {
		if err := experiment.AppendReorgPrivateRoot(experiment.ReorgPrivateRoot{
			Epoch:     w.Epoch,
			StartSlot: w.StartSlot,
			Slot:      uint64(block.Block().Slot()),
			Phase:     phase,
			BlockRoot: reorgRootString(root),
		}); err != nil {
			log.WithError(err).WithFields(reorgBeaconLogFields(phase, w)).Warn("[REORG] Failed to write private root")
		}
	}
	log.WithFields(logrus.Fields{
		"slot":       slot,
		"phase":      phase,
		"root":       fmt.Sprintf("%#x", root),
		"parentRoot": fmt.Sprintf("%#x", block.Block().ParentRoot()),
		"startSlot":  w.StartSlot,
	}).Warn("[REORG] Withheld private block")

	return &ethpb.ProposeResponse{BlockRoot: root[:]}, nil
}

const (
	eth1dataTimeout           = 2 * time.Second
	defaultBuilderBoostFactor = primitives.Gwei(100)
)

// Deprecated: The gRPC API will remain the default and fully supported through v8 (expected in 2026) but will be eventually removed in favor of REST API.
//
// GetBeaconBlock is called by a proposer during its assigned slot to request a block to sign
// by passing in the slot and the signed randao reveal of the slot.
func (vs *Server) GetBeaconBlock(ctx context.Context, req *ethpb.BlockRequest) (*ethpb.GenericBeaconBlock, error) {
	ctx, span := trace.StartSpan(ctx, "ProposerServer.GetBeaconBlock")
	defer span.End()
	span.SetAttributes(trace.Int64Attribute("slot", int64(req.Slot)))

	t, err := slots.StartTime(vs.TimeFetcher.GenesisTime(), req.Slot)
	if err != nil {
		log.WithError(err).Error("Could not convert slot to time")
	}

	log := log.WithField("slot", req.Slot)
	if phase, w, ok, err := experiment.ReorgPhaseForSlot(uint64(req.Slot)); err != nil {
		log.WithError(err).Warn("[REORG] Beacon failed to read scheduled reorg windows")
	} else if ok {
		log.WithFields(reorgBeaconLogFields(phase, w)).Warn("[REORG] Beacon GetBeaconBlock matched scheduled phase")
		if phase == "isolated_honest_slot_2" {
			vs.reorgScheduleTimedRelease(w, t.Add(9*time.Second))
		} else if phase == "release_slot" {
			if err := vs.reorgReleasePrivateBlocks(ctx, w); err != nil {
				log.WithError(err).WithFields(reorgBeaconLogFields(phase, w)).Warn("[REORG] Failed to release private blocks")
			}
		}
	}

	head, parentRoot, err := vs.getParentState(ctx, req.Slot)
	if err != nil {
		log.WithError(err).Error("Fail to build block: could not get parent state")
		return nil, err
	}

	if phase, w, ok, err := experiment.ReorgPhaseForSlot(uint64(req.Slot)); err != nil {
		log.WithError(err).Warn("[REORG] Beacon failed to read scheduled reorg windows")
	} else if ok && (phase == "private_slot_2" || phase == "private_slot_3") {
		parentSlot := w.PrivateSlot1
		if phase == "private_slot_3" {
			parentSlot = w.PrivateSlot2
		}

		if privateState, privateRoot, ok := reorgPrivateParent(parentSlot); ok {
			if privateState.Slot() < req.Slot {
				privateState, err = transition.ProcessSlots(ctx, privateState, req.Slot)
				if err != nil {
					log.WithError(err).WithFields(reorgBeaconLogFields(phase, w)).Warn("[REORG] Failed to advance private parent state")
					return nil, status.Errorf(codes.Internal, "could not advance private parent state: %v", err)
				}
			}
			head = privateState
			parentRoot = privateRoot
			log.WithFields(reorgBeaconLogFields(phase, w)).WithField("privateParentRoot", fmt.Sprintf("%#x", parentRoot)).Warn("[REORG] Building block on private parent")
		} else {
			log.WithFields(reorgBeaconLogFields(phase, w)).WithField("privateParentSlot", parentSlot).Warn("[REORG] Missing private parent; falling back to public head")
		}
	}
	sBlk, err := getEmptyBlock(req.Slot)
	if err != nil {
		log.WithError(err).Error("Fail to build block: could not get empty block")
		return nil, status.Errorf(codes.Internal, "Could not prepare block: %v", err)
	}
	// Set slot, graffiti, randao reveal, and parent root.
	sBlk.SetSlot(req.Slot)
	// Generate graffiti with client version info using flexible standard
	if vs.GraffitiInfo != nil {
		graffiti := vs.GraffitiInfo.GenerateGraffiti(req.Graffiti)
		sBlk.SetGraffiti(graffiti[:])
	} else {
		sBlk.SetGraffiti(req.Graffiti)
	}
	sBlk.SetRandaoReveal(req.RandaoReveal)
	sBlk.SetParentRoot(parentRoot[:])

	// Set proposer index.
	idx, err := helpers.BeaconProposerIndex(ctx, head)
	if err != nil {
		return nil, fmt.Errorf("could not calculate proposer index %w", err)
	}
	sBlk.SetProposerIndex(idx)

	builderBoostFactor := defaultBuilderBoostFactor
	if req.BuilderBoostFactor != nil {
		builderBoostFactor = primitives.Gwei(req.BuilderBoostFactor.Value)
	}

	resp, err := vs.BuildBlockParallel(ctx, sBlk, head, req.SkipMevBoost, builderBoostFactor)
	log = log.WithFields(logrus.Fields{
		"sinceSlotStartTime": time.Since(t),
		"validator":          sBlk.Block().ProposerIndex(),
	})

	if err != nil {
		log.WithError(err).Error("Finished building block")
		return nil, errors.Wrap(err, "could not build block in parallel")
	}

	log.Info("Finished building block")
	return resp, nil
}

func (vs *Server) handleSuccesfulReorgAttempt(ctx context.Context, slot primitives.Slot, parentRoot, _ [32]byte) (state.BeaconState, error) {
	// Try to get the state from the NSC
	accessRoot := parentRoot
	if slots.ToEpoch(slot) >= params.BeaconConfig().GloasForkEpoch {
		accessRoot, _ = vs.ForkchoiceFetcher.PayloadContentLookup(parentRoot)
	}
	head := transition.NextSlotState(accessRoot[:], slot)
	if head != nil {
		return head, nil
	}
	// cache miss
	head, err := vs.StateGen.StateByRoot(ctx, accessRoot)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "could not obtain head state")
	}
	return head, nil
}

func logFailedReorgAttempt(slot primitives.Slot, oldHeadRoot, headRoot [32]byte) {
	blockchain.LateBlockAttemptedReorgCount.Inc()
	log.WithFields(logrus.Fields{
		"slot":        slot,
		"oldHeadRoot": fmt.Sprintf("%#x", oldHeadRoot),
		"headRoot":    fmt.Sprintf("%#x", headRoot),
	}).Warn("Late block attempted reorg failed")
}

func (vs *Server) getHeadNoReorg(ctx context.Context, slot primitives.Slot, parentRoot [32]byte) (state.BeaconState, error) {
	// Try to get the state from the NSC
	accessRoot := parentRoot
	if slots.ToEpoch(slot) >= params.BeaconConfig().GloasForkEpoch {
		accessRoot, _ = vs.ForkchoiceFetcher.PayloadContentLookup(parentRoot)
	}
	head := transition.NextSlotState(accessRoot[:], slot)
	if head != nil {
		return head, nil
	}
	head, err := vs.HeadFetcher.HeadState(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not get head state: %v", err)
	}
	return head, nil
}

func (vs *Server) getParentStateFromReorgData(ctx context.Context, slot primitives.Slot, oldHeadRoot, parentRoot, headRoot [32]byte) (head state.BeaconState, err error) {
	if parentRoot != headRoot {
		head, err = vs.handleSuccesfulReorgAttempt(ctx, slot, parentRoot, headRoot)
	} else {
		if oldHeadRoot != headRoot {
			logFailedReorgAttempt(slot, oldHeadRoot, headRoot)
		}
		head, err = vs.getHeadNoReorg(ctx, slot, parentRoot)
	}
	if err != nil {
		return nil, err
	}
	if head.Slot() >= slot {
		return head, nil
	}
	head, err = transition.ProcessSlotsUsingNextSlotCache(ctx, head, parentRoot[:], slot)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not process slots up to %d: %v", slot, err)
	}
	return head, nil
}

func (vs *Server) getParentState(ctx context.Context, slot primitives.Slot) (state.BeaconState, [32]byte, error) {
	// process attestations and update head in forkchoice
	oldHeadRoot := vs.ForkchoiceFetcher.CachedHeadRoot()
	vs.ForkchoiceFetcher.UpdateHead(ctx, vs.TimeFetcher.CurrentSlot())
	headRoot := vs.ForkchoiceFetcher.CachedHeadRoot()
	parentRoot := vs.ForkchoiceFetcher.GetProposerHead()
	head, err := vs.getParentStateFromReorgData(ctx, slot, oldHeadRoot, parentRoot, headRoot)
	return head, parentRoot, err
}

func (vs *Server) BuildBlockParallel(ctx context.Context, sBlk interfaces.SignedBeaconBlock, head state.BeaconState, skipMevBoost bool, builderBoostFactor primitives.Gwei) (*ethpb.GenericBeaconBlock, error) {
	// Build consensus fields in background
	var wg sync.WaitGroup
	wg.Go(func() {

		// Set eth1 data.
		eth1Data, err := vs.eth1DataMajorityVote(ctx, head)
		if err != nil {
			eth1Data = &ethpb.Eth1Data{DepositRoot: params.BeaconConfig().ZeroHash[:], BlockHash: params.BeaconConfig().ZeroHash[:]}
			log.WithError(err).Error("Could not get eth1data")
		}
		sBlk.SetEth1Data(eth1Data)

		// Set deposit and attestation.
		deposits, atts, err := vs.packDepositsAndAttestations(ctx, head, sBlk.Block().Slot(), eth1Data) // TODO: split attestations and deposits
		if err != nil {
			sBlk.SetDeposits([]*ethpb.Deposit{})
			if err := sBlk.SetAttestations([]ethpb.Att{}); err != nil {
				log.WithError(err).Error("Could not set attestations on block")
			}
			log.WithError(err).Error("Could not pack deposits and attestations")
		} else {
			sBlk.SetDeposits(deposits)
			if err := sBlk.SetAttestations(atts); err != nil {
				log.WithError(err).Error("Could not set attestations on block")
			}
		}

		// Set slashings.
		validProposerSlashings, validAttSlashings := vs.getSlashings(ctx, head)
		sBlk.SetProposerSlashings(validProposerSlashings)
		if err := sBlk.SetAttesterSlashings(validAttSlashings); err != nil {
			log.WithError(err).Error("Could not set attester slashings on block")
		}

		// Set exits.
		sBlk.SetVoluntaryExits(vs.getExits(head, sBlk.Block().Slot()))

		// Set sync aggregate. New in Altair.
		vs.setSyncAggregate(ctx, sBlk, head)

		// Set bls to execution change. New in Capella.
		vs.setBlsToExecData(sBlk, head)

		// Set payload attestations. New in Gloas.
		if sBlk.Version() >= version.Gloas {
			if err := sBlk.SetPayloadAttestations(vs.getPayloadAttestations(ctx, head, sBlk.Block().ParentRoot())); err != nil {
				log.WithError(err).Error("Could not set payload attestations")
			}
		}
	})

	winningBid := primitives.ZeroWei()
	selfBuildEnvelope := true
	var bundle enginev1.BlobsBundler
	var local *blocks.GetPayloadResponse
	if sBlk.Version() >= version.Bellatrix {
		var err error
		local, err = vs.getLocalPayload(ctx, sBlk.Block(), head)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Could not get local payload: %v", err)
		}

		if sBlk.Version() < version.Gloas {
			// There's no reason to try to get a builder bid if local override is true.
			var builderBid builderapi.Bid
			if !(local.OverrideBuilder || skipMevBoost) {
				latestHeader, err := head.LatestExecutionPayloadHeader()
				if err != nil {
					return nil, status.Errorf(codes.Internal, "Could not get latest execution payload header: %v", err)
				}
				parentGasLimit := latestHeader.GasLimit()
				builderBid, err = vs.getBuilderPayloadAndBlobs(ctx, sBlk.Block().Slot(), sBlk.Block().ProposerIndex(), parentGasLimit)
				if err != nil {
					builderGetPayloadMissCount.Inc()
					log.WithError(err).Error("Could not get builder payload")
				}
			}

			winningBid, bundle, err = setExecutionData(ctx, sBlk, local, builderBid, builderBoostFactor)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "Could not set execution data: %v", err)
			}
		} else {
			selfBuildOnly := local.OverrideBuilder || skipMevBoost
			selfBuildEnvelope, err = vs.setExecutionPayloadBid(ctx, sBlk, local, selfBuildOnly)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "Could not set execution data for Gloas: %v", err)
			}
		}
	}

	wg.Wait()

	sr, err := vs.computeStateRoot(ctx, sBlk)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not compute state root: %v", err)
	}
	sBlk.SetStateRoot(sr)

	// For Gloas self-build, cache the execution payload envelope now that the
	// block is fully built (state root set). The envelope needs the final block
	// HTR as BeaconBlockRoot and the post-payload state root as StateRoot.
	// When a remote P2P bid was selected, the winning builder is responsible
	// for producing the envelope, so we must not cache a self-build one.
	if sBlk.Version() >= version.Gloas && selfBuildEnvelope {
		if err := vs.storeExecutionPayloadEnvelope(sBlk, local); err != nil {
			return nil, status.Errorf(codes.Internal, "Could not build execution payload envelope: %v", err)
		}
	}

	return vs.constructGenericBeaconBlock(sBlk, bundle, winningBid)
}

// Deprecated: The gRPC API will remain the default and fully supported through v8 (expected in 2026) but will be eventually removed in favor of REST API.
//
// ProposeBeaconBlock handles the proposal of beacon blocks.
func (vs *Server) ProposeBeaconBlock(ctx context.Context, req *ethpb.GenericSignedBeaconBlock) (*ethpb.ProposeResponse, error) {
	var (
		blobSidecars       []*ethpb.BlobSidecar
		dataColumnSidecars []blocks.RODataColumn
	)

	ctx, span := trace.StartSpan(ctx, "ProposerServer.ProposeBeaconBlock")
	defer span.End()

	if req == nil {
		return nil, status.Errorf(codes.InvalidArgument, "empty request")
	}

	block, err := blocks.NewSignedBeaconBlock(req.Block)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%s: %v", "decode block failed", err)
	}
	root, err := block.Block().HashTreeRoot()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not hash tree root: %v", err)
	}
	if phase, w, ok, err := experiment.ReorgPhaseForSlot(uint64(block.Block().Slot())); err != nil {
		log.WithError(err).WithField("slot", block.Block().Slot()).Warn("[REORG] Beacon failed to read scheduled reorg windows")
	} else if ok {
		log.WithField("slot", block.Block().Slot()).WithFields(reorgBeaconLogFields(phase, w)).Warn("[REORG] Beacon ProposeBeaconBlock matched scheduled phase")
		if phase == "isolated_honest_slot_1" || phase == "isolated_honest_slot_2" {
			reorgStoreIsolatedRoot(uint64(block.Block().Slot()), reorgRootString(root))
		}
	}

	// For post-Fulu blinded blocks, submit to relay and return early
	if block.IsBlinded() && slots.ToEpoch(block.Block().Slot()) >= params.BeaconConfig().FuluForkEpoch {
		err := vs.BlockBuilder.SubmitBlindedBlockPostFulu(ctx, block)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Could not submit blinded block post-Fulu: %v", err)
		}
		return &ethpb.ProposeResponse{BlockRoot: root[:]}, nil
	}

	rob, err := blocks.NewROBlockWithRoot(block, root)
	if block.IsBlinded() {
		block, blobSidecars, err = vs.handleBlindedBlock(ctx, block)
		if errors.Is(err, builderapi.ErrBadGateway) {
			log.WithError(err).Info("Optimistically proposed block - builder relay temporarily unavailable, block may arrive over P2P")
			return &ethpb.ProposeResponse{BlockRoot: root[:]}, nil
		}
	} else if block.Version() >= version.Deneb && block.Version() < version.Gloas {
		blobSidecars, dataColumnSidecars, err = vs.handleUnblindedBlock(rob, req)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%s: %v", "handle block failed", err)
	}

	if phase, w, ok, err := experiment.ReorgPhaseForSlot(uint64(block.Block().Slot())); err != nil {
		log.WithError(err).WithField("slot", block.Block().Slot()).Warn("[REORG] Beacon failed to read scheduled reorg windows")
	} else if ok && (phase == "private_slot_1" || phase == "private_slot_2" || phase == "private_slot_3") {
		return vs.reorgWithholdPrivateBlock(ctx, phase, w, block, root)
	} else if ok && phase == "release_slot" {
		private1, ok1 := reorgLoadPrivateBlock(w.PrivateSlot1)
		private2, ok2 := reorgLoadPrivateBlock(w.PrivateSlot2)
		private3, ok3 := reorgLoadPrivateBlock(w.PrivateSlot3)

		privateRoot1 := ""
		privateRoot2 := ""
		privateRoot3 := ""
		if ok1 {
			privateRoot1 = reorgRootString(private1.root)
		}
		if ok2 {
			privateRoot2 = reorgRootString(private2.root)
		}
		if ok3 {
			privateRoot3 = reorgRootString(private3.root)
		}

		releaseBlockRoot := reorgRootString(root)
		releaseParentRoot := fmt.Sprintf("%#x", block.Block().ParentRoot())
		isolatedRoot1 := reorgLoadIsolatedRoot(w.IsolatedHonestSlot1)
		isolatedRoot2 := reorgLoadIsolatedRoot(w.IsolatedHonestSlot2)

		success := "false"
		reason := "release_parent_not_private_slot_3"
		if !ok1 {
			reason = "missing_private_slot_1"
		} else if !ok2 {
			reason = "missing_private_slot_2"
		} else if !ok3 {
			reason = "missing_private_slot_3"
		} else if releaseParentRoot == privateRoot3 {
			success = "true"
			reason = "release_parent_matches_private_slot_3"
		}

		if experiment.ShouldWriteReorgResults() {
			if err := experiment.AppendReorgResult(experiment.ReorgResult{
				Epoch:               w.Epoch,
				StartSlot:           w.StartSlot,
				PrivateSlot1:        w.PrivateSlot1,
				PrivateSlot2:        w.PrivateSlot2,
				PrivateSlot3:        w.PrivateSlot3,
				IsolatedHonestSlot1: w.IsolatedHonestSlot1,
				IsolatedHonestSlot2: w.IsolatedHonestSlot2,
				ReleaseSlot:         w.ReleaseSlot,
				PrivateRoot1:        privateRoot1,
				PrivateRoot2:        privateRoot2,
				PrivateRoot3:        privateRoot3,
				IsolatedHonestRoot1: isolatedRoot1,
				IsolatedHonestRoot2: isolatedRoot2,
				ReleaseBlockRoot:    releaseBlockRoot,
				ReleaseParentRoot:   releaseParentRoot,
				Success:             success,
				Reason:              reason,
			}); err != nil {
				log.WithError(err).WithFields(reorgBeaconLogFields(phase, w)).Warn("[REORG] Failed to write reorg result")
			}
		}

		log.WithFields(reorgBeaconLogFields(phase, w)).WithFields(logrus.Fields{
			"privateRoot1":      privateRoot1,
			"privateRoot2":      privateRoot2,
			"privateRoot3":      privateRoot3,
			"isolatedRoot1":     isolatedRoot1,
			"isolatedRoot2":     isolatedRoot2,
			"releaseBlockRoot":  releaseBlockRoot,
			"releaseParentRoot": releaseParentRoot,
			"success":           success,
			"reason":            reason,
		}).Warn("[REORG] Recorded reorg result")
	}

	var wg sync.WaitGroup
	errChan := make(chan error, 1)

	wg.Add(1)
	go func() {
		if err := vs.broadcastReceiveBlock(ctx, &wg, block, root); err != nil {
			errChan <- errors.Wrap(err, "broadcast/receive block failed")
			return
		}
		errChan <- nil
	}()

	wg.Wait()

	if block.Version() < version.Gloas {
		if err := vs.broadcastAndReceiveSidecars(ctx, block, root, blobSidecars, dataColumnSidecars); err != nil {
			return nil, status.Errorf(codes.Internal, "Could not broadcast/receive sidecars: %v", err)
		}
	}
	if err := <-errChan; err != nil {
		return nil, status.Errorf(codes.Internal, "Could not broadcast/receive block: %v", err)
	}

	return &ethpb.ProposeResponse{BlockRoot: root[:]}, nil
}

// broadcastAndReceiveSidecars broadcasts and receives sidecars.
func (vs *Server) broadcastAndReceiveSidecars(
	ctx context.Context,
	block interfaces.SignedBeaconBlock,
	root [fieldparams.RootLength]byte,
	blobSidecars []*ethpb.BlobSidecar,
	dataColumnSidecars []blocks.RODataColumn,
) error {
	if block.Version() >= version.Fulu {
		if err := vs.broadcastAndReceiveDataColumns(ctx, dataColumnSidecars); err != nil {
			return errors.Wrap(err, "broadcast and receive data columns")
		}
		return nil
	}

	if err := vs.broadcastAndReceiveBlobs(ctx, blobSidecars, root); err != nil {
		return errors.Wrap(err, "broadcast and receive blobs")
	}

	return nil
}

// handleBlindedBlock processes blinded beacon blocks (pre-Fulu only).
// Post-Fulu blinded blocks are handled directly in ProposeBeaconBlock.
func (vs *Server) handleBlindedBlock(ctx context.Context, block interfaces.SignedBeaconBlock) (interfaces.SignedBeaconBlock, []*ethpb.BlobSidecar, error) {
	if block.Version() < version.Bellatrix {
		return nil, nil, errors.New("pre-Bellatrix blinded block")
	}

	if vs.BlockBuilder == nil || !vs.BlockBuilder.Configured() {
		return nil, nil, errors.New("unconfigured block builder")
	}

	copiedBlock, err := block.Copy()
	if err != nil {
		return nil, nil, err
	}

	payload, bundle, err := vs.BlockBuilder.SubmitBlindedBlock(ctx, block)
	if err != nil {
		return nil, nil, errors.Wrap(err, "submit blinded block failed")
	}

	if err := copiedBlock.Unblind(payload); err != nil {
		return nil, nil, errors.Wrap(err, "unblind failed")
	}

	sidecars, err := unblindBlobsSidecars(copiedBlock, bundle)
	if err != nil {
		return nil, nil, errors.Wrap(err, "unblind blobs sidecars: commitment value doesn't match block")
	}

	return copiedBlock, sidecars, nil
}

func (vs *Server) handleUnblindedBlock(
	block blocks.ROBlock,
	req *ethpb.GenericSignedBeaconBlock,
) ([]*ethpb.BlobSidecar, []blocks.RODataColumn, error) {
	rawBlobs, proofs, err := blobsAndProofs(req)
	if err != nil {
		return nil, nil, err
	}

	if block.Version() >= version.Fulu {
		// Compute cells and proofs from the blobs and cell proofs.
		cellsPerBlob, proofsPerBlob, err := peerdas.ComputeCellsAndProofsFromFlat(rawBlobs, proofs)
		if err != nil {
			return nil, nil, errors.Wrap(err, "compute cells and proofs")
		}

		// Construct data column sidecars from the signed block and cells and proofs.
		roDataColumnSidecars, err := peerdas.DataColumnSidecars(cellsPerBlob, proofsPerBlob, peerdas.PopulateFromBlock(block))
		if err != nil {
			return nil, nil, errors.Wrap(err, "data column sidcars")
		}

		return nil, roDataColumnSidecars, nil
	}

	blobSidecars, err := BuildBlobSidecars(block, rawBlobs, proofs)
	if err != nil {
		return nil, nil, errors.Wrap(err, "build blob sidecars")
	}

	return blobSidecars, nil, nil
}

// broadcastReceiveBlock broadcasts a block and handles its reception.
func (vs *Server) broadcastReceiveBlock(ctx context.Context, wg *sync.WaitGroup, block interfaces.SignedBeaconBlock, root [fieldparams.RootLength]byte) error {
	if err := vs.broadcastBlock(ctx, wg, block, root); err != nil {
		return errors.Wrap(err, "broadcast block")
	}

	vs.BlockNotifier.BlockFeed().Send(&feed.Event{
		Type: blockfeed.ReceivedBlock,
		Data: &blockfeed.ReceivedBlockData{SignedBlock: block},
	})

	if err := vs.BlockReceiver.ReceiveBlock(ctx, block, root, nil); err != nil {
		return errors.Wrap(err, "receive block")
	}

	return nil
}

func (vs *Server) broadcastBlock(ctx context.Context, wg *sync.WaitGroup, block interfaces.SignedBeaconBlock, root [fieldparams.RootLength]byte) error {
	defer wg.Done()

	protoBlock, err := block.Proto()
	if err != nil {
		return errors.Wrap(err, "protobuf conversion failed")
	}
	if err := vs.P2P.Broadcast(ctx, protoBlock); err != nil {
		return errors.Wrap(err, "broadcast failed")
	}

	log.WithFields(logrus.Fields{
		"slot": block.Block().Slot(),
		"root": fmt.Sprintf("%#x", root),
	}).Debug("Broadcasted block")

	return nil
}

// broadcastAndReceiveBlobs handles the broadcasting and reception of blob sidecars.
func (vs *Server) broadcastAndReceiveBlobs(ctx context.Context, sidecars []*ethpb.BlobSidecar, root [fieldparams.RootLength]byte) error {
	eg, eCtx := errgroup.WithContext(ctx)
	for subIdx, sc := range sidecars {
		eg.Go(func() error {
			if err := vs.P2P.BroadcastBlob(eCtx, uint64(subIdx), sc); err != nil {
				return errors.Wrap(err, "broadcast blob failed")
			}
			readOnlySc, err := blocks.NewROBlobWithRoot(sc, root)
			if err != nil {
				return errors.Wrap(err, "ROBlob creation failed")
			}
			verifiedBlob := blocks.NewVerifiedROBlob(readOnlySc)
			if err := vs.BlobReceiver.ReceiveBlob(ctx, verifiedBlob); err != nil {
				return errors.Wrap(err, "receive blob failed")
			}
			vs.OperationNotifier.OperationFeed().Send(&feed.Event{
				Type: operation.BlobSidecarReceived,
				Data: &operation.BlobSidecarReceivedData{Blob: &verifiedBlob},
			})
			return nil
		})
	}
	return eg.Wait()
}

// broadcastAndReceiveDataColumns handles the broadcasting and reception of data columns sidecars.
func (vs *Server) broadcastAndReceiveDataColumns(ctx context.Context, roSidecars []blocks.RODataColumn) error {
	// We built this block ourselves, so we can upgrade the read only data column sidecar into a verified one.
	verifiedSidecars := make([]blocks.VerifiedRODataColumn, 0, len(roSidecars))
	for _, sidecar := range roSidecars {
		verifiedSidecar := blocks.NewVerifiedRODataColumn(sidecar)
		verifiedSidecars = append(verifiedSidecars, verifiedSidecar)
	}

	// Broadcast sidecars (non blocking).
	if err := vs.P2P.BroadcastDataColumnSidecars(ctx, verifiedSidecars); err != nil {
		return errors.Wrap(err, "broadcast data column sidecars")
	}

	// In parallel, receive sidecars.
	if err := vs.DataColumnReceiver.ReceiveDataColumns(verifiedSidecars); err != nil {
		return errors.Wrap(err, "receive data columns")
	}

	return nil
}

// Deprecated: The gRPC API will remain the default and fully supported through v8 (expected in 2026) but will be eventually removed in favor of REST API.
//
// PrepareBeaconProposer caches and updates the fee recipient for the given proposer.
func (vs *Server) PrepareBeaconProposer(
	_ context.Context, request *ethpb.PrepareBeaconProposerRequest,
) (*emptypb.Empty, error) {
	var validatorIndices []primitives.ValidatorIndex

	for _, r := range request.Recipients {
		recipient := hexutil.Encode(r.FeeRecipient)
		if !common.IsHexAddress(recipient) {
			return nil, status.Errorf(codes.InvalidArgument, "Invalid fee recipient address: %v", recipient)
		}
		// Use default address if the burn address is return
		feeRecipient := primitives.ExecutionAddress(r.FeeRecipient)
		if feeRecipient == primitives.ExecutionAddress([20]byte{}) {
			feeRecipient = primitives.ExecutionAddress(params.BeaconConfig().DefaultFeeRecipient)
			if feeRecipient == primitives.ExecutionAddress([20]byte{}) {
				log.WithField("validatorIndex", r.ValidatorIndex).Warn("Fee recipient is the burn address")
			}
		}
		val := cache.TrackedValidator{
			Active:       true, // TODO: either check or add the field in the request
			Index:        r.ValidatorIndex,
			FeeRecipient: feeRecipient,
		}
		vs.TrackedValidatorsCache.Set(val)
		validatorIndices = append(validatorIndices, r.ValidatorIndex)
	}

	if len(validatorIndices) == 0 {
		return &emptypb.Empty{}, nil

	}

	log := log.WithField("validatorCount", len(validatorIndices))
	if logrus.GetLevel() >= logrus.TraceLevel {
		log = log.WithField("validatorIndices", validatorIndices)
	}

	log.Debug("Updated fee recipient addresses")

	return &emptypb.Empty{}, nil
}

// Deprecated: The gRPC API will remain the default and fully supported through v8 (expected in 2026) but will be eventually removed in favor of REST API.
//
// GetFeeRecipientByPubKey returns a fee recipient from the beacon node's settings or db based on a given public key
func (vs *Server) GetFeeRecipientByPubKey(ctx context.Context, request *ethpb.FeeRecipientByPubKeyRequest) (*ethpb.FeeRecipientByPubKeyResponse, error) {
	ctx, span := trace.StartSpan(ctx, "validator.GetFeeRecipientByPublicKey")
	defer span.End()
	if request == nil {
		return nil, status.Errorf(codes.InvalidArgument, "request was empty")
	}

	resp, err := vs.ValidatorIndex(ctx, &ethpb.ValidatorIndexRequest{PublicKey: request.PublicKey})
	if err != nil {
		if strings.Contains(err.Error(), "Could not find validator index") {
			return &ethpb.FeeRecipientByPubKeyResponse{
				FeeRecipient: params.BeaconConfig().DefaultFeeRecipient.Bytes(),
			}, nil
		} else {
			log.WithError(err).Error("An error occurred while retrieving validator index")
			return nil, err
		}
	}
	address, err := vs.BeaconDB.FeeRecipientByValidatorID(ctx, resp.GetIndex())
	if err != nil {
		if errors.Is(err, kv.ErrNotFoundFeeRecipient) {
			return &ethpb.FeeRecipientByPubKeyResponse{
				FeeRecipient: params.BeaconConfig().DefaultFeeRecipient.Bytes(),
			}, nil
		} else {
			log.WithError(err).Error("An error occurred while retrieving fee recipient from db")
			return nil, status.Errorf(codes.Internal, "error=%s", err)
		}
	}
	return &ethpb.FeeRecipientByPubKeyResponse{
		FeeRecipient: address.Bytes(),
	}, nil
}

// computeStateRoot computes the state root after a block has been processed through a state transition and
// returns it to the validator client.
func (vs *Server) computeStateRoot(ctx context.Context, block interfaces.SignedBeaconBlock) ([]byte, error) {
	roblock, err := blocks.NewROBlockWithRoot(block, [32]byte{}) // root is not used
	if err != nil {
		return nil, errors.Wrap(err, "could not create ROBlock")
	}
	beaconState, err := vs.BlockReceiver.GetPrestateToPropose(ctx, roblock)
	if err != nil {
		if phase, w, ok, phaseErr := experiment.ReorgPhaseForSlot(uint64(block.Block().Slot())); phaseErr == nil && ok && (phase == "private_slot_2" || phase == "private_slot_3") {
			parentSlot := w.PrivateSlot1
			if phase == "private_slot_3" {
				parentSlot = w.PrivateSlot2
			}
			if privateState, _, privateOK := reorgPrivateParent(parentSlot); privateOK {
				beaconState = privateState
				err = nil
			}
		}
		if err != nil {
			return nil, errors.Wrap(err, "could not retrieve beacon state")
		}
	}
	root, err := transition.CalculateStateRoot(
		ctx,
		beaconState,
		block,
	)
	if err != nil {
		return vs.handleStateRootError(ctx, block, err)
	}

	log.WithField("beaconStateRoot", fmt.Sprintf("%#x", root)).Debugf("Computed state root")
	return root[:], nil
}

type computeStateRootAttemptsKeyType string

const computeStateRootAttemptsKey = computeStateRootAttemptsKeyType("compute-state-root-attempts")
const maxComputeStateRootAttempts = 3

// handleStateRootError retries block construction in some error cases.
func (vs *Server) handleStateRootError(ctx context.Context, block interfaces.SignedBeaconBlock, err error) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, status.Errorf(codes.Canceled, "context error: %v", ctx.Err())
	}
	switch {
	case errors.Is(err, transition.ErrAttestationsSignatureInvalid),
		errors.Is(err, transition.ErrProcessAttestationsFailed):
		log.WithError(err).Warn("Retrying block construction without attestations")
		if err := block.SetAttestations([]ethpb.Att{}); err != nil {
			return nil, errors.Wrap(err, "could not set attestations")
		}
	case errors.Is(err, transition.ErrProcessBLSChangesFailed), errors.Is(err, transition.ErrBLSToExecutionChangesSignatureInvalid):
		log.WithError(err).Warn("Retrying block construction without BLS to execution changes")
		if err := block.SetBLSToExecutionChanges([]*ethpb.SignedBLSToExecutionChange{}); err != nil {
			return nil, errors.Wrap(err, "could not set BLS to execution changes")
		}
	case errors.Is(err, transition.ErrProcessProposerSlashingsFailed):
		log.WithError(err).Warn("Retrying block construction without proposer slashings")
		block.SetProposerSlashings([]*ethpb.ProposerSlashing{})
	case errors.Is(err, transition.ErrProcessAttesterSlashingsFailed):
		log.WithError(err).Warn("Retrying block construction without attester slashings")
		if err := block.SetAttesterSlashings([]ethpb.AttSlashing{}); err != nil {
			return nil, errors.Wrap(err, "could not set attester slashings")
		}
	case errors.Is(err, transition.ErrProcessVoluntaryExitsFailed):
		log.WithError(err).Warn("Retrying block construction without voluntary exits")
		block.SetVoluntaryExits([]*ethpb.SignedVoluntaryExit{})
	case errors.Is(err, transition.ErrProcessSyncAggregateFailed):
		log.WithError(err).Warn("Retrying block construction without sync aggregate")
		emptySig := [96]byte{0xC0}
		emptyAggregate := &ethpb.SyncAggregate{
			SyncCommitteeBits:      make([]byte, params.BeaconConfig().SyncCommitteeSize/8),
			SyncCommitteeSignature: emptySig[:],
		}
		if err := block.SetSyncAggregate(emptyAggregate); err != nil {
			log.WithError(err).Error("Could not set sync aggregate")
		}

	default:
		return nil, errors.Wrap(err, "could not compute state root")
	}
	// prevent deep recursion by limiting max attempts.
	if v, ok := ctx.Value(computeStateRootAttemptsKey).(int); !ok {
		ctx = context.WithValue(ctx, computeStateRootAttemptsKey, int(1))
	} else if v >= maxComputeStateRootAttempts {
		return nil, fmt.Errorf("attempted max compute state root attempts %d", maxComputeStateRootAttempts)
	} else {
		ctx = context.WithValue(ctx, computeStateRootAttemptsKey, v+1)
	}
	// recursive call to compute state root again
	return vs.computeStateRoot(ctx, block)
}

// Deprecated: The gRPC API will remain the default and fully supported through v8 (expected in 2026) but will be eventually removed in favor of REST API.
//
// SubmitValidatorRegistrations submits validator registrations.
func (vs *Server) SubmitValidatorRegistrations(ctx context.Context, reg *ethpb.SignedValidatorRegistrationsV1) (*emptypb.Empty, error) {
	if vs.BlockBuilder == nil || !vs.BlockBuilder.Configured() {
		return &emptypb.Empty{}, status.Errorf(codes.InvalidArgument, "Could not register block builder: %v", builder.ErrNoBuilder)
	}

	if err := vs.BlockBuilder.RegisterValidator(ctx, reg.Messages); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "Could not register block builder: %v", err)
	}

	return &emptypb.Empty{}, nil
}

func blobsAndProofs(req *ethpb.GenericSignedBeaconBlock) ([][]byte, [][]byte, error) {
	switch {
	case req.GetDeneb() != nil:
		dbBlockContents := req.GetDeneb()
		return dbBlockContents.Blobs, dbBlockContents.KzgProofs, nil
	case req.GetElectra() != nil:
		dbBlockContents := req.GetElectra()
		return dbBlockContents.Blobs, dbBlockContents.KzgProofs, nil
	case req.GetFulu() != nil:
		dbBlockContents := req.GetFulu()
		return dbBlockContents.Blobs, dbBlockContents.KzgProofs, nil
	default:
		return nil, nil, errors.Errorf("unknown request type provided: %T", req)
	}
}
