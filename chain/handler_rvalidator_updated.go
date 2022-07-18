package chain

import (
	"encoding/hex"
	"fmt"

	"github.com/cosmos/cosmos-sdk/types"
	hubClient "github.com/stafihub/cosmos-relay-sdk/client"
	"github.com/stafihub/rtoken-relay-core/common/core"
	stafiHubXLedgerTypes "github.com/stafihub/stafihub/x/ledger/types"
)

// update rvalidator added
func (h *Handler) handleRValidatorAddedEvent(m *core.Message) error {
	h.log.Info("handleRValidatorAddedEvent", "m", m)

	eventRValidatorAdded, ok := m.Content.(core.EventRValidatorAdded)
	if !ok {
		return fmt.Errorf("EventRValidatorAdded cast failed, %+v", m)
	}

	poolClient, _, err := h.conn.GetPoolClient(eventRValidatorAdded.PoolAddress)
	if err != nil {
		h.log.Error("handleRValidatorAddedEvent GetPoolClient failed",
			"pool address", eventRValidatorAdded.PoolAddress,
			"error", err)
		return err
	}

	done := core.UseSdkConfigContext(poolClient.GetAccountPrefix())
	addedValAddress, err := types.ValAddressFromBech32(eventRValidatorAdded.AddedAddress)
	if err != nil {
		done()
		return err
	}
	done()

	h.conn.AddPoolTargetValidator(eventRValidatorAdded.PoolAddress, addedValAddress)
	return nil
}

//process validatorUpdated
//1 gen redelegate unsigned tx and cache it
//2 sign it with subKey
//3 send signature to stafi
//4 wait until signature enough and send tx to cosmoshub
//5 rvalidator update report to stafihub
func (h *Handler) handleRValidatorUpdatedEvent(m *core.Message) error {
	h.log.Info("handleRValidatorUpdatedEvent", "m", m)
	eventRValidatorUpdated, ok := m.Content.(core.EventRValidatorUpdated)
	if !ok {
		return fmt.Errorf("EventRValidatorUpdatedEvent cast failed, %+v", m)
	}

	poolClient, isIcaPool, err := h.conn.GetPoolClient(eventRValidatorUpdated.PoolAddress)
	if err != nil {
		h.log.Error("handleRValidatorUpdatedEvent GetPoolClient failed",
			"pool address", eventRValidatorUpdated.PoolAddress,
			"error", err)
		return err
	}

	if isIcaPool {
		return h.dealIcaRValidatorUpdatedEvent(poolClient, eventRValidatorUpdated)
	}

	done := core.UseSdkConfigContext(poolClient.GetAccountPrefix())
	poolAddress, err := types.AccAddressFromBech32(eventRValidatorUpdated.PoolAddress)
	if err != nil {
		done()
		return err
	}
	poolAddressStr := poolAddress.String()
	memo := GetValidatorUpdatedMemo(eventRValidatorUpdated.Era, eventRValidatorUpdated.CycleVersion, eventRValidatorUpdated.CycleNumber)

	oldValidator, err := types.ValAddressFromBech32(eventRValidatorUpdated.OldAddress)
	if err != nil {
		h.log.Error("old validator cast to cosmos AccAddress failed",
			"old val address", eventRValidatorUpdated.OldAddress,
			"err", err)
		done()
		return err
	}
	newValidator, err := types.ValAddressFromBech32(eventRValidatorUpdated.NewAddress)
	if err != nil {
		h.log.Error("new validator cast to cosmos AccAddress failed",
			"new val address", eventRValidatorUpdated.NewAddress,
			"err", err)
		done()
		return err
	}
	done()

	h.conn.ReplacePoolTargetValidator(poolAddressStr, oldValidator, newValidator)

	// got target height
	height, err := poolClient.GetHeightByEra(uint32(eventRValidatorUpdated.CycleNumber), int64(eventRValidatorUpdated.CycleSeconds), 0)
	if err != nil {
		return err
	}
	threshold, err := h.conn.GetPoolThreshold(poolAddressStr)
	if err != nil {
		return err
	}
	subKeyName, err := h.conn.GetPoolSubkeyName(poolAddressStr)
	if err != nil {
		return err
	}

	delRes, err := poolClient.QueryDelegation(poolAddress, oldValidator, height)
	if err != nil {
		h.log.Error("QueryDelegation failed",
			"pool", poolAddressStr,
			"old validator", eventRValidatorUpdated.OldAddress,
			"err", err)
		return err
	}
	amount := delRes.GetDelegationResponse().GetBalance()

	unSignedTx, err := poolClient.GenMultiSigRawReDelegateTxWithMemo(poolAddress, oldValidator, newValidator, amount, memo)
	if err != nil {
		h.log.Error("GenMultiSigRawReDelegateTx failed",
			"pool", poolAddressStr,
			"old validator", eventRValidatorUpdated.OldAddress,
			"new validator", eventRValidatorUpdated.NewAddress,
			"err", err)
		return err
	}

	wrapUnsignedTx := WrapUnsignedTx{
		UnsignedTx:     unSignedTx,
		SnapshotId:     "",
		Era:            eventRValidatorUpdated.Era,
		Type:           stafiHubXLedgerTypes.TxTypeDealValidatorUpdated,
		PoolAddressStr: poolAddressStr,
		CycleVersion:   eventRValidatorUpdated.CycleVersion,
		CycleNumber:    eventRValidatorUpdated.CycleNumber}

	var txHash, txBts []byte
	for i := 0; i < 5; i++ {
		//use current seq
		seq, err := poolClient.GetSequence(0, poolAddress)
		if err != nil {
			h.log.Error("GetSequence failed",
				"pool address", poolAddressStr,
				"err", err)
			return err
		}

		sigBts, err := poolClient.SignMultiSigRawTxWithSeq(seq, unSignedTx, subKeyName)
		if err != nil {
			h.log.Error("handleRValidatorUpdatedEvent SignMultiSigRawTx failed",
				"pool address", poolAddressStr,
				"unsignedTx", string(unSignedTx),
				"err", err)
			return err
		}

		//cache unSignedTx
		proposalId := GetValidatorUpdateProposalId(unSignedTx, uint8(i))
		proposalIdHexStr := hex.EncodeToString(proposalId)

		// send to stafihub
		submitSignature := core.ParamSubmitSignature{
			Denom:     eventRValidatorUpdated.Denom,
			Era:       eventRValidatorUpdated.Era,
			Pool:      poolAddressStr,
			TxType:    stafiHubXLedgerTypes.TxTypeDealValidatorUpdated,
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
			h.log.Error("handleRValidatorUpdatedEvent AssembleMultiSigTx failed",
				"pool address ", poolAddressStr,
				"unsignedTx", hex.EncodeToString(wrapUnsignedTx.UnsignedTx),
				"signatures", bytesArrayToStr(signatures),
				"threshold", threshold,
				"err", err)
			continue
		}

		res, err := poolClient.QueryTxByHash(hex.EncodeToString(txHash))
		if err == nil && res.Code != 0 {
			h.log.Warn("handleRValidatorUpdatedEvent queryTxHash failed, will retry",
				"txHash", hex.EncodeToString(txHash),
				"res.Code", res.Code,
				"res.Rawlog", res.RawLog)
			continue
		}
		break
	}

	return h.checkAndSend(poolClient, &wrapUnsignedTx, m, txHash, txBts, poolAddress)
}

func (h *Handler) dealIcaRValidatorUpdatedEvent(poolClient *hubClient.Client, eventRValidatorUpdated core.EventRValidatorUpdated) error {
	done := core.UseSdkConfigContext(poolClient.GetAccountPrefix())
	poolAddress, err := types.AccAddressFromBech32(eventRValidatorUpdated.PoolAddress)
	if err != nil {
		done()
		return err
	}
	poolAddressStr := poolAddress.String()

	oldValidator, err := types.ValAddressFromBech32(eventRValidatorUpdated.OldAddress)
	if err != nil {
		h.log.Error("old validator cast to cosmos AccAddress failed",
			"old val address", eventRValidatorUpdated.OldAddress,
			"err", err)
		done()
		return err
	}
	newValidator, err := types.ValAddressFromBech32(eventRValidatorUpdated.NewAddress)
	if err != nil {
		h.log.Error("new validator cast to cosmos AccAddress failed",
			"new val address", eventRValidatorUpdated.NewAddress,
			"err", err)
		done()
		return err
	}
	done()

	h.conn.ReplacePoolTargetValidator(poolAddressStr, oldValidator, newValidator)
	// got target height
	height, err := poolClient.GetHeightByEra(uint32(eventRValidatorUpdated.CycleNumber), int64(eventRValidatorUpdated.CycleSeconds), 0)
	if err != nil {
		return err
	}
	delRes, err := poolClient.QueryDelegation(poolAddress, oldValidator, height)
	if err != nil {
		h.log.Error("QueryDelegation failed",
			"pool", poolAddressStr,
			"old validator", eventRValidatorUpdated.OldAddress,
			"err", err)
		return err
	}
	amount := delRes.GetDelegationResponse().GetBalance()

	msgs, err := poolClient.GenReDelegateMsgs(poolAddress, oldValidator, newValidator, amount)
	if err != nil {
		h.log.Error("GenMultiSigRawReDelegateTx failed",
			"pool", poolAddressStr,
			"old validator", eventRValidatorUpdated.OldAddress,
			"new validator", eventRValidatorUpdated.NewAddress,
			"err", err)
		return err
	}

	// todo check era onchain?
	interchainTx, err := stafiHubXLedgerTypes.NewInterchainTxProposal(
		types.AccAddress{},
		eventRValidatorUpdated.Denom,
		poolAddressStr,
		eventRValidatorUpdated.Era,
		stafiHubXLedgerTypes.TxTypeDealEraUpdated,
		0,
		msgs)
	if err != nil {
		return err
	}
	proposalInterchainTx := core.ProposalInterchainTx{
		InterchainTx: *interchainTx,
	}

	err = h.sendInterchainTx(&proposalInterchainTx)
	if err != nil {
		return err
	}

	status, err := h.mustGetProposalStatusFromStafiHub(interchainTx.PropId)
	if err != nil {
		return err
	}
	if status != stafiHubXLedgerTypes.InterchainTxStatusSuccess {
		return fmt.Errorf("proposalId %s status: %s", interchainTx.PropId, status)
	}
	return h.sendRValidatorUpdateReportReportMsg(poolAddressStr, eventRValidatorUpdated.CycleVersion, eventRValidatorUpdated.CycleNumber)
}
