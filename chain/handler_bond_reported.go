package chain

import (
	"encoding/hex"
	"fmt"
	"math/big"
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
	//we just activeReport if total delegate amount <= 10000
	totalDelegateAmount := types.NewInt(0)
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
			totalDelegateAmount = totalDelegateAmount.Add(dele.Balance.Amount)
		}
	}
	if totalDelegateAmount.LTE(types.NewInt(1e4)) {
		h.log.Info("no need claim reward", "pool", poolAddressStr, "era", snap.Era)
		return h.sendActiveReportMsg(eventBondReported.ShotId, totalDelegateAmount.BigInt())
	}
	rewardCoins, height, alreadySendReported, err := GetRewardToBeDelegated(poolClient, poolAddressStr, snap.Era)
	if err != nil {
		if err == ErrNoRewardNeedDelegate {
			//will return ErrNoRewardNeedDelegate if no reward or reward of that height is less than now , we just activeReport
			h.log.Info("no need claim reward", "pool", poolAddressStr, "era", snap.Era)
			return h.sendActiveReportMsg(eventBondReported.ShotId, totalDelegateAmount.BigInt())
		} else {
			h.log.Error("handleBondReportedEvent GetRewardToBeDelegated failed",
				"pool address", poolAddressStr,
				"err", err)
			return err
		}
	}

	if alreadySendReported {
		_, redelegateTxHeight, err := GetLatestReDelegateTx(poolClient, poolAddressStr)
		if err != nil {
			h.log.Error("handleEraPoolUpdatedEvent GetLatestReDelegateTx failed",
				"pool address", poolAddressStr,
				"era", snap.Era,
				"err", err)
			return err
		}
		if redelegateTxHeight > height {
			height = redelegateTxHeight
		}
		targetValidators, err := h.conn.GetPoolTargetValidators(poolAddressStr)
		if err != nil {
			return err
		}
		// check tx type
		_, unSignedTxType, err := GetBondUnbondWithdrawUnsignedTxWithTargets(poolClient, snap.Chunk.Bond.BigInt(),
			snap.Chunk.Unbond.BigInt(), h.minUnDelegateAmount.BigInt(), poolAddress, height, targetValidators, "memo")
		if err != nil && err != hubClient.ErrNoMsgs {
			return err
		}
		total := totalDelegateAmount
		if err == nil && unSignedTxType == UnSignedTxTypeSkipAndWithdraw {
			diff := new(big.Int).Sub(snap.Chunk.Unbond.BigInt(), snap.Chunk.Bond.BigInt())
			if diff.Sign() < 0 {
				diff = big.NewInt(0)
			}
			// we should sub diff as we use eitherBondUnbond action to bondReport
			total = total.Sub(types.NewIntFromBigInt(diff))
			if total.IsNegative() {
				total = types.ZeroInt()
			}
		}
		h.log.Info("no need claim reward, already send reported", "pool", poolAddressStr, "era", snap.Era)
		return h.sendActiveReportMsg(eventBondReported.ShotId, total.BigInt())
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
	for i := 10; i < 15; i++ {
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

	return h.checkAndSend(poolClient, &wrapUnsignedTx, m, txHash, txBts, poolAddress, UnSignedTxTypeUnSpecified)
}

func (h *Handler) dealIcaPoolBondReportedEvent(poolClient *hubClient.Client, eventBondReported core.EventBondReported) error {
	h.log.Info("dealIcaPoolBondReportedEvent", "event", eventBondReported)

	// check lsm proposalId if upgrade to v050
	proposalId, err := h.mustGetLatestLsmProposalIdFromStafiHub()
	if err != nil {
		if err != ErrNotFound && err != ErrUnknownQueryPath {
			return err
		}
	} else {
		status, err := h.mustGetInterchainTxStatusFromStafiHub(proposalId)
		if err != nil {
			return err
		}
		if status != stafiHubXLedgerTypes.InterchainTxStatusSuccess {
			return fmt.Errorf("lsm proposalId %s not success", proposalId)
		}
	}

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
	factor := uint32(7)
	interchainTx, err := stafiHubXLedgerTypes.NewInterchainTxProposal(
		types.AccAddress{},
		snap.Denom,
		poolAddressStr,
		snap.Era,
		stafiHubXLedgerTypes.TxTypeWithdrawAddressSend,
		factor,
		[]types.Msg{&msg})
	if err != nil {
		return err
	}

	msgs := []types.Msg{&msg}
	proposalInterchainTx := core.ProposalInterchainTx{
		Denom:  snap.Denom,
		Pool:   poolAddressStr,
		Era:    snap.Era,
		TxType: stafiHubXLedgerTypes.TxTypeWithdrawAddressSend,
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

	var valAddrs []types.ValAddress
	totalShouldBondAmount := rewardBalanceRes.Balance.Amount

	// lsm case
	if poolClient.GetDenom() == "uatom" {
		stakingParams, err := poolClient.QueryStakingParams()
		if err != nil {
			return err
		}

		poolRes, poolErr := poolClient.QueryPool(height)
		if poolErr != nil {
			return poolErr
		}
		totalLiquidStakedAmountRes, lsErr := poolClient.TotalLiquidStaked(height)
		if lsErr != nil {
			return lsErr
		}
		totalLiquidStakedAmount, ok := types.NewIntFromString(totalLiquidStakedAmountRes.Tokens)
		if !ok {
			return fmt.Errorf("parse totalLiquidStakedAmount %s failed", totalLiquidStakedAmountRes.Tokens)
		}

		totalStakedAmount := poolRes.Pool.BondedTokens.Add(totalShouldBondAmount)
		totalLiquidStakedAmount = totalLiquidStakedAmount.Add(totalShouldBondAmount)

		// 0 check global liquid staking cap
		liquidStakePercent := types.NewDecFromInt(totalLiquidStakedAmount).Quo(types.NewDecFromInt(totalStakedAmount))
		if liquidStakePercent.GT(stakingParams.Params.GlobalLiquidStakingCap) {
			return fmt.Errorf("ExceedsGlobalLiquidStakingCap %s", liquidStakePercent.String())
		}

		for _, val := range targetValidators {
			done := core.UseSdkConfigContext(poolClient.GetAccountPrefix())
			valStr := val.String()
			done()

			valRes, sErr := poolClient.QueryValidator(valStr, height)
			if sErr != nil {
				return sErr
			}
			shares, sErr := valRes.Validator.SharesFromTokens(totalShouldBondAmount)
			if sErr != nil {
				return sErr
			}

			// 1 check val bond cap
			if !stakingParams.Params.ValidatorBondFactor.Equal(ValidatorBondCapDisabled) {
				maxValLiquidShares := valRes.Validator.ValidatorBondShares.Mul(stakingParams.Params.ValidatorBondFactor)
				if valRes.Validator.LiquidShares.Add(shares).GT(maxValLiquidShares) {
					continue
				}
			}

			// 2 check val liquid staking cap
			updatedLiquidShares := valRes.Validator.LiquidShares.Add(shares)
			updatedTotalShares := valRes.Validator.DelegatorShares.Add(shares)
			liquidStakePercent := updatedLiquidShares.Quo(updatedTotalShares)
			if liquidStakePercent.GT(stakingParams.Params.ValidatorLiquidStakingCap) {
				continue
			}

			valAddrs = append(valAddrs, val)
		}

	} else {
		valAddrs = targetValidators
	}

	valAddrsLen := len(valAddrs)
	//check valAddrs length
	if valAddrsLen == 0 {
		return fmt.Errorf("no enough target valAddrs, pool: %s", poolAddressStr)
	}

	msgs, err = poolClient.GenDelegateMsgs(
		poolAddress,
		valAddrs,
		*rewardBalanceRes.Balance)
	if err != nil {
		return err
	}

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
