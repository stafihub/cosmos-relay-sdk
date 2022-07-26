package client

import (
	"context"
	"fmt"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	xAuthTx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	xAuthTypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	xBankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	xDistriTypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	xGovTypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	xSlashingTypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	xStakeTypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
)

//no 0x prefix
func (c *Client) QueryTxByHashNoLock(hashHexStr string) (*types.TxResponse, error) {
	cc, err := c.retry(func() (interface{}, error) {
		return xAuthTx.QueryTx(c.Ctx(), hashHexStr)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*types.TxResponse), nil
}

func (c *Client) QueryDelegationNoLock(delegatorAddr types.AccAddress, validatorAddr types.ValAddress, height int64) (*xStakeTypes.QueryDelegationResponse, error) {
	cc, err := c.retry(func() (interface{}, error) {
		client := c.Ctx().WithHeight(height)
		queryClient := xStakeTypes.NewQueryClient(client)
		params := &xStakeTypes.QueryDelegationRequest{
			DelegatorAddr: delegatorAddr.String(),
			ValidatorAddr: validatorAddr.String(),
		}
		return queryClient.Delegation(context.Background(), params)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xStakeTypes.QueryDelegationResponse), nil
}

func (c *Client) QueryUnbondingDelegationNoLock(delegatorAddr types.AccAddress, validatorAddr types.ValAddress, height int64) (*xStakeTypes.QueryUnbondingDelegationResponse, error) {
	cc, err := c.retry(func() (interface{}, error) {
		client := c.Ctx().WithHeight(height)
		queryClient := xStakeTypes.NewQueryClient(client)
		params := &xStakeTypes.QueryUnbondingDelegationRequest{
			DelegatorAddr: delegatorAddr.String(),
			ValidatorAddr: validatorAddr.String(),
		}
		return queryClient.UnbondingDelegation(context.Background(), params)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xStakeTypes.QueryUnbondingDelegationResponse), nil
}

func (c *Client) QueryDelegationsNoLock(delegatorAddr types.AccAddress, height int64) (*xStakeTypes.QueryDelegatorDelegationsResponse, error) {
	cc, err := c.retry(func() (interface{}, error) {
		client := c.Ctx().WithHeight(height)
		queryClient := xStakeTypes.NewQueryClient(client)
		params := &xStakeTypes.QueryDelegatorDelegationsRequest{
			DelegatorAddr: delegatorAddr.String(),
			Pagination:    &query.PageRequest{},
		}
		return queryClient.DelegatorDelegations(context.Background(), params)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xStakeTypes.QueryDelegatorDelegationsResponse), nil
}

func (c *Client) QueryReDelegationsNoLock(delegatorAddr, src, dst string, height int64) (*xStakeTypes.QueryRedelegationsResponse, error) {
	cc, err := c.retry(func() (interface{}, error) {
		client := c.Ctx().WithHeight(height)
		queryClient := xStakeTypes.NewQueryClient(client)
		params := &xStakeTypes.QueryRedelegationsRequest{
			DelegatorAddr:    delegatorAddr,
			SrcValidatorAddr: src,
			DstValidatorAddr: dst,
			Pagination:       &query.PageRequest{},
		}
		return queryClient.Redelegations(context.Background(), params)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xStakeTypes.QueryRedelegationsResponse), nil
}

func (c *Client) QueryValidatorsNoLock(height int64) (*xStakeTypes.QueryValidatorsResponse, error) {
	cc, err := c.retry(func() (interface{}, error) {
		client := c.Ctx().WithHeight(height)
		queryClient := xStakeTypes.NewQueryClient(client)
		params := &xStakeTypes.QueryValidatorsRequest{
			Pagination: &query.PageRequest{
				Offset:     0,
				Limit:      1000,
				CountTotal: false,
				Reverse:    false,
			},
		}
		return queryClient.Validators(context.Background(), params)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xStakeTypes.QueryValidatorsResponse), nil
}

func (c *Client) QueryDelegationRewardsNoLock(delegatorAddr types.AccAddress, validatorAddr types.ValAddress, height int64) (*xDistriTypes.QueryDelegationRewardsResponse, error) {

	cc, err := c.retry(func() (interface{}, error) {
		client := c.Ctx().WithHeight(height)
		queryClient := xDistriTypes.NewQueryClient(client)
		return queryClient.DelegationRewards(
			context.Background(),
			&xDistriTypes.QueryDelegationRewardsRequest{DelegatorAddress: delegatorAddr.String(), ValidatorAddress: validatorAddr.String()},
		)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xDistriTypes.QueryDelegationRewardsResponse), nil
}

func (c *Client) QueryDelegationTotalRewardsNoLock(delegatorAddr types.AccAddress, height int64) (*xDistriTypes.QueryDelegationTotalRewardsResponse, error) {

	cc, err := c.retry(func() (interface{}, error) {
		client := c.Ctx().WithHeight(height)
		queryClient := xDistriTypes.NewQueryClient(client)
		return queryClient.DelegationTotalRewards(
			context.Background(),
			&xDistriTypes.QueryDelegationTotalRewardsRequest{DelegatorAddress: delegatorAddr.String()},
		)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xDistriTypes.QueryDelegationTotalRewardsResponse), nil
}

func (c *Client) QueryValidatorSlashesNoLock(validator types.ValAddress, startHeight, endHeight int64) (*xDistriTypes.QueryValidatorSlashesResponse, error) {

	cc, err := c.retry(func() (interface{}, error) {
		queryClient := xDistriTypes.NewQueryClient(c.Ctx())
		return queryClient.ValidatorSlashes(
			context.Background(), &xDistriTypes.QueryValidatorSlashesRequest{
				ValidatorAddress: validator.String(),
				StartingHeight:   uint64(startHeight),
				EndingHeight:     uint64(endHeight),
				Pagination: &query.PageRequest{
					Offset:     0,
					Limit:      1000,
					CountTotal: true,
				},
			},
		)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xDistriTypes.QueryValidatorSlashesResponse), nil
}

func (c *Client) QueryValidatorNoLock(validator string, height int64) (*xStakeTypes.QueryValidatorResponse, error) {

	cc, err := c.retry(func() (interface{}, error) {
		queryClient := xStakeTypes.NewQueryClient(c.Ctx().WithHeight(height))
		return queryClient.Validator(
			context.Background(),
			&xStakeTypes.QueryValidatorRequest{
				ValidatorAddr: validator,
			},
		)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xStakeTypes.QueryValidatorResponse), nil
}

func (c *Client) QueryAllRedelegationsNoLock(delegator string, height int64) (*xStakeTypes.QueryRedelegationsResponse, error) {

	cc, err := c.retry(func() (interface{}, error) {
		queryClient := xStakeTypes.NewQueryClient(c.Ctx().WithHeight(height))
		return queryClient.Redelegations(
			context.Background(),
			&xStakeTypes.QueryRedelegationsRequest{
				DelegatorAddr:    delegator,
				SrcValidatorAddr: "",
				DstValidatorAddr: "",
				Pagination: &query.PageRequest{
					Offset:     0,
					Limit:      1000,
					CountTotal: false,
				},
			},
		)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xStakeTypes.QueryRedelegationsResponse), nil
}

func (c *Client) QuerySigningInfoNoLock(consAddr string, height int64) (*xSlashingTypes.QuerySigningInfoResponse, error) {

	cc, err := c.retry(func() (interface{}, error) {
		queryClient := xSlashingTypes.NewQueryClient(c.Ctx().WithHeight(height))
		return queryClient.SigningInfo(
			context.Background(),
			&xSlashingTypes.QuerySigningInfoRequest{
				ConsAddress: consAddr,
			},
		)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xSlashingTypes.QuerySigningInfoResponse), nil
}

func (c *Client) QueryBlockNoLock(height int64) (*ctypes.ResultBlock, error) {

	cc, err := c.retry(func() (interface{}, error) {
		node, err := c.Ctx().GetNode()
		if err != nil {
			return nil, err
		}
		return node.Block(context.Background(), &height)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*ctypes.ResultBlock), nil
}

func (c *Client) QueryAccountNoLock(addr types.AccAddress) (client.Account, error) {

	return c.getAccount(0, addr)
}

func (c *Client) GetSequenceNoLock(height int64, addr types.AccAddress) (uint64, error) {

	account, err := c.getAccount(height, addr)
	if err != nil {
		return 0, err
	}
	return account.GetSequence(), nil
}

func (c *Client) QueryBalanceNoLock(addr types.AccAddress, denom string, height int64) (*xBankTypes.QueryBalanceResponse, error) {

	cc, err := c.retry(func() (interface{}, error) {
		client := c.Ctx().WithHeight(height)
		queryClient := xBankTypes.NewQueryClient(client)
		params := xBankTypes.NewQueryBalanceRequest(addr, denom)
		return queryClient.Balance(context.Background(), params)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xBankTypes.QueryBalanceResponse), nil
}

func (c *Client) GetCurrentBlockHeightNoLock() (int64, error) {

	status, err := c.getStatus()
	if err != nil {
		return 0, err
	}
	return status.SyncInfo.LatestBlockHeight, nil
}

func (c *Client) GetCurrentBLockAndTimestampNoLock() (int64, int64, error) {

	status, err := c.getStatus()
	if err != nil {
		return 0, 0, err
	}
	return status.SyncInfo.LatestBlockHeight, status.SyncInfo.LatestBlockTime.Unix(), nil
}

func (c *Client) getStatusNoLock() (*ctypes.ResultStatus, error) {
	cc, err := c.retry(func() (interface{}, error) {
		return c.Ctx().Client.Status(context.Background())
	})
	if err != nil {
		return nil, err
	}
	return cc.(*ctypes.ResultStatus), nil
}

func (c *Client) GetAccountNoLock() (client.Account, error) {

	return c.getAccount(0, c.Ctx().FromAddress)
}

func (c *Client) getAccountNoLock(height int64, addr types.AccAddress) (client.Account, error) {
	cc, err := c.retry(func() (interface{}, error) {
		client := c.Ctx().WithHeight(height)
		return client.AccountRetriever.GetAccount(c.Ctx(), addr)
	})
	if err != nil {
		return nil, err
	}
	return cc.(client.Account), nil
}

func (c *Client) GetTxsNoLock(events []string, page, limit int, orderBy string) (*types.SearchTxsResult, error) {

	cc, err := c.retry(func() (interface{}, error) {
		return xAuthTx.QueryTxsByEvents(c.Ctx(), events, page, limit, orderBy)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*types.SearchTxsResult), nil
}

func (c *Client) GetTxsWithParseErrSkipNoLock(events []string, page, limit int, orderBy string) (*types.SearchTxsResult, error) {

	cc, err := c.retry(func() (interface{}, error) {
		return xAuthTx.QueryTxsByEventsWithParseErrSkip(c.Ctx(), events, page, limit, orderBy)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*types.SearchTxsResult), nil
}

// will skip txs that parse failed
func (c *Client) GetBlockTxsWithParseErrSkipNoLock(height int64) ([]*types.TxResponse, error) {
	searchTxs, err := c.GetTxsWithParseErrSkip([]string{fmt.Sprintf("tx.height=%d", height)}, 1, 1000, "asc")
	if err != nil {
		return nil, err
	}
	if searchTxs.TotalCount != searchTxs.Count {
		return nil, fmt.Errorf("tx total count overflow, total: %d", searchTxs.TotalCount)
	}
	return searchTxs.GetTxs(), nil
}

func (c *Client) GetBlockTxsNoLock(height int64) ([]*types.TxResponse, error) {
	searchTxs, err := c.GetTxs([]string{fmt.Sprintf("tx.height=%d", height)}, 1, 1000, "asc")
	if err != nil {
		return nil, err
	}
	if searchTxs.TotalCount != searchTxs.Count {
		return nil, fmt.Errorf("tx total count overflow, total: %d", searchTxs.TotalCount)
	}
	return searchTxs.GetTxs(), nil
}

func (c *Client) GetChainIdNoLock() (string, error) {

	status, err := c.getStatus()
	if err != nil {
		return "", nil
	}
	return status.NodeInfo.Network, nil
}

func (c *Client) QueryBondedDenomNoLock() (*xStakeTypes.QueryParamsResponse, error) {

	cc, err := c.retry(func() (interface{}, error) {
		queryClient := xStakeTypes.NewQueryClient(c.Ctx())
		params := xStakeTypes.QueryParamsRequest{}
		return queryClient.Params(context.Background(), &params)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xStakeTypes.QueryParamsResponse), nil
}

func (c *Client) GetLastTxIncludeWithdrawNoLock(delegatorAddr string) (string, string, int64, error) {
	moduleAddressStr := xAuthTypes.NewModuleAddress(xDistriTypes.ModuleName).String()

	txs, err := c.GetTxs(
		[]string{
			fmt.Sprintf("transfer.recipient='%s'", delegatorAddr),
			fmt.Sprintf("transfer.sender='%s'", moduleAddressStr),
		}, 1, 1, "desc")
	if err != nil {
		return "", "", 0, err
	}

	if len(txs.Txs) != 1 {
		return "", "", 0, ErrNoTxIncludeWithdraw
	}
	txValue := txs.Txs[0].Tx.Value

	tx, err := c.GetTxConfig().TxDecoder()(txValue)
	if err != nil {
		return "", "", 0, err
	}
	memoTx, ok := tx.(types.TxWithMemo)
	if !ok {
		return "", "", 0, fmt.Errorf("tx is not type TxWithMemo, txhash: %s", txs.Txs[0].TxHash)
	}
	memoInTx := memoTx.GetMemo()

	return txs.Txs[0].TxHash, memoInTx, txs.Txs[0].Height, nil
}

func (c *Client) GetBlockResultsNoLock(height int64) (*ctypes.ResultBlockResults, error) {
	cc, err := c.retry(func() (interface{}, error) {
		return c.clientCtx.Client.BlockResults(context.Background(), &height)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*ctypes.ResultBlockResults), nil
}

func (c *Client) QueryVotesNoLock(proposalId uint64, height int64, page, limit uint64, countTotal bool) (*xGovTypes.QueryVotesResponse, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 10
	}
	cc, err := c.retry(func() (interface{}, error) {
		newCtx := c.Ctx().WithHeight(height)
		queryClient := xGovTypes.NewQueryClient(newCtx)
		return queryClient.Votes(context.Background(), &xGovTypes.QueryVotesRequest{
			ProposalId: proposalId,
			Pagination: &query.PageRequest{
				Offset:     (page - 1) * limit,
				Limit:      limit,
				CountTotal: countTotal,
				Reverse:    false,
			},
		})
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xGovTypes.QueryVotesResponse), nil
}
