package builder

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/OffchainLabs/prysm/v7/api"
	"github.com/OffchainLabs/prysm/v7/consensus-types/interfaces"
	"github.com/OffchainLabs/prysm/v7/consensus-types/primitives"
	ethpb "github.com/OffchainLabs/prysm/v7/proto/prysm/v1alpha1"
	"github.com/OffchainLabs/prysm/v7/runtime/version"
	"github.com/pkg/errors"
)

const postBeaconBlockPath = "/eth/v1/builder/beacon_block"

func executionPayloadBidPath(slot primitives.Slot, parentHash, parentRoot [32]byte, proposerPubkey [48]byte) string {
	return fmt.Sprintf("/eth/v1/builder/execution_payload_bid/%d/%#x/%#x/%#x", slot, parentHash, parentRoot, proposerPubkey)
}

func builderPreferencesPath(validatorPubkey [48]byte) string {
	return fmt.Sprintf("/eth/v1/builder/builder_preferences/%#x", validatorPubkey)
}

// sszRequestOpts sets the headers for an SSZ-encoded request body of the given consensus version.
func sszRequestOpts(v int) reqOption {
	return func(r *http.Request) {
		r.Header.Set("Content-Type", api.OctetStreamMediaType)
		r.Header.Set(api.VersionHeader, version.String(v))
	}
}

// GetExecutionPayloadBid requests an execution payload bid; returns nil on 204 (no bid).
func (c *Client) GetExecutionPayloadBid(
	ctx context.Context,
	slot primitives.Slot,
	parentHash, parentRoot [32]byte,
	proposerPubkey [48]byte,
	auth *ethpb.SignedRequestAuthV1,
) (*ethpb.SignedExecutionPayloadBid, error) {
	var body []byte
	opts := []reqOption{func(r *http.Request) { r.Header.Set("Accept", api.OctetStreamMediaType) }}
	if auth != nil {
		var err error
		body, err = auth.MarshalSSZ()
		if err != nil {
			return nil, errors.Wrap(err, "could not ssz encode SignedRequestAuthV1")
		}
		opts = append(opts, sszRequestOpts(version.Gloas))
	}

	path := executionPayloadBidPath(slot, parentHash, parentRoot, proposerPubkey)
	raw, status, _, err := c.doWithStatus(ctx, http.MethodPost, path, bytes.NewReader(body), []int{http.StatusOK, http.StatusNoContent}, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "error requesting execution payload bid from builder")
	}
	if status == http.StatusNoContent {
		return nil, nil
	}
	bid := &ethpb.SignedExecutionPayloadBid{}
	if err := bid.UnmarshalSSZ(raw); err != nil {
		return nil, errors.Wrap(err, "could not ssz decode SignedExecutionPayloadBid")
	}
	return bid, nil
}

// SubmitSignedBeaconBlock sends the signed block to the builder so it can reveal the envelope.
func (c *Client) SubmitSignedBeaconBlock(ctx context.Context, sb interfaces.ReadOnlySignedBeaconBlock) error {
	if sb.Version() < version.Gloas {
		return errors.Errorf("submitSignedBeaconBlock requires Gloas or later, got %s", version.String(sb.Version()))
	}
	body, err := sb.MarshalSSZ()
	if err != nil {
		return errors.Wrap(err, "could not ssz encode SignedBeaconBlock")
	}
	if _, _, err := c.do(ctx, http.MethodPost, postBeaconBlockPath, bytes.NewReader(body), http.StatusAccepted, sszRequestOpts(sb.Version())); err != nil {
		return errors.Wrap(err, "error submitting signed beacon block to builder")
	}
	return nil
}

// SubmitBuilderPreferences submits a proposer's per-builder preferences ahead of the bid request.
func (c *Client) SubmitBuilderPreferences(ctx context.Context, validatorPubkey [48]byte, req *ethpb.BuilderPreferencesRequestV1) error {
	if req == nil {
		return errors.Wrap(errMalformedRequest, "nil builder preferences request")
	}
	body, err := req.MarshalSSZ()
	if err != nil {
		return errors.Wrap(err, "could not ssz encode BuilderPreferencesRequestV1")
	}
	if _, _, err := c.do(ctx, http.MethodPost, builderPreferencesPath(validatorPubkey), bytes.NewReader(body), http.StatusAccepted, sszRequestOpts(version.Gloas)); err != nil {
		return errors.Wrap(err, "error submitting builder preferences")
	}
	return nil
}
