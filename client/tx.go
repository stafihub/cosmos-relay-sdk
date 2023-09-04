package client

import (
	"fmt"

	clientTx "github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	xAuthClient "github.com/cosmos/cosmos-sdk/x/auth/client"
	xBankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/spf13/cobra"
	"github.com/stafihub/rtoken-relay-core/common/core"
)

func (c *Client) SingleTransferTo(toAddr types.AccAddress, amount types.Coins) error {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	msg := xBankTypes.NewMsgSend(c.Ctx().GetFromAddress(), toAddr, amount)
	cmd := cobra.Command{}
	return clientTx.GenerateOrBroadcastTxCLI(c.Ctx(), cmd.Flags(), msg)
}

func (c *Client) BroadcastTx(tx []byte) (string, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	cc, err := c.retry(func() (interface{}, error) {
		return c.Ctx().BroadcastTx(tx)
	})
	if err != nil {
		return "", fmt.Errorf("retry broadcastTx err: %s", err)
	}
	res := cc.(*types.TxResponse)
	if res.Code != 0 {
		return res.TxHash, fmt.Errorf("broadcast err with res.code: %d, res.Codespace: %s", res.Code, res.Codespace)
	}
	return res.TxHash, nil
}

func (c *Client) ConstructAndSignTx(msgs ...types.Msg) ([]byte, error) {
	account, err := c.GetAccount()
	if err != nil {
		return nil, err
	}
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	clientCtx := c.Ctx()
	cmd := cobra.Command{}
	txf := clientTx.NewFactoryCLI(clientCtx, cmd.Flags())
	txf = txf.WithSequence(account.GetSequence()).
		WithAccountNumber(account.GetAccountNumber()).
		WithSignMode(signing.SignMode_SIGN_MODE_DIRECT). //multi sig need this mod
		WithGasAdjustment(1.5).
		WithGas(0).
		WithGasPrices(c.gasPrice).
		WithSimulateAndExecute(true)

	// auto cal gas with retry
	adjusted, err := c.CalculateGas(txf, msgs...)
	if err != nil {
		return nil, err
	}
	txf = txf.WithGas(adjusted)

	txBuilderRaw, err := txf.BuildUnsignedTx(msgs...)
	if err != nil {
		return nil, err
	}

	err = xAuthClient.SignTx(txf, c.Ctx(), clientCtx.GetFromName(), txBuilderRaw, true, true)
	if err != nil {
		return nil, err
	}

	txBytes, err := clientCtx.TxConfig.TxEncoder()(txBuilderRaw.GetTx())
	if err != nil {
		return nil, err
	}
	return txBytes, nil
}

func (c *Client) GetTxMemo(txBts []byte) (string, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	var memoInTx string
	tx, err := c.GetTxConfig().TxDecoder()(txBts)
	if err != nil {
		return "", err
	}
	memoTx, ok := tx.(types.TxWithMemo)
	if !ok {
		return "", fmt.Errorf("tx is not type TxWithMemo")
	}

	memoInTx = memoTx.GetMemo()
	return memoInTx, nil
}

func (c *Client) CalculateGas(txf clientTx.Factory, msgs ...types.Msg) (uint64, error) {
	cc, err := c.retry(func() (interface{}, error) {
		_, adjustGas, err := clientTx.CalculateGas(c.Ctx(), txf, msgs...)
		return adjustGas, err
	})
	if err != nil {
		return 0, err
	}

	return cc.(uint64), err
}
