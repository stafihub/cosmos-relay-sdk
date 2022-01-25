package chain

import (
	"errors"
	"fmt"

	"github.com/cosmos/cosmos-sdk/types"
	xBankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stafiprotocol/rtoken-relay-core/core"
)

const maxUint32 = ^uint32(0)

var (
	ErrEventAttributeNumberUnMatch = errors.New("ErrEventAttributeNumberTooFew")
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
	// not support multisend now
	switch {
	case event.Type == xBankTypes.EventTypeTransfer:
		if len(event.Attributes) != 3 {
			return ErrEventAttributeNumberUnMatch
		}
		

	default:
		return fmt.Errorf("not support event type: %s", event.Type)
	}
	return l.submitMessage(&m)
}
