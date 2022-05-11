package chain

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/types"
	errType "github.com/cosmos/cosmos-sdk/types/errors"
	xBankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	xStakingTypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	hubClient "github.com/stafihub/cosmos-relay-sdk/client"
	"github.com/stafihub/rtoken-relay-core/common/core"
	"github.com/stafihub/rtoken-relay-core/common/log"
	"github.com/stafihub/rtoken-relay-core/common/utils"
	stafiHubXLedgerTypes "github.com/stafihub/stafihub/x/ledger/types"
)

var ErrNoOutPuts = errors.New("outputs length is zero")

func ShotIdToArray(shotId string) ([32]byte, error) {
	shotIdBts, err := hex.DecodeString(shotId)
	if err != nil {
		return [32]byte{}, err
	}
	var shotIdArray [32]byte
	copy(shotIdArray[:], shotIdBts)
	return shotIdArray, nil
}

func GetBondUnBondProposalId(shotId [32]byte, bond, unbond *big.Int, index uint8) []byte {
	proposalId := make([]byte, 65)
	copy(proposalId, shotId[:])

	bondBts := make([]byte, 16)
	bond.FillBytes(bondBts)
	copy(proposalId[32:], bondBts)

	unbondBts := make([]byte, 16)
	unbond.FillBytes(unbondBts)
	copy(proposalId[48:], unbondBts)
	proposalId[64] = index
	return proposalId
}

func ParseBondUnBondProposalId(content []byte) (shotId [32]byte, bond, unbond *big.Int, index uint8, err error) {
	if len(content) != 65 {
		err = errors.New("cont length is not right")
		return
	}
	copy(shotId[:], content[:32])

	bond = new(big.Int).SetBytes(content[32:48])
	unbond = new(big.Int).SetBytes(content[48:64])

	index = content[64]
	return
}

func GetClaimRewardProposalId(shotId [32]byte, height uint64, index uint8) []byte {
	proposalId := make([]byte, 41)
	copy(proposalId, shotId[:])
	binary.BigEndian.PutUint64(proposalId[32:], height)
	proposalId[40] = index
	return proposalId
}

func ParseClaimRewardProposalId(content []byte) (shotId [32]byte, height uint64, index uint8, err error) {
	if len(content) != 41 {
		err = errors.New("cont length is not right")
		return
	}
	copy(shotId[:], content[:32])
	height = binary.BigEndian.Uint64(content[32:])
	index = content[40]
	return
}

func GetTransferProposalId(unSignTxHash [32]byte, index uint8) []byte {
	proposalId := make([]byte, 33)
	copy(proposalId, unSignTxHash[:])
	proposalId[32] = index
	return proposalId
}

func GetValidatorUpdateProposalId(content []byte) []byte {
	hash := utils.BlakeTwo256(content)
	return hash[:]
}

//if bond == unbond return err
//if bond > unbond gen delegate tx
//if bond < unbond gen undelegate tx
func GetBondUnbondUnsignedTx(client *hubClient.Client, bond, unbond *big.Int,
	poolAddr types.AccAddress, height int64) (unSignedTx []byte, err error) {
	//check bond unbond
	if bond.Cmp(unbond) == 0 {
		return nil, errors.New("bond equal to unbond")
	}

	deleRes, err := client.QueryDelegations(poolAddr, height)
	if err != nil {
		return nil, err
	}

	totalDelegateAmount := types.NewInt(0)
	valAddrs := make([]types.ValAddress, 0)
	deleAmount := make(map[string]types.Int)
	//get validators amount>=3
	for _, dele := range deleRes.GetDelegationResponses() {
		//filter old validator,we say validator is old if amount < 3 uatom
		if dele.GetBalance().Amount.LT(types.NewInt(3)) {
			continue
		}

		valAddr, err := types.ValAddressFromBech32(dele.GetDelegation().ValidatorAddress)
		if err != nil {
			return nil, err
		}
		valAddrs = append(valAddrs, valAddr)
		totalDelegateAmount = totalDelegateAmount.Add(dele.GetBalance().Amount)
		deleAmount[valAddr.String()] = dele.GetBalance().Amount
	}

	valAddrsLen := len(valAddrs)
	//check valAddrs length
	if valAddrsLen == 0 {
		return nil, fmt.Errorf("pool no valAddrs")
	}
	//check totalDelegateAmount
	if totalDelegateAmount.LT(types.NewInt(3 * int64(valAddrsLen))) {
		return nil, fmt.Errorf("validators have no reserve value to unbond")
	}

	//bond or unbond to their validators average
	if bond.Cmp(unbond) > 0 {
		val := bond.Sub(bond, unbond)
		val = val.Div(val, big.NewInt(int64(valAddrsLen)))
		unSignedTx, err = client.GenMultiSigRawDelegateTx(
			poolAddr,
			valAddrs,
			types.NewCoin(client.GetDenom(), types.NewIntFromBigInt(val)))
	} else {

		//make val <= totalDelegateAmount-3*len and we revserve 3 uatom
		val := unbond.Sub(unbond, bond)
		willUsetotalDelegateAmount := totalDelegateAmount.Sub(types.NewInt(3 * int64(valAddrsLen)))
		if val.Cmp(willUsetotalDelegateAmount.BigInt()) >= 0 {
			val = willUsetotalDelegateAmount.BigInt()
		}
		willUseTotalVal := types.NewIntFromBigInt(val)

		//sort validators by delegate amount
		sort.Slice(valAddrs, func(i int, j int) bool {
			return deleAmount[valAddrs[i].String()].
				GT(deleAmount[valAddrs[j].String()])
		})

		//choose validators to be undelegated
		choosedVals := make([]types.ValAddress, 0)
		choosedAmount := make(map[string]types.Int)

		selectedAmount := types.NewInt(0)
		for _, validator := range valAddrs {
			nowValMaxUnDeleAmount := deleAmount[validator.String()].Sub(types.NewInt(3))
			if selectedAmount.Add(nowValMaxUnDeleAmount).GTE(willUseTotalVal) {
				willUseChoosedAmount := willUseTotalVal.Sub(selectedAmount)

				choosedVals = append(choosedVals, validator)
				choosedAmount[validator.String()] = willUseChoosedAmount
				selectedAmount = selectedAmount.Add(willUseChoosedAmount)
				break
			}

			choosedVals = append(choosedVals, validator)
			choosedAmount[validator.String()] = nowValMaxUnDeleAmount
			selectedAmount = selectedAmount.Add(nowValMaxUnDeleAmount)
		}

		unSignedTx, err = client.GenMultiSigRawUnDelegateTxV2(
			poolAddr,
			choosedVals,
			choosedAmount)
	}

	return
}

//if bond == unbond return err
//if bond > unbond gen delegate tx
//if bond < unbond gen undelegate tx
func GetBondUnbondUnsignedTxWithTargets(client *hubClient.Client, bond, unbond *big.Int,
	poolAddr types.AccAddress, height int64, targets []types.ValAddress) (unSignedTx []byte, err error) {
	//check bond unbond
	if bond.Cmp(unbond) == 0 {
		return nil, errors.New("bond equal to unbond")
	}
	done := core.UseSdkConfigContext(client.GetAccountPrefix())
	poolAddrStr := poolAddr.String()
	done()

	//bond or unbond to their validators average
	if bond.Cmp(unbond) > 0 {
		valAddrs := targets
		valAddrsLen := len(valAddrs)
		//check valAddrs length
		if valAddrsLen == 0 {
			return nil, fmt.Errorf("no target valAddrs, pool: %s", poolAddrStr)
		}

		val := bond.Sub(bond, unbond)
		val = val.Div(val, big.NewInt(int64(valAddrsLen)))
		return client.GenMultiSigRawDelegateTx(
			poolAddr,
			valAddrs,
			types.NewCoin(client.GetDenom(), types.NewIntFromBigInt(val)))
	} else {
		deleRes, err := client.QueryDelegations(poolAddr, height)
		if err != nil {
			return nil, fmt.Errorf("QueryDelegations failed: %s", err)
		}

		totalDelegateAmount := types.NewInt(0)
		valAddrs := make([]types.ValAddress, 0)
		deleAmount := make(map[string]types.Int)
		//get validators amount>=3
		for _, dele := range deleRes.GetDelegationResponses() {
			//filter old validator,we say validator is old if amount < 3 uatom
			if dele.GetBalance().Amount.LT(types.NewInt(3)) {
				continue
			}

			done := core.UseSdkConfigContext(client.GetAccountPrefix())
			valAddr, err := types.ValAddressFromBech32(dele.GetDelegation().ValidatorAddress)
			if err != nil {
				done()
				return nil, err
			}

			valAddrs = append(valAddrs, valAddr)
			totalDelegateAmount = totalDelegateAmount.Add(dele.GetBalance().Amount)
			deleAmount[valAddr.String()] = dele.GetBalance().Amount
			done()
		}

		valAddrsLen := len(valAddrs)
		//check valAddrs length
		if valAddrsLen == 0 {
			return nil, fmt.Errorf("no valAddrs, pool: %s", poolAddr)
		}
		//check totalDelegateAmount
		if totalDelegateAmount.LT(types.NewInt(3 * int64(valAddrsLen))) {
			return nil, fmt.Errorf("validators have no reserve value to unbond")
		}

		//make val <= totalDelegateAmount-3*len and we reserve 3 uatom
		val := unbond.Sub(unbond, bond)
		willUsetotalDelegateAmount := totalDelegateAmount.Sub(types.NewInt(3 * int64(valAddrsLen)))
		if val.Cmp(willUsetotalDelegateAmount.BigInt()) > 0 {
			return nil, fmt.Errorf("no enough value can be used to unbond, pool: %s", poolAddr)
		}
		willUseTotalVal := types.NewIntFromBigInt(val)

		//remove if unbonding >= 7
		canUseValAddrs := make([]types.ValAddress, 0)
		for _, val := range valAddrs {
			res, err := client.QueryUnbondingDelegation(poolAddr, val, height)
			if err != nil {
				// unbonding empty case
				if strings.Contains(err.Error(), "NotFound") {
					canUseValAddrs = append(canUseValAddrs, val)
					continue
				}
				return nil, err
			}
			if len(res.GetUnbond().Entries) < 7 {
				canUseValAddrs = append(canUseValAddrs, val)
			}
		}
		valAddrs = canUseValAddrs
		if len(valAddrs) == 0 {
			return nil, fmt.Errorf("no valAddrs can be used to unbond, pool: %s", poolAddr)
		}

		//sort validators by delegate amount
		done := core.UseSdkConfigContext(client.GetAccountPrefix())
		sort.Slice(valAddrs, func(i int, j int) bool {
			return deleAmount[valAddrs[i].String()].
				GT(deleAmount[valAddrs[j].String()])
		})

		//choose validators to be undelegated
		choosedVals := make([]types.ValAddress, 0)
		choosedAmount := make(map[string]types.Int)

		selectedAmount := types.NewInt(0)
		enough := false
		for _, validator := range valAddrs {
			nowValMaxUnDeleAmount := deleAmount[validator.String()].Sub(types.NewInt(3))
			//if we find all validators needed
			if selectedAmount.Add(nowValMaxUnDeleAmount).GTE(willUseTotalVal) {
				willUseChoosedAmount := willUseTotalVal.Sub(selectedAmount)

				choosedVals = append(choosedVals, validator)
				choosedAmount[validator.String()] = willUseChoosedAmount
				selectedAmount = selectedAmount.Add(willUseChoosedAmount)

				enough = true
				break
			}

			choosedVals = append(choosedVals, validator)
			choosedAmount[validator.String()] = nowValMaxUnDeleAmount
			selectedAmount = selectedAmount.Add(nowValMaxUnDeleAmount)
		}
		done()

		if !enough {
			return nil, fmt.Errorf("can't find enough valAddrs to unbond, pool: %s", poolAddrStr)
		}

		return client.GenMultiSigRawUnDelegateTxV2(
			poolAddr,
			choosedVals,
			choosedAmount)
	}
}

//if bond == unbond if no delegation before, return errNoMsgs, else gen withdraw multiSig unsigned tx
//if bond > unbond gen delegate tx
//if bond < unbond gen undelegate+withdraw tx
func GetBondUnbondWithdrawUnsignedTxWithTargets(client *hubClient.Client, bond, unbond *big.Int,
	poolAddr types.AccAddress, height int64, targets []types.ValAddress) (unSignedTx []byte, unSignedType int, err error) {

	done := core.UseSdkConfigContext(client.GetAccountPrefix())
	poolAddrStr := poolAddr.String()
	done()

	switch bond.Cmp(unbond) {
	case 0:
		// return errnoMsgs if no delegation before
		var delegationRes *xStakingTypes.QueryDelegatorDelegationsResponse
		delegationRes, err = client.QueryDelegations(poolAddr, height)
		if err != nil {
			if strings.Contains(err.Error(), "unable to find delegations for address") {
				err = hubClient.ErrNoMsgs
				return
			} else {
				return
			}
		}
		if len(delegationRes.DelegationResponses) == 0 {
			err = hubClient.ErrNoMsgs
			return
		}

		unSignedTx, err = client.GenMultiSigRawWithdrawAllRewardTx(
			poolAddr,
			height)
		unSignedType = 0
		return
	case 1:
		valAddrs := targets
		valAddrsLen := len(valAddrs)
		//check valAddrs length
		if valAddrsLen == 0 {
			return nil, 0, fmt.Errorf("no target valAddrs, pool: %s", poolAddrStr)
		}

		val := bond.Sub(bond, unbond)
		val = val.Div(val, big.NewInt(int64(valAddrsLen)))
		unSignedTx, err = client.GenMultiSigRawDelegateTx(
			poolAddr,
			valAddrs,
			types.NewCoin(client.GetDenom(), types.NewIntFromBigInt(val)))
		unSignedType = 1
		return
	case -1:
		var deleRes *xStakingTypes.QueryDelegatorDelegationsResponse
		deleRes, err = client.QueryDelegations(poolAddr, height)
		if err != nil {
			return nil, 0, fmt.Errorf("QueryDelegations failed: %s", err)
		}

		totalDelegateAmount := types.NewInt(0)
		valAddrs := make([]types.ValAddress, 0)
		deleAmount := make(map[string]types.Int)

		//get validators amount>=3
		for _, dele := range deleRes.GetDelegationResponses() {
			//filter old validator,we say validator is old if amount < 3 uatom
			if dele.GetBalance().Amount.LT(types.NewInt(3)) {
				continue
			}

			done := core.UseSdkConfigContext(client.GetAccountPrefix())
			valAddr, err := types.ValAddressFromBech32(dele.GetDelegation().ValidatorAddress)
			if err != nil {
				done()
				return nil, 0, err
			}

			valAddrs = append(valAddrs, valAddr)
			totalDelegateAmount = totalDelegateAmount.Add(dele.GetBalance().Amount)
			deleAmount[valAddr.String()] = dele.GetBalance().Amount
			done()
		}

		//check valAddrs length
		valAddrsLen := len(valAddrs)
		if valAddrsLen == 0 {
			return nil, 0, fmt.Errorf("no valAddrs, pool: %s", poolAddr)
		}

		//check totalDelegateAmount
		if totalDelegateAmount.LT(types.NewInt(3 * int64(valAddrsLen))) {
			return nil, 0, fmt.Errorf("validators have no reserve value to unbond")
		}

		//make val <= totalDelegateAmount-3*len and we reserve 3 uatom
		val := new(big.Int).Sub(unbond, bond)
		willUsetotalDelegateAmount := totalDelegateAmount.Sub(types.NewInt(3 * int64(valAddrsLen)))
		if val.Cmp(willUsetotalDelegateAmount.BigInt()) > 0 {
			return nil, 0, fmt.Errorf("no enough value can be used to unbond, pool: %s", poolAddr)
		}
		willUseTotalVal := types.NewIntFromBigInt(val)

		//remove validator who's unbonding >= 7
		canUseValAddrs := make([]types.ValAddress, 0)
		for _, val := range valAddrs {
			res, err := client.QueryUnbondingDelegation(poolAddr, val, height)
			if err != nil {
				// unbonding empty case
				if strings.Contains(err.Error(), "NotFound") {
					canUseValAddrs = append(canUseValAddrs, val)
					continue
				}
				return nil, 0, err
			}
			if len(res.GetUnbond().Entries) < 7 {
				canUseValAddrs = append(canUseValAddrs, val)
			}
		}
		valAddrs = canUseValAddrs
		if len(valAddrs) == 0 {
			return nil, 0, fmt.Errorf("no valAddrs can be used to unbond, pool: %s", poolAddr)
		}

		//sort validators by delegate amount
		done := core.UseSdkConfigContext(client.GetAccountPrefix())
		sort.Slice(valAddrs, func(i int, j int) bool {
			return deleAmount[valAddrs[i].String()].
				GT(deleAmount[valAddrs[j].String()])
		})

		//choose validators to be undelegated
		choosedVals := make([]types.ValAddress, 0)
		choosedAmount := make(map[string]types.Int)

		selectedAmount := types.NewInt(0)
		enough := false
		for _, validator := range valAddrs {
			nowValMaxUnDeleAmount := deleAmount[validator.String()].Sub(types.NewInt(3))
			//if we find all validators needed
			if selectedAmount.Add(nowValMaxUnDeleAmount).GTE(willUseTotalVal) {
				willUseChoosedAmount := willUseTotalVal.Sub(selectedAmount)

				choosedVals = append(choosedVals, validator)
				choosedAmount[validator.String()] = willUseChoosedAmount
				selectedAmount = selectedAmount.Add(willUseChoosedAmount)

				enough = true
				break
			}

			choosedVals = append(choosedVals, validator)
			choosedAmount[validator.String()] = nowValMaxUnDeleAmount
			selectedAmount = selectedAmount.Add(nowValMaxUnDeleAmount)
		}

		if !enough {
			done()
			return nil, 0, fmt.Errorf("can't find enough valAddrs to unbond, pool: %s", poolAddrStr)
		}

		// filter withdraw validators
		withdrawVals := make([]types.ValAddress, 0)
		for valAddressStr := range deleAmount {
			if _, exist := choosedAmount[valAddressStr]; !exist {
				valAddr, err := types.ValAddressFromBech32(valAddressStr)
				if err != nil {
					done()
					return nil, 0, err
				}
				withdrawVals = append(withdrawVals, valAddr)
			}
		}
		done()

		unSignedTx, err = client.GenMultiSigRawUnDelegateTxV3(
			poolAddr,
			choosedVals,
			choosedAmount,
			withdrawVals)
		unSignedType = -1
		return
	default:
		return nil, 0, fmt.Errorf("unreached case err")
	}
}

//Notice: delegate/undelegate/withdraw operates will withdraw all reward
//if bond > unbond only gen delegate tx  (txType: 2)
//if bond <= unbond
//	(1)if balanceAmount > rewardAmount of era height ,gen delegate tx  (txType: 3)
//	(2)if balanceAmount < rewardAmount of era height, gen withdraw tx  (txTpye: 1)
func GetClaimRewardUnsignedTx(client *hubClient.Client, poolAddr types.AccAddress, height int64,
	bond, unBond *big.Int) ([]byte, int, *types.Int, error) {
	//get reward of height
	rewardRes, err := client.QueryDelegationTotalRewards(poolAddr, height)
	if err != nil {
		return nil, 0, nil, err
	}

	// no delegation just return
	if len(rewardRes.Rewards) == 0 {
		return nil, 0, nil, hubClient.ErrNoMsgs
	}

	rewardAmount := rewardRes.GetTotal().AmountOf(client.GetDenom()).TruncateInt()

	bondCmpUnbond := bond.Cmp(unBond)

	//check when we behind several eras, only bond==unbond we can check this
	// if rewardAmount > rewardAmountNow no need claim and delegate just return ErrNoMsgs
	if bondCmpUnbond == 0 {
		//get reward of now
		rewardResNow, err := client.QueryDelegationTotalRewards(poolAddr, 0)
		if err != nil {
			return nil, 0, nil, err
		}
		rewardAmountNow := rewardResNow.GetTotal().AmountOf(client.GetDenom()).TruncateInt()
		if rewardAmount.GT(rewardAmountNow) {
			return nil, 0, nil, hubClient.ErrNoMsgs
		}
	}

	//get balanceAmount of height
	balanceAmount, err := client.QueryBalance(poolAddr, client.GetDenom(), height)
	if err != nil {
		return nil, 0, nil, err
	}

	txType := 0
	var unSignedTx []byte
	if bondCmpUnbond <= 0 {
		//check balanceAmount and rewardAmount
		//(1)if balanceAmount>rewardAmount gen withdraw and delegate tx
		//(2)if balanceAmount<rewardAmount gen withdraw tx
		if balanceAmount.Balance.Amount.GT(rewardAmount) {
			unSignedTx, err = client.GenMultiSigRawWithdrawAllRewardThenDeleTx(
				poolAddr,
				height)
			txType = 3
		} else {
			unSignedTx, err = client.GenMultiSigRawWithdrawAllRewardTx(
				poolAddr,
				height)
			txType = 1
		}
	} else {
		//check balanceAmount and rewardAmount
		//(1)if balanceAmount>rewardAmount gen delegate tx
		//(2)if balanceAmount<rewardAmount gen withdraw tx
		if balanceAmount.Balance.Amount.GT(rewardAmount) {
			unSignedTx, err = client.GenMultiSigRawDeleRewardTx(
				poolAddr,
				height)
			txType = 2
		} else {
			unSignedTx, err = client.GenMultiSigRawWithdrawAllRewardTx(
				poolAddr,
				height)
			txType = 1
		}
	}

	if err != nil {
		return nil, 0, nil, fmt.Errorf("gen unsignedTx err: %s", err)
	}

	decodedTx, err := client.GetTxConfig().TxJSONDecoder()(unSignedTx)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("GetTxConfig().TxDecoder() failed: %s, unSignedTx: %s", err, string(unSignedTx))
	}
	totalAmountRet := types.NewInt(0)
	for _, msg := range decodedTx.GetMsgs() {
		if m, ok := msg.(*xStakingTypes.MsgDelegate); ok {
			totalAmountRet = totalAmountRet.Add(m.Amount.Amount)
		}
	}

	return unSignedTx, txType, &totalAmountRet, nil
}

//Notice: delegate/undelegate/withdraw operates will withdraw all reward
//all delegations had withdraw all reward in eraUpdatedEvent handler
//(0)if rewardAmount of  height == 0, hubClient.ErrNoMsgs
//(1)else gen delegate tx
func GetDelegateRewardUnsignedTx(client *hubClient.Client, poolAddr types.AccAddress, height int64) ([]byte, *types.Int, error) {
	//get reward of height
	rewardResOnHeight, err := client.QueryDelegationTotalRewards(poolAddr, height)
	if err != nil {
		return nil, nil, err
	}
	// no delegation just return
	if len(rewardResOnHeight.Rewards) == 0 {
		return nil, nil, hubClient.ErrNoMsgs
	}

	unSignedTx, err := client.GenMultiSigRawDeleRewardTx(
		poolAddr,
		height)
	if err != nil {
		return nil, nil, err
	}

	decodedTx, err := client.GetTxConfig().TxJSONDecoder()(unSignedTx)
	if err != nil {
		return nil, nil, fmt.Errorf("GetTxConfig().TxDecoder() failed: %s, unSignedTx: %s", err, string(unSignedTx))
	}
	totalAmountRet := types.NewInt(0)
	for _, msg := range decodedTx.GetMsgs() {
		if m, ok := msg.(*xStakingTypes.MsgDelegate); ok {
			totalAmountRet = totalAmountRet.Add(m.Amount.Amount)
		}
	}
	return unSignedTx, &totalAmountRet, nil
}

func GetTransferUnsignedTx(client *hubClient.Client, poolAddr types.AccAddress, receives []*stafiHubXLedgerTypes.Unbonding,
	logger log.Logger) ([]byte, []xBankTypes.Output, error) {

	outPuts := make([]xBankTypes.Output, 0)
	done := core.UseSdkConfigContext(client.GetAccountPrefix())
	for _, receive := range receives {
		addr, err := types.AccAddressFromBech32(receive.Recipient)
		if err != nil {
			logger.Error("GetTransferUnsignedTx AccAddressFromHex failed", "Account", receive.Recipient, "err", err)
			continue
		}
		out := xBankTypes.Output{
			Address: addr.String(),
			Coins:   types.NewCoins(types.NewCoin(client.GetDenom(), receive.Amount)),
		}
		outPuts = append(outPuts, out)
	}
	done()

	//len should not be 0
	if len(outPuts) == 0 {
		return nil, nil, ErrNoOutPuts
	}

	//sort outPuts for the same rawTx from different relayer
	sort.SliceStable(outPuts, func(i, j int) bool {
		return bytes.Compare([]byte(outPuts[i].Address), []byte(outPuts[j].Address)) < 0
	})

	txBts, err := client.GenMultiSigRawBatchTransferTx(poolAddr, outPuts)
	if err != nil {
		return nil, nil, ErrNoOutPuts
	}
	return txBts, outPuts, nil
}

func (h *Handler) checkAndSend(poolClient *hubClient.Client, wrappedUnSignedTx *WrapUnsignedTx,
	m *core.Message, txHash, txBts []byte, poolAddress types.AccAddress) error {

	txHashHexStr := hex.EncodeToString(txHash)
	done := core.UseSdkConfigContext(poolClient.GetAccountPrefix())
	poolAddressStr := poolAddress.String()
	done()

	retry := BlockRetryLimit
	var err error
	for {
		if retry <= 0 {
			h.log.Error("checkAndSend broadcast tx reach retry limit",
				"pool address", poolAddressStr,
				"txHash", txHashHexStr,
				"err", err)
			return fmt.Errorf("checkAndSend broadcast tx reach retry limit pool address: %s,err: %s", poolAddressStr, err)
		}
		//check on chain
		var res *types.TxResponse
		res, err = poolClient.QueryTxByHash(txHashHexStr)
		if err != nil || res.Empty() || res.Code != 0 {
			h.log.Debug(fmt.Sprintf(
				"checkAndSend QueryTxByHash failed. will rebroadcast after %f second",
				BlockRetryInterval.Seconds()),
				"tx hash", txHashHexStr,
				"err or res.empty", err)

			//broadcast if not on chain
			_, err = poolClient.BroadcastTx(txBts)
			if err != nil && err != errType.ErrTxInMempoolCache {
				h.log.Debug("checkAndSend BroadcastTx failed  will retry", "err", err)
			}
			time.Sleep(BlockRetryInterval)
			retry--
			continue
		}

		h.log.Info("checkAndSend success",
			"pool address", poolAddressStr,
			"tx type", wrappedUnSignedTx.Type,
			"era", wrappedUnSignedTx.Era,
			"txHash", txHashHexStr)
		//report to stafihub
		switch wrappedUnSignedTx.Type {
		case stafiHubXLedgerTypes.TxTypeBond: //bond or unbond
			return h.sendBondReportMsg(wrappedUnSignedTx.SnapshotId)
		case stafiHubXLedgerTypes.TxTypeClaim: //claim and redelegate
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
			return h.sendActiveReportMsg(wrappedUnSignedTx.SnapshotId, total.BigInt())
		case stafiHubXLedgerTypes.TxTypeTransfer: //transfer unbond token to user
			return h.sendTransferReportMsg(wrappedUnSignedTx.SnapshotId)
		default:
			h.log.Warn("checkAndSend failed,unknown unsigned tx type",
				"pool", poolAddressStr,
				"type", wrappedUnSignedTx.Type)
			return nil
		}
	}
}

func (h *Handler) sendBondReportMsg(shotId string) error {
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

	h.log.Info("sendBondReportMsg", "msg", m)
	return h.router.Send(&m)
}

func (h *Handler) sendActiveReportMsg(shotId string, staked *big.Int) error {
	m := core.Message{
		Source:      h.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonActiveReport,
		Content: core.ProposalActiveReport{
			Denom:    string(h.conn.symbol),
			ShotId:   shotId,
			Staked:   types.NewIntFromBigInt(staked),
			Unstaked: types.NewInt(0),
		},
	}

	return h.router.Send(&m)
}

func (h *Handler) sendTransferReportMsg(shotId string) error {
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

func (h *Handler) sendSubmitSignatureMsg(submitSignature *core.ParamSubmitSignature) error {
	m := core.Message{
		Source:      h.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonSubmitSignature,
		Content:     *submitSignature,
	}
	return h.router.Send(&m)
}

func bytesArrayToStr(bts [][]byte) string {
	ret := ""
	for _, b := range bts {
		ret += " | "
		ret += hex.EncodeToString(b)
	}
	return ret
}

func (h *Handler) mustGetSignatureFromStafiHub(param *core.ParamSubmitSignature, threshold uint32) (signatures [][]byte, err error) {
	retry := 0
	for {
		if retry > BlockRetryLimit {
			return nil, fmt.Errorf("getSignatureFromStafiHub reach retry limit, param: %v", param)
		}
		sigs, err := h.getSignatureFromStafiHub(param)
		if err != nil {
			retry++
			h.log.Debug("getSignatureFromStafiHub failed, will retry.", "err", err)
			time.Sleep(BlockRetryInterval)
			continue
		}
		if len(sigs) < int(threshold) {
			retry++
			h.log.Debug("getSignatureFromStafiHub sigs not enough, will retry.", "sigs len", len(sigs), "threshold", threshold)
			time.Sleep(BlockRetryInterval)
			continue
		}
		return sigs, nil
	}
}

func (h *Handler) getSignatureFromStafiHub(param *core.ParamSubmitSignature) (signatures [][]byte, err error) {
	getSignatures := core.ParamGetSignatures{
		Denom:  param.Denom,
		Era:    param.Era,
		Pool:   param.Pool,
		TxType: param.TxType,
		PropId: param.PropId,
		Sigs:   make(chan []string, 1),
	}
	msg := core.Message{
		Source:      h.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonGetSignatures,
		Content:     getSignatures,
	}
	err = h.router.Send(&msg)
	if err != nil {
		return nil, err
	}

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	h.log.Debug("wait getSignature from stafihub", "rSymbol", h.conn.symbol)
	select {
	case <-timer.C:
		return nil, fmt.Errorf("get signatures from stafihub timeout")
	case sigs := <-getSignatures.Sigs:
		for _, sig := range sigs {
			sigBts, err := hex.DecodeString(sig)
			if err != nil {
				return nil, fmt.Errorf("sig hex.DecodeString failed, err: %s, sig: %s", err, sig)
			}
			signatures = append(signatures, sigBts)
		}
		return signatures, nil
	}
}
