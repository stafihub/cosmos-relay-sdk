package chain

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"sort"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types"
	hubClient "github.com/stafihub/cosmos-relay-sdk/client"
	"github.com/stafihub/rtoken-relay-core/common/config"
	"github.com/stafihub/rtoken-relay-core/common/core"
	"github.com/stafihub/rtoken-relay-core/common/log"
	stafiHubXLedgerTypes "github.com/stafihub/stafihub/x/ledger/types"
)

type Connection struct {
	RParams
	symbol               core.RSymbol
	poolClients          map[string]*hubClient.Client // map[pool address]subClient
	poolSubKey           map[string]string            // map[pool address]subkey
	poolThreshold        map[string]uint32            // map[pool address]threshold
	log                  log.Logger
	poolTargetValidators map[string][]types.ValAddress
}

type RParams struct {
	eraSeconds int64
	leastBond  types.Coin
	offset     int64
}

type WrapUnsignedTx struct {
	UnsignedTx []byte
	Key        string
	SnapshotId string
	Era        uint32
	Bond       *big.Int
	Unbond     *big.Int
	Type       stafiHubXLedgerTypes.OriginalTxType
}

func NewConnection(cfg *config.RawChainConfig, option ConfigOption, log log.Logger) (*Connection, error) {
	if len(option.PoolTargetValidators) == 0 {
		return nil, fmt.Errorf("targetValidators empty")
	}

	valsMap := make(map[string][]types.ValAddress, 0)
	done := core.UseSdkConfigContext(option.AccountPrefix)
	for poolAddressStr, valsStr := range option.PoolTargetValidators {
		if len(valsStr) == 0 {
			done()
			return nil, fmt.Errorf("targetValidators empty")
		}
		vals := make([]types.ValAddress, 0)
		for _, val := range valsStr {
			useVal, err := types.ValAddressFromBech32(val)
			if err != nil {
				done()
				return nil, err
			}
			vals = append(vals, useVal)
		}
		valsMap[poolAddressStr] = vals
	}
	done()

	leastBond, err := types.ParseCoinNormalized(option.LeastBond)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Will open %s wallet from <%s>. \nPlease ", cfg.Name, cfg.KeystorePath)
	key, err := keyring.New(types.KeyringServiceName(), keyring.BackendFile, cfg.KeystorePath, os.Stdin)
	if err != nil {
		return nil, err
	}
	poolClients := make(map[string]*hubClient.Client)
	poolSubkey := make(map[string]string)

	for poolName, subKeyName := range option.PoolNameSubKey {
		poolInfo, err := key.Key(poolName)
		if err != nil {
			return nil, err
		}
		poolClient, err := hubClient.NewClient(key, poolName, option.GasPrice, option.AccountPrefix, cfg.EndpointList)
		if err != nil {
			return nil, err
		}
		done := core.UseSdkConfigContext(poolClient.GetAccountPrefix())
		poolAddress := poolInfo.GetAddress().String()
		poolClients[poolAddress] = poolClient
		poolSubkey[poolAddress] = subKeyName
		if _, exist := option.PoolAddressThreshold[poolAddress]; !exist {
			return nil, fmt.Errorf("no pool detail info in stafihub, pool: %s", poolAddress)
		}
		done()
		if poolClient.GetDenom() != leastBond.Denom {
			return nil, fmt.Errorf("leastBond denom: %s not equal poolClient's denom: %s", leastBond.Denom, poolClient.GetDenom())
		}
	}
	if len(poolClients) == 0 {
		return nil, fmt.Errorf("no pool clients")
	}

	c := Connection{
		RParams: RParams{
			eraSeconds: int64(option.EraSeconds),
			leastBond:  leastBond,
			offset:     int64(option.Offset),
		},
		symbol:               core.RSymbol(cfg.Rsymbol),
		poolClients:          poolClients,
		poolSubKey:           poolSubkey,
		poolThreshold:        option.PoolAddressThreshold,
		poolTargetValidators: valsMap,
		log:                  log,
	}
	return &c, nil
}

func (c *Connection) GetOnePoolClient() (*hubClient.Client, error) {
	for _, sub := range c.poolClients {
		if sub != nil {
			return sub, nil
		}
	}
	return nil, errors.New("no subClient")
}

func (c *Connection) GetPoolClient(poolAddrStr string) (*hubClient.Client, error) {
	if sub, exist := c.poolClients[poolAddrStr]; exist {
		return sub, nil
	}
	return nil, fmt.Errorf("subClient of this pool: %s not exist", poolAddrStr)
}

func (c *Connection) BlockStoreUseAddress() string {
	poolSlice := make([]string, 0)
	for pool := range c.poolClients {
		poolSlice = append(poolSlice, pool)
	}

	sort.Strings(poolSlice)
	return poolSlice[0]
}

func (c *Connection) GetPoolTargetValidators(poolAddrStr string) ([]types.ValAddress, error) {
	if vals, exist := c.poolTargetValidators[poolAddrStr]; exist {
		return vals, nil
	}
	return nil, fmt.Errorf("target validators of this pool: %s not exist", poolAddrStr)
}

func (c *Connection) GetPoolThreshold(poolAddrStr string) (uint32, error) {
	if value, exist := c.poolThreshold[poolAddrStr]; exist {
		return value, nil
	}
	return 0, fmt.Errorf("threshold this pool: %s not exist", poolAddrStr)
}

func (c *Connection) GetPoolSubkeyName(poolAddrStr string) (string, error) {
	if value, exist := c.poolSubKey[poolAddrStr]; exist {
		return value, nil
	}
	return "", fmt.Errorf("subkey name this pool: %s not exist", poolAddrStr)
}
