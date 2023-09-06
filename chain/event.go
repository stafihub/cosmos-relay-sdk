package chain

import (
	"bytes"
	"encoding/hex"
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

var (
	liquiditybondMemo = uint8(1)
	recoverMemo       = uint8(2)
)

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

	memoStr, msgSends, err := parseMemoAndMsgs(poolClient, tx.Tx.GetValue())
	if err != nil {
		return err
	}

	retMemoType, retStafiAddress, retRecoverTxHash := checkMemo(memoStr)

	shouldSkip, bondState, pool, nativeBondAmount, lsmBondAmount, msgs, err := l.checkMsgs(poolClient, msgSends, tx.Height, retMemoType)
	if err != nil {
		return err
	}
	if shouldSkip {
		return nil
	}

	if bondState != xLedgerTypes.LiquidityBondStateVerifyOk {
		proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
			Denom:  string(l.symbol),
			Bonder: zeroStafiAddressStr,
			Pool:   pool,
			Txhash: tx.TxHash,
			Amount: nativeBondAmount.Add(lsmBondAmount),
			State:  bondState,
		}
		return l.SubmitProposalExeLiquidityBond(proposalExeLiquidityBond)
	}

	switch retMemoType {
	case liquiditybondMemo:

	case recoverMemo:
		recoverTxRes, err := poolClient.QueryTxByHash(retRecoverTxHash)
		if err != nil || recoverTxRes.Code != 0 || recoverTxRes.Empty() {
			l.log.Warn("received token with recover memo, but QueryTxByHash failed", "txHash", tx.TxHash, "err", err)
			return nil
		}
		_, recoverMsgSends, err := parseMemoAndMsgs(poolClient, recoverTxRes.Tx.GetValue())
		if err != nil {
			return err
		}
		if len(recoverMsgSends) == 0 {
			l.log.Warn("received token with recover memo, but no msg send", "txHash", tx.TxHash, "err", err)
			return nil
		}

		if recoverMsgSends[0].FromAddress != msgSends[0].FromAddress {
			l.log.Warn("received token with recover memo, but have different sender", "txHash", tx.TxHash, "err", err)
			return nil
		}

		recoverShouldSkip, recoverBondState, recoverPool, recoverNativeBondAmount, recoverLsmBondAmount, recoverMsgs, err := l.checkMsgs(poolClient, recoverMsgSends, recoverTxRes.Height, liquiditybondMemo)
		if err != nil {
			return err
		}
		if recoverShouldSkip {
			l.log.Warn("received token with recover memo, but should skip", "txHash", tx.TxHash, "err", err)
			return nil
		}

		if recoverBondState != xLedgerTypes.LiquidityBondStateVerifyOk {
			l.log.Warn("received token with recover memo, but bond state not ok", "txHash", tx.TxHash, "err", err)
			return nil
		}

		pool = recoverPool
		nativeBondAmount = recoverNativeBondAmount
		lsmBondAmount = recoverLsmBondAmount
		msgs = recoverMsgs
		tx = recoverTxRes

	default:
		proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
			Denom:  string(l.symbol),
			Bonder: zeroStafiAddressStr,
			Pool:   pool,
			Txhash: tx.TxHash,
			Amount: nativeBondAmount.Add(lsmBondAmount),
			State:  xLedgerTypes.LiquidityBondStateMemoUnmatch,
		}
		return l.SubmitProposalExeLiquidityBond(proposalExeLiquidityBond)
	}

	switch {
	case nativeBondAmount.IsPositive() && lsmBondAmount.IsZero():
		proposalExeLiquidityBond := core.ProposalExeLiquidityBond{
			Denom:  string(l.symbol),
			Bonder: retStafiAddress,
			Pool:   pool,
			Txhash: tx.TxHash,
			Amount: nativeBondAmount.Add(lsmBondAmount),
			State:  xLedgerTypes.LiquidityBondStateVerifyOk,
		}
		return l.SubmitProposalExeLiquidityBond(proposalExeLiquidityBond)
	case nativeBondAmount.IsZero() && lsmBondAmount.IsPositive():

		proposal := core.ProposalExeNativeAndLsmLiquidityBond{
			Denom:            string(l.symbol),
			Bonder:           retStafiAddress,
			Pool:             pool,
			Txhash:           tx.TxHash,
			NativeBondAmount: types.ZeroInt(),
			LsmBondAmount:    lsmBondAmount,
			State:            xLedgerTypes.LiquidityBondStateVerifyOk,
			Msgs:             msgs,
		}
		return l.SubmitProposalExeNativeAndLsmLiquidityBond(proposal)
	case nativeBondAmount.IsPositive() && lsmBondAmount.IsPositive():
		return fmt.Errorf("not support amounts: native: %s lsm: %s", nativeBondAmount.String(), lsmBondAmount.String())
	default:
		return fmt.Errorf("unknown amounts: native: %s lsm: %s", nativeBondAmount.String(), lsmBondAmount.String())
	}
}

func (l *Listener) checkMsgs(client *hubClient.Client, msgSends []*xBankTypes.MsgSend, height int64, memoType uint8) (shouldSkip bool,
	retBondState xLedgerTypes.LiquidityBondState, retPool string, retNativeBondAmount, retLsmBondAmount types.Int, retMsgs []types.Msg, retErr error) {
	nativeBondAmount := types.ZeroInt()
	lsmBondAmount := types.ZeroInt()
	poolRecipient := ""
	var poolAddress types.AccAddress
	msgs := make([]types.Msg, 0)

	for _, msg := range msgSends {
		recipient := msg.ToAddress
		transferCoins := msg.Amount

		// skip if not to this pool
		if !l.hasPool(recipient) {
			continue
		}
		if len(poolRecipient) != 0 && poolRecipient != recipient {
			return false, -1, poolRecipient, types.ZeroInt(), types.ZeroInt(), nil, fmt.Errorf("not support multi pool in one tx")
		}
		if len(poolRecipient) == 0 {
			poolRecipient = recipient
			done := core.UseSdkConfigContext(client.GetAccountPrefix())
			address, err := types.AccAddressFromBech32(poolRecipient)
			if err != nil {
				done()
				return false, xLedgerTypes.LiquidityBondStateDenomUnmatch, poolRecipient, types.ZeroInt(), types.ZeroInt(), nil, fmt.Errorf("pool recipient fmt error: %s", poolRecipient)
			}
			poolAddress = address
			done()
		}

		for _, transferCoin := range transferCoins {
			switch {
			case transferCoin.GetDenom() == client.GetDenom():
				nativeBondAmount = nativeBondAmount.Add(transferCoin.Amount)
			case strings.Contains(transferCoin.GetDenom(), "/"):
				splits := strings.Split(transferCoin.GetDenom(), "/")
				if len(splits) != 2 {
					l.log.Warn(fmt.Sprintf("transfer denom not support, %s", transferCoin.GetDenom()))
					return false, xLedgerTypes.LiquidityBondStateDenomUnmatch, poolRecipient, types.ZeroInt(), types.ZeroInt(), nil, nil
				}
				valAddressStr := splits[0]

				done := core.UseSdkConfigContext(client.GetAccountPrefix())
				valAddress, err := types.ValAddressFromBech32(valAddressStr)
				if err != nil {
					done()
					l.log.Warn(fmt.Sprintf("transfer denom not support, %s", transferCoin.GetDenom()))
					return false, xLedgerTypes.LiquidityBondStateDenomUnmatch, poolRecipient, types.ZeroInt(), types.ZeroInt(), nil, nil
				}

				done()

				targetVals, err := l.conn.GetPoolTargetValidators(recipient)
				if err != nil {
					l.log.Warn("pool not support, %s", recipient)
					return false, -1, poolRecipient, types.ZeroInt(), types.ZeroInt(), nil, fmt.Errorf("pool %s targetVal not exists", recipient)
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
					return false, xLedgerTypes.LiquidityBondStateDenomUnmatch, poolRecipient, types.ZeroInt(), types.ZeroInt(), nil, nil
				}

				validatorInfoRes, err := client.QueryValidator(valAddressStr, height)
				if err != nil {
					return false, -1, poolRecipient, types.ZeroInt(), types.ZeroInt(), nil, err
				}

				shareToTokenAmount := validatorInfoRes.Validator.TokensFromShares(types.NewDecFromInt(transferCoin.Amount)).TruncateInt()

				lsmBondAmount = lsmBondAmount.Add(shareToTokenAmount)

				msgs = append(msgs, xLedgerTypes.NewMsgRedeemTokensForShares(poolAddress, transferCoin))

			default:
				l.log.Warn(fmt.Sprintf("transfer denom not support, %s", transferCoin.GetDenom()))
				return false, xLedgerTypes.LiquidityBondStateDenomUnmatch, poolRecipient, types.ZeroInt(), types.ZeroInt(), nil, nil
			}
		}

	}

	if nativeBondAmount.IsPositive() || lsmBondAmount.IsPositive() {
		if memoType == recoverMemo {
			return false, xLedgerTypes.LiquidityBondStateVerifyOk, poolRecipient, nativeBondAmount, lsmBondAmount, msgs, nil
		}

		if nativeBondAmount.Add(lsmBondAmount).LT(l.conn.leastBond.Amount) {
			return false, xLedgerTypes.LiquidityBondStateAmountUnmatch, poolRecipient, nativeBondAmount, lsmBondAmount, nil, nil
		} else {
			return false, xLedgerTypes.LiquidityBondStateVerifyOk, poolRecipient, nativeBondAmount, lsmBondAmount, msgs, nil
		}
	}

	return true, -1, poolRecipient, types.ZeroInt(), types.ZeroInt(), nil, nil
}

func parseMemoAndMsgs(client *hubClient.Client, txValue []byte) (string, []*xBankTypes.MsgSend, error) {
	msgSends := make([]*xBankTypes.MsgSend, 0)
	done := core.UseSdkConfigContext(client.GetAccountPrefix())
	defer func() {
		done()
	}()

	tx, err := client.GetTxConfig().TxDecoder()(txValue)
	if err != nil {
		return "", nil, err
	}
	memoTx, ok := tx.(types.TxWithMemo)
	if !ok {
		return "", nil, fmt.Errorf("tx is not type TxWithMemo")
	}

	memoStr := memoTx.GetMemo()

	for _, msg := range memoTx.GetMsgs() {
		if types.MsgTypeURL(msg) == types.MsgTypeURL((*xBankTypes.MsgSend)(nil)) {
			msgSend, ok := msg.(*xBankTypes.MsgSend)
			if !ok {
				return "", nil, fmt.Errorf("msgsend cast err: %s", hex.EncodeToString(txValue))
			}
			msgSends = append(msgSends, msgSend)
		}
	}
	return memoStr, msgSends, nil
}

func checkMemo(memoStr string) (retMomoType uint8, retStafiAddress string, retRecoverTxHash string) {

	if len(memoStr) == 0 {
		return 0, "", ""
	}
	split := strings.Split(memoStr, ":")
	if len(split) < 2 {
		return 0, "", ""
	}
	switch split[0] {
	case "1":
		done := core.UseSdkConfigContext("stafi")
		_, err := types.AccAddressFromBech32(split[1])
		if err != nil {
			done()
			return 0, "", ""
		}
		done()
		return liquiditybondMemo, split[1], ""
	case "2":
		if len(split) != 3 {
			return 0, "", ""
		}
		// check user stafi address
		done := core.UseSdkConfigContext("stafi")
		_, err := types.AccAddressFromBech32(split[1])
		if err != nil {
			done()
			return 0, "", ""
		}
		done()

		// checkout recovered tx
		recoveredTxHash := split[2]

		return recoverMemo, split[1], recoveredTxHash

	default:
		return 0, "", ""
	}
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
