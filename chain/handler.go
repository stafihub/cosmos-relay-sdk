package chain

import (
	"fmt"

	"github.com/ChainSafe/log15"
	"github.com/stafiprotocol/rtoken-relay-core/core"
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

func (w *Handler) setRouter(r *core.Router) {
	w.router = r
}

func (w *Handler) start() error {
	go w.msgHandler()
	return nil
}

//resolve msg from other chains
func (w *Handler) HandleMessage(m *core.Message) {
	w.queueMessage(m)
}

func (w *Handler) queueMessage(m *core.Message) {
	w.msgChan <- m
}

func (w *Handler) msgHandler() {
	for {
		select {
		case <-w.stopChan:
			w.log.Info("msgHandler stop")
			return
		case msg := <-w.msgChan:
			err := w.handleMessage(msg)
			if err != nil {
				w.sysErrChan <- fmt.Errorf("resolveMessage process failed.err: %s, msg: %+v", err, msg)
				return
			}
		}
	}
}

//resolve msg from other chains
func (w *Handler) handleMessage(m *core.Message) error {
	switch m.Reason {
	default:
		return fmt.Errorf("message reason unsupported reason: %s", m.Reason)
	}
}
