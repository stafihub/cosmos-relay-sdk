package chain

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/cosmos/cosmos-sdk/types"
	xBankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	hubClient "github.com/stafihub/cosmos-relay-sdk/client"
	"github.com/stafihub/rtoken-relay-core/common/core"
)

var (
	ErrEventAttributeNumberUnMatch = errors.New("ErrEventAttributeNumberUnMatch")
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
					if err == ErrEventAttributeNumberUnMatch {
						// l.log.Warn("got multisend transfer event", "txHash", tx.TxHash, "event", event)
						continue
					}
					return err
				}
			}
		}
	}

	return nil
}

func (l *Listener) processStringEvents(client *hubClient.Client, txValue []byte, height int64, txHash string, event types.StringEvent) error {
	switch {
	case event.Type == xBankTypes.EventTypeTransfer:
		// not support multisend now
		if len(event.Attributes) != 3 {
			return ErrEventAttributeNumberUnMatch
		}
		recipient := event.Attributes[0].Value
		if !l.hasPool(recipient) {
			return nil
		}
		from := event.Attributes[1].Value
		amountStr := event.Attributes[2].Value

		m := core.Message{
			Source:      l.symbol,
			Destination: core.HubRFIS,
			Reason:      core.ReasonExeLiquidityBond,
		}
		coin, err := types.ParseCoinNormalized(amountStr)
		if err != nil {
			return fmt.Errorf("amount format err, %s", err)
		}
		if coin.GetDenom() != client.GetDenom() {
			return fmt.Errorf("transfer denom not equal,expect %s got %s", client.GetDenom(), coin.GetDenom())
		}

		block, err := client.QueryBlock(height)
		if err != nil {
			return err
		}
		blockHash := hex.EncodeToString(block.BlockID.Hash)

		done := core.UseSdkConfigContext(hubClient.AccountPrefix)
		var memoInTx string
		tx, err := client.GetTxConfig().TxDecoder()(txValue)
		if err != nil {
			done()
			return err
		}
		memoTx, ok := tx.(types.TxWithMemo)
		if !ok {
			done()
			return fmt.Errorf("tx is not type TxWithMemo, txhash: %s", txHash)
		}
		memoInTx = memoTx.GetMemo()
		if len(memoInTx) == 0 {
			done()
			l.log.Warn("received token but no memo", "pool", recipient, "from", from, "txHash", txHash, "coin", amountStr)
			return nil
		}
		done()

		done = core.UseSdkConfigContext("fis")
		bonder, err := types.AccAddressFromBech32(memoInTx)
		if err != nil {
			done()
			return err
		}
		bonderStr := bonder.String()
		done()

		proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
			Denom:     string(l.symbol),
			Bonder:    bonderStr,
			Pool:      recipient,
			Blockhash: blockHash,
			Txhash:    txHash,
			Amount:    coin.Amount,
		}
		m.Content = proposalExeLiquidityBond
		l.log.Info("find liquiditybond transfer", "msg", m)
		return l.submitMessage(&m)
	default:
		return nil
	}
}
