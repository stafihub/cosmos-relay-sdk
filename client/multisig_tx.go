package client

import (
	"errors"
	"fmt"

	tendermintTypes "github.com/cometbft/cometbft/types"
	clientTx "github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	kMultiSig "github.com/cosmos/cosmos-sdk/crypto/keys/multisig"
	"github.com/cosmos/cosmos-sdk/crypto/types/multisig"
	"github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	xAuthClient "github.com/cosmos/cosmos-sdk/x/auth/client"
	xAuthSigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	xBankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	xDistriTypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	xStakingTypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/spf13/cobra"
	"github.com/stafihub/rtoken-relay-core/common/core"
)

var ErrNoMsgs = errors.New("no tx msgs")

// c.clientCtx.FromAddress must be multi sig address
func (c *Client) GenMultiSigRawTransferTx(toAddr types.AccAddress, amount types.Coins) ([]byte, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	msg := xBankTypes.NewMsgSend(c.Ctx().GetFromAddress(), toAddr, amount)
	return c.GenMultiSigRawTx(msg)
}

// only support one type coin
func (c *Client) GenMultiSigRawBatchTransferTx(poolAddr types.AccAddress, outs []xBankTypes.Output) ([]byte, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	totalAmount := types.NewInt(0)
	for _, out := range outs {
		for _, coin := range out.Coins {
			totalAmount = totalAmount.Add(coin.Amount)
		}
	}
	input := xBankTypes.Input{
		Address: poolAddr.String(),
		Coins:   types.NewCoins(types.NewCoin(c.denom, totalAmount))}

	msg := xBankTypes.NewMsgMultiSend([]xBankTypes.Input{input}, outs)
	return c.GenMultiSigRawTx(msg)
}

// only support one type coin
func (c *Client) GenMultiSigRawBatchTransferTxWithMemo(poolAddr types.AccAddress, outs []xBankTypes.Output, memo string) ([]byte, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	totalAmount := types.NewInt(0)
	for _, out := range outs {
		for _, coin := range out.Coins {
			totalAmount = totalAmount.Add(coin.Amount)
		}
	}
	input := xBankTypes.Input{
		Address: poolAddr.String(),
		Coins:   types.NewCoins(types.NewCoin(c.denom, totalAmount))}

	msg := xBankTypes.NewMsgMultiSend([]xBankTypes.Input{input}, outs)
	return c.GenMultiSigRawTxWithMemo(memo, msg)
}

// only support one type coin
func (c *Client) GenBatchTransferMsg(poolAddr types.AccAddress, outs []xBankTypes.Output) (types.Msg, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	totalAmount := types.NewInt(0)
	for _, out := range outs {
		for _, coin := range out.Coins {
			totalAmount = totalAmount.Add(coin.Amount)
		}
	}
	input := xBankTypes.Input{
		Address: poolAddr.String(),
		Coins:   types.NewCoins(types.NewCoin(c.denom, totalAmount))}

	msg := xBankTypes.NewMsgMultiSend([]xBankTypes.Input{input}, outs)
	return msg, nil
}

// generate unsigned delegate tx
func (c *Client) GenMultiSigRawDelegateTx(delAddr types.AccAddress, valAddrs []types.ValAddress, amount types.Coin) ([]byte, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	if len(valAddrs) == 0 {
		return nil, errors.New("no valAddrs")
	}
	if amount.IsZero() {
		return nil, errors.New("amount is zero")
	}

	msgs := make([]types.Msg, 0)
	for _, valAddr := range valAddrs {
		msg := xStakingTypes.NewMsgDelegate(delAddr, valAddr, amount)
		msgs = append(msgs, msg)
	}

	return c.GenMultiSigRawTx(msgs...)
}

// generate unsigned delegate tx
func (c *Client) GenMultiSigRawDelegateTxWithMemo(delAddr types.AccAddress, valAddrs []types.ValAddress, amount types.Coin, memo string) ([]byte, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	if len(valAddrs) == 0 {
		return nil, errors.New("no valAddrs")
	}
	if amount.IsZero() {
		return nil, errors.New("amount is zero")
	}

	msgs := make([]types.Msg, 0)
	for _, valAddr := range valAddrs {
		msg := xStakingTypes.NewMsgDelegate(delAddr, valAddr, amount)
		msgs = append(msgs, msg)
	}

	return c.GenMultiSigRawTxWithMemo(memo, msgs...)
}

// generate unsigned delegate tx
func (c *Client) GenDelegateMsgs(delAddr types.AccAddress, valAddrs []types.ValAddress, totalAmount types.Coin) ([]types.Msg, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()
	valAddrsLen := len(valAddrs)
	if valAddrsLen == 0 {
		return nil, errors.New("no valAddrs")
	}
	if totalAmount.IsZero() {
		return nil, errors.New("amount is zero")
	}
	averageUseAmount := totalAmount.Amount.Quo(types.NewInt(int64(valAddrsLen)))

	msgs := make([]types.Msg, 0)
	for i, valAddr := range valAddrs {
		willUseAmount := averageUseAmount

		if valAddrsLen > 1 && i == valAddrsLen-1 {
			willUseAmount = totalAmount.Amount.Sub(averageUseAmount.Mul(types.NewInt(int64(i))))
		}

		msg := xStakingTypes.NewMsgDelegate(delAddr, valAddr, types.NewCoin(totalAmount.Denom, willUseAmount))
		msgs = append(msgs, msg)
	}

	return msgs, nil
}

// generate unsigned unDelegate tx
func (c *Client) GenMultiSigRawUnDelegateTx(delAddr types.AccAddress, valAddrs []types.ValAddress,
	amounts map[string]types.Int) ([]byte, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	if len(valAddrs) == 0 {
		return nil, errors.New("no valAddrs")
	}
	msgs := make([]types.Msg, 0)
	for _, valAddr := range valAddrs {
		amount := types.NewCoin(c.GetDenom(), amounts[valAddr.String()])
		if amount.IsZero() {
			return nil, errors.New("amount is zero")
		}
		msg := xStakingTypes.NewMsgUndelegate(delAddr, valAddr, amount)
		msgs = append(msgs, msg)
	}
	return c.GenMultiSigRawTx(msgs...)
}

// generate unsigned unDelegate+withdraw tx
func (c *Client) GenMultiSigRawUnDelegateWithdrawTxWithMemo(delAddr types.AccAddress, valAddrs []types.ValAddress,
	amounts map[string]types.Int, withdrawValAddress []types.ValAddress, memo string) ([]byte, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	if len(valAddrs) == 0 {
		return nil, errors.New("no valAddrs")
	}
	msgs := make([]types.Msg, 0)

	//gen undelegate
	for _, valAddr := range valAddrs {
		amount := types.NewCoin(c.GetDenom(), amounts[valAddr.String()])
		if amount.IsZero() {
			return nil, errors.New("amount is zero")
		}
		msg := xStakingTypes.NewMsgUndelegate(delAddr, valAddr, amount)
		msgs = append(msgs, msg)
	}

	//gen withdraw
	for _, valAddr := range withdrawValAddress {
		msg := xDistriTypes.NewMsgWithdrawDelegatorReward(delAddr, valAddr)
		if err := msg.ValidateBasic(); err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}

	return c.GenMultiSigRawTxWithMemo(memo, msgs...)
}

// generate unsigned unDelegate+withdraw tx
func (c *Client) GenUnDelegateWithdrawMsgs(delAddr types.AccAddress, valAddrs []types.ValAddress,
	amounts map[string]types.Int, withdrawValAddress []types.ValAddress) ([]types.Msg, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	if len(valAddrs) == 0 {
		return nil, errors.New("no valAddrs")
	}
	msgs := make([]types.Msg, 0)

	//gen undelegate
	for _, valAddr := range valAddrs {
		amount := types.NewCoin(c.GetDenom(), amounts[valAddr.String()])
		if amount.IsZero() {
			return nil, errors.New("amount is zero")
		}
		msg := xStakingTypes.NewMsgUndelegate(delAddr, valAddr, amount)
		msgs = append(msgs, msg)
	}

	//gen withdraw
	for _, valAddr := range withdrawValAddress {
		msg := xDistriTypes.NewMsgWithdrawDelegatorReward(delAddr, valAddr)
		if err := msg.ValidateBasic(); err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}

	return msgs, nil
}

// generate unsigned reDelegate tx
func (c *Client) GenMultiSigRawReDelegateTxWithMemo(delAddr types.AccAddress, valSrcAddr, valDstAddr types.ValAddress, amount types.Coin, memo string) ([]byte, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	msg := xStakingTypes.NewMsgBeginRedelegate(delAddr, valSrcAddr, valDstAddr, amount)
	return c.GenMultiSigRawTxWithMemo(memo, msg)
}

// generate unsigned reDelegate tx
func (c *Client) GenReDelegateMsgs(delAddr types.AccAddress, valSrcAddr, valDstAddr types.ValAddress, amount types.Coin) ([]types.Msg, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	msg := xStakingTypes.NewMsgBeginRedelegate(delAddr, valSrcAddr, valDstAddr, amount)
	return []types.Msg{msg}, nil
}

// generate unsigned reDelegate tx
func (c *Client) GenMultiSigRawReDelegateTxWithMultiSrc(delAddr types.AccAddress, valSrcAddrs map[string]types.Coin, valDstAddr types.ValAddress) ([]byte, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	msgs := make([]types.Msg, 0)
	for addrStr, amount := range valSrcAddrs {
		valAddress, err := types.ValAddressFromBech32(addrStr)
		if err != nil {
			return nil, err
		}
		msg := xStakingTypes.NewMsgBeginRedelegate(delAddr, valAddress, valDstAddr, amount)
		msgs = append(msgs, msg)
	}
	return c.GenMultiSigRawTx(msgs...)
}

// generate unsigned withdraw delegate reward tx
func (c *Client) GenMultiSigRawWithdrawDelegatorRewardTx(delAddr types.AccAddress, valAddr types.ValAddress) ([]byte, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	msg := xDistriTypes.NewMsgWithdrawDelegatorReward(delAddr, valAddr)
	return c.GenMultiSigRawTx(msg)
}

// generate unsigned withdraw all reward tx
func (c *Client) GenMultiSigRawWithdrawAllRewardTxWithMemo(delAddr types.AccAddress, height int64, memo string) ([]byte, error) {
	delValsRes, err := c.QueryDelegations(delAddr, height)
	if err != nil {
		return nil, err
	}
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	delegations := delValsRes.GetDelegationResponses()
	// build multi-message transaction
	msgs := make([]types.Msg, 0)
	for _, delegation := range delegations {
		valAddr := delegation.Delegation.ValidatorAddress
		val, err := types.ValAddressFromBech32(valAddr)
		if err != nil {
			return nil, err
		}
		//skip zero amount
		if delegation.Balance.Amount.IsZero() {
			continue
		}
		//gen withdraw
		msg := xDistriTypes.NewMsgWithdrawDelegatorReward(delAddr, val)
		if err := msg.ValidateBasic(); err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)

	}
	return c.GenMultiSigRawTxWithMemo(memo, msgs...)
}

// generate unsigned withdraw all reward tx
func (c *Client) GenWithdrawAllRewardMsgs(delAddr types.AccAddress, height int64) ([]types.Msg, error) {
	delValsRes, err := c.QueryDelegations(delAddr, height)
	if err != nil {
		return nil, err
	}
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	delegations := delValsRes.GetDelegationResponses()
	// build multi-message transaction
	msgs := make([]types.Msg, 0)
	for _, delegation := range delegations {
		valAddr := delegation.Delegation.ValidatorAddress
		val, err := types.ValAddressFromBech32(valAddr)
		if err != nil {
			return nil, err
		}
		//skip zero amount
		if delegation.Balance.Amount.IsZero() {
			continue
		}
		//gen withdraw
		msg := xDistriTypes.NewMsgWithdrawDelegatorReward(delAddr, val)
		if err := msg.ValidateBasic(); err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)

	}
	return msgs, nil
}

// generate unsigned delegate reward tx
func (c *Client) GenMultiSigRawDeleRewardTxWithRewardsWithMemo(delAddr types.AccAddress, height int64, rewards map[string]types.Coin, memo string) ([]byte, error) {
	delValsRes, err := c.QueryDelegations(delAddr, height)
	if err != nil {
		return nil, err
	}

	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	// build multi-message transaction
	msgs := make([]types.Msg, 0)
	for _, delegation := range delValsRes.GetDelegationResponses() {
		valAddr := delegation.Delegation.ValidatorAddress

		//must filter zero value or tx will failure
		if reward, exist := rewards[valAddr]; !exist || reward.IsZero() {
			continue
		}
		//skip zero amount
		if delegation.Balance.Amount.IsZero() {
			continue
		}

		val, err := types.ValAddressFromBech32(valAddr)
		if err != nil {
			return nil, err
		}

		//gen delegate
		msg2 := xStakingTypes.NewMsgDelegate(delAddr, val, rewards[valAddr])
		if err := msg2.ValidateBasic(); err != nil {
			return nil, err
		}

		msgs = append(msgs, msg2)
	}

	if len(msgs) == 0 {
		return nil, ErrNoMsgs
	}

	return c.GenMultiSigRawTxWithMemo(memo, msgs...)
}

// c.clientCtx.FromAddress must be multi sig address,no need sequence
func (c *Client) GenMultiSigRawTx(msgs ...types.Msg) ([]byte, error) {
	cmd := cobra.Command{}
	txf, err := clientTx.NewFactoryCLI(c.Ctx(), cmd.Flags())
	if err != nil {
		return nil, err
	}
	txf = txf.WithAccountNumber(c.accountNumber).
		WithSignMode(signing.SignMode_SIGN_MODE_LEGACY_AMINO_JSON). //multi sig need this mod
		WithGasAdjustment(1.5).
		WithGasPrices(c.gasPrice).
		WithGas(1600000).
		WithSimulateAndExecute(true)

	txBuilderRaw, err := txf.BuildUnsignedTx(msgs...)
	if err != nil {
		return nil, err
	}
	return c.Ctx().TxConfig.TxJSONEncoder()(txBuilderRaw.GetTx())
}

// c.clientCtx.FromAddress must be multi sig address,no need sequence
func (c *Client) GenMultiSigRawTxWithMemo(memo string, msgs ...types.Msg) ([]byte, error) {
	cmd := cobra.Command{}
	txf, err := clientTx.NewFactoryCLI(c.Ctx(), cmd.Flags())
	if err != nil {
		return nil, err
	}
	txf = txf.WithAccountNumber(c.accountNumber).
		WithSignMode(signing.SignMode_SIGN_MODE_LEGACY_AMINO_JSON). //multi sig need this mod
		WithGasAdjustment(1.5).
		WithGasPrices(c.gasPrice).
		WithGas(1600000).
		WithSimulateAndExecute(true)

	txBuilderRaw, err := txf.BuildUnsignedTx(msgs...)
	if err != nil {
		return nil, err
	}
	txBuilderRaw.SetMemo(memo)
	return c.Ctx().TxConfig.TxJSONEncoder()(txBuilderRaw.GetTx())
}

// c.clientCtx.FromAddress  must be multi sig address
func (c *Client) SignMultiSigRawTxWithSeq(sequence uint64, rawTx []byte, fromSubKey string) (signature []byte, err error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	cmd := cobra.Command{}
	txf, err := clientTx.NewFactoryCLI(c.Ctx(), cmd.Flags())
	if err != nil {
		return nil, err
	}
	txf = txf.WithSequence(sequence).
		WithAccountNumber(c.accountNumber).
		WithSignMode(signing.SignMode_SIGN_MODE_LEGACY_AMINO_JSON) //multi sig need this mod

	tx, err := c.Ctx().TxConfig.TxJSONDecoder()(rawTx)
	if err != nil {
		return nil, err
	}
	txBuilder, err := c.Ctx().TxConfig.WrapTxBuilder(tx)
	if err != nil {
		return nil, err
	}
	err = xAuthClient.SignTxWithSignerAddress(txf, c.Ctx(), c.Ctx().GetFromAddress(), fromSubKey, txBuilder, true, true)
	if err != nil {
		return nil, err
	}
	return marshalSignatureJSON(c.Ctx().TxConfig, txBuilder, true)
}

// assemble multiSig tx bytes for broadcast
func (c *Client) AssembleMultiSigTx(rawTx []byte, signatures [][]byte, threshold uint32) (txHash, txBts []byte, err error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()
	multisigInfo, err := c.Ctx().Keyring.Key(c.Ctx().FromName)
	if err != nil {
		return
	}
	if multisigInfo.GetType() != keyring.TypeMulti {
		return nil, nil, fmt.Errorf("%q must be of type %s: %s",
			c.Ctx().FromName, keyring.TypeMulti, multisigInfo.GetType())
	}

	multisigPubkey, err := multisigInfo.GetPubKey()
	if err != nil {
		return
	}
	multiSigPub := multisigPubkey.(*kMultiSig.LegacyAminoPubKey)

	tx, err := c.Ctx().TxConfig.TxJSONDecoder()(rawTx)
	if err != nil {
		return nil, nil, err
	}
	txBuilder, err := c.Ctx().TxConfig.WrapTxBuilder(tx)
	if err != nil {
		return nil, nil, err
	}

	willUseSigs := make([]signing.SignatureV2, 0)
	for _, s := range signatures {
		ss, err := c.Ctx().TxConfig.UnmarshalSignatureJSON(s)
		if err != nil {
			return nil, nil, err
		}
		willUseSigs = append(willUseSigs, ss...)
	}

	multiSigData := multisig.NewMultisig(len(multiSigPub.PubKeys))
	var useSequence uint64

	errStr := ""
	correntSigNumber := uint32(0)
	for i, sig := range willUseSigs {
		if correntSigNumber == threshold {
			break
		}
		//check sequence, we use first sig's sequence as flag
		if i == 0 {
			useSequence = sig.Sequence
		} else {
			if useSequence != sig.Sequence {
				c.logger.Warn("useSequence != sig.Sequence", "useSequence", useSequence, "sig.Sequence", sig.Sequence)
				continue
			}
		}
		//check sig
		signingData := xAuthSigning.SignerData{
			Address:       c.Ctx().FromAddress.String(),
			ChainID:       c.Ctx().ChainID,
			AccountNumber: c.accountNumber,
			Sequence:      useSequence,
			PubKey:        sig.PubKey,
		}

		err = xAuthSigning.VerifySignature(sig.PubKey, signingData, sig.Data, c.Ctx().TxConfig.SignModeHandler(), txBuilder.GetTx())
		if err != nil {
			errStr += fmt.Sprintf("xAuthSigning.VerifySignature err: %s", err.Error())
			continue
		}

		if err := multisig.AddSignatureV2(multiSigData, sig, multiSigPub.GetPubKeys()); err != nil {
			errStr += fmt.Sprintf("multisig.AddSignatureV2 err: %s", err.Error())
			continue
		}
		correntSigNumber++
	}

	if correntSigNumber != threshold {
		return nil, nil, fmt.Errorf("correct sig number:%d  threshold %d, err: %s", correntSigNumber, threshold, errStr)
	}

	sigV2 := signing.SignatureV2{
		PubKey:   multiSigPub,
		Data:     multiSigData,
		Sequence: useSequence,
	}

	err = txBuilder.SetSignatures(sigV2)
	if err != nil {
		return nil, nil, err
	}
	txBytes, err := c.Ctx().TxConfig.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return nil, nil, err
	}
	tendermintTx := tendermintTypes.Tx(txBytes)
	return tendermintTx.Hash(), tendermintTx, nil
}

func (c *Client) VerifyMultiSigTxForTest(rawTx []byte, signatures [][]byte, threshold uint32, accountNumber uint64) (err error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()
	// multisigInfo, err := c.Ctx().Keyring.Key(c.Ctx().FromName)
	// if err != nil {
	// 	return
	// }
	// if multisigInfo.GetType() != keyring.TypeMulti {
	// 	return nil, nil, fmt.Errorf("%q must be of type %s: %s",
	// 		c.Ctx().FromName, keyring.TypeMulti, multisigInfo.GetType())
	// }

	// pubkey, err := multisigInfo.GetPubKey()
	// if err != nil {
	// 	return nil, nil, err
	// }
	// multiSigPub := pubkey.(*kMultiSig.LegacyAminoPubKey)

	tx, err := c.Ctx().TxConfig.TxJSONDecoder()(rawTx)
	if err != nil {
		return err
	}
	txBuilder, err := c.Ctx().TxConfig.WrapTxBuilder(tx)
	if err != nil {
		return err
	}

	willUseSigs := make([]signing.SignatureV2, 0)
	for _, s := range signatures {
		ss, err := c.Ctx().TxConfig.UnmarshalSignatureJSON(s)
		if err != nil {
			return err
		}
		willUseSigs = append(willUseSigs, ss...)
	}

	// multiSigData := multisig.NewMultisig(len(multiSigPub.PubKeys))
	var useSequence uint64

	correntSigNumber := uint32(0)
	for i, sig := range willUseSigs {
		if correntSigNumber == threshold {
			break
		}
		//check sequence, we use first sig's sequence as flag
		if i == 0 {
			useSequence = sig.Sequence
		} else {
			if useSequence != sig.Sequence {
				continue
			}
		}
		//check sig
		signingData := xAuthSigning.SignerData{
			ChainID:       c.Ctx().ChainID,
			AccountNumber: accountNumber,
			Sequence:      useSequence,
		}

		err = xAuthSigning.VerifySignature(sig.PubKey, signingData, sig.Data, c.Ctx().TxConfig.SignModeHandler(), txBuilder.GetTx())
		if err != nil {
			return fmt.Errorf("xAuthSigning.VerifySignature %s", err.Error())
		}

		// if err := multisig.AddSignatureV2(multiSigData, sig, multiSigPub.GetPubKeys()); err != nil {
		// 	continue
		// }
		correntSigNumber++
	}

	if correntSigNumber != threshold {
		return fmt.Errorf("correct sig number:%d  threshold %d", correntSigNumber, threshold)
	}
	return nil

	// sigV2 := signing.SignatureV2{
	// 	PubKey:   multiSigPub,
	// 	Data:     multiSigData,
	// 	Sequence: useSequence,
	// }

	// err = txBuilder.SetSignatures(sigV2)
	// if err != nil {
	// 	return nil, nil, err
	// }
	// txBytes, err := c.Ctx().TxConfig.TxEncoder()(txBuilder.GetTx())
	// if err != nil {
	// 	return nil, nil, err
	// }
	// tendermintTx := tendermintTypes.Tx(txBytes)
	// return tendermintTx.Hash(), tendermintTx, nil
}
