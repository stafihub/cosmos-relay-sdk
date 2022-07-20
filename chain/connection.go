package chain

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"os"
	"sort"
	"sync"

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
	icaPoolClients       map[string]*hubClient.Client // map[ica pool address]subClient
	rewardAddresses      map[string]types.AccAddress
	poolSubKey           map[string]string // map[pool address]subkey
	poolThreshold        map[string]uint32 // map[pool address]threshold
	log                  log.Logger
	poolTargetValidators map[string][]types.ValAddress
	poolTargetMutex      sync.RWMutex
}

type RParams struct {
	eraSeconds int64
	leastBond  types.Coin
	offset     int64
}

type WrapUnsignedTx struct {
	UnsignedTx     []byte
	Key            string
	SnapshotId     string
	Era            uint32
	Bond           *big.Int
	Unbond         *big.Int
	Type           stafiHubXLedgerTypes.OriginalTxType
	PoolAddressStr string
	CycleVersion   uint64
	CycleNumber    uint64
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
	icaPoolClients := make(map[string]*hubClient.Client)
	poolSubkey := make(map[string]string)
	rewardAddrs := make(map[string]types.AccAddress)
	// pool clients
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

	// ica pool clients
	for delegationAddr, withdrawalAddr := range option.IcaPools {
		poolClient, err := hubClient.NewClient(nil, "", "", option.AccountPrefix, cfg.EndpointList)
		if err != nil {
			return nil, err
		}
		icaPoolClients[delegationAddr] = poolClient

		done := core.UseSdkConfigContext(poolClient.GetAccountPrefix())
		withdrawalAddress, err := types.AccAddressFromBech32(withdrawalAddr)
		if err != nil {
			done()
			return nil, err
		}
		done()

		rewardAddrs[delegationAddr] = withdrawalAddress
		if poolClient.GetDenom() != leastBond.Denom {
			return nil, fmt.Errorf("leastBond denom: %s not equal poolClient's denom: %s", leastBond.Denom, poolClient.GetDenom())
		}
	}

	if len(poolClients) == 0 && len(icaPoolClients) == 0 {
		return nil, fmt.Errorf("no pool clients")
	}

	// ensure not duplicate
	for poolAddr, _ := range poolClients {
		if _, exist := icaPoolClients[poolAddr]; exist {
			return nil, fmt.Errorf("duplicate pool address")
		}
	}

	c := Connection{
		RParams: RParams{
			eraSeconds: int64(option.EraSeconds),
			leastBond:  leastBond,
			offset:     int64(option.Offset),
		},
		symbol:               core.RSymbol(cfg.Rsymbol),
		poolClients:          poolClients,
		icaPoolClients:       icaPoolClients,
		rewardAddresses:      rewardAddrs,
		poolSubKey:           poolSubkey,
		poolThreshold:        option.PoolAddressThreshold,
		poolTargetValidators: valsMap,
		log:                  log,
	}
	return &c, nil
}

func (c *Connection) GetOnePoolClient() (*hubClient.Client, error) {
	for _, sub := range c.icaPoolClients {
		if sub != nil {
			return sub, nil
		}
	}
	for _, sub := range c.poolClients {
		if sub != nil {
			return sub, nil
		}
	}
	return nil, errors.New("no subClient")
}

func (c *Connection) GetPoolClient(poolAddrStr string) (*hubClient.Client, bool, error) {

	if sub, exist := c.icaPoolClients[poolAddrStr]; exist {
		return sub, true, nil
	}

	if sub, exist := c.poolClients[poolAddrStr]; exist {
		return sub, false, nil
	}
	return nil, false, fmt.Errorf("subClient of this pool: %s not exist", poolAddrStr)
}

func (c *Connection) BlockStoreUseAddress() string {
	poolSlice := make([]string, 0)
	for pool := range c.poolClients {
		poolSlice = append(poolSlice, pool)
	}
	for pool := range c.icaPoolClients {
		poolSlice = append(poolSlice, pool)
	}

	sort.Strings(poolSlice)
	return poolSlice[0]
}

func (c *Connection) GetPoolTargetValidators(poolAddrStr string) ([]types.ValAddress, error) {
	c.poolTargetMutex.RLock()
	defer c.poolTargetMutex.RUnlock()

	if vals, exist := c.poolTargetValidators[poolAddrStr]; exist {
		return vals, nil
	}
	return nil, fmt.Errorf("target validators of this pool: %s not exist", poolAddrStr)
}

func (c *Connection) SetPoolTargetValidators(poolAddrStr string, vals []types.ValAddress) {
	c.poolTargetMutex.Lock()
	defer c.poolTargetMutex.Unlock()

	c.poolTargetValidators[poolAddrStr] = vals
}

func (c *Connection) AddPoolTargetValidator(poolAddrStr string, newVal types.ValAddress) {
	c.poolTargetMutex.Lock()
	defer c.poolTargetMutex.Unlock()

	c.poolTargetValidators[poolAddrStr] = append(c.poolTargetValidators[poolAddrStr], newVal)
}

func (c *Connection) ReplacePoolTargetValidator(poolAddrStr string, oldVal, newVal types.ValAddress) {
	c.poolTargetMutex.Lock()
	defer c.poolTargetMutex.Unlock()

	newVals := make([]types.ValAddress, 0)
	for _, val := range c.poolTargetValidators[poolAddrStr] {
		if !bytes.Equal(val, oldVal) {
			newVals = append(newVals, val)
		}
	}
	newVals = append(newVals, newVal)

	c.poolTargetValidators[poolAddrStr] = newVals
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

func (c *Connection) GetRewardAddress(poolAddrStr string) (types.AccAddress, error) {
	if value, exist := c.rewardAddresses[poolAddrStr]; exist {
		return value, nil
	}
	return types.AccAddress{}, fmt.Errorf("reward address of this pool: %s not exist", poolAddrStr)
}
