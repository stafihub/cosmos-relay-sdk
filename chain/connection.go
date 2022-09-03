package chain

import (
	"bytes"
	"encoding/hex"
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
	symbol  core.RSymbol
	chainId string

	log           log.Logger
	endpointList  []string
	accountPrefix string

	poolClients   map[string]*hubClient.Client // map[pool address]subClient
	poolSubKey    map[string]string            // map[pool address]subkey
	poolThreshold map[string]uint32            // map[pool address]threshold

	icaPoolClients     map[string]*hubClient.Client // map[ica pool address]subClient
	icaPoolRewardAddr  map[string]types.AccAddress  // map[ica pool address]rewardAddress
	icaPoolHostChannel map[string]string            // map[ica pool address]hostChannelId

	poolClientMutex sync.RWMutex

	poolTargetValidators map[string][]types.ValAddress //map[ pool/ica pool address] valaddresses
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
	OldValidator   types.ValAddress
	NewValidator   types.ValAddress
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

		if err := checkDuplicate(vals); err != nil {
			done()
			return nil, err
		}
		valsMap[poolAddressStr] = vals
	}
	done()

	leastBond, err := types.ParseCoinNormalized(option.LeastBond)
	if err != nil {
		return nil, err
	}

	var key keyring.Keyring
	if len(option.PoolNameSubKey) != 0 {
		fmt.Printf("Will open %s wallet from <%s>. \nPlease ", cfg.Name, cfg.KeystorePath)
		key, err = keyring.New(types.KeyringServiceName(), keyring.BackendFile, cfg.KeystorePath, os.Stdin)
		if err != nil {
			return nil, err
		}
	}

	poolClients := make(map[string]*hubClient.Client)
	icaPoolClients := make(map[string]*hubClient.Client)
	poolSubkey := make(map[string]string)
	rewardAddrs := make(map[string]types.AccAddress)
	chainId := ""
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
		if chainId == "" {
			chainId = poolClient.Ctx().ChainID
		}
	}

	// ica pool clients
	for delegationAddr, withdrawalAddr := range option.IcaPoolWithdrawalAddr {
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
		chainId:              chainId,
		endpointList:         cfg.EndpointList,
		accountPrefix:        option.AccountPrefix,
		symbol:               core.RSymbol(cfg.Rsymbol),
		poolClients:          poolClients,
		poolSubKey:           poolSubkey,
		poolThreshold:        option.PoolAddressThreshold,
		icaPoolClients:       icaPoolClients,
		icaPoolRewardAddr:    rewardAddrs,
		icaPoolHostChannel:   option.IcaPoolHostChannel,
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
	c.poolClientMutex.RLock()
	defer c.poolClientMutex.RUnlock()

	if sub, exist := c.icaPoolClients[poolAddrStr]; exist {
		return sub, true, nil
	}

	if sub, exist := c.poolClients[poolAddrStr]; exist {
		return sub, false, nil
	}
	return nil, false, fmt.Errorf("subClient of this pool: %s not exist", poolAddrStr)
}

func (c *Connection) RemovePool(poolAddrStr string) {
	c.RemovePoolClient(poolAddrStr)
	c.RemovePoolTarget(poolAddrStr)
}

func (c *Connection) RemovePoolClient(poolAddrStr string) {
	c.poolClientMutex.Lock()
	defer c.poolClientMutex.Unlock()

	if _, exist := c.poolClients[poolAddrStr]; exist {
		delete(c.poolClients, poolAddrStr)
		delete(c.poolSubKey, poolAddrStr)
		delete(c.poolThreshold, poolAddrStr)
		return
	}

	if _, exist := c.icaPoolClients[poolAddrStr]; exist {
		delete(c.icaPoolClients, poolAddrStr)
		delete(c.icaPoolRewardAddr, poolAddrStr)
		delete(c.icaPoolHostChannel, poolAddrStr)
		return
	}
}

func (c *Connection) RemovePoolTarget(poolAddrStr string) {
	c.poolTargetMutex.Lock()
	defer c.poolTargetMutex.Unlock()

	delete(c.poolTargetValidators, poolAddrStr)
}

func (c *Connection) AddIcaPool(poolAddrStr, withdrawalAddr, hostChannelId string, targetValidators []string) error {
	c.poolClientMutex.Lock()
	defer c.poolClientMutex.Unlock()

	if _, exist := c.icaPoolClients[poolAddrStr]; exist {
		return nil
	}

	poolClient, err := hubClient.NewClient(nil, "", "", c.accountPrefix, c.endpointList)
	if err != nil {
		return err
	}
	c.icaPoolClients[poolAddrStr] = poolClient

	done := core.UseSdkConfigContext(poolClient.GetAccountPrefix())
	withdrawalAddress, err := types.AccAddressFromBech32(withdrawalAddr)
	if err != nil {
		done()
		return err
	}
	done()

	c.icaPoolRewardAddr[poolAddrStr] = withdrawalAddress
	c.icaPoolHostChannel[poolAddrStr] = hostChannelId

	done = core.UseSdkConfigContext(poolClient.GetAccountPrefix())
	for _, targetVal := range targetValidators {
		val, err := types.ValAddressFromBech32(targetVal)
		if err != nil {
			done()
			return err
		}
		c.AddPoolTargetValidator(poolAddrStr, val)
	}
	done()

	return nil
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

	// deterministic vals
	if vals, exist := c.poolTargetValidators[poolAddrStr]; exist {
		sort.SliceStable(vals, func(i, j int) bool {
			return bytes.Compare(vals[i].Bytes(), vals[j].Bytes()) >= 0
		})

		return vals, nil
	}
	return nil, fmt.Errorf("target validators of this pool: %s not exist", poolAddrStr)
}

func (c *Connection) SetPoolTargetValidators(poolAddrStr string, vals []types.ValAddress) error {
	c.poolTargetMutex.Lock()
	defer c.poolTargetMutex.Unlock()

	if err := checkDuplicate(vals); err != nil {
		return err
	}

	c.poolTargetValidators[poolAddrStr] = vals
	return nil
}

func checkDuplicate(vals []types.ValAddress) error {
	existVal := make(map[string]bool)
	for _, val := range vals {
		if existVal[hex.EncodeToString(val)] {
			return fmt.Errorf("validator dubpicate err")
		} else {
			existVal[hex.EncodeToString(val)] = true
		}
	}
	return nil
}

func (c *Connection) AddPoolTargetValidator(poolAddrStr string, newVal types.ValAddress) {
	c.poolTargetMutex.Lock()
	defer c.poolTargetMutex.Unlock()

	newVals := make([]types.ValAddress, 0)
	for _, val := range c.poolTargetValidators[poolAddrStr] {
		// rm new if exist
		if !bytes.Equal(val, newVal) {
			newVals = append(newVals, val)
		}
	}
	newVals = append(newVals, newVal)

	c.poolTargetValidators[poolAddrStr] = newVals
}

func (c *Connection) ReplacePoolTargetValidator(poolAddrStr string, oldVal, newVal types.ValAddress) {
	c.poolTargetMutex.Lock()
	defer c.poolTargetMutex.Unlock()

	newVals := make([]types.ValAddress, 0)
	for _, val := range c.poolTargetValidators[poolAddrStr] {
		// rm old and new if exist
		if !bytes.Equal(val, oldVal) && !bytes.Equal(val, newVal) {
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

func (c *Connection) GetIcaPoolRewardAddress(poolAddrStr string) (types.AccAddress, error) {
	if value, exist := c.icaPoolRewardAddr[poolAddrStr]; exist {
		return value, nil
	}
	return types.AccAddress{}, fmt.Errorf("reward address of this pool: %s not exist", poolAddrStr)
}

func (c *Connection) GetIcaPoolHostChannelId(poolAddrStr string) (string, error) {
	if value, exist := c.icaPoolHostChannel[poolAddrStr]; exist {
		return value, nil
	}
	return "", fmt.Errorf("srcChannelId of this pool: %s not exist", poolAddrStr)
}
