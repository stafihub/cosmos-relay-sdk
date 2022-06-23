package chain

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/types"
	hubClient "github.com/stafihub/cosmos-relay-sdk/client"
	"github.com/stafihub/rtoken-relay-core/common/core"
	"github.com/stafihub/rtoken-relay-core/common/log"
	"github.com/stafihub/rtoken-relay-core/common/utils"
	stafiHubXLedgerTypes "github.com/stafihub/stafihub/x/ledger/types"
)

const msgLimit = 4096

type Handler struct {
	conn       *Connection
	router     *core.Router
	msgChan    chan *core.Message
	log        log.Logger
	stopChan   <-chan struct{}
	sysErrChan chan<- error
}

func NewHandler(conn *Connection, log log.Logger, stopChan <-chan struct{}, sysErrChan chan<- error) *Handler {
	return &Handler{
		conn:       conn,
		msgChan:    make(chan *core.Message, msgLimit),
		log:        log,
		stopChan:   stopChan,
		sysErrChan: sysErrChan,
	}
}

func (h *Handler) setRouter(r *core.Router) {
	h.router = r
}

func (h *Handler) start() error {
	go h.msgHandler()
	return nil
}

//resolve msg from other chains
func (h *Handler) HandleMessage(m *core.Message) {
	h.queueMessage(m)
}

func (h *Handler) queueMessage(m *core.Message) {
	h.msgChan <- m
}

func (h *Handler) msgHandler() {
	for {
		select {
		case <-h.stopChan:
			h.log.Info("msgHandler receive stopChan, will stop")
			return
		case msg := <-h.msgChan:
			err := h.handleMessage(msg)
			if err != nil {
				h.sysErrChan <- fmt.Errorf("resolveMessage process failed.err: %s, msg: %+v", err, msg)
				return
			}
		}
	}
}

//resolve msg from other chains
func (h *Handler) handleMessage(m *core.Message) error {
	switch m.Reason {
	case core.ReasonEraPoolUpdatedEvent:
		return h.handleEraPoolUpdatedEvent(m)
	case core.ReasonBondReportedEvent:
		return h.handleBondReportedEvent(m)
	case core.ReasonActiveReportedEvent:
		return h.handleActiveReportedEvent(m)
	case core.ReasonRParamsChangedEvent:
		return h.handleRParamsChangedEvent(m)
	case core.ReasonRValidatorUpdatedEvent:
		return h.handleRValidatorUpdatedEvent(m)
	case core.ReasonRValidatorAddedEvent:
		return h.handleRValidatorAddedEvent(m)
	default:
		return fmt.Errorf("message reason unsupported reason: %s", m.Reason)
	}
}

// handle eraPoolUpdated event
// 1
//   1) bond>unbond, gen bond multiSig unsigned tx
//   2) bond<unbond, gen unbond+withdraw multiSig unsigned tx
//   3) bond==unbond, if no delegation before, just sendbondreport, else gen withdraw multiSig unsigned tx
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
	poolClient, err := h.conn.GetPoolClient(snap.GetPool())
	if err != nil {
		h.log.Error("handleEraPoolUpdatedEvent GetPoolClient failed",
			"pool address", snap.GetPool(),
			"err", err)
		return err
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
	unSignedTx, unSignedType, err := GetBondUnbondWithdrawUnsignedTxWithTargets(poolClient, snap.Chunk.Bond.BigInt(),
		snap.Chunk.Unbond.BigInt(), poolAddress, height, targetValidators, memo)
	if err != nil {
		if err == hubClient.ErrNoMsgs {
			return h.sendBondReportMsg(eventEraPoolUpdated.ShotId)
		} else {
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
		Type:       stafiHubXLedgerTypes.TxTypeBond}

	var txHash, txBts []byte
	for i := 0; i < 5; i++ {
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

		switch unSignedType {
		case 0:
			h.log.Info("handleEraPoolUpdatedEvent gen withdraw Tx",
				"pool address", poolAddressStr,
				"bond amount", new(big.Int).Sub(snap.Chunk.Bond.BigInt(), snap.Chunk.Unbond.BigInt()).String(),
				"proposalId", proposalIdHexStr)
		case 1:
			h.log.Info("handleEraPoolUpdatedEvent gen unsigned bond Tx",
				"pool address", poolAddressStr,
				"bond amount", new(big.Int).Sub(snap.Chunk.Bond.BigInt(), snap.Chunk.Unbond.BigInt()).String(),
				"proposalId", proposalIdHexStr)
		case -1:
			h.log.Info("handleEraPoolUpdatedEvent gen unsigned unbond+withdraw Tx",
				"pool address", poolAddressStr,
				"unbond amount", new(big.Int).Sub(snap.Chunk.Unbond.BigInt(), snap.Chunk.Bond.BigInt()).String(),
				"proposalId", proposalIdHexStr)
		}

		// send signature to stafihub
		submitSignature := core.ParamSubmitSignature{
			Denom:     snap.GetDenom(),
			Era:       snap.GetEra(),
			Pool:      poolAddressStr,
			TxType:    stafiHubXLedgerTypes.TxTypeBond,
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

	return h.checkAndSend(poolClient, &wrapUnsignedTx, m, txHash, txBts, poolAddress)
}

// handle bondReportedEvent from stafihub
// 1 query reward on last withdraw tx  height,
//   1) if no reward, just send active report to stafihub
//   2) if has reward
//     1) gen delegate unsigned tx
//     2) sign it with subKey
//     3) send signature to stafihub
//     4) wait until signature enough and send tx to cosmoshub
//     5) active report to stafihub
func (h *Handler) handleBondReportedEvent(m *core.Message) error {
	h.log.Info("handleBondReportedEvent", "m", m)
	eventBondReported, ok := m.Content.(core.EventBondReported)
	if !ok {
		return fmt.Errorf("ProposalLiquidityBond cast failed, %+v", m)
	}
	snap := eventBondReported.Snapshot
	poolClient, err := h.conn.GetPoolClient(snap.GetPool())
	if err != nil {
		h.log.Error("handleBondReportedEvent GetPoolClient failed",
			"pool address", snap.GetPool(),
			"error", err)
		return err
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
		Type:       stafiHubXLedgerTypes.TxTypeClaim}

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
			TxType:    stafiHubXLedgerTypes.TxTypeClaim,
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

	return h.checkAndSend(poolClient, &wrapUnsignedTx, m, txHash, txBts, poolAddress)
}

// handle activeReportedEvent from stafihub
// 1
//   1) if no transfer info, just transfer report to stafihub
//   2) has transfer info, gen transfer unsigned tx
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
	poolClient, err := h.conn.GetPoolClient(snap.GetPool())
	if err != nil {
		h.log.Error("handleActiveReportedEvent GetPoolClient failed",
			"pool address", snap.GetPool(),
			"error", err)
		return err
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
		Type:       stafiHubXLedgerTypes.TxTypeTransfer}

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
			TxType:    stafiHubXLedgerTypes.TxTypeTransfer,
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

// update rparams
func (h *Handler) handleRParamsChangedEvent(m *core.Message) error {
	h.log.Info("handleRParamsChangedEvent", "m", m)

	eventRParamsChanged, ok := m.Content.(core.EventRParamsChanged)
	if !ok {
		return fmt.Errorf("EventRParamsChanged cast failed, %+v", m)
	}

	leastBond, err := types.ParseCoinNormalized(eventRParamsChanged.LeastBond)
	if err != nil {
		return err
	}

	h.conn.RParams.eraSeconds = int64(eventRParamsChanged.EraSeconds)
	h.conn.RParams.leastBond = leastBond
	h.conn.RParams.offset = int64(eventRParamsChanged.Offset)
	for _, c := range h.conn.poolClients {
		err := c.SetGasPrice(eventRParamsChanged.GasPrice)
		if err != nil {
			return fmt.Errorf("setGasPrice failed, err: %s", err)
		}
	}
	return nil
}

// update rvalidator added
func (h *Handler) handleRValidatorAddedEvent(m *core.Message) error {
	h.log.Info("handleRValidatorAddedEvent", "m", m)

	eventRValidatorAdded, ok := m.Content.(core.EventRValidatorAdded)
	if !ok {
		return fmt.Errorf("EventRValidatorAdded cast failed, %+v", m)
	}

	poolClient, err := h.conn.GetPoolClient(eventRValidatorAdded.PoolAddress)
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
	eventRValidatorUpdated, ok := m.Content.(core.EventRValidatorUpdated)
	if !ok {
		return fmt.Errorf("EventRValidatorUpdatedEvent cast failed, %+v", m)
	}

	poolClient, err := h.conn.GetPoolClient(eventRValidatorUpdated.PoolAddress)
	if err != nil {
		h.log.Error("handleRValidatorUpdatedEvent GetPoolClient failed",
			"pool address", eventRValidatorUpdated.PoolAddress,
			"error", err)
		return err
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
		Type:           stafiHubXLedgerTypes.TxTypeUnbond,
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
			TxType:    stafiHubXLedgerTypes.TxTypeUnbond,
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
