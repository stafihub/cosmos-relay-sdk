package chain

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/cosmos/cosmos-sdk/types"
	xBankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	hubClient "github.com/stafiprotocol/cosmos-relay-sdk/client"
	"github.com/stafiprotocol/rtoken-relay-core/common/core"
)

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
				err := l.processStringEvents(poolClient, tx.Tx.GetValue(), tx.Height, tx.TxHash, event)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (l *Listener) processStringEvents(client *hubClient.Client, txValue []byte, height int64, txHash string, event types.StringEvent) error {
	// not support multisend now
	switch {
	case event.Type == xBankTypes.EventTypeTransfer:
		if len(event.Attributes) != 3 {
			return ErrEventAttributeNumberUnMatch
		}
		recipient := event.Attributes[0].Value
		if !l.hasPool(recipient) {
			return nil
		}
		amountStr := event.Attributes[2].Value

		m := core.Message{
			Source:      l.symbol,
			Destination: l.caredSymbol,
		}
		amount, ok := types.NewIntFromString(amountStr)
		if !ok {
			return fmt.Errorf("amount format err, %s", amountStr)
		}

		block, err := client.QueryBlock(height)
		if err != nil {
			return err
		}
		blockHash := hex.EncodeToString(block.BlockID.Hash)

		var memoInTx string
		tx, err := client.GetTxConfig().TxDecoder()(txValue)
		if err != nil {
			return err
		}
		memoTx, ok := tx.(types.TxWithMemo)
		if !ok {
			return fmt.Errorf("tx is not type TxWithMemo, txhash: %s", txHash)
		}
		memoInTx = memoTx.GetMemo()
		bonder, err := types.AccAddressFromBech32(memoInTx)
		if err != nil {
			return err
		}

		proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
			Denom:     string(core.HubRFIS),
			Bonder:    bonder.String(),
			Pool:      recipient,
			Blockhash: blockHash,
			Txhash:    txHash,
			Amount:    amount,
		}
		m.Content = proposalExeLiquidityBond
		return l.submitMessage(&m)
	default:
		return nil
	}
}
