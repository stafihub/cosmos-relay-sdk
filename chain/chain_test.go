package chain_test

import (
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
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
	tmpfile, err := os.CreateTemp("", "example")
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
	// client, err := hubClient.NewClient(nil, "", "", "cosmos", []string{"http://127.0.0.1:16657"}, log.NewLog("client"))
	client, err := hubClient.NewClient(nil, "", "", "iaa", []string{"https://iris-rpc1.stafihub.io:443"}, log.NewLog("client", "cosmos"))
	if err != nil {
		panic(err)
	}
	rewardMap, height, send, err := chain.GetRewardToBeDelegated(client, "iaa1wvpzras7ac3rm3mw96djhe9t8dh6uq6tc76mr2", 19665)
	if err != nil {
		t.Log(err, send)
	} else {
		t.Log(rewardMap, height, send)
	}

}

func TestGetLatestRedelegateTx(t *testing.T) {
	// client, err:= hubClient.NewClient(nil, "", "", "cosmos", []string{"https://test-cosmos-rpc1.stafihub.io:443"})
	// client, err := hubClient.NewClient(nil, "", "", "cosmos", []string{"http://127.0.0.1:16657"}, log.NewLog("cosmos"))
	// if err != nil {
	// 	panic(err)
	// }
	client, err := hubClient.NewClient(nil, "", "", "iaa", []string{"https://iris-rpc1.stafihub.io:443"}, log.NewLog("client", "cosmos"))
	if err != nil {
		panic(err)
	}
	tx, height, err := chain.GetLatestReDelegateTx(client, "iaa1wvpzras7ac3rm3mw96djhe9t8dh6uq6tc76mr2")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(height, tx.TxHash)
}

func TestGetLatestDealEraUpdatedTx(t *testing.T) {
	// client, err:= hubClient.NewClient(nil, "", "", "cosmos", []string{"https://test-cosmos-rpc1.stafihub.io:443"})
	// client, err := hubClient.NewClient(nil, "", "", "cosmos", []string{"https://cosmos-rpc4.stafi.io:443"}, log.NewLog("client"))
	// client, err := hubClient.NewClient(nil, "", "", "cosmos", []string{"https://public-rpc1.stafihub.io:443"})
	client, err := hubClient.NewClient(nil, "", "", "swth", []string{"https://tm-api.carbon.network:443"}, log.NewLog("client"))
	logrus.SetLevel(logrus.TraceLevel)
	// client, err := hubClient.NewClient(nil, "", "", "swth", []string{"https://carbon-rpc.stafi.io:443"}, log.NewLog("client"))
	if err != nil {
		t.Fatal(err)
	}
	for {
		go func(tt *testing.T) {
			_, height, err := chain.GetLatestDealEraUpdatedTx(client, "channel-31")
			if err != nil {
				tt.Log(err)
			}
			tt.Log(height)
		}(t)
		time.Sleep(time.Millisecond * 10)
	}
}
