package validator

import (
	"bytes"
	"context"
	"time"

	"github.com/OffchainLabs/prysm/v7/beacon-chain/state"
	"github.com/OffchainLabs/prysm/v7/config/params"
	consensusblocks "github.com/OffchainLabs/prysm/v7/consensus-types/blocks"
	"github.com/OffchainLabs/prysm/v7/consensus-types/interfaces"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	"github.com/OffchainLabs/prysm/v7/crypto/bls/common"
	"github.com/OffchainLabs/prysm/v7/encoding/bytesutil"
	"github.com/OffchainLabs/prysm/v7/monitoring/tracing/trace"
	ethpb "github.com/OffchainLabs/prysm/v7/proto/prysm/v1alpha1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// builderBidTimeout bounds the builder bid request so a slow builder cannot eat the slot.
const builderBidTimeout = 500 * time.Millisecond

// bidSource indicates where the winning execution payload bid came from.
type bidSource int

const (
	bidSourceSelfBuild  bidSource = iota // local self-build; caller caches the envelope
	bidSourceP2P                         // P2P gossip bid; the builder reveals the envelope
	bidSourceBuilderAPI                  // Builder-API bid; caller submits the signed block to the builder
)

// setExecutionPayloadBid picks the best of the local, P2P, and Builder-API bids and
// returns where the winning bid came from.
func (vs *Server) setExecutionPayloadBid(
	ctx context.Context,
	sBlk interfaces.SignedBeaconBlock,
	local *consensusblocks.GetPayloadResponse,
	builderBid *ethpb.SignedExecutionPayloadBid,
	selfBuildOnly bool,
) (bidSource, error) {
	_, span := trace.StartSpan(ctx, "ProposerServer.setExecutionPayloadBid")
	defer span.End()

	if local == nil || local.ExecutionData == nil {
		return bidSourceSelfBuild, errors.New("local execution payload is nil")
	}

	if !selfBuildOnly {
		if chosen, src := vs.winningRemoteBid(sBlk, local, builderBid); chosen != nil {
			if err := sBlk.SetSignedExecutionPayloadBid(chosen); err != nil {
				return bidSourceSelfBuild, errors.Wrap(err, "could not set remote execution payload bid")
			}
			return src, nil
		}
	}

	// Fall back to self-build bid.
	bid, err := vs.createSelfBuildExecutionPayloadBid(local, sBlk.Block())
	if err != nil {
		return bidSourceSelfBuild, errors.Wrap(err, "could not create execution payload bid")
	}

	// Per spec, self-build bids must use G2 point-at-infinity as the signature.
	signedBid := &ethpb.SignedExecutionPayloadBid{
		Message:   bid,
		Signature: common.InfiniteSignature[:],
	}
	if err := sBlk.SetSignedExecutionPayloadBid(signedBid); err != nil {
		return bidSourceSelfBuild, errors.Wrap(err, "could not set signed execution payload bid")
	}

	return bidSourceSelfBuild, nil
}

// winningRemoteBid returns the highest-value remote bid exceeding the local value, or nil.
func (vs *Server) winningRemoteBid(
	sBlk interfaces.SignedBeaconBlock,
	local *consensusblocks.GetPayloadResponse,
	builderBid *ethpb.SignedExecutionPayloadBid,
) (*ethpb.SignedExecutionPayloadBid, bidSource) {
	var chosen *ethpb.SignedExecutionPayloadBid
	src := bidSourceSelfBuild

	if p2p := vs.winningP2PBid(sBlk, local, false); p2p != nil {
		chosen, src = p2p, bidSourceP2P
	}
	if builderBid != nil && builderBid.Message.Value > primitives.WeiToGwei(local.Bid) {
		if chosen == nil || builderBid.Message.Value > chosen.Message.Value {
			chosen, src = builderBid, bidSourceBuilderAPI
		}
	}
	return chosen, src
}

// getBuilderExecutionPayloadBid requests a bid from the configured builder, returning nil if none or invalid.
func (vs *Server) getBuilderExecutionPayloadBid(
	ctx context.Context,
	sBlk interfaces.SignedBeaconBlock,
	head state.BeaconState,
	local *consensusblocks.GetPayloadResponse,
) *ethpb.SignedExecutionPayloadBid {
	if vs.BlockBuilder == nil || !vs.BlockBuilder.Configured() {
		return nil
	}
	val, err := head.ValidatorAtIndexReadOnly(sBlk.Block().ProposerIndex())
	if err != nil {
		log.WithError(err).Error("Could not get proposer for builder bid request")
		return nil
	}
	pubkey := val.PublicKey()
	parentHash := bytesutil.ToBytes32(local.ExecutionData.ParentHash())
	parentRoot := sBlk.Block().ParentRoot()
	ctx, cancel := context.WithTimeout(ctx, builderBidTimeout)
	defer cancel()
	// TODO: attach a SignedRequestAuthV1 once the validator client supplies it.
	bid, err := vs.BlockBuilder.GetExecutionPayloadBid(ctx, sBlk.Block().Slot(), parentHash, parentRoot, pubkey, nil)
	if err != nil {
		builderGetPayloadMissCount.Inc()
		log.WithError(err).Error("Could not get builder execution payload bid")
		return nil
	}
	if bid == nil {
		return nil
	}
	var maxPayment uint64
	if v, ok := vs.maxExecutionPayments.Load(pubkey); ok {
		maxPayment, _ = v.(uint64)
	}
	if err := validateBuilderBid(sBlk, bid, parentHash, maxPayment); err != nil {
		log.WithError(err).Warn("Discarding invalid builder execution payload bid")
		return nil
	}
	return bid
}

// validateBuilderBid pre-filters a Builder-API bid on slot, parent linkage, and payment cap.
func validateBuilderBid(sBlk interfaces.SignedBeaconBlock, signed *ethpb.SignedExecutionPayloadBid, parentHash [32]byte, maxExecutionPayment uint64) error {
	if signed == nil || signed.Message == nil {
		return errors.New("nil builder bid")
	}
	bid := signed.Message
	if bid.Slot != sBlk.Block().Slot() {
		return errors.Errorf("bid slot %d does not match block slot %d", bid.Slot, sBlk.Block().Slot())
	}
	parentRoot := sBlk.Block().ParentRoot()
	if !bytes.Equal(bid.ParentBlockRoot, parentRoot[:]) {
		return errors.New("bid parent block root does not match block parent root")
	}
	if !bytes.Equal(bid.ParentBlockHash, parentHash[:]) {
		return errors.New("bid parent block hash does not match expected parent hash")
	}
	if uint64(bid.ExecutionPayment) > maxExecutionPayment {
		return errors.Errorf("bid execution payment %d exceeds max %d", bid.ExecutionPayment, maxExecutionPayment)
	}
	return nil
}

// recordBidSource remembers where the winning bid for slot came from.
func (vs *Server) recordBidSource(slot primitives.Slot, src bidSource) {
	vs.lastBidLock.Lock()
	defer vs.lastBidLock.Unlock()
	vs.lastBidSlot, vs.lastBidSource = slot, src
}

// bidSourceForSlot returns the recorded bid source for slot, or self-build if the record is for another slot.
func (vs *Server) bidSourceForSlot(slot primitives.Slot) bidSource {
	vs.lastBidLock.Lock()
	defer vs.lastBidLock.Unlock()
	if vs.lastBidSlot != slot {
		return bidSourceSelfBuild
	}
	return vs.lastBidSource
}

// submitBlockToBuilder sends the signed block to the builder so it can reveal the envelope.
// Best-effort and detached from the propose RPC; the builder also learns of the block via P2P.
func (vs *Server) submitBlockToBuilder(block interfaces.ReadOnlySignedBeaconBlock) {
	if vs.BlockBuilder == nil || !vs.BlockBuilder.Configured() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(params.BeaconConfig().SecondsPerSlot)*time.Second)
	defer cancel()
	if err := vs.BlockBuilder.SubmitSignedBeaconBlock(ctx, block); err != nil {
		log.WithError(err).Error("Could not submit signed beacon block to builder")
	}
}

// winningP2PBid returns a cached P2P bid if one exists and exceeds the local EL value.
func (vs *Server) winningP2PBid(
	sBlk interfaces.SignedBeaconBlock,
	local *consensusblocks.GetPayloadResponse,
	selfBuildOnly bool,
) *ethpb.SignedExecutionPayloadBid {
	if selfBuildOnly || vs.HighestBidCache == nil {
		return nil
	}

	ed := local.ExecutionData
	var parentHash [32]byte
	copy(parentHash[:], ed.ParentHash())
	cached, ok := vs.HighestBidCache.Get(sBlk.Block().Slot(), parentHash, sBlk.Block().ParentRoot())
	if !ok {
		return nil
	}

	builderValueGwei := cached.Message.Value
	localValueGwei := primitives.WeiToGwei(local.Bid)
	if builderValueGwei <= localValueGwei {
		log.WithFields(logrus.Fields{
			"slot":             sBlk.Block().Slot(),
			"builderValueGwei": builderValueGwei,
			"localValueGwei":   localValueGwei,
		}).Info("Local EL value exceeds P2P bid, using self-build")
		return nil
	}

	log.WithFields(logrus.Fields{
		"slot":             sBlk.Block().Slot(),
		"builderIndex":     cached.Message.BuilderIndex,
		"builderValueGwei": builderValueGwei,
		"localValueGwei":   localValueGwei,
	}).Info("Using P2P execution payload bid over self-build")
	return cached
}

// createSelfBuildExecutionPayloadBid creates an ExecutionPayloadBid for self-building,
// where the proposer acts as its own builder. Per spec, the bid value must be zero
// and the builder index must be BUILDER_INDEX_SELF_BUILD.
func (vs *Server) createSelfBuildExecutionPayloadBid(
	local *consensusblocks.GetPayloadResponse,
	block interfaces.ReadOnlyBeaconBlock,
) (*ethpb.ExecutionPayloadBid, error) {
	ed := local.ExecutionData
	if ed == nil || ed.IsNil() {
		return nil, errors.New("execution data is nil")
	}

	parentBlockRoot := block.ParentRoot()
	executionRequestsRoot, err := local.ExecutionRequests.HashTreeRoot()
	if err != nil {
		return nil, errors.Wrap(err, "could not compute execution requests root")
	}
	return &ethpb.ExecutionPayloadBid{
		ParentBlockHash:       ed.ParentHash(),
		ParentBlockRoot:       bytesutil.SafeCopyBytes(parentBlockRoot[:]),
		BlockHash:             ed.BlockHash(),
		PrevRandao:            ed.PrevRandao(),
		FeeRecipient:          ed.FeeRecipient(),
		GasLimit:              ed.GasLimit(),
		BuilderIndex:          params.BeaconConfig().BuilderIndexSelfBuild,
		Slot:                  block.Slot(),
		Value:                 0,
		ExecutionPayment:      0,
		BlobKzgCommitments:    local.BlobsBundler.GetKzgCommitments(),
		ExecutionRequestsRoot: executionRequestsRoot[:],
	}, nil
}
