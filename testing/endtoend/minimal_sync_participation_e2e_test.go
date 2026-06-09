package endtoend

import (
	"fmt"
	"testing"

	"github.com/OffchainLabs/prysm/v7/config/params"
	"github.com/OffchainLabs/prysm/v7/runtime/version"
	ev "github.com/OffchainLabs/prysm/v7/testing/endtoend/evaluators"
	e2eParams "github.com/OffchainLabs/prysm/v7/testing/endtoend/params"
	"github.com/OffchainLabs/prysm/v7/testing/endtoend/types"
	"github.com/OffchainLabs/prysm/v7/testing/require"
)

func TestEndToEnd_MinimalConfig_ValidatorSyncParticipationStartup(t *testing.T) {
	params.SetupTestConfigCleanup(t)
	cfg := types.InitForkCfg(version.Electra, version.Electra, params.E2ETestConfig())
	require.NoError(t, params.SetActive(cfg))
	require.NoError(t, e2eParams.Init(t, e2eParams.StandardBeaconCount))
	tracingEndpoint := fmt.Sprintf("127.0.0.1:%d", e2eParams.TestParams.Ports.JaegerTracingPort)

	testConfig := &types.E2EConfig{
		BeaconFlags:    []string{},
		ValidatorFlags: []string{},
		EpochsToRun:    1,
		TestSync:       false,
		TestFeature:    false,
		TestDeposits:   false,
		UsePprof:       false,
		Evaluators: []types.Evaluator{
			ev.ValidatorSyncParticipation,
		},
		EvalInterceptor:     defaultInterceptor,
		TracingSinkEndpoint: tracingEndpoint,
	}

	newTestRunner(t, testConfig).run()
}
