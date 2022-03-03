package chain

type ConfigOption struct {
	// get from config file
	BlockstorePath string            `json:"blockstorePath"`
	StartBlock     int               `json:"startBlock"`
	PoolNameSubKey map[string]string `json:"pools"`
	AccountPrefix  string            `json:"accountPrefix"`

	// get from stafihub rparams
	GasPrice         string   `json:"gasPrice"`
	EraSeconds       int      `json:"eraSeconds"`
	LeastBond        string   `json:"leastBond"`
	TargetValidators []string `json:"targetValidators"`

	// get from stafihub pooldetail
	PoolAddressThreshold map[string]uint32 `json:"poolThreshold"`
}
