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
	xAuthTypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	xBankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	xDistriTypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	xStakingTypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	icatypes "github.com/cosmos/ibc-go/v7/modules/apps/27-interchain-accounts/types"
	hubClient "github.com/stafihub/cosmos-relay-sdk/client"
	"github.com/stafihub/rtoken-relay-core/common/core"
	"github.com/stafihub/rtoken-relay-core/common/log"
	"github.com/stafihub/rtoken-relay-core/common/utils"
	stafiHubXLedgerTypes "github.com/stafihub/stafihub/x/ledger/types"
	stafiHubXRValidatorTypes "github.com/stafihub/stafihub/x/rvalidator/types"
)

const (
	UnSignedTxTypeUnSpecified             = 0
	UnSignedTxTypeBondEqualUnbondWithdraw = 1
	UnSignedTxTypeDelegate                = 2
	UnSignedTxTypeUnDelegateAndWithdraw   = 3
	UnSignedTxTypeSkipAndWithdraw         = 4
)

var (
	ErrNoOutPuts            = errors.New("outputs length is zero")
	ErrNoRewardNeedDelegate = fmt.Errorf("no tx reward need delegate")
	ErrNotFound             = errors.New("NotFound")
	ErrUnknownQueryPath     = errors.New("UnknownQueryPath")
)

var (
	TxTypeHandleEraPoolUpdatedEvent    = "handleEraPoolUpdatedEvent"
	TxTypeHandleBondReportedEvent      = "handleBondReportedEvent"
	TxTypeHandleActiveReportedEvent    = "handleActiveReportedEvent"
	TxTypeHandleRValidatorUpdatedEvent = "handleRValidatorUpdatedEvent"
)

var ValidatorBondCapDisabled = types.NewDecFromInt(types.NewInt(-1))

func GetMemo(era uint32, txType string) string {
	return fmt.Sprintf("%d:%s", era, txType)
}

func GetValidatorUpdatedMemo(era uint32, cycleVersion, cycleNumber uint64) string {
	return fmt.Sprintf("%d:%d:%d:%s", era, cycleVersion, cycleNumber, TxTypeHandleRValidatorUpdatedEvent)
}

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

func GetValidatorUpdateProposalId(content []byte, index uint8) []byte {
	hash := utils.BlakeTwo256(content)
	return append(hash[:], index)
}

// ensue every validator claim reward
// if bond == unbond: if no delegation before, return errNoMsgs, else gen withdraw tx
// if bond > unbond: gen delegate tx
// if bond < unbond: gen undelegate+withdraw tx or withdraw tx when no available vals for unbonding
func GetBondUnbondWithdrawUnsignedTxWithTargets(client *hubClient.Client, bond, unbond, minUnDelegateAmount *big.Int,
	poolAddr types.AccAddress, height int64, targets []types.ValAddress, memo string) (unSignedTx []byte, unSignedTxType int, err error) {

	done := core.UseSdkConfigContext(client.GetAccountPrefix())
	poolAddrStr := poolAddr.String()
	done()

	switch bond.Cmp(unbond) {
	case 0:
		// return errnoMsgs if total delegate amount <= 10000
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

		totalDelegateAmount := big.NewInt(0)
		for _, res := range delegationRes.DelegationResponses {
			totalDelegateAmount = new(big.Int).Add(totalDelegateAmount, res.Balance.Amount.BigInt())
		}
		if totalDelegateAmount.Cmp(big.NewInt(1e4)) <= 0 {
			err = hubClient.ErrNoMsgs
			return
		}

		unSignedTx, err = client.GenMultiSigRawWithdrawAllRewardTxWithMemo(
			poolAddr,
			height,
			memo)
		unSignedTxType = UnSignedTxTypeBondEqualUnbondWithdraw
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
		unSignedTx, err = client.GenMultiSigRawDelegateTxWithMemo(
			poolAddr,
			valAddrs,
			types.NewCoin(client.GetDenom(), types.NewIntFromBigInt(val)),
			memo)
		unSignedTxType = UnSignedTxTypeDelegate
		return
	case -1:
		// skip and withdraw if less than min undelegate amount
		if minUnDelegateAmount.Sign() > 0 && new(big.Int).Sub(unbond, bond).Cmp(minUnDelegateAmount) < 0 {
			unSignedTx, err = client.GenMultiSigRawWithdrawAllRewardTxWithMemo(
				poolAddr,
				height,
				memo)
			unSignedTxType = UnSignedTxTypeSkipAndWithdraw
			return
		}

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
			unSignedTx, err = client.GenMultiSigRawWithdrawAllRewardTxWithMemo(
				poolAddr,
				height,
				memo)
			unSignedTxType = UnSignedTxTypeSkipAndWithdraw
			return
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

				enough = true
				break
			}

			choosedVals = append(choosedVals, validator)
			choosedAmount[validator.String()] = nowValMaxUnDeleAmount
			selectedAmount = selectedAmount.Add(nowValMaxUnDeleAmount)
		}

		if !enough {
			done()
			unSignedTx, err = client.GenMultiSigRawWithdrawAllRewardTxWithMemo(
				poolAddr,
				height,
				memo)
			unSignedTxType = UnSignedTxTypeSkipAndWithdraw
			return
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

		sort.Slice(withdrawVals, func(i int, j int) bool {
			return bytes.Compare(withdrawVals[i].Bytes(), withdrawVals[j].Bytes()) > 0
		})

		done()

		unSignedTx, err = client.GenMultiSigRawUnDelegateWithdrawTxWithMemo(
			poolAddr,
			choosedVals,
			choosedAmount,
			withdrawVals,
			memo)
		unSignedTxType = UnSignedTxTypeUnDelegateAndWithdraw
		return
	default:
		return nil, 0, fmt.Errorf("unreached case err")
	}
}

// ensue every validator claim reward
// if bond == unbond: if no delegation before, return errNoMsgs, else gen withdraw tx
// if bond > unbond: gen delegate tx
// if bond < unbond: gen undelegate+withdraw tx
func GetBondUnbondWithdrawMsgsWithTargets(client *hubClient.Client, bond, unbond *big.Int,
	poolAddr types.AccAddress, height int64, targets []types.ValAddress, logger log.Logger) (msgs []types.Msg, unSignedType int, err error) {
	logger.Debug("GetBondUnbondWithdrawMsgsWithTargets", "targets", targets, "height", height, "bond", bond.String(), "unbond", unbond.String())

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

		msgs, err = client.GenWithdrawAllRewardMsgs(
			poolAddr,
			height)
		unSignedType = 0
		return
	case 1:
		var valAddrs []types.ValAddress
		totalShouldBondAmount := types.NewIntFromBigInt(new(big.Int).Sub(bond, unbond))

		// lsm case
		if client.GetDenom() == "uatom" {
			logger.Debug("lsm case")
			var stakingParams *xStakingTypes.QueryParamsResponse
			stakingParams, err = client.QueryStakingParams()
			if err != nil {
				return
			}

			poolRes, poolErr := client.QueryPool(height)
			if poolErr != nil {
				err = poolErr
				return
			}
			totalLiquidStakedAmountRes, lsErr := client.TotalLiquidStaked(height)
			if lsErr != nil {
				err = lsErr
				return
			}
			totalLiquidStakedAmount, ok := types.NewIntFromString(totalLiquidStakedAmountRes.Tokens)
			if !ok {
				err = fmt.Errorf("parse totalLiquidStakedAmount %s failed", totalLiquidStakedAmountRes.Tokens)
				return
			}

			totalStakedAmount := poolRes.Pool.BondedTokens.Add(totalShouldBondAmount)
			totalLiquidStakedAmount = totalLiquidStakedAmount.Add(totalShouldBondAmount)

			// 0 check global liquid staking cap
			liquidStakePercent := types.NewDecFromInt(totalLiquidStakedAmount).Quo(types.NewDecFromInt(totalStakedAmount))
			if liquidStakePercent.GT(stakingParams.Params.GlobalLiquidStakingCap) {
				return nil, 0, fmt.Errorf("ExceedsGlobalLiquidStakingCap %s", liquidStakePercent.String())
			}

			for _, val := range targets {
				done := core.UseSdkConfigContext(client.GetAccountPrefix())
				valStr := val.String()
				done()

				valRes, sErr := client.QueryValidator(valStr, height)
				if sErr != nil {
					err = sErr
					return
				}
				shares, sErr := valRes.Validator.SharesFromTokens(totalShouldBondAmount)
				if sErr != nil {
					err = sErr
					return
				}

				maxValLiquidSharesLog := valRes.Validator.ValidatorBondShares.Mul(stakingParams.Params.ValidatorBondFactor)
				logger.Debug("validatorInfo", "address", valStr, "ValidatorBondShares", valRes.Validator.ValidatorBondShares, "LiquidShares", valRes.Validator.LiquidShares, "shares", shares, "maxValLiquidShares", maxValLiquidSharesLog)
				// 1 check val bond cap
				if !stakingParams.Params.ValidatorBondFactor.Equal(ValidatorBondCapDisabled) {
					maxValLiquidShares := valRes.Validator.ValidatorBondShares.Mul(stakingParams.Params.ValidatorBondFactor)
					logger.Debug("check info", "maxValLiquidShares", maxValLiquidShares)
					if valRes.Validator.LiquidShares.Add(shares).GT(maxValLiquidShares) {
						continue
					}
				}

				// 2 check val liquid staking cap
				updatedLiquidShares := valRes.Validator.LiquidShares.Add(shares)
				updatedTotalShares := valRes.Validator.DelegatorShares.Add(shares)
				liquidStakePercent := updatedLiquidShares.Quo(updatedTotalShares)
				logger.Debug("check info", "ValidatorLiquidStakingCap", stakingParams.Params.ValidatorLiquidStakingCap, "liquidStakePercent", liquidStakePercent, "updatedTotalShares", updatedTotalShares)
				if liquidStakePercent.GT(stakingParams.Params.ValidatorLiquidStakingCap) {
					continue
				}

				valAddrs = append(valAddrs, val)
			}

		} else {
			valAddrs = targets
		}

		valAddrsLen := len(valAddrs)
		//check valAddrs length
		if valAddrsLen == 0 {
			return nil, 0, fmt.Errorf("no enough target valAddrs, pool: %s", poolAddrStr)
		}

		msgs, err = client.GenDelegateMsgs(
			poolAddr,
			valAddrs,
			types.NewCoin(client.GetDenom(), totalShouldBondAmount))
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

		sort.Slice(withdrawVals, func(i int, j int) bool {
			return bytes.Compare(withdrawVals[i].Bytes(), withdrawVals[j].Bytes()) > 0
		})
		done()

		msgs, err = client.GenUnDelegateWithdrawMsgs(
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

// Notice: delegate/undelegate/withdraw operates will withdraw all reward
// all delegations had withdraw all reward in eraUpdatedEvent handler
// (0)if rewardAmount of  height == 0, hubClient.ErrNoMsgs
// (1)else gen delegate tx
func GetDelegateRewardUnsignedTxWithReward(client *hubClient.Client, poolAddr types.AccAddress, height int64,
	rewards map[string]types.Coin, memo string) ([]byte, *types.Int, error) {

	unSignedTx, err := client.GenMultiSigRawDeleRewardTxWithRewardsWithMemo(
		poolAddr,
		height,
		rewards,
		memo)
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

func GetTransferUnsignedTxWithMemo(client *hubClient.Client, poolAddr types.AccAddress, receives []*stafiHubXLedgerTypes.Unbonding, memo string,
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
	outPuts = combineSameAddress(outPuts)
	//len should not be 0
	if len(outPuts) == 0 {
		return nil, nil, ErrNoOutPuts
	}

	txBts, err := client.GenMultiSigRawBatchTransferTxWithMemo(poolAddr, outPuts, memo)
	if err != nil {
		return nil, nil, ErrNoOutPuts
	}
	return txBts, outPuts, nil
}

func GetTransferMsgs(client *hubClient.Client, poolAddr types.AccAddress, receives []*stafiHubXLedgerTypes.Unbonding,
	logger log.Logger) ([]types.Msg, []xBankTypes.Output, error) {

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

	outPuts = combineSameAddress(outPuts)
	//len should not be 0
	if len(outPuts) == 0 {
		return nil, nil, ErrNoOutPuts
	}

	msg, err := client.GenBatchTransferMsg(poolAddr, outPuts)
	if err != nil {
		return nil, nil, ErrNoOutPuts
	}
	return []types.Msg{msg}, outPuts, nil
}

func combineSameAddress(outPuts []xBankTypes.Output) []xBankTypes.Output {
	amountMap := make(map[string]types.Coins)
	for _, outPut := range outPuts {
		if existCoin, exist := amountMap[outPut.Address]; exist {
			amountMap[outPut.Address] = existCoin.Add(outPut.Coins...)
		} else {
			amountMap[outPut.Address] = outPut.Coins
		}
	}

	retOutputs := make([]xBankTypes.Output, 0)
	for address, sendCoins := range amountMap {
		retOutputs = append(retOutputs, xBankTypes.Output{
			Address: address,
			Coins:   sendCoins,
		})
	}
	//sort outPuts for the same rawTx from different relayer
	sort.SliceStable(retOutputs, func(i, j int) bool {
		return bytes.Compare([]byte(retOutputs[i].Address), []byte(retOutputs[j].Address)) < 0
	})
	return retOutputs
}

// get reward info of pre txs(tx in eraUpdatedEvent of this era and tx in bondReportedEvent/rValidatorUpdatedEvent of era-1)
// tx in bondReportedEvent of era-1 should use the old validator
// tx in rValidatorUpdatedEvent of era -1 should replace old to new validator
// tx in eraUpdatedEvent of this era should already use the new validator
func GetRewardToBeDelegated(c *hubClient.Client, delegatorAddr string, era uint32) (map[string]types.Coin, int64, bool, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	moduleAddressStr := xAuthTypes.NewModuleAddress(xDistriTypes.ModuleName).String()
	delAddress, err := types.AccAddressFromBech32(delegatorAddr)
	if err != nil {
		done()
		return nil, 0, false, err
	}
	done()

	txs, err := c.GetTxs(
		[]string{
			fmt.Sprintf("transfer.recipient='%s'", delegatorAddr),
			fmt.Sprintf("transfer.sender='%s'", moduleAddressStr),
		}, 1, 10, "desc")
	if err != nil {
		return nil, 0, false, err
	}

	if len(txs.Txs) == 0 {
		return nil, 0, false, ErrNoRewardNeedDelegate
	}

	valRewards := make(map[string]types.Coin)
	retHeight := int64(0)
	alreadyDealBondReportedEvent := false
	for i := len(txs.Txs) - 1; i >= 0; i-- {
		tx := txs.Txs[i]
		txValue := tx.Tx.Value

		decodeTx, err := c.GetTxConfig().TxDecoder()(txValue)
		if err != nil {
			return nil, 0, false, err
		}
		memoTx, ok := decodeTx.(types.TxWithMemo)
		if !ok {
			return nil, 0, false, fmt.Errorf("tx is not type TxWithMemo, txhash: %s", txs.Txs[0].TxHash)
		}
		memoInTx := memoTx.GetMemo()

		switch {
		case memoInTx == GetMemo(era, TxTypeHandleBondReportedEvent):
			alreadyDealBondReportedEvent = true
		case memoInTx == GetMemo(era, TxTypeHandleEraPoolUpdatedEvent):
			//return tx handleEraPoolUpdatedEvent height
			retHeight = tx.Height - 1
			fallthrough // should go next case logic
		case memoInTx == GetMemo(era-1, TxTypeHandleBondReportedEvent):
			height := tx.Height - 1

			totalReward, err := c.QueryDelegationTotalRewards(delAddress, height)
			if err != nil {
				return nil, 0, false, err
			}

			for _, r := range totalReward.Rewards {
				rewardCoin := types.NewCoin(c.GetDenom(), r.Reward.AmountOf(c.GetDenom()).TruncateInt())
				if rewardCoin.IsZero() {
					continue
				}

				if _, exist := valRewards[r.ValidatorAddress]; !exist {
					valRewards[r.ValidatorAddress] = rewardCoin
				} else {
					valRewards[r.ValidatorAddress] = valRewards[r.ValidatorAddress].Add(rewardCoin)
				}

			}
		case strings.Contains(memoInTx, TxTypeHandleRValidatorUpdatedEvent):
			fragments := strings.Split(memoInTx, ":")
			if len(fragments) != 4 {
				continue
			}
			// only collect the old validator's reward info which is redelegated in era-1
			if fragments[0] == fmt.Sprintf("%d", era-1) {
				msg, ok := decodeTx.GetMsgs()[0].(*xStakingTypes.MsgBeginRedelegate)
				if !ok {
					continue
				}
				height := tx.Height - 1
				totalReward, err := c.QueryDelegationTotalRewards(delAddress, height)
				if err != nil {
					return nil, 0, false, err
				}
				willRemoveVal := msg.ValidatorSrcAddress
				willUseVal := msg.ValidatorDstAddress

				for _, r := range totalReward.Rewards {
					if willRemoveVal == r.ValidatorAddress {
						rewardCoin := types.NewCoin(c.GetDenom(), r.Reward.AmountOf(c.GetDenom()).TruncateInt())
						if rewardCoin.IsZero() {
							continue
						}
						if reward, exist := valRewards[willRemoveVal]; exist {
							valRewards[willUseVal] = reward
							// put reward on new validator and delete old validator
							delete(valRewards, willRemoveVal)

							valRewards[willUseVal] = valRewards[willUseVal].Add(rewardCoin)
						} else {
							valRewards[willUseVal] = rewardCoin
						}
						break
					}
				}
			}

		default:
		}
	}

	if len(valRewards) == 0 {
		return nil, 0, false, ErrNoRewardNeedDelegate
	}

	return valRewards, retHeight, alreadyDealBondReportedEvent, nil
}

func GetLatestReDelegateTx(c *hubClient.Client, delegatorAddr string) (*types.TxResponse, int64, error) {

	txs, err := c.GetTxs(
		[]string{
			fmt.Sprintf("message.sender='%s'", delegatorAddr),
			fmt.Sprintf("message.action='%s'", "/cosmos.staking.v1beta1.MsgBeginRedelegate"),
		}, 1, 1, "desc")
	if err != nil {
		return nil, 0, err
	}

	if len(txs.Txs) == 0 {
		return nil, 0, nil
	}

	return txs.Txs[0], txs.Txs[0].Height, nil
}

// used for ica pool
// should filter with dst channel, because the dst channel is unique on dst chain.
func GetLatestDealEraUpdatedTx(c *hubClient.Client, dstChannelId string) (*types.TxResponse, int64, error) {
	txs, err := c.GetTxs(
		[]string{
			fmt.Sprintf("recv_packet.packet_dst_channel='%s'", dstChannelId),
		}, 1, 5, "desc")
	if err != nil {
		return nil, 0, err
	}

	if len(txs.Txs) == 0 {
		return nil, 0, nil
	}

	for _, tx := range txs.Txs {
		for _, event := range tx.Events {
			if event.Type == "recv_packet" {
				for _, a := range event.Attributes {
					if string(a.Key) == "packet_data_hex" {
						bts, err := hex.DecodeString(string(a.Value))
						if err == nil {
							var packetData icatypes.InterchainAccountPacketData
							err := icatypes.ModuleCdc.UnmarshalJSON(bts, &packetData)
							if err == nil {
								if packetData.Memo == stafiHubXLedgerTypes.TxTypeDealEraUpdated.String() {
									return tx, tx.Height, nil
								}
							}
						}
					}
				}
			}
		}
	}

	return nil, 0, nil
}

func (h *Handler) checkAndSend(poolClient *hubClient.Client, wrappedUnSignedTx *WrapUnsignedTx,
	m *core.Message, txHash, txBts []byte, poolAddress types.AccAddress, unSignedTxType int) error {
	if len(txBts) == 0 {
		return fmt.Errorf("tx empty, pool: %s", poolAddress.String())
	}

	h.log.Debug("checkAndSend", "txBts", hex.EncodeToString(txBts))
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
			h.log.Warn(fmt.Sprintf(
				"checkAndSend QueryTxByHash failed. will rebroadcast after %f second",
				BlockRetryInterval.Seconds()),
				"tx hash", txHashHexStr,
				"err or res.empty", err)

			//broadcast if not on chain
			_, err = poolClient.BroadcastTx(txBts)
			if err != nil && err != errType.ErrTxInMempoolCache {
				h.log.Warn("checkAndSend BroadcastTx failed  will retry", "err", err)
			}

			poolBalanceStr := ""
			poolBalance, err := poolClient.QueryBalance(poolAddress, poolClient.GetDenom(), 0)
			if err != nil {
				h.log.Warn("queryBalance err", "err", err)
			} else {
				poolBalanceStr = poolBalance.Balance.String()
			}
			currentBlock, currentTimestamp, err := poolClient.GetCurrentBLockAndTimestamp()
			if err != nil {
				h.log.Warn("GetCurrentBLockAndTimestamp err", "err", err)
			}

			h.log.Info("rpc info", "poolBalance", poolBalanceStr, "currentBlock", currentBlock,
				"currentTimestamp", currentTimestamp)

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
		case stafiHubXLedgerTypes.TxTypeDealEraUpdated: //bond/unbond/claim
			switch unSignedTxType {
			case UnSignedTxTypeSkipAndWithdraw:
				return h.sendBondReportMsg(wrappedUnSignedTx.SnapshotId, stafiHubXLedgerTypes.EitherBondUnbond)
			default:
				return h.sendBondReportMsg(wrappedUnSignedTx.SnapshotId, stafiHubXLedgerTypes.BothBondUnbond)
			}

		case stafiHubXLedgerTypes.TxTypeDealBondReported: //delegate reward
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

			// height = max(latest redelegate height, era height)?
			// the redelegate tx height maybe bigger than era height if rValidatorUpdatedEvent is dealed after a long time
			height, err := poolClient.GetHeightByEra(wrappedUnSignedTx.Era, h.conn.eraSeconds, h.conn.offset)
			if err != nil {
				h.log.Error("handleEraPoolUpdatedEvent GetHeightByEra failed",
					"pool address", poolAddressStr,
					"era", wrappedUnSignedTx.Era,
					"err", err)
				return err
			}

			_, redelegateTxHeight, err := GetLatestReDelegateTx(poolClient, poolAddressStr)
			if err != nil {
				h.log.Error("handleEraPoolUpdatedEvent GetLatestReDelegateTx failed",
					"pool address", poolAddressStr,
					"era", wrappedUnSignedTx.Era,
					"err", err)
				return err
			}
			if redelegateTxHeight > height {
				height = redelegateTxHeight
			}
			targetValidators, err := h.conn.GetPoolTargetValidators(poolAddressStr)
			if err != nil {
				return err
			}
			// check tx type
			_, unSignedTxType, err := GetBondUnbondWithdrawUnsignedTxWithTargets(poolClient, wrappedUnSignedTx.Bond,
				wrappedUnSignedTx.Unbond, h.minUnDelegateAmount.BigInt(), poolAddress, height, targetValidators, "memo")
			if err != nil && err != hubClient.ErrNoMsgs {
				return err
			}
			if err == nil && unSignedTxType == UnSignedTxTypeSkipAndWithdraw {
				diff := new(big.Int).Sub(wrappedUnSignedTx.Unbond, wrappedUnSignedTx.Bond)
				if diff.Sign() < 0 {
					diff = big.NewInt(0)
				}
				// we should sub diff as we use eitherBondUnbond action to bondReport
				total = total.Sub(types.NewIntFromBigInt(diff))
				if total.IsNegative() {
					total = types.ZeroInt()
				}
			}

			return h.sendActiveReportMsg(wrappedUnSignedTx.SnapshotId, total.BigInt())

		case stafiHubXLedgerTypes.TxTypeDealActiveReported: //transfer unbond token to user
			return h.sendTransferReportMsg(wrappedUnSignedTx.SnapshotId)

		case stafiHubXLedgerTypes.TxTypeDealValidatorUpdated: // redelegate
			// update target validator when redelegate success
			h.conn.ReplacePoolTargetValidator(poolAddressStr, wrappedUnSignedTx.OldValidator, wrappedUnSignedTx.NewValidator)
			return h.sendRValidatorUpdateReportReportMsg(
				wrappedUnSignedTx.PoolAddressStr,
				wrappedUnSignedTx.CycleVersion,
				wrappedUnSignedTx.CycleNumber,
				stafiHubXRValidatorTypes.UpdateRValidatorStatusSuccess)
		default:
			h.log.Warn("checkAndSend failed,unknown unsigned tx type",
				"pool", poolAddressStr,
				"type", wrappedUnSignedTx.Type)
			return nil
		}
	}
}

func (h *Handler) sendBondReportMsg(shotId string, action stafiHubXLedgerTypes.BondAction) error {
	m := core.Message{
		Source:      h.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonBondReport,
		Content: core.ProposalBondReport{
			Denom:  string(h.conn.symbol),
			ShotId: shotId,
			Action: action,
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

func (h *Handler) sendRValidatorUpdateReportReportMsg(poolAddressStr string, cycleVersion, cycleNumber uint64, status stafiHubXRValidatorTypes.UpdateRValidatorStatus) error {
	m := core.Message{
		Source:      h.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonRValidatorUpdateReport,
		Content: core.ProposalRValidatorUpdateReport{
			Denom:        string(h.conn.symbol),
			PoolAddress:  poolAddressStr,
			CycleVersion: cycleVersion,
			CycleNumber:  cycleNumber,
			Status:       status,
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

func (h *Handler) sendInterchainTx(proposalInterchainTx *core.ProposalInterchainTx) error {
	m := core.Message{
		Source:      h.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonInterchainTx,
		Content:     *proposalInterchainTx,
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

// will wait until signature enough
func (h *Handler) mustGetSignatureFromStafiHub(param *core.ParamSubmitSignature, threshold uint32) (signatures [][]byte, err error) {
	retry := 0
	for {
		if retry > BlockRetryLimit {
			return nil, fmt.Errorf("mustGetSignatureFromStafiHub reach retry limit")
		}
		sigs, err := h.getSignatureFromStafiHub(param)
		if err != nil {
			h.log.Warn("getSignatureFromStafiHub failed, will retry.", "err", err)
			time.Sleep(BlockRetryInterval)
			retry++
			continue
		}
		if len(sigs) < int(threshold) {
			h.log.Warn("getSignatureFromStafiHub sigs not enough, will retry.", "sigs len", len(sigs), "threshold", threshold)
			time.Sleep(BlockRetryInterval)
			retry++
			continue
		}
		return sigs, nil
	}
}

// will wait until interchain tx status ready
func (h *Handler) mustGetInterchainTxStatusFromStafiHub(propId string) (stafiHubXLedgerTypes.InterchainTxStatus, error) {
	var err error
	var status stafiHubXLedgerTypes.InterchainTxStatus
	retry := 0
	for {
		if retry > BlockRetryLimit {
			return status, fmt.Errorf("mustGetInterchainTxStatusFromStafiHub reach retry limit")
		}
		status, err = h.getInterchainTxStatusFromStafiHub(propId)
		if err != nil {
			h.log.Warn("getInterchainTxStatusFromStafiHub failed, will retry.", "err", err)
			time.Sleep(BlockRetryInterval)
			retry++
			continue
		}
		if status == stafiHubXLedgerTypes.InterchainTxStatusUnspecified || status == stafiHubXLedgerTypes.InterchainTxStatusInit {
			err = fmt.Errorf("status not match, status: %s", status)
			h.log.Warn("handler getInterchainTxStatusFromStafiHub status not success, will retry.", "err", err)
			time.Sleep(BlockRetryInterval)
			retry++
			continue
		}
		return status, nil
	}
}

// if not found return "NotFound",nil
func (h *Handler) mustGetLatestLsmProposalIdFromStafiHub() (string, error) {
	var err error
	var s string
	retry := 0
	for {
		if retry > BlockRetryLimit {
			return "", fmt.Errorf("mustGetLatestLsmProposalIdFromStafiHub reach retry limit")
		}
		s, err = h.getLatestLsmBondProposalIdFromStafiHub()
		if err != nil {
			h.log.Warn("mustGetLatestLsmProposalIdFromStafiHub failed, will retry.", "err", err)
			time.Sleep(BlockRetryInterval)
			retry++
			continue
		}
		if len(s) == 0 {
			h.log.Warn("mustGetLatestLsmProposalIdFromStafiHub failed, will retry.")
			time.Sleep(BlockRetryInterval)
			retry++
			continue
		}
		if s == "NotFound" {
			return "", ErrNotFound
		}
		if s == "UnknownQueryPath" {
			return "", ErrUnknownQueryPath
		}

		return s, nil
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

func (h *Handler) getInterchainTxStatusFromStafiHub(proposalId string) (s stafiHubXLedgerTypes.InterchainTxStatus, err error) {
	getInterchainTxStatus := core.ParamGetInterchainTxStatus{
		PropId: proposalId,
		Status: make(chan stafiHubXLedgerTypes.InterchainTxStatus, 1),
	}
	msg := core.Message{
		Source:      h.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonGetInterchainTxStatus,
		Content:     getInterchainTxStatus,
	}
	err = h.router.Send(&msg)
	if err != nil {
		return stafiHubXLedgerTypes.InterchainTxStatusUnspecified, err
	}

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	h.log.Debug("wait getInterchainTxStatusFromStafiHub from stafihub", "rSymbol", h.conn.symbol)
	select {
	case <-timer.C:
		return stafiHubXLedgerTypes.InterchainTxStatusUnspecified, fmt.Errorf("getInterchainTxStatus from stafihub timeout")
	case status := <-getInterchainTxStatus.Status:
		return status, nil
	}
}

func (h *Handler) getLatestLsmBondProposalIdFromStafiHub() (string, error) {
	c := core.ParamGetLatestLsmBondProposalId{
		PropId: make(chan string, 1),
	}
	msg := core.Message{
		Source:      h.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonGetLatestLsmBondProposalId,
		Content:     c,
	}
	err := h.router.Send(&msg)
	if err != nil {
		return "", err
	}

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	h.log.Debug("wait getLatestLsmBondProposalIdFromStafiHub from stafihub", "rSymbol", h.conn.symbol)
	select {
	case <-timer.C:
		return "", fmt.Errorf("getLatestLsmBondProposalIdFromStafiHub from stafihub timeout")
	case status := <-c.PropId:
		return status, nil
	}
}
