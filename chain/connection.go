package chain

import (
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/ChainSafe/log15"
	hubClient "github.com/stafiprotocol/cosmos-relay-sdk/client"
	"github.com/stafiprotocol/rtoken-relay-core/config"
	"github.com/stafiprotocol/rtoken-relay-core/core"
)

type Connection struct {
	symbol           core.RSymbol
	eraSeconds       int64
	eraFactor        int64
	poolClients      map[string]*hubClient.Client //map[addressHexStr]subClient
	log              log15.Logger
	cachedUnsignedTx map[string]*WrapUnsignedTx //map[hash(unsignedTx)]unsignedTx
	mtx              sync.RWMutex
}

type WrapUnsignedTx struct {
	UnsignedTx []byte
	Key        string
	SnapshotId string
	Era        uint32
	Bond       *big.Int
	Unbond     *big.Int
	Type       core.OriginalTx
}

func NewConnection(cfg *config.RawChainConfig, log log15.Logger) (*Connection, error) {

	return nil, nil
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

func (pc *Connection) CacheUnsignedTx(key string, tx *WrapUnsignedTx) {
	pc.mtx.Lock()
	pc.cachedUnsignedTx[key] = tx
	pc.mtx.Unlock()
}
func (pc *Connection) GetWrappedUnsignedTx(key string) (*WrapUnsignedTx, error) {
	pc.mtx.RLock()
	defer pc.mtx.RUnlock()
	if tx, exist := pc.cachedUnsignedTx[key]; exist {
		return tx, nil
	}
	return nil, errors.New("unsignedTx of this key not exist")
}

func (pc *Connection) RemoveUnsignedTx(key string) {
	pc.mtx.Lock()
	delete(pc.cachedUnsignedTx, key)
	pc.mtx.Unlock()
}

func (pc *Connection) CachedUnsignedTxNumber() int {
	return len(pc.cachedUnsignedTx)
}

func (pc *Connection) GetHeightByEra(era uint32) (int64, error) {
	targetTimestamp := (int64(era) + pc.eraFactor) * pc.eraSeconds
	poolClient, err := pc.GetOnePoolClient()
	if err != nil {
		return 0, err
	}
	blockNumber, timestamp, err := poolClient.GetCurrentBLockAndTimestamp()
	if err != nil {
		return 0, err
	}
	seconds := timestamp - targetTimestamp
	if seconds < 0 {
		return 0, fmt.Errorf("timestamp can not less than targetTimestamp")
	}

	tmpTargetBlock := blockNumber - seconds/7

	block, err := poolClient.QueryBlock(tmpTargetBlock)
	if err != nil {
		return 0, err
	}

	findDuTime := block.Block.Header.Time.Unix() - targetTimestamp

	if findDuTime == 0 {
		return block.Block.Height, nil
	}

	if findDuTime > 7 || findDuTime < -7 {
		tmpTargetBlock -= findDuTime / 7

		block, err = poolClient.QueryBlock(tmpTargetBlock)
		if err != nil {
			return 0, err
		}
	}

	var afterBlockNumber int64
	var preBlockNumber int64
	if block.Block.Header.Time.Unix() > targetTimestamp {
		afterBlockNumber = block.Block.Height

		for {
			block, err := poolClient.QueryBlock(afterBlockNumber - 1)
			if err != nil {
				return 0, err
			}
			if block.Block.Time.Unix() > targetTimestamp {
				afterBlockNumber = block.Block.Height
			} else {
				preBlockNumber = block.Block.Height
				break
			}
		}

	} else {
		preBlockNumber = block.Block.Height
		for {
			block, err := poolClient.QueryBlock(preBlockNumber + 1)
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
