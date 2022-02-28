package chain

import "math/big"

type ConfigOption struct {
	// get from config file
	BlockstorePath string            `json:"blockstorePath"`
	StartBlock     int               `json:"startBlock"`
	PoolNameSubKey map[string]string `json:"pools"`

	// get from stafihub rparams
	GasPrice         string   `json:"gasPrice"`
	EraSeconds       int      `json:"eraSeconds"`
	LeastBond        *big.Int `json:"leastBond"`
	TargetValidators []string `json:"targetValidators"`

	// get from stafihub pooldetail
	PoolAddressThreshold map[string]uint32 `json:"poolThreshold"`
}
