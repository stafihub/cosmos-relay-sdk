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
	hubClient "github.com/stafihub/cosmos-relay-sdk/client"
	"github.com/stafihub/rtoken-relay-core/common/core"
	"github.com/stafihub/rtoken-relay-core/common/log"
	"github.com/stafihub/rtoken-relay-core/common/utils"
	stafiHubXLedgerTypes "github.com/stafihub/stafihub/x/ledger/types"
)

var (
	ErrNoOutPuts            = errors.New("outputs length is zero")
	ErrNoRewardNeedDelegate = fmt.Errorf("no tx reward need delegate")
)

var (
	TxTypeHandleEraPoolUpdatedEvent    = "handleEraPoolUpdatedEvent"
	TxTypeHandleBondReportedEvent      = "handleBondReportedEvent"
	TxTypeHandleActiveReportedEvent    = "handleActiveReportedEvent"
	TxTypeHandleRValidatorUpdatedEvent = "handleRValidatorUpdatedEvent"
)

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

//ensue every validator claim reward
//if bond == unbond: if no delegation before, return errNoMsgs, else gen withdraw tx
//if bond > unbond: gen delegate tx
//if bond < unbond: gen undelegate+withdraw tx
func GetBondUnbondWithdrawUnsignedTxWithTargets(client *hubClient.Client, bond, unbond *big.Int,
	poolAddr types.AccAddress, height int64, targets []types.ValAddress, memo string) (unSignedTx []byte, unSignedType int, err error) {

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

		unSignedTx, err = client.GenMultiSigRawWithdrawAllRewardTxWithMemo(
			poolAddr,
			height,
			memo)
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
		unSignedTx, err = client.GenMultiSigRawDelegateTxWithMemo(
			poolAddr,
			valAddrs,
			types.NewCoin(client.GetDenom(), types.NewIntFromBigInt(val)),
			memo)
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

		unSignedTx, err = client.GenMultiSigRawUnDelegateWithdrawTxWithMemo(
			poolAddr,
			choosedVals,
			choosedAmount,
			withdrawVals,
			memo)
		unSignedType = -1
		return
	default:
		return nil, 0, fmt.Errorf("unreached case err")
	}
}

//ensue every validator claim reward
//if bond == unbond: if no delegation before, return errNoMsgs, else gen withdraw tx
//if bond > unbond: gen delegate tx
//if bond < unbond: gen undelegate+withdraw tx
func GetBondUnbondWithdrawMsgsWithTargets(client *hubClient.Client, bond, unbond *big.Int,
	poolAddr types.AccAddress, height int64, targets []types.ValAddress) (msgs []types.Msg, unSignedType int, err error) {

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
		valAddrs := targets
		valAddrsLen := len(valAddrs)
		//check valAddrs length
		if valAddrsLen == 0 {
			return nil, 0, fmt.Errorf("no target valAddrs, pool: %s", poolAddrStr)
		}

		val := bond.Sub(bond, unbond)
		val = val.Div(val, big.NewInt(int64(valAddrsLen)))
		msgs, err = client.GenDelegateMsgs(
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

//Notice: delegate/undelegate/withdraw operates will withdraw all reward
//all delegations had withdraw all reward in eraUpdatedEvent handler
//(0)if rewardAmount of  height == 0, hubClient.ErrNoMsgs
//(1)else gen delegate tx
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

	//len should not be 0
	if len(outPuts) == 0 {
		return nil, nil, ErrNoOutPuts
	}

	//sort outPuts for the same rawTx from different relayer
	sort.SliceStable(outPuts, func(i, j int) bool {
		return bytes.Compare([]byte(outPuts[i].Address), []byte(outPuts[j].Address)) < 0
	})

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

	//len should not be 0
	if len(outPuts) == 0 {
		return nil, nil, ErrNoOutPuts
	}

	//sort outPuts for the same rawTx from different relayer
	sort.SliceStable(outPuts, func(i, j int) bool {
		return bytes.Compare([]byte(outPuts[i].Address), []byte(outPuts[j].Address)) < 0
	})

	msg, err := client.GenBatchTransferMsg(poolAddr, outPuts)
	if err != nil {
		return nil, nil, ErrNoOutPuts
	}
	return []types.Msg{msg}, outPuts, nil
}

// get reward info of pre txs(tx in eraUpdatedEvent of this era and tx in bondReportedEvent/rValidatorUpdatedEvent of era-1)
// tx in bondReportedEvent of era-1 should use the old validator
// tx in rValidatorUpdatedEvent of era -1 should replace old to new validator
// tx in eraUpdatedEvent of this era should already use the new validator
func GetRewardToBeDelegated(c *hubClient.Client, delegatorAddr string, era uint32) (map[string]types.Coin, int64, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	moduleAddressStr := xAuthTypes.NewModuleAddress(xDistriTypes.ModuleName).String()
	delAddress, err := types.AccAddressFromBech32(delegatorAddr)
	if err != nil {
		done()
		return nil, 0, err
	}
	done()

	txs, err := c.GetTxs(
		[]string{
			fmt.Sprintf("transfer.recipient='%s'", delegatorAddr),
			fmt.Sprintf("transfer.sender='%s'", moduleAddressStr),
		}, 1, 10, "desc")
	if err != nil {
		return nil, 0, err
	}

	if len(txs.Txs) == 0 {
		return nil, 0, ErrNoRewardNeedDelegate
	}

	valRewards := make(map[string]types.Coin)
	retHeight := int64(0)
	for i := len(txs.Txs) - 1; i >= 0; i-- {
		tx := txs.Txs[i]
		txValue := tx.Tx.Value

		decodeTx, err := c.GetTxConfig().TxDecoder()(txValue)
		if err != nil {
			return nil, 0, err
		}
		memoTx, ok := decodeTx.(types.TxWithMemo)
		if !ok {
			return nil, 0, fmt.Errorf("tx is not type TxWithMemo, txhash: %s", txs.Txs[0].TxHash)
		}
		memoInTx := memoTx.GetMemo()

		switch {
		case memoInTx == GetMemo(era, TxTypeHandleEraPoolUpdatedEvent):
			//return tx handleEraPoolUpdatedEvent height
			retHeight = tx.Height - 1
			fallthrough
		case memoInTx == GetMemo(era-1, TxTypeHandleBondReportedEvent):
			height := tx.Height - 1
			totalReward, err := c.QueryDelegationTotalRewards(delAddress, height)
			if err != nil {
				return nil, 0, err
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
					return nil, 0, err
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
		return nil, 0, ErrNoRewardNeedDelegate
	}

	return valRewards, retHeight, nil
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
		case stafiHubXLedgerTypes.TxTypeDealEraUpdated: //bond/unbond/claim
			return h.sendBondReportMsg(wrappedUnSignedTx.SnapshotId)
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
			return h.sendActiveReportMsg(wrappedUnSignedTx.SnapshotId, total.BigInt())
		case stafiHubXLedgerTypes.TxTypeDealActiveReported: //transfer unbond token to user
			return h.sendTransferReportMsg(wrappedUnSignedTx.SnapshotId)
		case stafiHubXLedgerTypes.TxTypeDealValidatorUpdated: // redelegate
			return h.sendRValidatorUpdateReportReportMsg(wrappedUnSignedTx.PoolAddressStr, wrappedUnSignedTx.CycleVersion, wrappedUnSignedTx.CycleNumber)
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

func (h *Handler) sendRValidatorUpdateReportReportMsg(poolAddressStr string, cycleVersion, cycleNumber uint64) error {
	m := core.Message{
		Source:      h.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonRValidatorUpdateReport,
		Content: core.ProposalRValidatorUpdateReport{
			Denom:        string(h.conn.symbol),
			PoolAddress:  poolAddressStr,
			CycleVersion: cycleVersion,
			CycleNumber:  cycleNumber,
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

func (h *Handler) mustGetProposalStatusFromStafiHub(propId string) (s stafiHubXLedgerTypes.InterchainTxStatus, err error) {
	retry := 0
	for {
		if retry > BlockRetryLimit {
			return stafiHubXLedgerTypes.InterchainTxStatusInit, fmt.Errorf("mustGetProposalStatusFromStafiHub reach retry limit, propId: %s", propId)
		}
		status, err := h.getProposalStatusFromStafiHub(propId)
		if err != nil {
			retry++
			h.log.Debug("getSignatureFromStafiHub failed, will retry.", "err", err)
			time.Sleep(BlockRetryInterval)
			continue
		}

		return status, nil
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

func (h *Handler) getProposalStatusFromStafiHub(proposalId string) (s stafiHubXLedgerTypes.InterchainTxStatus, err error) {
	getProposalStatus := core.ParamGetProposalStatus{
		PropId: proposalId,
		Status: make(chan stafiHubXLedgerTypes.InterchainTxStatus, 1),
	}
	msg := core.Message{
		Source:      h.conn.symbol,
		Destination: core.HubRFIS,
		Reason:      core.ReasonGetProposalStatus,
		Content:     getProposalStatus,
	}
	err = h.router.Send(&msg)
	if err != nil {
		return stafiHubXLedgerTypes.InterchainTxStatusInit, err
	}

	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	h.log.Debug("wait getProposalStatusFromStafiHub from stafihub", "rSymbol", h.conn.symbol)
	select {
	case <-timer.C:
		return stafiHubXLedgerTypes.InterchainTxStatusInit, fmt.Errorf("getProposalStatus from stafihub timeout")
	case status := <-getProposalStatus.Status:
		return status, nil
	}
}
