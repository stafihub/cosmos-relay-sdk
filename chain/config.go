package chain

type ConfigOption struct {
	BlockstorePath   string            `json:"blockstorePath"`
	StartBlock       int               `json:"startBlock"`
	ChainID          string            `json:"chainId"`
	Denom            string            `json:"denom"`
	GasPrice         string            `json:"gasPrice"`
	EraSeconds       int               `json:"eraSeconds"`
	Pools            map[string]string `json:"pools"`
	TargetValidators []string          `json:"targetValidators"`
}
