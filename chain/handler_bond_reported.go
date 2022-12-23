package chain

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/cosmos/cosmos-sdk/types"
	sdkXBankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	hubClient "github.com/stafihub/cosmos-relay-sdk/client"
	"github.com/stafihub/rtoken-relay-core/common/core"
	stafiHubXLedgerTypes "github.com/stafihub/stafihub/x/ledger/types"
)

// handle bondReportedEvent from stafihub
// 1 query reward on last withdraw tx  height,
//  1. if no reward, just send active report to stafihub
//  2. if has reward
//  1. gen delegate unsigned tx
//  2. sign it with subKey
//  3. send signature to stafihub
//  4. wait until signature enough and send tx to cosmoshub
//  5. active report to stafihub
func (h *Handler) handleBondReportedEvent(m *core.Message) error {
	h.log.Info("handleBondReportedEvent", "m", m)
	eventBondReported, ok := m.Content.(core.EventBondReported)
	if !ok {
		return fmt.Errorf("ProposalLiquidityBond cast failed, %+v", m)
	}
	snap := eventBondReported.Snapshot
	poolClient, isIcaPool, err := h.conn.GetPoolClient(snap.GetPool())
	if err != nil {
		h.log.Error("handleBondReportedEvent GetPoolClient failed",
			"pool address", snap.GetPool(),
			"error", err)
		return err
	}

	if isIcaPool {
		return h.dealIcaPoolBondReportedEvent(poolClient, eventBondReported)
	}

	done := core.UseSdkConfigContext(poolClient.GetAccountPrefix())
	poolAddress, err := types.AccAddressFromBech32(snap.GetPool())
	if err != nil {
		done()
		h.log.Error("PoolAddr cast to cosmos AccAddress failed",
			"pool address", snap.GetPool(),
			"err", err)
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

	rewardCoins, height, err := GetRewardToBeDelegated(poolClient, poolAddressStr, snap.Era)
	if err != nil {
		if err == ErrNoRewardNeedDelegate {
			//will return ErrNoMsgs if no reward or reward of that height is less than now , we just activeReport
			total := types.NewInt(0)
			delegationsRes, err := poolClient.QueryDelegations(poolAddress, 0)
			if err != nil {
				if !strings.Contains(err.Error(), "unable to find delegations for address") {
					h.log.Error("QueryDelegations failed",
						"pool", poolAddressStr,
						"err", err)
					return err
				}
			} else {
				for _, dele := range delegationsRes.GetDelegationResponses() {
					total = total.Add(dele.Balance.Amount)
				}
			}
			h.log.Info("no need claim reward", "pool", poolAddressStr, "era", snap.Era)
			return h.sendActiveReportMsg(eventBondReported.ShotId, total.BigInt())
		} else {
			h.log.Error("handleBondReportedEvent GetRewardToBeDelegated failed",
				"pool address", poolAddressStr,
				"err", err)
			return err
		}
	}

	memo := GetMemo(snap.Era, TxTypeHandleBondReportedEvent)
	unSignedTx, totalDeleAmount, err := GetDelegateRewardUnsignedTxWithReward(poolClient, poolAddress, height, rewardCoins, memo)
	if err != nil {
		if err == hubClient.ErrNoMsgs {
			//will return ErrNoMsgs if no reward or reward of that height is less than now , we just activeReport
			total := types.NewInt(0)
			delegationsRes, err := poolClient.QueryDelegations(poolAddress, 0)
			if err != nil {
				if !strings.Contains(err.Error(), "unable to find delegations for address") {
					h.log.Error("QueryDelegations failed",
						"pool", poolAddressStr,
						"err", err)
					return err
				}
			} else {
				for _, dele := range delegationsRes.GetDelegationResponses() {
					total = total.Add(dele.Balance.Amount)
				}
			}
			h.log.Info("handleBondReportedEvent no need claim reward", "pool", poolAddressStr, "era", snap.Era, "height", height)
			return h.sendActiveReportMsg(eventBondReported.ShotId, total.BigInt())
		} else {
			h.log.Error("handleBondReportedEvent GetDelegateRewardUnsignedTxWithReward failed",
				"pool address", poolAddressStr,
				"height", height,
				"err", err)
			return err
		}
	}

	wrapUnsignedTx := WrapUnsignedTx{
		UnsignedTx: unSignedTx,
		SnapshotId: eventBondReported.ShotId,
		Era:        snap.Era,
		Bond:       snap.Chunk.Bond.BigInt(),
		Unbond:     snap.Chunk.Unbond.BigInt(),
		Type:       stafiHubXLedgerTypes.TxTypeDealBondReported}

	var txHash, txBts []byte
	for i := 0; i < 5; i++ {
		//use current seq
		seq, err := poolClient.GetSequence(0, poolAddress)
		if err != nil {
			h.log.Error("handleBondReportedEvent GetSequence failed",
				"pool address", poolAddressStr,
				"err", err)
			return err
		}

		sigBts, err := poolClient.SignMultiSigRawTxWithSeq(seq, unSignedTx, subKeyName)
		if err != nil {
			h.log.Error("handleBondReportedEvent SignMultiSigRawTx failed",
				"pool address", poolAddressStr,
				"unsignedTx", string(unSignedTx),
				"err", err)
			return err
		}

		shotIdArray, err := ShotIdToArray(eventBondReported.ShotId)
		if err != nil {
			return err
		}
		//cache unSignedTx
		proposalId := GetClaimRewardProposalId(shotIdArray, uint64(height), uint8(i))
		proposalIdHexStr := hex.EncodeToString(proposalId)

		h.log.Info("handleBondReportedEvent gen unsigned delegate reward Tx",
			"pool address", poolAddressStr,
			"total delegate amount", totalDeleAmount.String(),
			"proposalId", proposalIdHexStr)

		// send signature to stafi
		submitSignature := core.ParamSubmitSignature{
			Denom:     snap.GetDenom(),
			Era:       snap.GetEra(),
			Pool:      poolAddressStr,
			TxType:    stafiHubXLedgerTypes.TxTypeDealBondReported,
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
			h.log.Error("handleBondReportedEvent AssembleMultiSigTx failed",
				"pool address ", poolAddressStr,
				"unsignedTx", hex.EncodeToString(wrapUnsignedTx.UnsignedTx),
				"signatures", bytesArrayToStr(signatures),
				"threshold", threshold,
				"err", err)
			continue
		}
		res, err := poolClient.QueryTxByHash(hex.EncodeToString(txHash))
		if err == nil && res.Code != 0 {
			continue
		}
		break
	}

	return h.checkAndSend(poolClient, &wrapUnsignedTx, m, txHash, txBts, poolAddress, 0)
}

func (h *Handler) dealIcaPoolBondReportedEvent(poolClient *hubClient.Client, eventBondReported core.EventBondReported) error {
	h.log.Info("dealIcaPoolBondReportedEvent", "event", eventBondReported)

	snap := eventBondReported.Snapshot
	done := core.UseSdkConfigContext(poolClient.GetAccountPrefix())
	poolAddress, err := types.AccAddressFromBech32(snap.GetPool())
	if err != nil {
		done()
		h.log.Error("PoolAddr cast to cosmos AccAddress failed",
			"pool address", snap.GetPool(),
			"err", err)
		return err
	}
	poolAddressStr := poolAddress.String()
	done()

	rewardAddress, err := h.conn.GetIcaPoolRewardAddress(poolAddressStr)
	if err != nil {
		return err
	}

	done = core.UseSdkConfigContext(poolClient.GetAccountPrefix())
	rewardAddressStr := rewardAddress.String()
	done()

	height, err := poolClient.GetHeightByEra(snap.Era, h.conn.eraSeconds, h.conn.offset)
	if err != nil {
		h.log.Error("dealIcaPoolBondReportedEvent GetHeightByEra failed",
			"pool address", poolAddressStr,
			"era", snap.Era,
			"err", err)
		return err
	}
	hostChannelId, err := h.conn.GetIcaPoolHostChannelId(poolAddressStr)
	if err != nil {
		return err
	}

	_, dealEraHeight, err := GetLatestDealEraUpdatedTx(poolClient, hostChannelId)
	if err != nil {
		h.log.Error("GetLatestDealEraUpdatedTx failed",
			"pool address", poolAddressStr,
			"era", snap.Era,
			"err", err)
		return err
	}

	if dealEraHeight > height {
		height = dealEraHeight + 1
	}
	h.log.Info("GetLatestDealEraUpdatedTx", "dealEraHeight", dealEraHeight)

	rewardBalanceRes, err := poolClient.QueryBalance(rewardAddress, h.conn.leastBond.Denom, height)
	if err != nil {
		return err
	}
	// no need res stake reward
	if rewardBalanceRes.Balance.IsZero() {
		total := types.NewInt(0)
		delegationsRes, err := poolClient.QueryDelegations(poolAddress, 0)
		if err != nil {
			if !strings.Contains(err.Error(), "unable to find delegations for address") {
				h.log.Error("QueryDelegations failed",
					"pool", poolAddressStr,
					"err", err)
				return err
			}
		} else {
			for _, dele := range delegationsRes.GetDelegationResponses() {
				total = total.Add(dele.Balance.Amount)
			}
		}
		return h.sendActiveReportMsg(eventBondReported.ShotId, total.BigInt())
	}

	msg := sdkXBankTypes.MsgSend{
		FromAddress: rewardAddressStr,
		ToAddress:   poolAddressStr,
		Amount:      types.NewCoins(*rewardBalanceRes.Balance),
	}

	interchainTx, err := stafiHubXLedgerTypes.NewInterchainTxProposal(
		types.AccAddress{},
		snap.Denom,
		poolAddressStr,
		snap.Era,
		stafiHubXLedgerTypes.TxTypeReserved,
		0,
		[]types.Msg{&msg})
	if err != nil {
		return err
	}

	msgs := []types.Msg{&msg}
	proposalInterchainTx := core.ProposalInterchainTx{
		Denom:  snap.Denom,
		Pool:   poolAddressStr,
		Era:    snap.Era,
		TxType: stafiHubXLedgerTypes.TxTypeReserved,
		Factor: 0,
		Msgs:   msgs,
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
	// skip this proId 9520a86df8db89d21e7a11b617114ebdcb825b1544914ab0bc1d4bb709795701 for dragonberry upgrade
	if !strings.EqualFold(interchainTx.PropId, "9520a86df8db89d21e7a11b617114ebdcb825b1544914ab0bc1d4bb709795701") && status != stafiHubXLedgerTypes.InterchainTxStatusSuccess {
		return fmt.Errorf("interchainTx proposalId: %s, txType: %s status: %s", interchainTx.PropId, interchainTx.TxType.String(), status.String())
	}

	targetValidators, err := h.conn.GetPoolTargetValidators(poolAddressStr)
	if err != nil {
		return err
	}

	msgs, err = poolClient.GenDelegateMsgs(
		poolAddress,
		targetValidators,
		*rewardBalanceRes.Balance)
	if err != nil {
		return err
	}

	factor := uint32(1)
	interchainTx, err = stafiHubXLedgerTypes.NewInterchainTxProposal(
		types.AccAddress{},
		snap.Denom,
		poolAddressStr,
		snap.Era,
		stafiHubXLedgerTypes.TxTypeDealBondReported,
		factor,
		msgs)
	if err != nil {
		return err
	}
	proposalInterchainTx = core.ProposalInterchainTx{
		Denom:  snap.Denom,
		Pool:   poolAddressStr,
		Era:    snap.Era,
		TxType: stafiHubXLedgerTypes.TxTypeDealBondReported,
		Factor: factor,
		Msgs:   msgs,
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

	status, err = h.mustGetInterchainTxStatusFromStafiHub(interchainTx.PropId)
	if err != nil {
		return err
	}
	if status != stafiHubXLedgerTypes.InterchainTxStatusSuccess {
		return fmt.Errorf("interchainTx proposalId: %s, txType: %s status: %s", interchainTx.PropId, interchainTx.TxType.String(), status.String())
	}

	total := types.NewInt(0)
	delegationsRes, err := poolClient.QueryDelegations(poolAddress, 0)
	if err != nil {
		if !strings.Contains(err.Error(), "unable to find delegations for address") {
			h.log.Error("QueryDelegations failed",
				"pool", poolAddressStr,
				"err", err)
			return err
		}
	} else {
		for _, dele := range delegationsRes.GetDelegationResponses() {
			total = total.Add(dele.Balance.Amount)
		}
	}
	return h.sendActiveReportMsg(eventBondReported.ShotId, total.BigInt())
}
