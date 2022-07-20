package chain

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/types"
	"github.com/stafihub/rtoken-relay-core/common/core"
)

// handle rvalidator added
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
