package comet

import (
	"time"

	cfg "github.com/cometbft/cometbft/config"
)

func SetDefaultConfig(config *cfg.Config) {
	config.DBBackend = "pebbledb"
	config.Consensus.CreateEmptyBlocks = true
	config.Consensus.TimeoutCommit = time.Second * 2
	// Required for local testing.
	config.P2P.AllowDuplicateIP = true
}
