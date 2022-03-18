package chain

type ConfigOption struct {
	// get from config file
	BlockstorePath string            `json:"blockstorePath"`
	StartBlock     int               `json:"startBlock"`
	PoolNameSubKey map[string]string `json:"pools"`

	// get from stafihub rparams
	GasPrice         string   `json:"gasPrice"`
	EraSeconds       string   `json:"eraSeconds"`
	LeastBond        string   `json:"leastBond"`
	Offset           string   `json:"offset"`
	TargetValidators []string `json:"targetValidators"`
	AccountPrefix    string   `json:"accountPrefix"`

	// get from stafihub pooldetail
	PoolAddressThreshold map[string]uint32 `json:"poolThreshold"`
}
