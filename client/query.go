package client

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"syscall"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	xAuthTx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	xBankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	xDistriTypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	xStakeTypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stafihub/rtoken-relay-core/common/core"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
)

const retryLimit = 60
const waitTime = time.Millisecond * 500

//no 0x prefix
func (c *Client) QueryTxByHash(hashHexStr string) (*types.TxResponse, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	cc, err := retry(func() (interface{}, error) {
		return xAuthTx.QueryTx(c.clientCtx, hashHexStr)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*types.TxResponse), nil
}

func (c *Client) QueryDelegation(delegatorAddr types.AccAddress, validatorAddr types.ValAddress, height int64) (*xStakeTypes.QueryDelegationResponse, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	client := c.clientCtx.WithHeight(height)
	queryClient := xStakeTypes.NewQueryClient(client)
	params := &xStakeTypes.QueryDelegationRequest{
		DelegatorAddr: delegatorAddr.String(),
		ValidatorAddr: validatorAddr.String(),
	}

	cc, err := retry(func() (interface{}, error) {
		return queryClient.Delegation(context.Background(), params)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xStakeTypes.QueryDelegationResponse), nil
}

func (c *Client) QueryUnbondingDelegation(delegatorAddr types.AccAddress, validatorAddr types.ValAddress, height int64) (*xStakeTypes.QueryUnbondingDelegationResponse, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	client := c.clientCtx.WithHeight(height)
	queryClient := xStakeTypes.NewQueryClient(client)
	params := &xStakeTypes.QueryUnbondingDelegationRequest{
		DelegatorAddr: delegatorAddr.String(),
		ValidatorAddr: validatorAddr.String(),
	}

	cc, err := retry(func() (interface{}, error) {
		return queryClient.UnbondingDelegation(context.Background(), params)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xStakeTypes.QueryUnbondingDelegationResponse), nil
}

func (c *Client) QueryDelegations(delegatorAddr types.AccAddress, height int64) (*xStakeTypes.QueryDelegatorDelegationsResponse, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	client := c.clientCtx.WithHeight(height)
	queryClient := xStakeTypes.NewQueryClient(client)
	params := &xStakeTypes.QueryDelegatorDelegationsRequest{
		DelegatorAddr: delegatorAddr.String(),
		Pagination:    &query.PageRequest{},
	}
	cc, err := retry(func() (interface{}, error) {
		return queryClient.DelegatorDelegations(context.Background(), params)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xStakeTypes.QueryDelegatorDelegationsResponse), nil
}

func (c *Client) QueryDelegationRewards(delegatorAddr types.AccAddress, validatorAddr types.ValAddress, height int64) (*xDistriTypes.QueryDelegationRewardsResponse, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	client := c.clientCtx.WithHeight(height)
	queryClient := xDistriTypes.NewQueryClient(client)
	cc, err := retry(func() (interface{}, error) {
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

func (c *Client) QueryDelegationTotalRewards(delegatorAddr types.AccAddress, height int64) (*xDistriTypes.QueryDelegationTotalRewardsResponse, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	client := c.clientCtx.WithHeight(height)
	queryClient := xDistriTypes.NewQueryClient(client)

	cc, err := retry(func() (interface{}, error) {
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

func (c *Client) QueryBlock(height int64) (*ctypes.ResultBlock, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	node, err := c.clientCtx.GetNode()
	if err != nil {
		return nil, err
	}

	cc, err := retry(func() (interface{}, error) {
		return node.Block(context.Background(), &height)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*ctypes.ResultBlock), nil
}

func (c *Client) QueryAccount(addr types.AccAddress) (client.Account, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	return c.getAccount(0, addr)
}

func (c *Client) GetSequence(height int64, addr types.AccAddress) (uint64, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	account, err := c.getAccount(height, addr)
	if err != nil {
		return 0, err
	}
	return account.GetSequence(), nil
}

func (c *Client) QueryBalance(addr types.AccAddress, denom string, height int64) (*xBankTypes.QueryBalanceResponse, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	client := c.clientCtx.WithHeight(height)
	queryClient := xBankTypes.NewQueryClient(client)
	params := xBankTypes.NewQueryBalanceRequest(addr, denom)

	cc, err := retry(func() (interface{}, error) {
		return queryClient.Balance(context.Background(), params)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xBankTypes.QueryBalanceResponse), nil
}

func (c *Client) GetCurrentBlockHeight() (int64, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	status, err := c.getStatus()
	if err != nil {
		return 0, err
	}
	return status.SyncInfo.LatestBlockHeight, nil
}

func (c *Client) GetCurrentBLockAndTimestamp() (int64, int64, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	status, err := c.getStatus()
	if err != nil {
		return 0, 0, err
	}
	return status.SyncInfo.LatestBlockHeight, status.SyncInfo.LatestBlockTime.Unix(), nil
}

func (c *Client) getStatus() (*ctypes.ResultStatus, error) {
	cc, err := retry(func() (interface{}, error) {
		return c.clientCtx.Client.Status(context.Background())
	})
	if err != nil {
		return nil, err
	}
	return cc.(*ctypes.ResultStatus), nil
}

func (c *Client) GetAccount() (client.Account, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	return c.getAccount(0, c.clientCtx.FromAddress)
}

func (c *Client) getAccount(height int64, addr types.AccAddress) (client.Account, error) {
	cc, err := retry(func() (interface{}, error) {
		client := c.clientCtx.WithHeight(height)
		return client.AccountRetriever.GetAccount(c.clientCtx, addr)
	})
	if err != nil {
		return nil, err
	}
	return cc.(client.Account), nil
}

func (c *Client) GetTxs(events []string, page, limit int, orderBy string) (*types.SearchTxsResult, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	cc, err := retry(func() (interface{}, error) {
		return xAuthTx.QueryTxsByEvents(c.clientCtx, events, page, limit, orderBy)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*types.SearchTxsResult), nil
}

func (c *Client) GetBlockTxs(height int64) ([]*types.TxResponse, error) {
	searchTxs, err := c.GetTxs([]string{fmt.Sprintf("tx.height=%d", height)}, 1, 1000, "asc")
	if err != nil {
		return nil, err
	}
	if searchTxs.TotalCount != searchTxs.Count {
		return nil, fmt.Errorf("tx total count overflow, total: %d", searchTxs.TotalCount)
	}
	return searchTxs.GetTxs(), nil
}

func (c *Client) GetChainId() (string, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	status, err := c.getStatus()
	if err != nil {
		return "", nil
	}
	return status.NodeInfo.Network, nil
}

func (c *Client) QueryBondedDenom() (*xStakeTypes.QueryParamsResponse, error) {
	done := core.UseSdkConfigContext(GetAccountPrefix())
	defer done()
	client := c.clientCtx
	queryClient := xStakeTypes.NewQueryClient(client)
	params := xStakeTypes.QueryParamsRequest{}
	cc, err := retry(func() (interface{}, error) {
		return queryClient.Params(context.Background(), &params)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*xStakeTypes.QueryParamsResponse), nil
}

func (c *Client) GetHeightByEra(era uint32, eraSeconds, offset int64) (int64, error) {
	if int64(era) < offset {
		return 0, fmt.Errorf("era mustn't less than offset")
	}
	targetTimestamp := (int64(era) - offset) * eraSeconds

	blockNumber, timestamp, err := c.GetCurrentBLockAndTimestamp()
	if err != nil {
		return 0, err
	}
	// fmt.Println("cur blocknumber", blockNumber, "timestamp", timestamp, "targettimestamp", targetTimestamp)
	seconds := timestamp - targetTimestamp
	if seconds < 0 {
		return 0, fmt.Errorf("timestamp can not less than targetTimestamp")
	}

	tmpTargetBlock := blockNumber - seconds/7
	if tmpTargetBlock <= 0 {
		tmpTargetBlock = 1
	}

	// fmt.Println("tmpTargetBLock", tmpTargetBlock)

	block, err := c.QueryBlock(tmpTargetBlock)
	if err != nil {
		return 0, err
	}

	// findDuTime := block.Block.Header.Time.Unix() - targetTimestamp
	// fmt.Println("findDuTime", findDuTime)

	// if findDuTime == 0 {
	// 	return block.Block.Height, nil
	// }

	// if findDuTime > 7 || findDuTime < -7 {
	// 	tmpTargetBlock -= findDuTime / 7

	// 	if tmpTargetBlock <= 0 {
	// 		tmpTargetBlock = 1
	// 	}
	// 	block, err = c.QueryBlock(tmpTargetBlock)
	// 	if err != nil {
	// 		return 0, err
	// 	}
	// }

	var afterBlockNumber int64
	var preBlockNumber int64
	if block.Block.Header.Time.Unix() > targetTimestamp {
		afterBlockNumber = block.Block.Height
		for {
			// fmt.Println("block", afterBlockNumber-1)
			block, err := c.QueryBlock(afterBlockNumber - 1)
			if err != nil {
				return 0, err
			}
			if block.Block.Time.Unix() > targetTimestamp {
				afterBlockNumber = block.Block.Height
			} else {
				break
			}
		}

	} else {
		preBlockNumber = block.Block.Height
		for {
			// fmt.Println("block", preBlockNumber+1)
			block, err := c.QueryBlock(preBlockNumber + 1)
			if err != nil {
				return 0, err
			}
			if block.Block.Time.Unix() > targetTimestamp {
				afterBlockNumber = block.Block.Height
				break
			} else {
				preBlockNumber = block.Block.Height
			}
		}
	}

	return afterBlockNumber, nil
}

func Retry(f func() (interface{}, error)) (interface{}, error) {
	return retry(f)
}

//only retry func when return connection err here
func retry(f func() (interface{}, error)) (interface{}, error) {
	var err error
	var result interface{}
	for i := 0; i < retryLimit; i++ {
		result, err = f()
		if err != nil && isConnectionError(err) {
			time.Sleep(waitTime)
			continue
		}
		return result, err
	}
	panic(fmt.Sprintf("reach retry limit. err: %s", err))
}

func isConnectionError(err error) bool {
	switch t := err.(type) {
	case *url.Error:
		if t.Timeout() || t.Temporary() {
			return true
		}
		return isConnectionError(t.Err)
	}

	switch t := err.(type) {
	case *net.OpError:
		if t.Op == "dial" || t.Op == "read" {
			return true
		}
		return isConnectionError(t.Err)

	case syscall.Errno:
		if t == syscall.ECONNREFUSED {
			return true
		}
	}

	switch t := err.(type) {
	case wrapError:
		newErr := t.Unwrap()
		return isConnectionError(newErr)
	}

	return false
}

type wrapError interface {
	Unwrap() error
}
