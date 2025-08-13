# BaseApp — CometBFT (ABCI++) Template

> A minimal, extensible starter for building apps on **CometBFT v0.38.x (ABCI++)**.  
> This README explains **what to put in each core function** and gives drop-in snippets.

---

## Table of Contents

- [Overview](#overview)
- [Lifecycle (big picture)](#lifecycle-big-picture)
- [Core Functions (what to put in each)](#core-functions-what-to-put-in-each)
  - [CheckTx (mempool admission)](#checktx-mempool-admission)
  - [PrepareProposal (proposer shapes the block)](#prepareproposal-proposer-shapes-the-block)
  - [ProcessProposal (validators sanity-check the proposal)](#processproposal-validators-sanity-check-the-proposal)
  - [FinalizeBlock (deterministic execution & AppHash)](#finalizeblock-deterministic-execution--apphash)
  - [Commit (durable persistence & height bump)](#commit-durable-persistence--height-bump)
  - [Info (handshake: where is the app?)](#info-handshake-where-is-the-app)
  - [Query (read-only state access)](#query-read-only-state-access)
- [Storage (Pebble via `cometbft-db`)](#storage-pebble-via-cometbft-db)
- [Quick Start Checklist](#quick-start-checklist)
- [RPC Cheat Sheet](#rpc-cheat-sheet)
- [Determinism & Gotchas](#determinism--gotchas)
- [Extending the Template](#extending-the-template)
- [License](#license)

---

## Overview

This repo provides a **base app** you can build on. The philosophy:

- Keep ABCI++ plumbing **boring and consistent**.
- Put **all execution** in `FinalizeBlock`.
- Put **only persistence** in `Commit`.
- Report last committed height/hash in `Info`.
- Use Pebble (via `cometbft-db`) for storage.

You then add app logic by plugging in **tx handlers** and **query routes**.

---

## Lifecycle (big picture)

Per height **H**:

```text
CheckTx*             (async, mempool admission)
PrepareProposal(H)   (proposer filters/orders/limits txs <= MaxTxBytes)
ProcessProposal(H)   (all validators accept/reject the tx list)
FinalizeBlock(H)     (execute deterministically; produce TxResults & AppHash)
Commit(H)            (durably persist what was computed; bump lastHeight)
```

On startup:

```text
Info()               (report last committed height/app hash for replay)
```

Clients:

```text
Query()              (read-only state lookups)
```

**Golden rule:** execute in `FinalizeBlock`, persist in `Commit`, report via `Info`.

---

## Core Functions (what to put in each)

### CheckTx (mempool admission)

**Purpose:** Cheap, stateless screening before mempool acceptance.

**Do here**

- Decode/size checks, basic format/signature, fee policy, optional anti-replay.
- Return non-zero `Code` to reject.

**Don’t do**

- Heavy state reads/writes.

```go
func (app *App) CheckTx(ctx context.Context, req *abci.CheckTxRequest) (*abci.CheckTxResponse, error) {
    // TODO: decode & quick stateless checks
    return &abci.CheckTxResponse{Code: 0}, nil
}
```

---

### PrepareProposal (proposer shapes the block)

**Who/when:** Only on the **proposer** for height H.

**Do here**

- Select/filter/order txs.
- Enforce `req.MaxTxBytes`.

```go
func (app *App) PrepareProposal(ctx context.Context, req *abci.PrepareProposalRequest) (*abci.PrepareProposalResponse, error) {
    var out [][]byte
    var sz int64
    for _, tx := range req.Txs {
        if req.MaxTxBytes >= 0 && sz+int64(len(tx)) > req.MaxTxBytes { break }
        out = append(out, tx)
        sz += int64(len(tx))
    }
    return &abci.PrepareProposalResponse{Txs: out}, nil
}
```

---

### ProcessProposal (validators sanity-check the proposal)

**Who/when:** Every validator after seeing the proposer’s tx list.

**Do here**

- Re-enforce `MaxTxBytes`.
- Optional stateless checks (duplicates, obvious violations).

```go
func (app *App) ProcessProposal(ctx context.Context, req *abci.ProcessProposalRequest) (*abci.ProcessProposalResponse, error) {
    var sz int64
    for _, tx := range req.Txs { sz += int64(len(tx)) }
    if req.MaxTxBytes >= 0 && sz > req.MaxTxBytes {
        return &abci.ProcessProposalResponse{Status: abci.PROCESS_PROPOSAL_STATUS_REJECT}, nil
    }
    return &abci.ProcessProposalResponse{Status: abci.PROCESS_PROPOSAL_STATUS_ACCEPT}, nil
}
```

---

### FinalizeBlock (deterministic execution & AppHash)

**Purpose:** Execute the block **deterministically**:

- Apply txs to **pending** (not yet persisted) state.
- Produce one `ExecTxResult` per tx (`len(TxResults) == len(req.Txs)`).
- Compute **post-block AppHash** (state root).
- Optionally set events, validator updates, consensus param updates.

**Pattern**

- Stage writes in a `db.Batch`.
- Compute AppHash from staged state.
- Keep the batch in memory for `Commit`.

```go
func (app *App) FinalizeBlock(ctx context.Context, req *abci.FinalizeBlockRequest) (*abci.FinalizeBlockResponse, error) {
    app.pendingBatch = app.db.NewBatch() // don't Write yet
    app.nextHeight   = req.Height

    results := make([]*abci.ExecTxResult, len(req.Txs))
    for i, tx := range req.Txs {
        // TODO: decode tx; read committed state; stage writes into pendingBatch
        results[i] = &abci.ExecTxResult{Code: 0}
        _ = tx
    }

    // TODO: compute deterministically from staged state (e.g., Merkle/SMT or sorted writes)
    app.lastHash = computeStateRoot(app.pendingBatch, app.lastHash, req.Height)

    return &abci.FinalizeBlockResponse{
        TxResults:             results,
        AppHash:               app.lastHash,
        ValidatorUpdates:      nil,
        ConsensusParamUpdates: nil, // or &abci.ConsensusParams{} if you update them
        Events:                nil,
    }, nil
}
```

> **Determinism tips:** never rely on map iteration order, random values, clocks, or `%v` formatting. Use canonical encodings and stable ordering when hashing.

---

### Commit (durable persistence & height bump)

**Purpose:** Make durable the state computed in `FinalizeBlock`.

**Do here**

- `pendingBatch.WriteSync()` to persist writes.
- Persist `lastHeight` (8-byte big-endian) and `lastHash`.
- Clear the pending batch.

**Don’t do**

- Re-execute txs or recompute hash.

```go
func (app *App) Commit(ctx context.Context, _ *abci.CommitRequest) (*abci.CommitResponse, error) {
    if app.pendingBatch == nil {
        return nil, fmt.Errorf("no pending batch; FinalizeBlock not called")
    }
    app.lastHeight = app.nextHeight

    // Persist metadata atomically in the same batch
    hb := make([]byte, 8)
    binary.BigEndian.PutUint64(hb, uint64(app.lastHeight))
    if err := app.pendingBatch.Set([]byte("lastHeight"), hb); err != nil { return nil, err }
    if err := app.pendingBatch.Set([]byte("lastAppHash"), app.lastHash); err != nil { return nil, err }

    if err := app.pendingBatch.WriteSync(); err != nil { return nil, err }
    app.pendingBatch.Close()
    app.pendingBatch = nil

    return &abci.CommitResponse{RetainHeight: 0}, nil // set >0 only if you want pruning
}
```

---

### Info (handshake: where is the app?)

**Purpose:** Tell the node your last committed height/app hash so it can **replay** if needed.

```go
func (app *App) Info(ctx context.Context, _ *abci.InfoRequest) (*abci.InfoResponse, error) {
    return &abci.InfoResponse{
        LastBlockHeight:  app.lastHeight,
        LastBlockAppHash: app.lastHash,
    }, nil
}
```

> If `appHeight` is below the node’s `stateHeight`, CometBFT replays.  
> If `appHeight` is **below the blockstore base height** (pruned), handshake fails — reset/import.

---

### Query (read-only state access)

**Purpose:** Client lookups. Define paths and encoding.

```go
func (app *App) Query(ctx context.Context, req *abci.QueryRequest) (*abci.QueryResponse, error) {
    // Example: /kv/<hex-key>
    key, err := parseKeyFromPath(req.Path)
    if err != nil { return &abci.QueryResponse{Code: 1, Log: err.Error()}, nil }

    val, err := app.db.Get(key)
    if err != nil { return &abci.QueryResponse{Code: 1, Log: err.Error()}, nil }
    return &abci.QueryResponse{Code: 0, Value: val}, nil
}
```

**Don’t** mutate state here; serve **committed** data only.

---

## Storage (Pebble via `cometbft-db`)

Open once, consistently:

```go
db, err := dbm.NewDB("app", dbm.PebbleDBBackend, filepath.Join(home, "data"))
// results in: <home>/data/app.db
```

**Rules**

- Keep **name** (`"app"`) and **directory** stable across restarts.
- Use `WriteSync()` in `Commit` for durability.
- `Close()` the DB cleanly on shutdown.

If you start with a **fresh app DB** but **old node data**, you may get “app below blockstore base” at handshake. Either import a matching snapshot for the app or reset the node’s `data/`.

---

## Quick Start Checklist

- [ ] Implement minimal `CheckTx` (decode + cheap checks).
- [ ] `PrepareProposal`: copy txs up to `MaxTxBytes`.
- [ ] `ProcessProposal`: reject if over `MaxTxBytes`.
- [ ] `FinalizeBlock`: execute txs → fill `ExecTxResult[]`; compute `AppHash`; keep `pendingBatch`.
- [ ] `Commit`: `WriteSync()`; persist `lastHeight` (8-byte BE) & `lastHash`.
- [ ] `Info`: return persisted height/hash.
- [ ] `Query`: read-only lookups with a clear path contract.
- [ ] Use Pebble with a **stable** path/name.
- [ ] Log `FinalizeBlock(H)` and `Commit(H)` to verify ordering.

---

## RPC Cheat Sheet

```bash
# App handshake info
curl -s localhost:26657/abci_info | jq .

# Latest block height
curl -s localhost:26657/status | jq '.result.sync_info.latest_block_height'

# Query (adapt path parsing in your app)
curl -s 'localhost:26657/abci_query?path="/kv/68656c6c6f"' | jq .
```

---

## Determinism & Gotchas

- `TxResults` length **must equal** `len(req.Txs)`.
- Use **8-byte big-endian** for persisted heights (not one byte).
- Don’t compute `AppHash` with `%v` or from unordered maps; sort keys/ops.
- If `db.Get` is empty after `Commit`, you probably forgot `Write/WriteSync()` or opened a **different DB path/name**.
- If `appHeight` < `stateHeight`, replay happens.  
  If `appHeight` < **blockstore base**, handshake fails → reset/import.
- **FinalizeBlock before Commit** — never re-execute in `Commit`.

---

## Extending the Template

- Define a **tx envelope** (JSON or protobuf) with `Type` + `Data`.
- Register **tx handlers** by type; each writes into the pending batch.
- Swap the toy hash for a **Merkle/SMT** state root if you need proofs.
- Add **Query** routes for UX/ops (balances, key/value, etc.).
- Implement **validator updates** and **consensus param updates** when needed.

---
