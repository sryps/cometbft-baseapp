package comet

import (
	"context"
	"fmt"
	"log"
	"os"

	dbm "github.com/cometbft/cometbft-db"
	cfg "github.com/cometbft/cometbft/config"
	cmtflags "github.com/cometbft/cometbft/libs/cli/flags"
	cmtlog "github.com/cometbft/cometbft/libs/log"
	nm "github.com/cometbft/cometbft/node"
	"github.com/cometbft/cometbft/p2p"
	"github.com/cometbft/cometbft/privval"
	"github.com/cometbft/cometbft/proxy"
)

func Run(logLevel *string, dir *string) {
	// Initialize the logger with the default log level
	// Set up the CometBFT configuration
	config := cfg.DefaultConfig()
	config.LogLevel = *logLevel

	logger := cmtlog.NewTMLogger(cmtlog.NewSyncWriter(os.Stdout))
	logger, err := cmtflags.ParseLogLevel(config.LogLevel, logger, cfg.DefaultLogLevel)
	if err != nil {
		log.Fatalf("failed to parse log level: %v", err)
	}

	logger.Info("Config directory", dir)
	SetDefaultConfig(config)
	config.RootDir = *dir
	config.SetRoot(*dir)

	// Initialize the CometBFT application
	pv := privval.LoadFilePV(
		config.PrivValidatorKeyFile(),
		config.PrivValidatorStateFile(),
	)

	nodeKey, err := p2p.LoadNodeKey(config.NodeKeyFile())
	if err != nil {
		log.Fatalf("failed to load node's key: %v", err)
	}

	// Init Database
	appDB, err := dbm.NewDB("app", dbm.BackendType(config.DBBackend), config.DBDir())
	if err != nil {
		log.Fatalf("failed to create database: %v", err)
	}

	// Create the application instance
	app := NewCometApp(appDB)

	// Create the CometBFT node
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node, err := nm.NewNode(
		ctx,
		config,
		pv,
		nodeKey,
		proxy.NewLocalClientCreator(app),
		nm.DefaultGenesisDocProviderFunc(config),
		cfg.DBProvider(cfg.DefaultDBProvider),
		nm.DefaultMetricsProvider(config.Instrumentation),
		logger,
	)
	if err != nil {
		log.Fatalf("Creating node: %v", err)
	}

	// Start the CometBFT node
	if err := node.Start(); err != nil {
		log.Fatalf("Starting node: %v", err)
	}
	defer func() {
		node.Stop()
		node.Wait()
	}()

	<-ctx.Done()
	log.Println("Received shutdown signal, stopping all services...")
}

func GetLastBlockHashAndHeight(db dbm.DB) ([]byte, int64) {
	lastHash, _ := db.Get([]byte("lastAppHash"))
	heightBytes, _ := db.Get([]byte("lastHeight"))

	fmt.Println("Last Block Hash:", lastHash)
	fmt.Println("Last Block Height Bytes:", heightBytes)

	height := int64(0)
	if len(heightBytes) > 0 {
		height = int64(heightBytes[0])
	}
	return lastHash, height
}
