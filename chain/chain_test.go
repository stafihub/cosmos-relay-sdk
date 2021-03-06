package chain_test

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/stafihub/cosmos-relay-sdk/chain"
	hubClient "github.com/stafihub/cosmos-relay-sdk/client"
	"github.com/stafihub/rtoken-relay-core/common/config"
	"github.com/stafihub/rtoken-relay-core/common/core"
	"github.com/stafihub/rtoken-relay-core/common/log"
)

var (
	logger = log.NewLog("./")

	option = chain.ConfigOption{
		BlockstorePath: "/Users/tpkeeper/gowork/stafi/rtoken-relay-core/blockstore",
		StartBlock:     0,
		GasPrice:       "0.0001stake",
		PoolNameSubKey: map[string]string{
			"multisig1": "key1",
		},
	}
	cfg = config.RawChainConfig{
		Name:         "testChain",
		Rsymbol:      "ratom2",
		EndpointList: []string{"http://127.0.0.1:36657"},
		KeystorePath: "/Users/tpkeeper/.gaia",
		Opts:         option,
	}
)

func mockStdin() error {
	content := []byte("tpkeeper\n")
	tmpfile, err := ioutil.TempFile("", "example")
	if err != nil {
		return err
	}
	if _, err := tmpfile.Write(content); err != nil {
		return err
	}

	if _, err := tmpfile.Seek(0, 0); err != nil {
		return err
	}

	os.Stdin = tmpfile
	return nil
}
func TestNewConnection(t *testing.T) {
	err := mockStdin()
	if err != nil {
		t.Fatal(err)
	}
	_, err = chain.NewConnection(&cfg, option, logger)
	if err != nil {
		t.Fatal(err)
	}
}

func TestChainInitialize(t *testing.T) {
	err := mockStdin()
	if err != nil {
		t.Fatal(err)
	}
	c := chain.NewChain()
	sysErr := make(chan error)
	err = c.Initialize(&cfg, logger, sysErr)
	if err != nil {
		t.Fatal(err)
	}
	router := core.NewRouter(logger)

	c.SetRouter(router)
	c.Start()
}

func TestGetRewardToBeDelegated(t *testing.T) {
	// client, err:= hubClient.NewClient(nil, "", "", "cosmos", []string{"https://test-cosmos-rpc1.stafihub.io:443"})
	client, err := hubClient.NewClient(nil, "", "", "cosmos", []string{"http://127.0.0.1:16657"})
	if err != nil {
		panic(err)
	}
	for i := 5519471; i > 0; i-- {
		t.Log("---------------", i)
		rewardMap, height, err := chain.GetRewardToBeDelegated(client, "cosmos13jd2vn5wt8h6slj0gcv05lasgpkwpm26n04y75", uint32(i))
		if err != nil {
			t.Log(err)
		} else {
			t.Log(rewardMap, height)
		}
	}
}

func TestGetLatestRedelegateTx(t *testing.T) {
	// client, err:= hubClient.NewClient(nil, "", "", "cosmos", []string{"https://test-cosmos-rpc1.stafihub.io:443"})
	client, err := hubClient.NewClient(nil, "", "", "cosmos", []string{"http://127.0.0.1:16657"})
	if err != nil {
		panic(err)
	}
	tx, height, err := chain.GetLatestReDelegateTx(client, "cosmos13jd2vn5wt8h6slj0gcv05lasgpkwpm26n04y75")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(height, tx.TxHash)
}

func TestGetLatestDealEraUpdatedTx(t *testing.T) {
	// client, err:= hubClient.NewClient(nil, "", "", "cosmos", []string{"https://test-cosmos-rpc1.stafihub.io:443"})
	client, err := hubClient.NewClient(nil, "", "", "cosmos", []string{"http://127.0.0.1:16657"})
	if err != nil {
		panic(err)
	}
	tx, height, err := chain.GetLatestDealEraUpdatedTx(client, "channel-0")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(height, tx.TxHash)
}
