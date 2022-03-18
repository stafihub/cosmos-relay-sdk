package chain

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"sort"

	"github.com/ChainSafe/log15"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types"
	hubClient "github.com/stafihub/cosmos-relay-sdk/client"
	"github.com/stafihub/rtoken-relay-core/common/config"
	"github.com/stafihub/rtoken-relay-core/common/core"
	stafiHubXLedgerTypes "github.com/stafihub/stafihub/x/ledger/types"
)

type Connection struct {
	RParams
	symbol        core.RSymbol
	poolClients   map[string]*hubClient.Client // map[pool address]subClient
	poolSubKey    map[string]string            // map[pool address]subkey
	poolThreshold map[string]uint32            // map[pool address]threshold
	log           log15.Logger
}

type RParams struct {
	eraSeconds       int64
	leastBond        types.Coin
	offset           int64
	targetValidators []types.ValAddress
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

func NewConnection(cfg *config.RawChainConfig, option ConfigOption, log log15.Logger) (*Connection, error) {
	if len(option.TargetValidators) == 0 {
		return nil, fmt.Errorf("targetValidators empty")
	}
	vals := make([]types.ValAddress, 0)
	for _, val := range option.TargetValidators {
		done := core.UseSdkConfigContext(option.AccountPrefix)
		useVal, err := types.ValAddressFromBech32(val)
		if err != nil {
			done()
			return nil, err
		}
		done()
		vals = append(vals, useVal)
	}
	leastBond, err := types.ParseCoinNormalized(option.LeastBond)
	if err != nil {
		return nil, err
	}

	eraSeconds, ok := types.NewIntFromString(option.EraSeconds)
	if !ok {
		return nil, fmt.Errorf("eraSeconds format err, eraSeconds: %s", option.EraSeconds)
	}

	offset, ok := types.NewIntFromString(option.Offset)
	if !ok {
		return nil, fmt.Errorf("offset format err, offset: %s", option.Offset)
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
		poolClient, err := hubClient.NewClient(key, poolName, option.GasPrice, cfg.Endpoint, option.AccountPrefix)
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
	}
	if len(poolClients) == 0 {
		return nil, fmt.Errorf("no pool clients")
	}

	c := Connection{
		RParams: RParams{
			eraSeconds:       eraSeconds.Int64(),
			leastBond:        leastBond,
			offset:           offset.Int64(),
			targetValidators: vals,
		},
		symbol:        core.RSymbol(cfg.Rsymbol),
		poolClients:   poolClients,
		poolSubKey:    poolSubkey,
		poolThreshold: option.PoolAddressThreshold,
		log:           log,
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

func (c *Connection) GetPoolClient(poolAddr string) (*hubClient.Client, error) {
	if sub, exist := c.poolClients[poolAddr]; exist {
		return sub, nil
	}
	return nil, errors.New("subClient of this pool not exist")
}

func (c *Connection) BlockStoreUseAddress() string {
	poolSlice := make([]string, 0)
	for pool := range c.poolClients {
		poolSlice = append(poolSlice, pool)
	}

	sort.Strings(poolSlice)
	return poolSlice[0]
}
