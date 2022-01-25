package chain

import (
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/ChainSafe/log15"
	"github.com/cosmos/cosmos-sdk/types"
	hubClient "github.com/stafiprotocol/cosmos-relay-sdk/client"
	"github.com/stafiprotocol/rtoken-relay-core/core"
	"github.com/stafiprotocol/rtoken-relay-core/utils"
	stafiHubXLedgerTypes "github.com/stafiprotocol/stafihub/x/ledger/types"
)

const msgLimit = 4096

type Handler struct {
	conn       *Connection
	router     *core.Router
	msgChan    chan *core.Message
	log        log15.Logger
	stopChan   <-chan struct{}
	sysErrChan chan<- error
}

func NewHandler(conn *Connection, log log15.Logger, stopChan <-chan struct{}, sysErrChan chan<- error) *Handler {
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
			h.log.Info("msgHandler stop")
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
	case core.ReasonSignatureEnoughEvent:
	default:
		return fmt.Errorf("message reason unsupported reason: %s", m.Reason)
	}
	return nil
}

func (h *Handler) sendBondReportMsg(shotIdStr string) error {
	shotId, err := hex.DecodeString(shotIdStr)
	if err != nil {
		return err
	}
	m := core.Message{
		Source:      h.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonBondReport,
		Content: core.ProposalBondReport{
			Denom:  string(h.conn.symbol),
			ShotId: shotId,
			Action: stafiHubXLedgerTypes.BothBondUnbond,
		},
	}

	return h.router.Send(&m)
}

func (h *Handler) sendActiveReportMsg(shotIdStr string, staked, unstaked *big.Int) error {
	shotId, err := hex.DecodeString(shotIdStr)
	if err != nil {
		return err
	}
	m := core.Message{
		Source:      h.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonActiveReport,
		Content: core.ProposalActiveReport{
			Denom:    string(h.conn.symbol),
			ShotId:   shotId,
			Staked:   types.NewIntFromBigInt(staked),
			Unstaked: types.NewIntFromBigInt(unstaked),
		},
	}

	return h.router.Send(&m)
}

func (h *Handler) sendTransferReportMsg(shotIdStr string) error {
	shotId, err := hex.DecodeString(shotIdStr)
	if err != nil {
		return err
	}
	m := core.Message{
		Source:      h.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonTransferReport,
		Content: core.ProposalTransferReport{
			Denom:  string(h.conn.symbol),
			ShotId: shotId,
		},
	}

	return h.router.Send(&m)
}

//process eraPoolUpdate event
//1 gen bond/unbond multiSig unsigned tx and cache it
//2 sign it with subKey
//3 send signature to stafihub
func (h *Handler) handleEraPoolUpdatedEvent(m *core.Message) error {
	eventEraPoolUpdated, ok := m.Content.(core.EventEraPoolUpdated)
	if !ok {
		return fmt.Errorf("ProposalLiquidityBond cast failed, %+v", m)
	}
	snap := eventEraPoolUpdated.Snapshot
	poolAddress, err := types.AccAddressFromBech32(snap.GetPool())
	if err != nil {
		return err
	}
	//check bond/unbond is needed
	//bond report if no need
	bondCmpUnbondResult := snap.Chunk.Bond.BigInt().Cmp(snap.Chunk.Unbond.BigInt())
	if bondCmpUnbondResult == 0 {
		h.log.Info("EvtEraPoolUpdated bond equal to unbond, no need to bond/unbond")
		return h.sendBondReportMsg(eventEraPoolUpdated.ShotId)
	}

	//get poolClient of this pool address
	poolClient, err := h.conn.GetPoolClient(poolAddress.String())
	if err != nil {
		h.log.Error("EraPoolUpdated pool failed",
			"pool hex address", poolAddress.String(),
			"err", err)
		return err
	}

	height, err := h.conn.GetHeightByEra(snap.Era)
	if err != nil {
		h.log.Error("GetHeightByEra failed",
			"pool address", poolAddress.String(),
			"err", snap.Era,
			"err", err)
		return err
	}
	unSignedTx, err := GetBondUnbondUnsignedTx(poolClient, snap.Chunk.Bond.BigInt(), snap.Chunk.Unbond.BigInt(), poolAddress, height)
	if err != nil {
		h.log.Error("GetBondUnbondUnsignedTx failed",
			"pool address", poolAddress.String(),
			"height", height,
			"err", err)
		return err
	}

	//use current seq
	seq, err := poolClient.GetSequence(0, poolAddress)
	if err != nil {
		h.log.Error("GetSequence failed",
			"pool address", poolAddress.String(),
			"err", err)
		return err
	}

	_, err = poolClient.SignMultiSigRawTxWithSeq(seq, unSignedTx, poolClient.GetFromName())
	if err != nil {
		h.log.Error("SignMultiSigRawTxWithSeq failed",
			"pool address", poolAddress.String(),
			"unsignedTx", string(unSignedTx),
			"err", err)
		return err
	}

	shotIdArray, err := ShotIdToArray(eventEraPoolUpdated.ShotId)
	if err != nil {
		return err
	}
	//cache unSignedTx
	proposalId := GetBondUnBondProposalId(shotIdArray, snap.Chunk.Bond.BigInt(), snap.Chunk.Unbond.BigInt(), seq)
	proposalIdHexStr := hex.EncodeToString(proposalId)
	wrapUnsignedTx := WrapUnsignedTx{
		UnsignedTx: unSignedTx,
		SnapshotId: eventEraPoolUpdated.ShotId,
		Era:        snap.Era,
		Bond:       snap.Chunk.Bond.BigInt(),
		Unbond:     snap.Chunk.Unbond.BigInt(),
		Key:        proposalIdHexStr,
		Type:       core.OriginalBond}

	h.conn.CacheUnsignedTx(proposalIdHexStr, &wrapUnsignedTx)

	if bondCmpUnbondResult > 0 {
		h.log.Info("processEraPoolUpdatedEvt gen unsigned bond Tx",
			"pool address", poolAddress.String(),
			"bond amount", new(big.Int).Sub(snap.Chunk.Bond.BigInt(), snap.Chunk.Unbond.BigInt()).String(),
			"proposalId", proposalIdHexStr)
	} else {
		h.log.Info("processEraPoolUpdatedEvt gen unsigned unbond Tx",
			"pool address", poolAddress.String(),
			"unbond amount", new(big.Int).Sub(snap.Chunk.Unbond.BigInt(), snap.Chunk.Bond.BigInt()).String(),
			"proposalId", proposalIdHexStr)
	}
	//todo send signature to stafihub
	return nil
}

//process bondReportEvent from stafihub
//1 query reward on era height
//2 gen (claim reward && delegate) or (claim reward) unsigned tx and cache it
//3 sign it with subKey
//4 send signature to stafihub
func (h *Handler) handleBondReportedEvent(m *core.Message) error {
	eventBondReported, ok := m.Content.(core.EventBondReported)
	if !ok {
		return fmt.Errorf("ProposalLiquidityBond cast failed, %+v", m)
	}
	snap := eventBondReported.Snapshot
	poolAddress, err := types.AccAddressFromBech32(snap.GetPool())
	if err != nil {
		h.log.Error("PoolAddr cast to cosmos AccAddress failed",
			"pool address", snap.GetPool(),
			"err", err)
		return err
	}
	poolClient, err := h.conn.GetPoolClient(poolAddress.String())
	if err != nil {
		h.log.Error("processBondReportEvent failed",
			"pool address", poolAddress.String(),
			"error", err)
		return err
	}

	height, err := h.conn.GetHeightByEra(snap.Era)
	if err != nil {
		h.log.Error("GetHeightByEra failed",
			"pool address", poolAddress.String(),
			"era", snap.Era,
			"err", err)
		return err
	}
	unSignedTx, genTxType, totalDeleAmount, err := GetClaimRewardUnsignedTx(poolClient, poolAddress, height, snap.Chunk.Bond.BigInt(), snap.Chunk.Unbond.BigInt())
	if err != nil && err != hubClient.ErrNoMsgs {
		h.log.Error("GetClaimRewardUnsignedTx failed",
			"pool address", poolAddress.String(),
			"height", height,
			"err", err)
		return err
	}
	//will return ErrNoMsgs if no reward or reward of that height is less than now , we just activeReport
	if err == hubClient.ErrNoMsgs {
		delegationsRes, err := poolClient.QueryDelegations(poolAddress, 0)
		if err != nil {
			h.log.Error("activeReport failed",
				"pool", poolAddress.String(),
				"err", err)
			return err
		}
		total := types.NewInt(0)
		for _, dele := range delegationsRes.GetDelegationResponses() {
			total = total.Add(dele.Balance.Amount)
		}
		h.log.Info("no need claim reward", "pool", poolAddress, "era", snap.Era, "height", height)
		return h.sendActiveReportMsg(eventBondReported.ShotId, total.BigInt(), big.NewInt(0))
	}

	//use current seq
	seq, err := poolClient.GetSequence(0, poolAddress)
	if err != nil {
		h.log.Error("GetSequence failed",
			"pool address", poolAddress.String(),
			"err", err)
		return err
	}

	_, err = poolClient.SignMultiSigRawTxWithSeq(seq, unSignedTx, poolClient.GetFromName())
	if err != nil {
		h.log.Error("SignMultiSigRawTx failed",
			"pool address", poolAddress.String(),
			"unsignedTx", string(unSignedTx),
			"err", err)
		return err
	}

	shotIdBts, err := ShotIdToArray(eventBondReported.ShotId)
	if err != nil {
		return err
	}
	//cache unSignedTx
	proposalId := GetClaimRewardProposalId(shotIdBts, uint64(height))
	proposalIdHexStr := hex.EncodeToString(proposalId)
	wrapUnsignedTx := WrapUnsignedTx{
		UnsignedTx: unSignedTx,
		Key:        proposalIdHexStr,
		SnapshotId: eventBondReported.ShotId,
		Era:        snap.Era,
		Bond:       snap.Chunk.Bond.BigInt(),
		Unbond:     snap.Chunk.Unbond.BigInt(),
		Type:       core.OriginalClaimRewards}

	h.conn.CacheUnsignedTx(proposalIdHexStr, &wrapUnsignedTx)

	switch genTxType {
	case 1:
		h.log.Info("processBondReportEvent gen unsigned claim reward Tx",
			"pool address", poolAddress.String(),
			"total delegate amount", totalDeleAmount.String(),
			"proposalId", proposalIdHexStr)

	case 2:
		h.log.Info("processBondReportEvent gen unsigned delegate reward Tx",
			"pool address", poolAddress.String(),
			"total delegate amount", totalDeleAmount.String(),
			"proposalId", proposalIdHexStr)

	case 3:
		h.log.Info("processBondReportEvent gen unsigned claim and delegate reward Tx",
			"pool address", poolAddress.String(),
			"total delegate amount", totalDeleAmount.String(),
			"proposalId", proposalIdHexStr)

	}

	//todo send signature to stafi
	return nil
}

//process activeReportedEvent from stafihub
//1 gen transfer  unsigned tx and cache it
//2 sign it with subKey
//3 send signature to stafihub
func (h *Handler) handleActiveReportedEvent(m *core.Message) error {
	eventActiveReported, ok := m.Content.(core.EventActiveReported)
	if !ok {
		return fmt.Errorf("ProposalLiquidityBond cast failed, %+v", m)
	}
	snap := eventActiveReported.Snapshot
	poolAddress, err := types.AccAddressFromBech32(snap.Pool)
	if err != nil {
		h.log.Error("PoolAddr cast to cosmos AccAddress failed",
			"pool address", snap.GetPool(),
			"err", err)
		return err
	}
	poolClient, err := h.conn.GetPoolClient(poolAddress.String())
	if err != nil {
		h.log.Error("processBondReportEvent failed",
			"pool address", poolAddress.String(),
			"error", err)
		return err
	}

	unSignedTx, outPuts, err := GetTransferUnsignedTx(poolClient, poolAddress, eventActiveReported.PoolUnbond.Unbondings, h.log)
	if err != nil && err != ErrNoOutPuts {
		h.log.Error("GetTransferUnsignedTx failed", "pool address", poolAddress.String(), "err", err)
		return err
	}
	if err == ErrNoOutPuts {
		h.log.Info("processActiveReportedEvent no need transfer Tx",
			"pool address", poolAddress.String(),
			"era", snap.Era,
			"snapId", eventActiveReported.ShotId)
		return h.sendTransferReportMsg(eventActiveReported.ShotId)
	}

	//use current seq
	seq, err := poolClient.GetSequence(0, poolAddress)
	if err != nil {
		h.log.Error("GetSequence failed",
			"pool address", poolAddress.String(),
			"err", err)
		return err
	}

	sigBts, err := poolClient.SignMultiSigRawTxWithSeq(seq, unSignedTx, poolClient.GetFromName())
	if err != nil {
		h.log.Error("processActiveReportedEvent SignMultiSigRawTx failed",
			"pool address", poolAddress.String(),
			"unsignedTx", string(unSignedTx),
			"err", err)
		return err
	}

	//cache unSignedTx
	proposalId := GetTransferProposalId(utils.BlakeTwo256(unSignedTx), seq)
	proposalIdHexStr := hex.EncodeToString(proposalId)
	wrapUnsignedTx := WrapUnsignedTx{
		UnsignedTx: unSignedTx,
		Key:        proposalIdHexStr,
		SnapshotId: eventActiveReported.ShotId,
		Era:        snap.Era,
		Type:       core.OriginalTransfer}

	h.conn.CacheUnsignedTx(proposalIdHexStr, &wrapUnsignedTx)

	h.log.Info("processActiveReportedEvent gen unsigned transfer Tx",
		"pool address", poolAddress.String(),
		"out put", outPuts,
		"proposalId", proposalIdHexStr,
		"unsignedTx", hex.EncodeToString(unSignedTx),
		"signature", hex.EncodeToString(sigBts))

	//todo send to stafihub
	return nil
}
