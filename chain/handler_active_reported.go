package chain

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/cosmos/cosmos-sdk/types"
	hubClient "github.com/stafihub/cosmos-relay-sdk/client"
	"github.com/stafihub/rtoken-relay-core/common/core"
	"github.com/stafihub/rtoken-relay-core/common/utils"
	stafiHubXLedgerTypes "github.com/stafihub/stafihub/x/ledger/types"
)

// handle activeReportedEvent from stafihub
// 1
//  1. if no transfer info, just transfer report to stafihub
//  2. has transfer info, gen transfer unsigned tx
//
// 2 sign it with subKey
// 3 send signature to stafihub
// 4 wait until signature enough and send tx to cosmoshub
// 5 transfer report to stafihub
func (h *Handler) handleActiveReportedEvent(m *core.Message) error {
	h.log.Info("handleActiveReportedEvent", "m", m)

	eventActiveReported, ok := m.Content.(core.EventActiveReported)
	if !ok {
		return fmt.Errorf("handleActiveReportedEvent cast failed, %+v", m)
	}
	snap := eventActiveReported.Snapshot
	poolClient, isIcaPool, err := h.conn.GetPoolClient(snap.GetPool())
	if err != nil {
		h.log.Error("handleActiveReportedEvent GetPoolClient failed",
			"pool address", snap.GetPool(),
			"error", err)
		return err
	}
	if isIcaPool {
		return h.dealIcaActiveReportedEvent(poolClient, eventActiveReported)
	}

	done := core.UseSdkConfigContext(poolClient.GetAccountPrefix())
	poolAddress, err := types.AccAddressFromBech32(snap.GetPool())
	if err != nil {
		done()
		return err
	}
	poolAddressStr := poolAddress.String()
	done()
	threshold, err := h.conn.GetPoolThreshold(poolAddressStr)
	if err != nil {
		return err
	}
	subKeyName, err := h.conn.GetPoolSubkeyName(poolAddressStr)
	if err != nil {
		return err
	}

	memo := GetMemo(snap.Era, TxTypeHandleActiveReportedEvent)
	unSignedTx, outPuts, err := GetTransferUnsignedTxWithMemo(poolClient, poolAddress, eventActiveReported.PoolUnbond, memo, h.log)
	if err != nil && err != ErrNoOutPuts {
		h.log.Error("handleActiveReportedEvent GetTransferUnsignedTx failed", "pool address", poolAddressStr, "err", err)
		return err
	}
	if err == ErrNoOutPuts {
		h.log.Info("handleActiveReportedEvent no need transfer Tx",
			"pool address", poolAddressStr,
			"era", snap.Era,
			"snapId", eventActiveReported.ShotId)
		return h.sendTransferReportMsg(eventActiveReported.ShotId)
	}
	wrapUnsignedTx := WrapUnsignedTx{
		UnsignedTx: unSignedTx,
		SnapshotId: eventActiveReported.ShotId,
		Era:        snap.Era,
		Type:       stafiHubXLedgerTypes.TxTypeDealActiveReported}

	var txHash, txBts []byte
	for i := 0; i < 5; i++ {
		//use current seq
		seq, err := poolClient.GetSequence(0, poolAddress)
		if err != nil {
			h.log.Error("handleActiveReportedEvent GetSequence failed",
				"pool address", poolAddressStr,
				"err", err)
			return err
		}

		sigBts, err := poolClient.SignMultiSigRawTxWithSeq(seq, unSignedTx, subKeyName)
		if err != nil {
			h.log.Error("handleActiveReportedEvent SignMultiSigRawTx failed",
				"pool address", poolAddressStr,
				"unsignedTx", string(unSignedTx),
				"err", err)
			return err
		}

		//cache unSignedTx
		proposalId := GetTransferProposalId(utils.BlakeTwo256(unSignedTx), uint8(i))
		proposalIdHexStr := hex.EncodeToString(proposalId)

		h.log.Info("handleActiveReportedEvent gen unsigned transfer Tx",
			"pool address", poolAddressStr,
			"out put", outPuts,
			"proposalId", proposalIdHexStr,
			"signature", hex.EncodeToString(sigBts))

		// send to stafihub
		submitSignature := core.ParamSubmitSignature{
			Denom:     snap.GetDenom(),
			Era:       snap.GetEra(),
			Pool:      poolAddressStr,
			TxType:    stafiHubXLedgerTypes.TxTypeDealActiveReported,
			PropId:    proposalIdHexStr,
			Signature: hex.EncodeToString(sigBts),
		}
		err = h.sendSubmitSignatureMsg(&submitSignature)
		if err != nil {
			return err
		}
		signatures, err := h.mustGetSignatureFromStafiHub(&submitSignature, threshold)
		if err != nil {
			return err
		}
		txHash, txBts, err = poolClient.AssembleMultiSigTx(wrapUnsignedTx.UnsignedTx, signatures, threshold)
		if err != nil {
			h.log.Error("handleActiveReportedEvent AssembleMultiSigTx failed",
				"pool address ", poolAddressStr,
				"unsignedTx", hex.EncodeToString(wrapUnsignedTx.UnsignedTx),
				"signatures", bytesArrayToStr(signatures),
				"threshold", threshold,
				"err", err)
			continue
		}
		// check tx is already onchain
		// 1 onchain:  if tx is failed continue construct new tx else go to next checkAndSend()
		// 2 not onchain: check banlance enough then go to next checkAndSend()
		res, err := poolClient.QueryTxByHash(hex.EncodeToString(txHash))
		if err == nil {
			if res.Code != 0 {
				continue
			} else {
				break
			}
		} else {
			totalSend := types.NewInt(0)
			for _, out := range outPuts {
				totalSend = totalSend.Add(out.Coins.AmountOf(poolClient.GetDenom()))
			}

			// now we will wait until enough balance or sent on chain by other nodes
			for {
				balanceRes, err := poolClient.QueryBalance(poolAddress, poolClient.GetDenom(), 0)
				if err == nil && balanceRes.Balance.Amount.GT(totalSend) {
					break
				}
				// in case of sent on chain by other nodes
				_, err = poolClient.QueryTxByHash(hex.EncodeToString(txHash))
				if err == nil {
					break
				}
				h.log.Warn("pool balance not enouth and transfer tx not onchain, will wait", "pool address", poolAddressStr)
				time.Sleep(6 * time.Second)
			}

			break
		}
	}

	return h.checkAndSend(poolClient, &wrapUnsignedTx, m, txHash, txBts, poolAddress)
}

func (h *Handler) dealIcaActiveReportedEvent(poolClient *hubClient.Client, eventActiveReported core.EventActiveReported) error {
	h.log.Info("dealIcaActiveReportedEvent", "event", eventActiveReported)
	snap := eventActiveReported.Snapshot
	done := core.UseSdkConfigContext(poolClient.GetAccountPrefix())
	poolAddress, err := types.AccAddressFromBech32(snap.GetPool())
	if err != nil {
		done()
		return err
	}
	poolAddressStr := poolAddress.String()
	done()

	msgs, outPuts, err := GetTransferMsgs(poolClient, poolAddress, eventActiveReported.PoolUnbond, h.log)
	if err != nil && err != ErrNoOutPuts {
		h.log.Error("handleActiveReportedEvent GetTransferUnsignedTx failed", "pool address", poolAddressStr, "err", err)
		return err
	}

	if err == ErrNoOutPuts {
		h.log.Info("handleActiveReportedEvent no need transfer Tx",
			"pool address", poolAddressStr,
			"era", snap.Era,
			"snapId", eventActiveReported.ShotId)
		return h.sendTransferReportMsg(eventActiveReported.ShotId)
	}
	interchainTx, err := stafiHubXLedgerTypes.NewInterchainTxProposal(
		types.AccAddress{},
		snap.Denom,
		poolAddressStr,
		snap.Era,
		stafiHubXLedgerTypes.TxTypeDealActiveReported,
		0,
		msgs)
	if err != nil {
		return err
	}
	proposalInterchainTx := core.ProposalInterchainTx{
		Denom:  snap.Denom,
		Pool:   poolAddressStr,
		Era:    snap.Era,
		TxType: stafiHubXLedgerTypes.TxTypeDealActiveReported,
		Factor: 0,
		Msgs:   msgs,
	}

	totalSend := types.NewInt(0)
	for _, out := range outPuts {
		totalSend = totalSend.Add(out.Coins.AmountOf(poolClient.GetDenom()))
	}
	h.log.Info("dealIcaActiveReportedEvent gen transfer msg",
		"pool address", poolAddressStr,
		"out put", outPuts,
		"proposalId", interchainTx.PropId,
		"totalSend", totalSend.String())

	// now we will wait until enough balance or sent on chain by other nodes
	for {
		balanceRes, err := poolClient.QueryBalance(poolAddress, poolClient.GetDenom(), 0)
		if err == nil && balanceRes.Balance.Amount.GTE(totalSend) {
			break
		}
		// in case of sent on chain by other nodes
		status, err := h.getInterchainTxStatusFromStafiHub(interchainTx.PropId)
		if err == nil && status != stafiHubXLedgerTypes.InterchainTxStatusUnspecified {
			break
		}
		h.log.Warn("pool balance not enouth and ica tx not execute, will wait", "pool address", poolAddressStr)
		time.Sleep(6 * time.Second)
	}

	err = h.sendInterchainTx(&proposalInterchainTx)
	if err != nil {
		return err
	}
	h.log.Info("sendInterchainTx",
		"pool address", poolAddressStr,
		"era", snap.Era,
		"propId", interchainTx.PropId,
		"msgs", msgs)

	status, err := h.mustGetInterchainTxStatusFromStafiHub(interchainTx.PropId)
	if err != nil {
		return err
	}
	if status != stafiHubXLedgerTypes.InterchainTxStatusSuccess {
		return fmt.Errorf("interchainTx proposalId: %s, txType: %s status: %s", interchainTx.PropId, interchainTx.TxType.String(), status.String())
	}
	return h.sendTransferReportMsg(eventActiveReported.ShotId)
}
