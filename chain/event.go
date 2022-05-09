package chain

import (
	"fmt"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/types"
	xBankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	hubClient "github.com/stafihub/cosmos-relay-sdk/client"
	"github.com/stafihub/rtoken-relay-core/common/core"
	xLedgerTypes "github.com/stafihub/stafihub/x/ledger/types"
)

var zeroAddress = types.AccAddress{0x0000000000000000000000000000000000000000}
var zeroStafiAddressStr string

func init() {
	done := core.UseSdkConfigContext("stafi")
	zeroStafiAddressStr = zeroAddress.String()
	done()
}

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
		err := l.processTx(poolClient, tx, false, "", "")
		if err != nil {
			return err
		}
	}
	return nil
}

// process tx or recovered tx
func (l *Listener) processTx(poolClient *hubClient.Client, tx *types.TxResponse, isRecover bool, bonder, signer string) error {
	if tx.Code != 0 || tx.Empty() {
		return nil
	}
	for _, log := range tx.Logs {
		for _, event := range log.Events {
			err := l.processStringEvents(poolClient, tx.Tx.GetValue(), tx.Height, tx.TxHash, event, isRecover, bonder, signer)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *Listener) processStringEvents(client *hubClient.Client, txValue []byte, height int64, txHash string, event types.StringEvent, isRecover bool, bonder, signer string) error {
	switch {
	case event.Type == xBankTypes.EventTypeTransfer:
		// not support multisend now
		if len(event.Attributes) != 3 {
			l.log.Debug("got multisend transfer event", "txHash", txHash, "event", event)
			return nil
		}
		recipient := event.Attributes[0].Value
		// not to this pool
		if !l.hasPool(recipient) {
			return nil
		}
		from := event.Attributes[1].Value
		amountStr := event.Attributes[2].Value

		// skip reward event
		if strings.EqualFold(from, l.distributionAddStr) {
			return nil
		}

		coin, err := types.ParseCoinNormalized(amountStr)
		if err != nil {
			return fmt.Errorf("amount format err, %s", err)
		}
		if coin.GetDenom() != client.GetDenom() || coin.GetDenom() != l.conn.leastBond.GetDenom() {
			l.log.Warn(fmt.Sprintf("transfer denom not equal,expect %s got %s,leastBond denom: %s", client.GetDenom(), coin.GetDenom(), l.conn.leastBond.GetDenom()))
			return nil
		}

		done := core.UseSdkConfigContext(client.GetAccountPrefix())
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
		done()

		memoInTx = memoTx.GetMemo()

		if isRecover {
			// check bond amount if it is a tx that will be recovered
			if !coin.IsGTE(l.conn.leastBond) {
				l.log.Debug("got transfer event but less than leastBond", "txHash", txHash, "event", event)
				proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
					Denom:  string(l.symbol),
					Bonder: zeroStafiAddressStr,
					Pool:   recipient,
					Txhash: txHash,
					Amount: coin.Amount,
					State:  xLedgerTypes.LiquidityBondStateAmountUnmatch,
				}
				return l.SubmitProposalExeLiquidityBond(proposalExeLiquidityBond)
			}
			//check signer of recover tx == from of this tx
			if from != signer {
				l.log.Warn("received token with recover memo, but from!=signer", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String(), "signer", signer)
				proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
					Denom:  string(l.symbol),
					Bonder: zeroStafiAddressStr,
					Pool:   recipient,
					Txhash: txHash,
					Amount: coin.Amount,
					State:  xLedgerTypes.LiquidityBondStateBonderUnmatch,
				}
				return l.SubmitProposalExeLiquidityBond(proposalExeLiquidityBond)
			}
			proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
				Denom:  string(l.symbol),
				Bonder: bonder,
				Pool:   recipient,
				Txhash: txHash,
				Amount: coin.Amount,
				State:  xLedgerTypes.LiquidityBondStateVerifyOk,
			}
			return l.SubmitProposalExeLiquidityBond(proposalExeLiquidityBond)
		} else {
			return l.dealMemo(client, memoInTx, recipient, from, txHash, coin)
		}

	default:
		return nil
	}
}

// memo case: empty, just return
// memo case: 1:[address], submit exeLiquidityBond proposal to stafihub
// memo case: 2:[address]:[txHash], recover for txHash (which is send from signer ?)
// memo case: unkonw format, just return
func (l Listener) dealMemo(poolClient *hubClient.Client, memo, recipient, from, txHash string, coin types.Coin) error {

	if len(memo) == 0 {
		l.log.Warn("received token but no memo", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String())
		proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
			Denom:  string(l.symbol),
			Bonder: zeroStafiAddressStr,
			Pool:   recipient,
			Txhash: txHash,
			Amount: coin.Amount,
			State:  xLedgerTypes.LiquidityBondStateMemoUnmatch,
		}
		return l.SubmitProposalExeLiquidityBond(proposalExeLiquidityBond)
	}

	split := strings.Split(memo, ":")
	if len(split) < 2 {
		l.log.Warn("received token with memo, but unknow format", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String(), "memo", memo)
		proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
			Denom:  string(l.symbol),
			Bonder: zeroStafiAddressStr,
			Pool:   recipient,
			Txhash: txHash,
			Amount: coin.Amount,
			State:  xLedgerTypes.LiquidityBondStateMemoUnmatch,
		}
		return l.SubmitProposalExeLiquidityBond(proposalExeLiquidityBond)
	}

	switch split[0] {
	case "1":
		// check bond amount
		if !coin.IsGTE(l.conn.leastBond) {
			l.log.Debug("got transfer event but less than leastBond", "txHash", txHash, "bond amount", coin.String())
			proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
				Denom:  string(l.symbol),
				Bonder: zeroStafiAddressStr,
				Pool:   recipient,
				Txhash: txHash,
				Amount: coin.Amount,
				State:  xLedgerTypes.LiquidityBondStateAmountUnmatch,
			}
			return l.SubmitProposalExeLiquidityBond(proposalExeLiquidityBond)
		}
		done := core.UseSdkConfigContext("stafi")
		bonder, err := types.AccAddressFromBech32(split[1])
		if err != nil {
			done()
			l.log.Warn("received token with memo, but unknow format", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String(), "memo", memo)
			proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
				Denom:  string(l.symbol),
				Bonder: zeroStafiAddressStr,
				Pool:   recipient,
				Txhash: txHash,
				Amount: coin.Amount,
				State:  xLedgerTypes.LiquidityBondStateBonderUnmatch,
			}
			return l.SubmitProposalExeLiquidityBond(proposalExeLiquidityBond)
		}
		bonderStr := bonder.String()
		done()

		proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
			Denom:  string(l.symbol),
			Bonder: bonderStr,
			Pool:   recipient,
			Txhash: txHash,
			Amount: coin.Amount,
			State:  xLedgerTypes.LiquidityBondStateVerifyOk,
		}
		return l.SubmitProposalExeLiquidityBond(proposalExeLiquidityBond)
	case "2":
		if len(split) != 3 {
			l.log.Warn("received token with recover memo, but unknow format", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String(), "memo", memo)
			return nil
		}
		done := core.UseSdkConfigContext("stafi")
		bonder, err := types.AccAddressFromBech32(split[1])
		if err != nil {
			done()
			l.log.Warn("received token with memo, but unknow format", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String(), "memo", memo)
			return nil
		}
		bonderStr := bonder.String()
		done()

		recoverTxHash := split[2]
		txRes, err := poolClient.QueryTxByHash(recoverTxHash)
		if err != nil || txRes.Code != 0 || txRes.Empty() {
			l.log.Warn("received token with recover memo, but QueryTxByHash failed", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String(), "memo", memo, "err", err)
			return nil
		}

		l.log.Info("received token with recover memo, will deal this txHash", "pool", recipient, "from", from, "txHash", txHash, "recoverTxhash", recoverTxHash, "memo", memo)
		return l.processTx(poolClient, txRes, true, bonderStr, from)

	default:
		l.log.Warn("received token with memo, but unknow format", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String(), "memo", memo)
		return nil
	}

}

//todo blocked until tx is dealed on stafichain
func (l Listener) SubmitProposalExeLiquidityBond(proposalExeLiquidityBond core.ProposalExeLiquidityBond) error {
	m := core.Message{
		Source:      l.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonExeLiquidityBond,
	}
	m.Content = proposalExeLiquidityBond
	if proposalExeLiquidityBond.State == xLedgerTypes.LiquidityBondStateVerifyOk {
		l.log.Info("find liquiditybond transfer", "msg", m)
	}
	err := l.submitMessage(&m)
	if err != nil {
		return err
	}

	// here we wait until bondrecord write on chain
	for {
		_, err := l.mustGetBondRecordFromStafiHub(proposalExeLiquidityBond.Denom, proposalExeLiquidityBond.Txhash)
		if err != nil {
			l.log.Warn("mustGetBondRecordFromStafiHub failed, will retry", "err", err)
			time.Sleep(BlockRetryInterval)
			continue
		}
		break
	}
	return nil
}
