package chain

type ConfigOption struct {
	// get from config file
	BlockstorePath string            `json:"blockstorePath"`
	StartBlock     int               `json:"startBlock"`
	PoolNameSubKey map[string]string `json:"pools"`

	// get from stafihub rparams
	GasPrice      string `json:"gasPrice"`
	EraSeconds    uint32 `json:"eraSeconds"`
	LeastBond     string `json:"leastBond"`
	Offset        int32  `json:"offset"`
	AccountPrefix string `json:"accountPrefix"`

	// get from stafihub
	IcaPools map[string]string //delegationAddres => withdrawalAddress
	// get from stafihub rvalidator
	PoolTargetValidators map[string][]string `json:"targetValidators"`
	// get from stafihub pooldetail
	PoolAddressThreshold map[string]uint32 `json:"poolThreshold"`
}
