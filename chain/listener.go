package chain

import (
	"fmt"
	"math/big"
	"time"

	"github.com/cosmos/cosmos-sdk/types"
	xAuthTypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	xDistriTypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	"github.com/stafihub/rtoken-relay-core/common/core"
	"github.com/stafihub/rtoken-relay-core/common/log"
	"github.com/stafihub/rtoken-relay-core/common/utils"
	stafiHubXLedgerTypes "github.com/stafihub/stafihub/x/ledger/types"
)

var (
	BlockRetryInterval = time.Second * 6
	BlockRetryLimit    = 500
	BlockConfirmNumber = int64(1)
)

type Listener struct {
	symbol             core.RSymbol
	startBlock         uint64
	blockstore         utils.Blockstorer
	conn               *Connection
	router             *core.Router
	log                log.Logger
	distributionAddStr string
	blockResults       chan *BlockResult
	stopChan           <-chan struct{}
	sysErrChan         chan<- error
}
type BlockResult struct {
	Height uint64
	Txs    []*types.TxResponse
}

func NewListener(symbol core.RSymbol, startBlock uint64, bs utils.Blockstorer, conn *Connection, log log.Logger, stopChan <-chan struct{}, sysErr chan<- error) *Listener {
	client, err := conn.GetOnePoolClient()
	if err != nil {
		sysErr <- fmt.Errorf("no pool client")
		return nil
	}
	done := core.UseSdkConfigContext(client.GetAccountPrefix())
	moduleAddressStr := xAuthTypes.NewModuleAddress(xDistriTypes.ModuleName).String()
	done()
	return &Listener{
		symbol:             symbol,
		startBlock:         startBlock,
		distributionAddStr: moduleAddressStr,
		blockstore:         bs,
		conn:               conn,
		log:                log,
		stopChan:           stopChan,
		sysErrChan:         sysErr,
		blockResults:       make(chan *BlockResult, 4096),
	}
}

func (l *Listener) setRouter(r *core.Router) {
	l.router = r
}

func (l *Listener) start() error {
	if l.router == nil {
		return fmt.Errorf("must set router with setRouter()")
	}
	poolClient, err := l.conn.GetOnePoolClient()
	if err != nil {
		return err
	}
	latestBlk, err := poolClient.GetCurrentBlockHeight()
	if err != nil {
		return err
	}

	if latestBlk < int64(l.startBlock) {
		return fmt.Errorf("starting block (%d) is greater than latest known block (%d)", l.startBlock, latestBlk)
	}

	go func() {
		err := l.pollEra()
		if err != nil {
			l.log.Error("Polling era failed", "err", err)
			l.sysErrChan <- err
		}
	}()

	go func() {
		err := l.dealBlocks()
		if err != nil {
			l.log.Error("Dealling blocks failed", "err", err)
			l.sysErrChan <- err
		}
	}()

	go func() {
		err := l.fetchBlocks()
		if err != nil {
			l.log.Error("Fetching blocks failed", "err", err)
			l.sysErrChan <- err
		}
	}()
	return nil
}

func (l *Listener) pollEra() error {
	err := l.processEra()
	if err != nil {
		return err
	}
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopChan:
			l.log.Info("pollEra receive stop chan, will stop")
			return nil
		case <-ticker.C:
			err := l.processEra()
			if err != nil {
				return err
			}
		}
	}
}

func (l *Listener) fetchBlocks() error {
	var willDealBlock = l.startBlock
	poolClient, err := l.conn.GetOnePoolClient()
	if err != nil {
		return err
	}
	retry := 0
	for {
		select {
		case <-l.stopChan:
			l.log.Info("fetchBlocks receive stop chan, will stop")
			return nil
		default:
			if retry > BlockRetryLimit {
				return fmt.Errorf("fetchBlocks reach retry limit ,symbol: %s", l.symbol)
			}
			latestBlk, err := poolClient.GetCurrentBlockHeight()
			if err != nil {
				l.log.Warn("Failed to fetch latest blockNumber", "err", err)
				retry++
				time.Sleep(BlockRetryInterval)
				continue
			}
			if latestBlk < BlockConfirmNumber {
				l.log.Warn("latest blockNumber abnoumal", "latestBlk", latestBlk)
				retry++
				time.Sleep(BlockRetryInterval)
				continue
			}

			willFinalBlock := latestBlk - BlockConfirmNumber
			if willDealBlock > uint64(willFinalBlock) {
				time.Sleep(BlockRetryInterval)
				continue
			}

			for ; willDealBlock <= uint64(willFinalBlock); willDealBlock++ {
				for i := 0; i < BlockRetryLimit; i++ {
					txs, err := poolClient.GetBlockTxsWithParseErrSkip(int64(willDealBlock))
					if err != nil {
						l.log.Warn("GetBlockTxsWithParseErrSkip failed", "block", willDealBlock, "err", err.Error())
						time.Sleep(BlockRetryInterval)
						continue
					}
					l.blockResults <- &BlockResult{Height: willDealBlock, Txs: txs}
					break
				}
			}
			retry = 0
		}
	}
}

func (l *Listener) dealBlocks() error {
	poolClient, err := l.conn.GetOnePoolClient()
	if err != nil {
		return err
	}
	for {
		select {
		case <-l.stopChan:
			l.log.Info("dealBlocks receive stop chan, will stop")
			return nil
		case blockResult := <-l.blockResults:

			retry := 0
			for {
				if retry > BlockRetryLimit {
					return fmt.Errorf("dealBlocks reach retry limit ,symbol: %s", l.symbol)
				}
				err = l.processBlockResult(poolClient, blockResult)
				if err != nil {
					l.log.Error("Failed to process results in block", "block", blockResult.Txs, "err", err)
					retry++
					time.Sleep(BlockRetryInterval)
					continue
				}

				// Write to blockstore
				err = l.blockstore.StoreBlock(new(big.Int).SetUint64(blockResult.Height))
				if err != nil {
					l.log.Error("Failed to write to blockstore", "err", err)
				}
				retry = 0
				break
			}
		}
	}
}

func (l *Listener) submitMessage(m *core.Message) error {
	if len(m.Source) == 0 || len(m.Destination) == 0 {
		return fmt.Errorf("submitMessage failed, no source or destination %s", m)
	}
	err := l.router.Send(m)
	if err != nil {
		l.log.Error("failed to send message", "err", err, "msg", m)
	}
	return err
}

func (l *Listener) hasPool(p string) bool {
	_, exist := l.conn.poolClients[p]
	if exist {
		return true
	}
	_, exist = l.conn.icaPoolClients[p]

	return exist
}

func (l *Listener) processEra() error {
	poolClient, err := l.conn.GetOnePoolClient()
	if err != nil {
		return err
	}

	var timestamp int64
	retry := 0
	for {
		if retry >= BlockRetryLimit {
			return fmt.Errorf("GetCurrentBLockAndTimestamp reach retry limit, err: %s", err)
		}

		_, timestamp, err = poolClient.GetCurrentBLockAndTimestamp()
		if err != nil {
			retry++
			time.Sleep(BlockRetryInterval)
			continue
		}
		break
	}

	if l.conn.eraSeconds <= 0 {
		return fmt.Errorf("eraSeconds must bigger than zero, eraSeconds: %d", l.conn.eraSeconds)
	}
	era := timestamp/l.conn.eraSeconds + l.conn.offset

	return l.sendNewEraMsg(uint32(era))
}

func (l *Listener) sendNewEraMsg(era uint32) error {
	proposal := core.ProposalSetChainEra{
		Denom: string(l.symbol),
		Era:   era,
	}
	m := core.Message{
		Source:      l.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonNewEra,
		Content:     proposal,
	}

	l.log.Debug("sendNewEraMsg", "msg", m)
	return l.router.Send(&m)
}

func (h *Listener) mustGetBondRecordFromStafiHub(denom, txHash string) (bondRecord *stafiHubXLedgerTypes.BondRecord, err error) {
	retry := 0
	param := &core.ParamGetBondRecord{Denom: denom, TxHash: txHash}
	for {
		if retry > BlockRetryLimit {
			return nil, fmt.Errorf("getBondRecordFromStafiHub reach retry limit")
		}
		bondRecord, err := h.getBondRecordFromStafiHub(param)
		if err != nil {
			retry++
			h.log.Debug("getBondRecordFromStafiHub failed, will retry.", "err", err)
			time.Sleep(BlockRetryInterval)
			continue
		}
		if len(bondRecord.Denom) == 0 || len(bondRecord.Pool) == 0 {
			retry++
			h.log.Debug("getBondRecordFromStafiHub failed, will retry.")
			time.Sleep(BlockRetryInterval)
			continue
		}
		return bondRecord, nil
	}
}

// will wait until interchain tx status ready
func (h *Listener) mustGetInterchainTxStatusFromStafiHub(propId string) (stafiHubXLedgerTypes.InterchainTxStatus, error) {
	var err error
	var status stafiHubXLedgerTypes.InterchainTxStatus
	for {
		status, err = h.getInterchainTxStatusFromStafiHub(propId)
		if err != nil {
			h.log.Warn("getInterchainTxStatusFromStafiHub failed, will retry.", "err", err)
			time.Sleep(BlockRetryInterval)
			continue
		}
		if status == stafiHubXLedgerTypes.InterchainTxStatusUnspecified || status == stafiHubXLedgerTypes.InterchainTxStatusInit {
			err = fmt.Errorf("status not match, status: %s", status)
			h.log.Warn("getInterchainTxStatusFromStafiHub status not success, will retry.", "err", err)
			time.Sleep(BlockRetryInterval)
			continue
		}
		return status, nil
	}
}

func (h *Listener) getInterchainTxStatusFromStafiHub(proposalId string) (s stafiHubXLedgerTypes.InterchainTxStatus, err error) {
	getInterchainTxStatus := core.ParamGetInterchainTxStatus{
		PropId: proposalId,
		Status: make(chan stafiHubXLedgerTypes.InterchainTxStatus, 1),
	}
	msg := core.Message{
		Source:      h.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonGetInterchainTxStatus,
		Content:     getInterchainTxStatus,
	}
	err = h.router.Send(&msg)
	if err != nil {
		return stafiHubXLedgerTypes.InterchainTxStatusUnspecified, err
	}

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	h.log.Debug("wait getInterchainTxStatusFromStafiHub from stafihub", "rSymbol", h.conn.symbol)
	select {
	case <-timer.C:
		return stafiHubXLedgerTypes.InterchainTxStatusUnspecified, fmt.Errorf("getInterchainTxStatus from stafihub timeout")
	case status := <-getInterchainTxStatus.Status:
		return status, nil
	}
}

func (h *Listener) getBondRecordFromStafiHub(param *core.ParamGetBondRecord) (bondRecord *stafiHubXLedgerTypes.BondRecord, err error) {
	getBondRecord := core.ParamGetBondRecord{
		Denom:      param.Denom,
		TxHash:     param.TxHash,
		BondRecord: make(chan stafiHubXLedgerTypes.BondRecord, 1),
	}
	msg := core.Message{
		Source:      h.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonGetBondRecord,
		Content:     getBondRecord,
	}
	err = h.router.Send(&msg)
	if err != nil {
		return nil, err
	}

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	h.log.Debug("wait getBondRecord from stafihub", "rSymbol", h.conn.symbol)
	select {
	case <-timer.C:
		return nil, fmt.Errorf("get bond record from stafihub timeout")
	case bondRecord := <-getBondRecord.BondRecord:
		return &bondRecord, nil
	}
}
