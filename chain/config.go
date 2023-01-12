package chain

type ConfigOption struct {
	// get from config file
	BlockstorePath      string            `json:"blockstorePath"`
	StartBlock          int               `json:"startBlock"`
	PoolNameSubKey      map[string]string `json:"pools"`
	MinUnDelegateAmount string            `json:"minUnDelegateAmount"`

	// get from stafihub rparams
	GasPrice      string `json:"gasPrice"`
	EraSeconds    uint32 `json:"eraSeconds"`
	LeastBond     string `json:"leastBond"`
	Offset        int32  `json:"offset"`
	AccountPrefix string `json:"accountPrefix"`

	// get from stafihub
	IcaPoolWithdrawalAddr map[string]string   //delegationAddres => withdrawalAddress
	IcaPoolHostChannel    map[string]string   //delegationAddres => hostChannelId
	PoolTargetValidators  map[string][]string `json:"targetValidators"`
	PoolAddressThreshold  map[string]uint32   `json:"poolThreshold"`
}
