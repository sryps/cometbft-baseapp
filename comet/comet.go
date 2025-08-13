package comet

import (
	"cometbft-baseapp/app"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	dbm "github.com/cometbft/cometbft-db"
	abci "github.com/cometbft/cometbft/abci/types"
	v1 "github.com/cometbft/cometbft/api/cometbft/types/v1"
)

type CometApp struct {
	db         dbm.DB
	lastHash   []byte
	lastHeight int64
}

func NewCometApp(db dbm.DB) *CometApp {
	lastBlockHash, height := GetLastBlockHashAndHeight(db)
	return &CometApp{
		db:         db,
		lastHash:   lastBlockHash,
		lastHeight: height,
	}
}

// ------------------------
// InitChain
// ------------------------

func (cometApp *CometApp) InitChain(ctx context.Context, req *abci.InitChainRequest) (*abci.InitChainResponse, error) {
	return &abci.InitChainResponse{}, nil
}

// ------------------------
// Mempool + Proposal logic
// ------------------------

func (cometApp *CometApp) CheckTx(ctx context.Context, req *abci.CheckTxRequest) (*abci.CheckTxResponse, error) {
	// This is where the app is hooked into the CheckTx process.
	_, err := app.ProcessTX(req)
	if err != nil {
		fmt.Printf("Error processing CheckTx: %v\n", err)
		return nil, err
	}

	return &abci.CheckTxResponse{}, nil
}

// PrepareProposal: setup or filter transactions for the block proposal.
func (cometApp *CometApp) PrepareProposal(ctx context.Context, req *abci.PrepareProposalRequest) (*abci.PrepareProposalResponse, error) {
	var out [][]byte
	var sz int64
	for _, tx := range req.Txs {
		if sz+int64(len(tx)) > req.MaxTxBytes {
			break
		}
		out = append(out, tx)
		sz += int64(len(tx))
	}
	return &abci.PrepareProposalResponse{Txs: out}, nil
}

// ProcessProposal: sanity-check the proposer’s list (size, optional quick tx checks)
func (cometApp *CometApp) ProcessProposal(ctx context.Context, req *abci.ProcessProposalRequest) (*abci.ProcessProposalResponse, error) {
	var sz int64
	for _, tx := range req.Txs {
		sz += int64(len(tx))
	}

	maxTxsBytes := int64(1000000)
	if maxTxsBytes > 0 && sz > maxTxsBytes {
		return &abci.ProcessProposalResponse{Status: abci.PROCESS_PROPOSAL_STATUS_REJECT}, nil
	}
	return &abci.ProcessProposalResponse{Status: abci.PROCESS_PROPOSAL_STATUS_ACCEPT}, nil
}

// ------------------------
// Consensus
// ------------------------

// Commit: this is called at the end of a block, after all transactions have been processed.
func (cometApp *CometApp) Commit(ctx context.Context, req *abci.CommitRequest) (*abci.CommitResponse, error) {

	// This is where the app is hooked into the Commit process.
	_, err := app.CommitData(req)
	if err != nil {
		fmt.Printf("Error processing Commit: %v\n", err)
		return nil, err
	}

	b := cometApp.db.NewBatch()
	defer b.Close()

	//commit data to db
	err = b.Set([]byte("lastAppHash"), cometApp.lastHash)
	if err != nil {
		return nil, err
	}
	err = b.Set([]byte("lastHeight"), []byte{byte(cometApp.lastHeight)})
	if err != nil {
		return nil, err
	}
	if err := b.WriteSync(); err != nil {
		return nil, err
	}

	return &abci.CommitResponse{}, nil
}

// FinalizeBlock: this is called at the end of a block, after all transactions have been processed but before the block is finalized and committed.
func (cometApp *CometApp) FinalizeBlock(ctx context.Context, req *abci.FinalizeBlockRequest) (*abci.FinalizeBlockResponse, error) {
	cometApp.lastHeight = req.Height

	h := sha256.New()
	var hb [8]byte
	binary.BigEndian.PutUint64(hb[:], uint64(req.Height))
	h.Write(hb[:])
	for _, tx := range req.Txs {
		h.Write(tx)
	}
	cometApp.lastHash = h.Sum(nil)

	results := make([]*abci.ExecTxResult, len(req.Txs))
	for i, tx := range req.Txs {
		results[i] = &abci.ExecTxResult{
			Code:      0,   // 0 means OK
			Data:      nil, // optional return data
			Log:       "",  // optional log string
			Info:      "",  // optional info string
			GasWanted: 0,   // optional
			GasUsed:   0,   // optional
			Events:    nil, // optional ABCI events
		}
		// You could decode/process tx here and fill fields appropriately
		_ = tx // placeholder so tx is "used"
	}

	return &abci.FinalizeBlockResponse{
		Events:                []abci.Event{},
		ValidatorUpdates:      []abci.ValidatorUpdate{},
		ConsensusParamUpdates: &v1.ConsensusParams{},
		AppHash:               cometApp.lastHash,
		TxResults:             results,
	}, nil
}

func (cometApp *CometApp) ExtendVote(ctx context.Context, req *abci.ExtendVoteRequest) (*abci.ExtendVoteResponse, error) {
	return &abci.ExtendVoteResponse{}, nil
}

func (cometApp *CometApp) VerifyVoteExtension(ctx context.Context, req *abci.VerifyVoteExtensionRequest) (*abci.VerifyVoteExtensionResponse, error) {
	return &abci.VerifyVoteExtensionResponse{}, nil
}

// ------------------------
// Info & Queries
// ------------------------

// Info: this is called to get information about the application on startup.
func (cometApp *CometApp) Info(ctx context.Context, req *abci.InfoRequest) (*abci.InfoResponse, error) {

	return &abci.InfoResponse{
		Data:             "mycometApp",
		Version:          "v0.1.0",
		AppVersion:       1,
		LastBlockHeight:  cometApp.lastHeight,
		LastBlockAppHash: cometApp.lastHash,
	}, nil
}

// Query: this is called to query the application for data based on a path and data at /abci_query.
func (cometApp *CometApp) Query(ctx context.Context, req *abci.QueryRequest) (*abci.QueryResponse, error) {
	// This is where the app is hooked into the Query process.
	_, err := app.QueryData(req)
	if err != nil {
		fmt.Printf("Error processing Query: %v\n", err)
		return nil, err
	}

	fmt.Printf("Query: path=%s, data=%X\n", req.Path, req.Data)
	return &abci.QueryResponse{}, nil
}

func (cometApp *CometApp) ListSnapshots(ctx context.Context, req *abci.ListSnapshotsRequest) (*abci.ListSnapshotsResponse, error) {
	return &abci.ListSnapshotsResponse{}, nil
}

func (cometApp *CometApp) OfferSnapshot(ctx context.Context, req *abci.OfferSnapshotRequest) (*abci.OfferSnapshotResponse, error) {
	return &abci.OfferSnapshotResponse{}, nil
}

func (cometApp *CometApp) LoadSnapshotChunk(ctx context.Context, req *abci.LoadSnapshotChunkRequest) (*abci.LoadSnapshotChunkResponse, error) {
	return &abci.LoadSnapshotChunkResponse{}, nil
}

func (cometApp *CometApp) ApplySnapshotChunk(ctx context.Context, req *abci.ApplySnapshotChunkRequest) (*abci.ApplySnapshotChunkResponse, error) {
	return &abci.ApplySnapshotChunkResponse{}, nil
}
