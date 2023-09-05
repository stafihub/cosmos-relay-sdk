package chain

import (
	"bytes"
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

func (l *Listener) processBlockResult(poolClient *hubClient.Client, blockResult *BlockResult) error {
	for _, tx := range blockResult.Txs {
		err := l.processTx(poolClient, tx)
		if err != nil {
			return err
		}
	}
	return nil
}

// process tx or recovered tx
func (l *Listener) processTx(poolClient *hubClient.Client, tx *types.TxResponse) error {
	if tx.Code != 0 || tx.Empty() {
		return nil
	}

	for _, log := range tx.Logs {
		for _, event := range log.Events {
			err := l.processStringEvents(poolClient, tx.Tx.GetValue(), tx.Height, tx.TxHash, event, false, "", "")
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// when isRecover is true, bonder and signer must have value, bonder is a stafi address of user, signer is the signer of recover tx
func (l *Listener) processStringEvents(client *hubClient.Client, txValue []byte, height int64, txHash string, event types.StringEvent, isRecover bool, bonder, signer string) error {
	//check height of cosmoshub-4, old tx shouldn't be dealed
	if l.conn.chainId == "cosmoshub-4" && height < 11665685 {
		l.log.Warn("find old tx, shouldn't be dealed", "txHash", txHash, "event", event, "height", height)
		return nil
	}

	switch {
	case event.Type == xBankTypes.EventTypeTransfer:
		// not support multisend now
		if len(event.Attributes) != 3 {
			l.log.Debug("got multisend transfer event", "txHash", txHash, "event", event)
			return nil
		}
		recipient := event.Attributes[0].Value
		// skip if not to this pool
		if !l.hasPool(recipient) {
			return nil
		}
		from := event.Attributes[1].Value
		amountStr := event.Attributes[2].Value

		// skip reward event
		if strings.EqualFold(from, l.distributionAddStr) {
			return nil
		}

		transferCoin, err := types.ParseCoinNormalized(amountStr)
		if err != nil {
			return fmt.Errorf("amount format err, %s", err)
		}

		var bondCoin types.Coin
		var shareCoin types.Coin
		var isNativeBond bool
		switch {
		case transferCoin.GetDenom() == client.GetDenom():
			bondCoin = transferCoin
			isNativeBond = true
		case strings.Contains(transferCoin.GetDenom(), "/"):
			shareCoin = transferCoin
			isNativeBond = false
			splits := strings.Split(transferCoin.GetDenom(), "/")
			if len(splits) != 2 {
				l.log.Warn(fmt.Sprintf("transfer denom not support, %s", transferCoin.GetDenom()))
				return nil
			}
			valAddressStr := splits[0]

			done := core.UseSdkConfigContext(client.GetAccountPrefix())
			valAddress, err := types.ValAddressFromBech32(valAddressStr)
			if err != nil {
				done()
				l.log.Warn(fmt.Sprintf("transfer denom not support, %s", transferCoin.GetDenom()))
				return nil
			}
			done()

			targetVals, err := l.conn.GetPoolTargetValidators(recipient)
			if err != nil {
				l.log.Warn(fmt.Sprintf("transfer denom not support, %s", transferCoin.GetDenom()))
				return nil
			}

			isInTargetVals := false
			for _, tVal := range targetVals {
				if bytes.Equal(tVal, valAddress) {
					isInTargetVals = true
					break
				}
			}
			if !isInTargetVals {
				l.log.Warn(fmt.Sprintf("transfer denom not support, %s", transferCoin.GetDenom()))
				return nil
			}

			validatorInfoRes, err := client.QueryValidator(valAddressStr, height)
			if err != nil {
				return err
			}

			tokenAmount := validatorInfoRes.Validator.TokensFromShares(types.NewDecFromInt(transferCoin.Amount)).TruncateInt()
			bondCoin = types.NewCoin(client.GetDenom(), tokenAmount)

		default:
			l.log.Warn(fmt.Sprintf("transfer denom not support, %s", transferCoin.GetDenom()))
			return nil
		}

		done := core.UseSdkConfigContext(client.GetAccountPrefix())
		var memoInTx string
		tx, err := client.GetTxConfig().TxDecoder()(txValue)
		if err != nil {
			done()
			return err
		}

		// only support one transfer msg in one tx
		sendMsgNumber := 0
		for _, msg := range tx.GetMsgs() {
			if types.MsgTypeURL(msg) == types.MsgTypeURL((*xBankTypes.MsgSend)(nil)) {
				sendMsgNumber++
			}
		}
		if sendMsgNumber != 1 {
			l.log.Debug("got multi send msgs in one tx", "txHash", txHash, "event", event)
			done()
			return nil
		}

		memoTx, ok := tx.(types.TxWithMemo)
		if !ok {
			done()
			return fmt.Errorf("tx is not type TxWithMemo, txhash: %s", txHash)
		}
		done()

		memoInTx = memoTx.GetMemo()

		if isRecover {
			return l.dealRecover(client, recipient, from, signer, bonder, txHash, bondCoin, shareCoin, isNativeBond)
		} else {
			return l.dealMemo(client, memoInTx, recipient, from, txHash, bondCoin, shareCoin, isNativeBond)
		}

	default:
		return nil
	}
}

func (l Listener) dealRecover(poolClient *hubClient.Client, recipient, from, signer, bonder, txHash string, coin, shareToken types.Coin, isNativeBond bool) error {

	var state xLedgerTypes.LiquidityBondState
	bonderAddressStr := zeroStafiAddressStr
	switch {
	// check bond amount if it is a tx which will be recovered
	case coin.IsLT(l.conn.leastBond):
		l.log.Warn("got transfer event but less than leastBond", "txHash", txHash)
		state = xLedgerTypes.LiquidityBondStateAmountUnmatch
		//check signer of recover tx is the from of this tx event
	case from != signer:
		l.log.Warn("received token with recover memo, but from!=signer", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String(), "signer", signer)
		state = xLedgerTypes.LiquidityBondStateBonderUnmatch
	default:
		bonderAddressStr = bonder
		state = xLedgerTypes.LiquidityBondStateVerifyOk
	}

	if isNativeBond {
		proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
			Denom:  string(l.symbol),
			Bonder: bonderAddressStr,
			Pool:   recipient,
			Txhash: txHash,
			Amount: coin.Amount,
			State:  state,
		}
		return l.SubmitProposalExeLiquidityBond(proposalExeLiquidityBond)
	} else {

		msg := xLedgerTypes.MsgRedeemTokensForShares{
			DelegatorAddress: recipient,
			Amount:           shareToken,
		}

		proposal := core.ProposalExeNativeAndLsmLiquidityBond{
			Denom:            string(l.symbol),
			Bonder:           bonderAddressStr,
			Pool:             recipient,
			Txhash:           txHash,
			NativeBondAmount: types.ZeroInt(),
			LsmBondAmount:    coin.Amount,
			State:            state,
			Msgs:             []types.Msg{&msg},
		}
		return l.SubmitProposalExeNativeAndLsmLiquidityBond(proposal)
	}
}

// memo case: empty, just return
// memo case: 1:[address], submit exeLiquidityBond proposal to stafihub
// memo case: 2:[address]:[txHash], recover for txHash (which is send from signer)
// memo case: unkonw format, just return
func (l Listener) dealMemo(poolClient *hubClient.Client, memo, recipient, from, txHash string, coin, shareToken types.Coin, isNativeBond bool) error {

	if len(memo) == 0 {
		l.log.Warn("received token but no memo", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String())
		return nil
	}

	split := strings.Split(memo, ":")
	if len(split) < 2 {
		l.log.Warn("received token with memo, but unknow format", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String(), "memo", memo)
		return nil
	}

	switch split[0] {
	case "1":
		// check bond amount
		if coin.IsLT(l.conn.leastBond) {
			l.log.Debug("got transfer event but less than leastBond", "txHash", txHash, "bond amount", coin.String())
			if isNativeBond {
				proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
					Denom:  string(l.symbol),
					Bonder: zeroStafiAddressStr,
					Pool:   recipient,
					Txhash: txHash,
					Amount: coin.Amount,
					State:  xLedgerTypes.LiquidityBondStateAmountUnmatch,
				}
				return l.SubmitProposalExeLiquidityBond(proposalExeLiquidityBond)
			} else {

				proposal := core.ProposalExeNativeAndLsmLiquidityBond{
					Denom:            string(l.symbol),
					Bonder:           zeroStafiAddressStr,
					Pool:             recipient,
					Txhash:           txHash,
					NativeBondAmount: types.ZeroInt(),
					LsmBondAmount:    coin.Amount,
					State:            xLedgerTypes.LiquidityBondStateAmountUnmatch,
					Msgs:             []types.Msg{},
				}
				return l.SubmitProposalExeNativeAndLsmLiquidityBond(proposal)
			}
		}

		// check user stafi address
		done := core.UseSdkConfigContext("stafi")
		bonder, err := types.AccAddressFromBech32(split[1])
		if err != nil {
			done()
			l.log.Warn("received token with memo, but unknow format", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String(), "memo", memo)
			if isNativeBond {
				proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
					Denom:  string(l.symbol),
					Bonder: zeroStafiAddressStr,
					Pool:   recipient,
					Txhash: txHash,
					Amount: coin.Amount,
					State:  xLedgerTypes.LiquidityBondStateBonderUnmatch,
				}
				return l.SubmitProposalExeLiquidityBond(proposalExeLiquidityBond)
			} else {

				proposal := core.ProposalExeNativeAndLsmLiquidityBond{
					Denom:            string(l.symbol),
					Bonder:           zeroStafiAddressStr,
					Pool:             recipient,
					Txhash:           txHash,
					NativeBondAmount: types.ZeroInt(),
					LsmBondAmount:    coin.Amount,
					State:            xLedgerTypes.LiquidityBondStateBonderUnmatch,
					Msgs:             []types.Msg{},
				}
				return l.SubmitProposalExeNativeAndLsmLiquidityBond(proposal)
			}
		}
		bonderStr := bonder.String()
		done()

		// all is ok
		if isNativeBond {
			proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
				Denom:  string(l.symbol),
				Bonder: bonderStr,
				Pool:   recipient,
				Txhash: txHash,
				Amount: coin.Amount,
				State:  xLedgerTypes.LiquidityBondStateVerifyOk,
			}
			return l.SubmitProposalExeLiquidityBond(proposalExeLiquidityBond)
		} else {
			msg := xLedgerTypes.MsgRedeemTokensForShares{
				DelegatorAddress: recipient,
				Amount:           shareToken,
			}

			proposal := core.ProposalExeNativeAndLsmLiquidityBond{
				Denom:            string(l.symbol),
				Bonder:           bonderStr,
				Pool:             recipient,
				Txhash:           txHash,
				NativeBondAmount: types.ZeroInt(),
				LsmBondAmount:    coin.Amount,
				State:            xLedgerTypes.LiquidityBondStateVerifyOk,
				Msgs:             []types.Msg{&msg},
			}
			return l.SubmitProposalExeNativeAndLsmLiquidityBond(proposal)
		}
	case "2":
		// check memo
		if len(split) != 3 {
			l.log.Warn("received token with recover memo, but unknow format", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String(), "memo", memo)
			return nil
		}
		// check user stafi address
		done := core.UseSdkConfigContext("stafi")
		bonder, err := types.AccAddressFromBech32(split[1])
		if err != nil {
			done()
			l.log.Warn("received token with memo, but unknow format", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String(), "memo", memo)
			return nil
		}
		bonderStr := bonder.String()
		done()

		// checkout recovered tx
		recoveredTxHash := split[2]
		txRes, err := poolClient.QueryTxByHash(recoveredTxHash)
		if err != nil || txRes.Code != 0 || txRes.Empty() {
			l.log.Warn("received token with recover memo, but QueryTxByHash failed", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String(), "memo", memo, "err", err)
			return nil
		}

		// all is ok
		l.log.Info("received token with recover memo, will deal this txHash", "pool", recipient, "from", from, "txHash", txHash, "recoveredTxhash", recoveredTxHash, "memo", memo)
		return l.processRecoveredTx(poolClient, txRes, bonderStr, from)

	default:
		l.log.Warn("received token with memo, but unknow format", "pool", recipient, "from", from, "txHash", txHash, "coin", coin.String(), "memo", memo)
		return nil
	}

}

// bonder is a stafi address of user, signer is the signer of recover tx
func (l *Listener) processRecoveredTx(poolClient *hubClient.Client, tx *types.TxResponse, bonder, signer string) error {
	if tx.Code != 0 || tx.Empty() {
		return nil
	}
	for _, log := range tx.Logs {
		for _, event := range log.Events {
			err := l.processStringEvents(poolClient, tx.Tx.GetValue(), tx.Height, tx.TxHash, event, true, bonder, signer)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// blocked until tx is dealed on stafichain
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

// blocked until tx is dealed on stafichain
func (l Listener) SubmitProposalExeNativeAndLsmLiquidityBond(proposalExeNativeAndLsmLiquidityBond core.ProposalExeNativeAndLsmLiquidityBond) error {
	m := core.Message{
		Source:      l.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonExeNativeAndLsmLiquidityBond,
	}
	m.Content = proposalExeNativeAndLsmLiquidityBond
	if proposalExeNativeAndLsmLiquidityBond.State == xLedgerTypes.LiquidityBondStateVerifyOk {
		l.log.Info("find liquiditybond transfer", "msg", m)
	}
	err := l.submitMessage(&m)
	if err != nil {
		return err
	}

	// here we wait until bondrecord write on chain
	for {
		_, err := l.mustGetBondRecordFromStafiHub(proposalExeNativeAndLsmLiquidityBond.Denom, proposalExeNativeAndLsmLiquidityBond.Txhash)
		if err != nil {
			l.log.Warn("mustGetBondRecordFromStafiHub failed, will retry", "err", err)
			time.Sleep(BlockRetryInterval)
			continue
		}
		break
	}

	if len(proposalExeNativeAndLsmLiquidityBond.Msgs) > 0 {
		proposal, err := xLedgerTypes.NewExecuteNativeAndLsmBondProposal(
			types.AccAddress{},
			proposalExeNativeAndLsmLiquidityBond.Denom,
			types.MustAccAddressFromBech32(proposalExeNativeAndLsmLiquidityBond.Bonder),
			proposalExeNativeAndLsmLiquidityBond.Pool,
			proposalExeNativeAndLsmLiquidityBond.Txhash,
			proposalExeNativeAndLsmLiquidityBond.NativeBondAmount,
			proposalExeNativeAndLsmLiquidityBond.LsmBondAmount,
			proposalExeNativeAndLsmLiquidityBond.State,
			proposalExeNativeAndLsmLiquidityBond.Msgs)
		if err != nil {
			return err
		}

		for {
			status, err := l.mustGetInterchainTxStatusFromStafiHub(proposal.PropId)
			if err != nil {
				l.log.Warn("mustGetInterchainTxStatusFromStafiHub failed, will retry", "proposalId", proposal.PropId, "err", err)
				time.Sleep(BlockRetryInterval)
				continue
			}

			if status == xLedgerTypes.InterchainTxStatusSuccess {
				break
			} else if status == xLedgerTypes.InterchainTxStatusFailed {
				return fmt.Errorf("InterchainTxStatusFromStafiHub status %s, proposalId: %s", status.String(), proposal.PropId)
			}

			return fmt.Errorf("unknown InterchainTxStatusFromStafiHub status %s, proposalId: %s", status.String(), proposal.PropId)

		}
	}
	return nil
}
