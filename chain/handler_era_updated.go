package chain

import (
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/cosmos/cosmos-sdk/types"
	hubClient "github.com/stafihub/cosmos-relay-sdk/client"
	"github.com/stafihub/rtoken-relay-core/common/core"
	stafiHubXLedgerTypes "github.com/stafihub/stafihub/x/ledger/types"
)

// handle eraPoolUpdated event
// 1
//  1. bond>unbond, gen bond multiSig unsigned tx
//  2. bond<unbond, gen unbond+withdraw multiSig unsigned tx
//  3. bond==unbond, if no delegation before, just sendbondreport, else gen withdraw multiSig unsigned tx
//
// 2 sign it with subKey
// 3 send signature to stafihub
// 4 wait until signature enough, then send tx to cosmoshub
// 5 bond report to stafihub
func (h *Handler) handleEraPoolUpdatedEvent(m *core.Message) error {
	h.log.Info("handleEraPoolUpdatedEvent", "m", m)
	eventEraPoolUpdated, ok := m.Content.(core.EventEraPoolUpdated)
	if !ok {
		return fmt.Errorf("ProposalLiquidityBond cast failed, %+v", m)
	}
	snap := eventEraPoolUpdated.Snapshot

	//get poolClient of this pool address
	poolClient, isIcaPool, err := h.conn.GetPoolClient(snap.GetPool())
	if err != nil {
		h.log.Error("handleEraPoolUpdatedEvent GetPoolClient failed",
			"pool address", snap.GetPool(),
			"err", err)
		return err
	}

	if isIcaPool {
		return h.dealIcaEraPoolUpdatedEvent(poolClient, eventEraPoolUpdated)
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
	targetValidators, err := h.conn.GetPoolTargetValidators(poolAddressStr)
	if err != nil {
		return err
	}
	subKeyName, err := h.conn.GetPoolSubkeyName(poolAddressStr)
	if err != nil {
		return err
	}
	// height = max(latest redelegate height, era height)?
	// the redelegate tx height maybe bigger than era height if rValidatorUpdatedEvent is dealed after a long time
	height, err := poolClient.GetHeightByEra(snap.Era, h.conn.eraSeconds, h.conn.offset)
	if err != nil {
		h.log.Error("handleEraPoolUpdatedEvent GetHeightByEra failed",
			"pool address", poolAddressStr,
			"era", snap.Era,
			"err", err)
		return err
	}

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

	memo := GetMemo(snap.Era, TxTypeHandleEraPoolUpdatedEvent)
	unSignedTx, unSignedTxType, err := GetBondUnbondWithdrawUnsignedTxWithTargets(poolClient, snap.Chunk.Bond.BigInt(),
		snap.Chunk.Unbond.BigInt(), h.minUnDelegateAmount.BigInt(), poolAddress, height, targetValidators, memo)
	if err != nil {
		switch {
		case err == hubClient.ErrNoMsgs:
			return h.sendBondReportMsg(eventEraPoolUpdated.ShotId, stafiHubXLedgerTypes.BothBondUnbond)
		default:
			h.log.Error("handleEraPoolUpdatedEvent GetBondUnbondUnsignedTx failed",
				"pool address", poolAddressStr,
				"height", height,
				"err", err)
			return err
		}
	}
	wrapUnsignedTx := WrapUnsignedTx{
		UnsignedTx: unSignedTx,
		SnapshotId: eventEraPoolUpdated.ShotId,
		Era:        snap.Era,
		Bond:       snap.Chunk.Bond.BigInt(),
		Unbond:     snap.Chunk.Unbond.BigInt(),
		Type:       stafiHubXLedgerTypes.TxTypeDealEraUpdated}

	var txHash, txBts []byte
	for i := 15; i < 20; i++ {
		//use current seq
		seq, err := poolClient.GetSequence(0, poolAddress)
		if err != nil {
			h.log.Error("handleEraPoolUpdatedEvent GetSequence failed",
				"pool address", poolAddressStr,
				"err", err)
			return err
		}

		sigBts, err := poolClient.SignMultiSigRawTxWithSeq(seq, unSignedTx, subKeyName)
		if err != nil {
			h.log.Error("handleEraPoolUpdatedEvent SignMultiSigRawTxWithSeq failed",
				"pool address", poolAddressStr,
				"unsignedTx", string(unSignedTx),
				"err", err)
			return err
		}

		shotIdArray, err := ShotIdToArray(eventEraPoolUpdated.ShotId)
		if err != nil {
			return err
		}

		proposalId := GetBondUnBondProposalId(shotIdArray, snap.Chunk.Bond.BigInt(), snap.Chunk.Unbond.BigInt(), uint8(i))
		proposalIdHexStr := hex.EncodeToString(proposalId)

		switch unSignedTxType {
		case UnSignedTxTypeBondEqualUnbondWithdraw:
			h.log.Info("handleEraPoolUpdatedEvent gen withdraw Tx",
				"pool address", poolAddressStr,
				"bond amount", new(big.Int).Sub(snap.Chunk.Bond.BigInt(), snap.Chunk.Unbond.BigInt()).String(),
				"proposalId", proposalIdHexStr)
		case UnSignedTxTypeDelegate:
			h.log.Info("handleEraPoolUpdatedEvent gen unsigned bond Tx",
				"pool address", poolAddressStr,
				"bond amount", new(big.Int).Sub(snap.Chunk.Bond.BigInt(), snap.Chunk.Unbond.BigInt()).String(),
				"proposalId", proposalIdHexStr)
		case UnSignedTxTypeUnDelegateAndWithdraw:
			h.log.Info("handleEraPoolUpdatedEvent gen unsigned unbond+withdraw Tx",
				"pool address", poolAddressStr,
				"unbond amount", new(big.Int).Sub(snap.Chunk.Unbond.BigInt(), snap.Chunk.Bond.BigInt()).String(),
				"proposalId", proposalIdHexStr)
		case UnSignedTxTypeSkipAndWithdraw:
			h.log.Info("handleEraPoolUpdatedEvent no available vals for unbond or unbond less than min undelegate amount and we gen withdraw Tx",
				"pool address", poolAddressStr,
				"unbond amount", new(big.Int).Sub(snap.Chunk.Unbond.BigInt(), snap.Chunk.Bond.BigInt()).String(),
				"minUndelegateAmount", h.minUnDelegateAmount.String(),
				"proposalId", proposalIdHexStr)
		default:
			return fmt.Errorf("unsupport unSignedType %d", unSignedTxType)
		}

		// send signature to stafihub
		submitSignature := core.ParamSubmitSignature{
			Denom:     snap.GetDenom(),
			Era:       snap.GetEra(),
			Pool:      poolAddressStr,
			TxType:    stafiHubXLedgerTypes.TxTypeDealEraUpdated,
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
			h.log.Error("handleEraPoolUpdatedEvent AssembleMultiSigTx failed",
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

	return h.checkAndSend(poolClient, &wrapUnsignedTx, m, txHash, txBts, poolAddress, unSignedTxType)
}

func (h *Handler) dealIcaEraPoolUpdatedEvent(poolClient *hubClient.Client, eventEraPoolUpdated core.EventEraPoolUpdated) error {
	h.log.Info("dealIcaEraPoolUpdatedEvent", "event", eventEraPoolUpdated)

	snap := eventEraPoolUpdated.Snapshot
	done := core.UseSdkConfigContext(poolClient.GetAccountPrefix())
	poolAddress, err := types.AccAddressFromBech32(snap.Pool)
	if err != nil {
		done()
		return err
	}
	poolAddressStr := poolAddress.String()
	done()

	targetValidators, err := h.conn.GetPoolTargetValidators(poolAddressStr)
	if err != nil {
		return err
	}

	// height = max(latest redelegate height, era height)?
	// the redelegate tx height maybe bigger than era height if rValidatorUpdatedEvent is dealed after a long time
	height, err := poolClient.GetHeightByEra(snap.Era, h.conn.eraSeconds, h.conn.offset)
	if err != nil {
		h.log.Error("handleEraPoolUpdatedEvent GetHeightByEra failed",
			"pool address", poolAddressStr,
			"era", snap.Era,
			"err", err)
		return err
	}
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

	msgs, _, err := GetBondUnbondWithdrawMsgsWithTargets(poolClient, snap.Chunk.Bond.BigInt(),
		snap.Chunk.Unbond.BigInt(), poolAddress, height, targetValidators)
	if err != nil {
		if err == hubClient.ErrNoMsgs {
			return h.sendBondReportMsg(eventEraPoolUpdated.ShotId, stafiHubXLedgerTypes.BothBondUnbond)
		} else {
			h.log.Error("handleEraPoolUpdatedEvent GetBondUnbondUnsignedTx failed",
				"pool address", poolAddressStr,
				"height", height,
				"err", err)
			return err
		}
	}

	factor := uint32(1)
	interchainTx, err := stafiHubXLedgerTypes.NewInterchainTxProposal(
		types.AccAddress{},
		snap.Denom,
		poolAddressStr,
		snap.Era,
		stafiHubXLedgerTypes.TxTypeDealEraUpdated,
		factor,
		msgs)
	if err != nil {
		return err
	}
	proposalInterchainTx := core.ProposalInterchainTx{
		Denom:  snap.Denom,
		Pool:   poolAddressStr,
		Era:    snap.Era,
		TxType: stafiHubXLedgerTypes.TxTypeDealEraUpdated,
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
	if status != stafiHubXLedgerTypes.InterchainTxStatusSuccess {
		return fmt.Errorf("interchainTx proposalId: %s, txType: %s status: %s", interchainTx.PropId, interchainTx.TxType.String(), status.String())
	}

	return h.sendBondReportMsg(eventEraPoolUpdated.ShotId, stafiHubXLedgerTypes.BothBondUnbond)
}
