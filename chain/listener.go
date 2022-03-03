package chain

import (
	"fmt"
	"math/big"
	"time"

	"github.com/ChainSafe/log15"
	"github.com/cosmos/cosmos-sdk/types"
	"github.com/stafihub/rtoken-relay-core/common/core"
	"github.com/stafiprotocol/chainbridge/utils/blockstore"
)

var (
	BlockRetryInterval = time.Second * 6
	BlockRetryLimit    = 50
	BlockConfirmNumber = int64(3)
)

type Listener struct {
	name       string
	symbol     core.RSymbol
	startBlock uint64
	blockstore blockstore.Blockstorer
	conn       *Connection
	router     *core.Router
	leastBond  types.Coin
	log        log15.Logger
	stopChan   <-chan struct{}
	sysErrChan chan<- error
}

func NewListener(name string, symbol core.RSymbol, leastBond types.Coin, startBlock uint64, bs blockstore.Blockstorer, conn *Connection, log log15.Logger, stopChan <-chan struct{}, sysErr chan<- error) *Listener {

	return &Listener{
		name:       name,
		symbol:     symbol,
		startBlock: startBlock,
		blockstore: bs,
		conn:       conn,
		leastBond:  leastBond,
		log:        log,
		stopChan:   stopChan,
		sysErrChan: sysErr,
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
		err := l.pollBlocks()
		if err != nil {
			l.log.Error("Polling blocks failed", "err", err)
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

func (l *Listener) pollBlocks() error {
	var willDealBlock = l.startBlock
	var retry = BlockRetryLimit
	poolClient, err := l.conn.GetOnePoolClient()
	if err != nil {
		return err
	}
	for {
		select {
		case <-l.stopChan:
			l.log.Info("pollBlocks receive stop chan, will stop")
			return nil
		default:
			if retry <= 0 {
				return fmt.Errorf("pollBlocks reach retry limit ,symbol: %s", l.symbol)
			}

			latestBlk, err := poolClient.GetCurrentBlockHeight()
			if err != nil {
				l.log.Error("Failed to fetch latest blockNumber", "err", err)
				retry--
				time.Sleep(BlockRetryInterval)
				continue
			}
			// Sleep if the block we want comes after the most recently finalized block
			if int64(willDealBlock)+BlockConfirmNumber > latestBlk {
				if willDealBlock%100 == 0 {
					l.log.Trace("Block not yet finalized", "target", willDealBlock, "finalBlk", latestBlk)
				}
				time.Sleep(BlockRetryInterval)
				continue
			}

			err = l.processBlockEvents(int64(willDealBlock))
			if err != nil {
				l.log.Error("Failed to process events in block", "block", willDealBlock, "err", err)
				retry--
				time.Sleep(BlockRetryInterval)
				continue
			}

			// Write to blockstore
			err = l.blockstore.StoreBlock(new(big.Int).SetUint64(willDealBlock))
			if err != nil {
				l.log.Error("Failed to write to blockstore", "err", err)
			}
			willDealBlock++

			retry = BlockRetryLimit
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
	era := timestamp / l.conn.eraSeconds

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
