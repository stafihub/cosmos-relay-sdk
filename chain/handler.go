package chain

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/types"
	"github.com/stafihub/rtoken-relay-core/common/core"
	"github.com/stafihub/rtoken-relay-core/common/log"
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

// resolve msg from other chains
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

// resolve msg from other chains
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
	case core.ReasonInitPoolEvent:
		return h.handleInitPoolEvent(m)
	case core.ReasonRemovePoolEvent:
		return h.handleRemovePoolEvent(m)
	default:
		return fmt.Errorf("message reason unsupported reason: %s", m.Reason)
	}
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

// only support ica pool
func (h *Handler) handleInitPoolEvent(m *core.Message) error {
	h.log.Info("handleInitPoolEvent", "m", m)

	eventInitPool, ok := m.Content.(core.EventInitPool)
	if !ok {
		return fmt.Errorf("EventInitPool cast failed, %+v", m)
	}

	return h.conn.AddIcaPool(eventInitPool.PoolAddress, eventInitPool.WithdrawalAddress, eventInitPool.HostChannelId, eventInitPool.Validators)
}

// support multisig/ica pool
func (h *Handler) handleRemovePoolEvent(m *core.Message) error {
	h.log.Info("handleRemovePoolEvent", "m", m)

	eventRemovePool, ok := m.Content.(core.EventRemovePool)
	if !ok {
		return fmt.Errorf("EventRemovePool cast failed, %+v", m)
	}

	h.conn.RemovePool(eventRemovePool.PoolAddress)
	return nil
}
