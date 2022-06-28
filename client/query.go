package client

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	xAuthTx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	xAuthTypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	xBankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	xDistriTypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	xSlashingTypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	xStakeTypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stafihub/rtoken-relay-core/common/core"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
)

const retryLimit = 600
const waitTime = time.Second * 2

var ErrNoTxIncludeWithdraw = fmt.Errorf("no tx include withdraw")

//no 0x prefix
func (c *Client) QueryTxByHash(hashHexStr string) (*types.TxResponse, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	cc, err := c.retry(func() (interface{}, error) {
		return xAuthTx.QueryTx(c.Ctx(), hashHexStr)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*types.TxResponse), nil
}

func (c *Client) QueryDelegation(delegatorAddr types.AccAddress, validatorAddr types.ValAddress, height int64) (*xStakeTypes.QueryDelegationResponse, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

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

func (c *Client) QueryUnbondingDelegation(delegatorAddr types.AccAddress, validatorAddr types.ValAddress, height int64) (*xStakeTypes.QueryUnbondingDelegationResponse, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

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

func (c *Client) QueryDelegations(delegatorAddr types.AccAddress, height int64) (*xStakeTypes.QueryDelegatorDelegationsResponse, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

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

func (c *Client) QueryReDelegations(delegatorAddr, src, dst string, height int64) (*xStakeTypes.QueryRedelegationsResponse, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

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

func (c *Client) QueryValidators(height int64) (*xStakeTypes.QueryValidatorsResponse, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

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

func (c *Client) QueryDelegationRewards(delegatorAddr types.AccAddress, validatorAddr types.ValAddress, height int64) (*xDistriTypes.QueryDelegationRewardsResponse, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

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

func (c *Client) QueryDelegationTotalRewards(delegatorAddr types.AccAddress, height int64) (*xDistriTypes.QueryDelegationTotalRewardsResponse, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

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

func (c *Client) QueryValidatorSlashes(validator types.ValAddress, startHeight, endHeight int64) (*xDistriTypes.QueryValidatorSlashesResponse, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

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

func (c *Client) QueryValidator(validator string, height int64) (*xStakeTypes.QueryValidatorResponse, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

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

func (c *Client) QueryAllRedelegations(delegator string, height int64) (*xStakeTypes.QueryRedelegationsResponse, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

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

func (c *Client) QuerySigningInfo(consAddr string, height int64) (*xSlashingTypes.SigningInfo, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

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
	return cc.(*xSlashingTypes.SigningInfo), nil
}

func (c *Client) QueryBlock(height int64) (*ctypes.ResultBlock, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

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

func (c *Client) QueryAccount(addr types.AccAddress) (client.Account, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	return c.getAccount(0, addr)
}

func (c *Client) GetSequence(height int64, addr types.AccAddress) (uint64, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	account, err := c.getAccount(height, addr)
	if err != nil {
		return 0, err
	}
	return account.GetSequence(), nil
}

func (c *Client) QueryBalance(addr types.AccAddress, denom string, height int64) (*xBankTypes.QueryBalanceResponse, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

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

func (c *Client) GetCurrentBlockHeight() (int64, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	status, err := c.getStatus()
	if err != nil {
		return 0, err
	}
	return status.SyncInfo.LatestBlockHeight, nil
}

func (c *Client) GetCurrentBLockAndTimestamp() (int64, int64, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	status, err := c.getStatus()
	if err != nil {
		return 0, 0, err
	}
	return status.SyncInfo.LatestBlockHeight, status.SyncInfo.LatestBlockTime.Unix(), nil
}

func (c *Client) getStatus() (*ctypes.ResultStatus, error) {
	cc, err := c.retry(func() (interface{}, error) {
		return c.Ctx().Client.Status(context.Background())
	})
	if err != nil {
		return nil, err
	}
	return cc.(*ctypes.ResultStatus), nil
}

func (c *Client) GetAccount() (client.Account, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	return c.getAccount(0, c.Ctx().FromAddress)
}

func (c *Client) getAccount(height int64, addr types.AccAddress) (client.Account, error) {
	cc, err := c.retry(func() (interface{}, error) {
		client := c.Ctx().WithHeight(height)
		return client.AccountRetriever.GetAccount(c.Ctx(), addr)
	})
	if err != nil {
		return nil, err
	}
	return cc.(client.Account), nil
}

func (c *Client) GetTxs(events []string, page, limit int, orderBy string) (*types.SearchTxsResult, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	cc, err := c.retry(func() (interface{}, error) {
		return xAuthTx.QueryTxsByEvents(c.Ctx(), events, page, limit, orderBy)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*types.SearchTxsResult), nil
}

func (c *Client) GetTxsWithParseErrSkip(events []string, page, limit int, orderBy string) (*types.SearchTxsResult, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	cc, err := c.retry(func() (interface{}, error) {
		return xAuthTx.QueryTxsByEventsWithParseErrSkip(c.Ctx(), events, page, limit, orderBy)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*types.SearchTxsResult), nil
}

// will skip txs that parse failed
func (c *Client) GetBlockTxsWithParseErrSkip(height int64) ([]*types.TxResponse, error) {
	searchTxs, err := c.GetTxsWithParseErrSkip([]string{fmt.Sprintf("tx.height=%d", height)}, 1, 1000, "asc")
	if err != nil {
		return nil, err
	}
	if searchTxs.TotalCount != searchTxs.Count {
		return nil, fmt.Errorf("tx total count overflow, total: %d", searchTxs.TotalCount)
	}
	return searchTxs.GetTxs(), nil
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
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	status, err := c.getStatus()
	if err != nil {
		return "", nil
	}
	return status.NodeInfo.Network, nil
}

func (c *Client) QueryBondedDenom() (*xStakeTypes.QueryParamsResponse, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

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

func (c *Client) GetLastTxIncludeWithdraw(delegatorAddr string) (string, string, int64, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	moduleAddressStr := xAuthTypes.NewModuleAddress(xDistriTypes.ModuleName).String()
	done()

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

func (c *Client) GetBlockResults(height int64) (*ctypes.ResultBlockResults, error) {
	done := core.UseSdkConfigContext(c.GetAccountPrefix())
	defer done()

	cc, err := c.retry(func() (interface{}, error) {
		return c.clientCtx.Client.BlockResults(context.Background(), &height)
	})
	if err != nil {
		return nil, err
	}
	return cc.(*ctypes.ResultBlockResults), nil
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
	seconds := timestamp - targetTimestamp
	if seconds < 0 {
		return 0, fmt.Errorf("timestamp can not less than targetTimestamp")
	}

	tmpTargetBlock := blockNumber - seconds/7
	if tmpTargetBlock <= 0 {
		tmpTargetBlock = 1
	}

	block, err := c.QueryBlock(tmpTargetBlock)
	if err != nil {
		return 0, err
	}

	var afterBlockNumber int64
	var preBlockNumber int64
	if block.Block.Header.Time.Unix() > targetTimestamp {
		afterBlockNumber = block.Block.Height
		for {
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

func (c *Client) Retry(f func() (interface{}, error)) (interface{}, error) {
	return c.retry(f)
}

//only retry func when return connection err here
func (c *Client) retry(f func() (interface{}, error)) (interface{}, error) {
	var err error
	var result interface{}
	for i := 0; i < retryLimit; i++ {
		result, err = f()
		if err != nil {
			// connection err case
			if isConnectionError(err) {
				c.ChangeEndpoint()

				time.Sleep(waitTime)
				continue
			} else {
				// business err case or other err case not captured
				for j := 0; j < len(c.rpcClientList)*2; j++ {
					c.ChangeEndpoint()

					subResult, subErr := f()
					if subErr != nil {
						// filter connection err
						if isConnectionError(subErr) {
							continue
						}

						result = subResult
						err = subErr
						continue
					}

					result = subResult
					err = subErr
					// if ok when using this rpc, just return
					return result, err
				}

				// if ok when using this rpc, just return
				return result, err
			}
		}
		// no err, just return
		return result, err
	}

	return nil, fmt.Errorf("reach retry limit. err: %s", err)
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

	if err != nil {
		// json unmarshal err when rpc server shutting down
		if strings.Contains(err.Error(), "looking for beginning of value") {
			return true
		}
		// server goroutine panic
		if strings.Contains(err.Error(), "recovered") {
			fmt.Println(err)
			return true
		}
		if strings.Contains(err.Error(), "panic") {
			fmt.Println(err)
			return true
		}
	}

	return false
}

type wrapError interface {
	Unwrap() error
}
