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

	"github.com/ChainSafe/log15"
	"github.com/cosmos/cosmos-sdk/types"
	errType "github.com/cosmos/cosmos-sdk/types/errors"
	xBankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	xStakingTypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	hubClient "github.com/stafiprotocol/cosmos-relay-sdk/client"
	"github.com/stafiprotocol/rtoken-relay-core/core"
	"github.com/stafiprotocol/rtoken-relay-core/utils"
	stafiHubXLedgerTypes "github.com/stafiprotocol/stafihub/x/ledger/types"
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

func GetBondUnBondProposalId(shotId [32]byte, bond, unbond *big.Int, seq uint64) []byte {
	proposalId := make([]byte, 72)
	copy(proposalId, shotId[:])

	bondBts := make([]byte, 16)
	bond.FillBytes(bondBts)
	copy(proposalId[32:], bondBts)

	unbondBts := make([]byte, 16)
	unbond.FillBytes(unbondBts)
	copy(proposalId[48:], unbondBts)

	binary.BigEndian.PutUint64(proposalId[64:], seq)
	return proposalId
}

func ParseBondUnBondProposalId(content []byte) (shotId [32]byte, bond, unbond *big.Int, seq uint64, err error) {
	if len(content) != 72 {
		err = errors.New("cont length is not right")
		return
	}
	copy(shotId[:], content[:32])

	bond = new(big.Int).SetBytes(content[32:48])
	unbond = new(big.Int).SetBytes(content[48:64])

	seq = binary.BigEndian.Uint64(content[64:])
	return
}

func GetClaimRewardProposalId(shotId [32]byte, height uint64) []byte {
	proposalId := make([]byte, 40)
	copy(proposalId, shotId[:])
	binary.BigEndian.PutUint64(proposalId[32:], height)
	return proposalId
}

func ParseClaimRewardProposalId(content []byte) (shotId [32]byte, height uint64, err error) {
	if len(content) != 40 {
		err = errors.New("cont length is not right")
		return
	}
	copy(shotId[:], content[:32])
	height = binary.BigEndian.Uint64(content[32:])
	return
}

func GetTransferProposalId(txHash [32]byte, seq uint64) []byte {
	proposalId := make([]byte, 40)
	copy(proposalId, txHash[:])
	binary.BigEndian.PutUint64(proposalId[32:], seq)
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
		return nil, fmt.Errorf("no valAddrs,pool: %s", poolAddr)
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
		return nil, fmt.Errorf("no valAddrs,pool: %s", poolAddr)
	}
	//check totalDelegateAmount
	if totalDelegateAmount.LT(types.NewInt(3 * int64(valAddrsLen))) {
		return nil, fmt.Errorf("validators have no reserve value to unbond")
	}

	//bond or unbond to their validators average
	if bond.Cmp(unbond) > 0 {
		valAddrs = targets
		valAddrsLen = len(valAddrs)
		//check valAddrs length
		if valAddrsLen == 0 {
			return nil, fmt.Errorf("no target valAddrs,pool: %s", poolAddr)
		}

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
		if val.Cmp(willUsetotalDelegateAmount.BigInt()) > 0 {
			return nil, fmt.Errorf("no enough value can be used to unbond, pool: %s", poolAddr)
		}
		willUseTotalVal := types.NewIntFromBigInt(val)

		//remove if unbonding >= 7
		canUseValAddrs := make([]types.ValAddress, 0)
		for _, val := range valAddrs {
			res, err := client.QueryUnbondingDelegation(poolAddr, val, 0)
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
			return nil, fmt.Errorf("can't find enough valAddrs to unbond, pool: %s", poolAddr)
		}

		unSignedTx, err = client.GenMultiSigRawUnDelegateTxV2(
			poolAddr,
			choosedVals,
			choosedAmount)
	}

	return
}

//if bond > unbond only gen delegate tx  (txType: 2)
//if bond <= unbond
//	(1)if balanceAmount > rewardAmount of era height ,gen withdraw and delegate tx  (txType: 3)
//	(2)if balanceAmount < rewardAmount of era height, gen withdraw tx  (txTpye: 1)
func GetClaimRewardUnsignedTx(client *hubClient.Client, poolAddr types.AccAddress, height int64,
	bond, unBond *big.Int) ([]byte, int, *types.Int, error) {
	//get reward of height
	rewardRes, err := client.QueryDelegationTotalRewards(poolAddr, height)
	if err != nil {
		return nil, 0, nil, err
	}
	rewardAmount := rewardRes.GetTotal().AmountOf(client.GetDenom()).TruncateInt()

	bondCmpUnbond := bond.Cmp(unBond)

	//check when we behind several eras,only bond==unbond we can check this
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
		return nil, 0, nil, err
	}

	decodedTx, err := client.GetTxConfig().TxDecoder()(unSignedTx)
	if err != nil {
		return nil, 0, nil, err
	}
	totalAmountRet := types.NewInt(0)
	for _, msg := range decodedTx.GetMsgs() {
		if m, ok := msg.(*xStakingTypes.MsgDelegate); ok {
			totalAmountRet = totalAmountRet.Add(m.Amount.Amount)
		}
	}

	return unSignedTx, txType, &totalAmountRet, nil
}

func GetTransferUnsignedTx(client *hubClient.Client, poolAddr types.AccAddress, receives []*stafiHubXLedgerTypes.Unbonding,
	logger log15.Logger) ([]byte, []xBankTypes.Output, error) {

	outPuts := make([]xBankTypes.Output, 0)
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
	sigs *core.SubmitSignatures, m *core.Message, txHash, txBts []byte) error {
	retry := BlockRetryLimit
	txHashHexStr := hex.EncodeToString(txHash)
	_, err := types.AccAddressFromHex(sigs.Pool)
	if err != nil {
		h.log.Error("checkAndSend AccAddressFromHex failed", "err", err)
		return err
	}

	for {
		if retry <= 0 {
			h.log.Error("checkAndSend broadcast tx reach retry limit",
				"pool address", sigs.Pool)

			if wrappedUnSignedTx.Type == core.OriginalClaimRewards {
				h.log.Info("claimRewards failed we still active report")

				h.conn.RemoveUnsignedTx(wrappedUnSignedTx.Key)
				return h.sendActiveReportMsg(wrappedUnSignedTx.SnapshotId, wrappedUnSignedTx.Bond, wrappedUnSignedTx.Unbond)
			}
			break
		}
		//check on chain
		res, err := poolClient.QueryTxByHash(txHashHexStr)
		if err != nil || res.Empty() || res.Code != 0 {
			h.log.Warn(fmt.Sprintf(
				"checkAndSend QueryTxByHash failed. will rebroadcast after %f second",
				BlockRetryInterval.Seconds()),
				"tx hash", txHashHexStr,
				"err or res.empty", err)

			//broadcast if not on chain
			_, err = poolClient.BroadcastTx(txBts)
			if err != nil && err != errType.ErrTxInMempoolCache {
				h.log.Warn("checkAndSend BroadcastTx failed  will retry",
					"err", err)
			}
			time.Sleep(BlockRetryInterval)
			retry--
			continue
		}

		h.log.Info("checkAndSend success",
			"pool address", sigs.Pool,
			"tx type", wrappedUnSignedTx.Type,
			"era", wrappedUnSignedTx.Era,
			"txHash", txHashHexStr)

		//inform stafi
		switch wrappedUnSignedTx.Type {
		case core.OriginalBond: //bond or unbond
			return h.sendBondReportMsg(wrappedUnSignedTx.SnapshotId)
		case core.OriginalClaimRewards:
			h.conn.RemoveUnsignedTx(wrappedUnSignedTx.Key)
			return h.sendActiveReportMsg(wrappedUnSignedTx.SnapshotId, wrappedUnSignedTx.Bond, wrappedUnSignedTx.Unbond)
		case core.OriginalTransfer:

			h.conn.RemoveUnsignedTx(wrappedUnSignedTx.Key)

			return h.sendTransferReportMsg(wrappedUnSignedTx.SnapshotId)
		case core.OriginalUpdateValidator: //update validator
			h.conn.RemoveUnsignedTx(wrappedUnSignedTx.Key)
			return nil
		default:
			h.log.Error("checkAndSend failed,unknown unsigned tx type",
				"pool", sigs.Pool,
				"type", wrappedUnSignedTx.Type)
			return nil
		}

	}
	return nil
}
