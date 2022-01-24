package chain

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/types"
	"github.com/stafiprotocol/rtoken-relay-core/core"
)

func (l *Listener) processBlockEvents(currentBlock int64) error {
	if currentBlock%100 == 0 {
		l.log.Debug("processEvents", "blockNum", currentBlock)
	}

	poolClient, err := l.conn.GetOnePoolClient()
	if err != nil {
		return err
	}
	txs, err := poolClient.GetBlockTxs(currentBlock)
	if err != nil {
		return err
	}
	for _, tx := range txs {
		for _, log := range tx.Logs {
			for _, event := range log.Events {
				err := l.processStringEvents(event)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (l *Listener) processStringEvents(event types.StringEvent) error {
	m := core.Message{
		Source:      l.symbol,
		Destination: l.caredSymbol,
	}
	switch {
	default:
		return fmt.Errorf("not support event type: %s", event.Type)
	}
	return l.submitMessage(&m)
}
