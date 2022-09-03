package client_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sort"

	// "strings"
	"sync"
	"testing"
	"time"

	"github.com/JFJun/go-substrate-crypto/ss58"
	// "github.com/cosmos/cosmos-sdk/crypto/keyring"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	"github.com/cosmos/cosmos-sdk/types"
	xDistributionType "github.com/cosmos/cosmos-sdk/x/distribution/types"
	xStakingType "github.com/cosmos/cosmos-sdk/x/staking/types"
	hubClient "github.com/stafihub/cosmos-relay-sdk/client"
	"github.com/stafihub/rtoken-relay-core/common/core"
	"github.com/stafihub/rtoken-relay-core/common/log"
	"github.com/stretchr/testify/assert"
)

var client *hubClient.Client

func initClient() {
	// key, err := keyring.New(types.KeyringServiceName(), keyring.BackendFile, "/Users/tpkeeper/.gaia", strings.NewReader("tpkeeper\n"))
	// if err != nil {
	// 	panic(err)
	// }

	var err error
	// client, err = hubClient.NewClient(nil, "", "", "cosmos", []string{"https://cosmos-rpc1.stafi.io:443", "https://test-cosmos-rpc1.stafihub.io:443", "https://test-cosmos-rpc1.stafihub.io:443"})
	// client, err = hubClient.NewClient(nil, "", "", "cosmos", []string{"https://cosmos-rpc1.stafi.io:443"})
	// client, err = hubClient.NewClient(nil, "", "", "uhuahua", []string{"https://test-chihuahua-rpc1.stafihub.io:443"})
	// client, err = hubClient.NewClient(nil, "", "", "cosmos", []string{"https://test-cosmos-rpc1.stafihub.io:443"})
	client, err = hubClient.NewClient(nil, "", "", "cosmos", []string{"https://mainnet-rpc.wetez.io:443/cosmos/tendermint/v1/af815794bc73d0152cc333eaf32e4982443", "https://cosmos-rpc1.stafi.io:443"}, log.NewLog("client", "cosmos"))
	// client, err = hubClient.NewClient(nil, "", "", "cosmos", []string{"https://mainnet-rpc.wetez.io:443/cosmos/tendermint/v1/af815794bc73d0152cc333eaf32e4982443"})
	// client, err = hubClient.NewClient(nil, "", "", "stafi", []string{"https://test-rpc1.stafihub.io:443"})
	// client, err = hubClient.NewClient(nil, "", "", "stafi", []string{"https://dev-rpc1.stafihub.io:443"})
	// client, err = hubClient.NewClient(key, "key1", "0.000000001stake", "cosmos", []string{"http://127.0.0.1:16657"})
	if err != nil {
		panic(err)
	}
}

func TestQueryBlock(t *testing.T) {
	initClient()

	init := int64(1327510)
	end := int64(1317510)
	initBlock, _ := client.QueryBlock(init)
	endBlock, _ := client.QueryBlock(end)
	t.Log("init height", init, "timestamp", initBlock.Block.Header.Time.Unix())
	t.Log("end height", end, "timestamp", endBlock.Block.Header.Time.Unix())
	t.Log(initBlock.Block.Header.Time.Unix() - endBlock.Block.Time.Unix())
	du := (initBlock.Block.Header.Time.Unix() - endBlock.Block.Time.Unix()) * 1000 / (init - end)
	t.Log(du)
}

func TestClient_GetHeightByEra(t *testing.T) {
	initClient()
	height, err := client.GetHeightByEra(149622, 11100, 0)
	assert.NoError(t, err)
	t.Log(height)
}

func TestQuerySignInfo(t *testing.T) {
	initClient()

	validator, err := client.QueryValidator("stafivaloper1yadulh67pu0y8xqy9kkeajjjhppfm384kczwsv", 0)
	if err != nil {
		t.Fatal(err)
	}

	consPubkeyJson, err := client.Ctx().Codec.MarshalJSON(validator.Validator.ConsensusPubkey)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(consPubkeyJson))
	var pk cryptotypes.PubKey

	if err := client.Ctx().Codec.UnmarshalInterfaceJSON(consPubkeyJson, &pk); err != nil {
		t.Fatal(err)
	}

	consAddr := types.ConsAddress(pk.Address())

	signingInfo, err := client.QuerySigningInfo(consAddr.String(), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(signingInfo)

}

func TestClient_QueryTxByHash(t *testing.T) {
	initClient()

	for {

		res, err := client.QueryTxByHash("e9b912361f4c3aa4de05651f2b4b9a63360707e597bf0f9dddff631e73aba27b")
		if err!=nil{
			t.Fatal(err)
		}
		t.Log(res.Code)

		curBlock, err := client.GetCurrentBlockHeight()
		assert.NoError(t, err)
		t.Log(curBlock)
		time.Sleep(1 * time.Second)
		t.Log("\n")
	}
}

func TestClient_GetEvent(t *testing.T) {
	initClient()
	tx, err := client.GetBlockTxsWithParseErrSkip(11845451)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(tx)

	// txHash, height, memo, err := client.GetLastTxIncludeWithdraw("cosmos12yprrdprzat35zhqxe2fcnn3u26gwlt6xcq0pj")

	// if err != nil {
	// 	t.Fatal(err)
	// }
	// t.Log(txHash, memo, height)
	// client.SetAccountPrefix("terra")
	// txs, err := client.GetTxs(
	// 	[]string{
	// 		fmt.Sprintf("transfer.recipient='%s'", "cosmos12yprrdprzat35zhqxe2fcnn3u26gwlt6xcq0pj"),
	// 		fmt.Sprintf("transfer.sender='%s'", "cosmos1jv65s3grqf6v6jl3dp4t6c9t9rk99cd88lyufl"),
	// 	}, 1, 2, "desc")
	// if err != nil {
	// 	t.Fatal(err)
	// }
	// for _, tx := range txs.Txs {
	// 	t.Logf("%s %d", tx.TxHash, tx.Height)
	// }

}

func TestGetTxs(t *testing.T) {
	initClient()

	for page := 1; page < 40; page++ {
		fmt.Println("page: ", page)
		txs, err := client.GetTxs([]string{
			fmt.Sprintf("message.sender='%s'", "cosmos12yprrdprzat35zhqxe2fcnn3u26gwlt6xcq0pj"),
		}, page, 30, "desc")
		if err != nil {
			t.Fatal(err)
		}
		for _, tx := range txs.Txs {
			fmt.Println("tx ------------->")
			fmt.Println(tx.TxHash)
			txValue := tx.Tx.Value
			decodeTx, err := client.GetTxConfig().TxDecoder()(txValue)
			if err != nil {
				t.Fatal(err)
			}

			for _, msg := range decodeTx.GetMsgs() {

				switch types.MsgTypeURL(msg) {

				case types.MsgTypeURL((*xStakingType.MsgDelegate)(nil)):
					fallthrough
				case types.MsgTypeURL((*xStakingType.MsgUndelegate)(nil)):
					fallthrough
				case types.MsgTypeURL((*xDistributionType.MsgWithdrawDelegatorReward)(nil)):
					fmt.Println(decodeTx.GetMsgs())
					fmt.Println(types.MsgTypeURL(msg))
					events := types.StringifyEvents(tx.Events)

					for _, event := range events {
						switch event.Type {
						case "delegate":

						case "transfer":
							// fmt.Println(event)
							if len(event.Attributes) == 6 {

								if event.Attributes[4].Value == "cosmos1jv65s3grqf6v6jl3dp4t6c9t9rk99cd88lyufl" &&
									event.Attributes[3].Value == "cosmos12yprrdprzat35zhqxe2fcnn3u26gwlt6xcq0pj" {
									fmt.Println("reward -> ", event.Attributes[5].Value)
								}
							}
						}
					}
					poolAddr, _ := types.AccAddressFromBech32("cosmos12yprrdprzat35zhqxe2fcnn3u26gwlt6xcq0pj")
					balance, err := client.QueryBalance(poolAddr, "uatom", tx.Height+1)
					if err != nil {
						t.Fatal(err)
					}
					fmt.Println("balance: ", balance.Balance.Amount, "height: ", tx.Height)
					if balance.Balance.Amount.GT(types.NewInt(1000000000)) {
						t.Log(tx.TxHash, tx.Height+1)
					}
				}

			}
			txWithMemo, ok := decodeTx.(types.TxWithMemo)
			if ok {
				fmt.Println("txmemo: ", txWithMemo.GetMemo())
			}

		}
	}
}

func TestGetPubKey(t *testing.T) {
	initClient()
	test, _ := types.AccAddressFromBech32("cosmos1u22lut8qgqg8znxam72pwgqp8c09rnvme00kea")
	account, _ := client.QueryAccount(test)
	t.Log(hex.EncodeToString(account.GetPubKey().Bytes()))

}

func TestDecodeAddress(t *testing.T) {
	initClient()
	client.SetAccountPrefix("iaa")
	done := core.UseSdkConfigContext(client.GetAccountPrefix())
	address, err := types.AccAddressFromBech32("iaa15lne70yk254s0pm2da6g59r82cjymzjqz9sa5h")
	if err != nil {
		t.Fatal(err)
	}
	done()
	t.Log(hex.EncodeToString(address))
	client.SetAccountPrefix("stafi")
	done = core.UseSdkConfigContext(client.GetAccountPrefix())
	address2, err := types.AccAddressFromBech32("stafi15lne70yk254s0pm2da6g59r82cjymzjqvvqxz7")
	if err != nil {
		t.Fatal(err)
	}
	done()
	t.Log(hex.EncodeToString(address2))
}

func TestClient_Sign(t *testing.T) {
	initClient()
	bts, err := hex.DecodeString("0E4F8F8FF7A3B67121711DA17FBE5AE8CB25DB272DDBF7DC0E02122947266604")
	assert.NoError(t, err)
	sigs, pubkey, err := client.Sign("recipient", bts)
	assert.NoError(t, err)
	t.Log(hex.EncodeToString(sigs))
	//4c6902bda88424923c62f95b3e3ead40769edab4ec794108d1c18994fac90d490087815823bd1a8af3d6a0271538cef4622b4b500a6253d2bd4c80d38e95aa6d
	t.Log(hex.EncodeToString(pubkey.Bytes()))
	//02e7710b4f7147c10ad90da06b69d2d6b8ff46786ef55a3f1e889c33de2bf0b416
}

func TestAddress(t *testing.T) {
	addrKey1, _ := types.AccAddressFromBech32("cosmos1a8mg9rj4nklhmwkf5vva8dvtgx4ucd9yjasret")
	addrKey2, _ := types.AccAddressFromBech32("cosmos1ztquzhpkve7szl99jkugq4l8jtpnhln76aetam")
	addrKey3, _ := types.AccAddressFromBech32("cosmos12zz2hm02sxe9f4pwt7y5q9wjhcu98vnuwmjz4x")
	addrKey4, _ := types.AccAddressFromBech32("cosmos12yprrdprzat35zhqxe2fcnn3u26gwlt6xcq0pj")
	addrKey5, _ := types.AccAddressFromBech32("cosmos1em384d8ek3y8nlugapz7p5k5skg58j66je3las")
	t.Log(hex.EncodeToString(addrKey1.Bytes()))
	t.Log(hex.EncodeToString(addrKey2.Bytes()))
	t.Log(hex.EncodeToString(addrKey3.Bytes()))
	t.Log(hex.EncodeToString(addrKey4.Bytes()))
	t.Log(hex.EncodeToString(addrKey5.Bytes()))
	//client_test.go:347: e9f6828e559dbf7dbac9a319d3b58b41abcc34a4
	//client_test.go:348: 12c1c15c36667d017ca595b88057e792c33bfe7e
	//client_test.go:349: 5084abedea81b254d42e5f894015d2be3853b27c
}

func TestClient_QueryDelegations(t *testing.T) {
	initClient()
	addr, err := types.AccAddressFromBech32("cosmos12yprrdprzat35zhqxe2fcnn3u26gwlt6xcq0pj")
	assert.NoError(t, err)
	res, err := client.QueryDelegations(addr, 2458080)
	assert.NoError(t, err)
	t.Log(res.String())
	for i, d := range res.GetDelegationResponses() {
		t.Log(i, d.Balance.Amount.IsZero())
	}
}

func TestClient_QueryDelegationTotalRewards(t *testing.T) {
	initClient()
	addr, err := types.AccAddressFromBech32("cosmos12yprrdprzat35zhqxe2fcnn3u26gwlt6xcq0pj")
	assert.NoError(t, err)
	t.Log(client.GetDenom())
	res, err := client.QueryDelegationTotalRewards(addr, 2458080)
	assert.NoError(t, err)
	for i, _ := range res.Rewards {
		t.Log(i, res.Rewards[i].Reward.AmountOf(client.GetDenom()))
		t.Log(i, res.Rewards[i].Reward.AmountOf(client.GetDenom()).TruncateInt())

	}
	t.Log("total ", res.GetTotal().AmountOf(client.GetDenom()).TruncateInt())
}

func TestMemo(t *testing.T) {
	// initClient()
	// res, err := client.QueryTxByHash("c7e3f7baf5a5f1d8cbc112080f32070dddd7cca5fe4272e06f8d42c17b25193f")
	// assert.NoError(t, err)

	txBts, err := hex.DecodeString("")
	tx, err := client.GetTxConfig().TxDecoder()(txBts)
	//tx, err := client.GetTxConfig().TxJSONDecoder()(res.Tx.Value)
	assert.NoError(t, err)
	memoTx, ok := tx.(types.TxWithMemo)
	assert.Equal(t, true, ok)
	t.Log(memoTx.GetMemo())
	hb, _ := hex.DecodeString("0xbebd0355ae360c8e6a7ed940a819838c66ca7b8f581f9c0e81dbb5faff346a30")
	//t.Log(string(hb))
	bonderAddr, _ := ss58.Encode(hb, ss58.StafiPrefix)
	t.Log(bonderAddr)
}

func TestToAddress(t *testing.T) {

	address, err := types.AccAddressFromHex("76ec5242a51191b37bc1043d894c3ddce66e4020")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(address.String())
}

func TestMultiThread(t *testing.T) {
	initClient()
	wg := sync.WaitGroup{}
	wg.Add(50)

	for i := 0; i < 50; i++ {
		go func(i int) {
			t.Log(i)
			time.Sleep(5 * time.Second)
			height, err := client.GetAccount()
			if err != nil {
				t.Log("fail", i, err)
			} else {
				t.Log("success", i, height.GetSequence())
			}
			time.Sleep(15 * time.Second)
			height, err = client.GetAccount()
			if err != nil {
				t.Log("fail", i, err)
			} else {
				t.Log("success", i, height.GetSequence())
			}
			wg.Done()
		}(i)
	}
	wg.Wait()
}

func TestSort(t *testing.T) {
	a := []string{"cosmos1kuyde8vpt8c0ty4pxqgxw3makse7md80umthvg"}
	t.Log(a)
	sort.SliceStable(a, func(i, j int) bool {
		return bytes.Compare([]byte(a[i]), []byte(a[j])) < 0
	})
	t.Log(a)
	// rawTx := "7b22626f6479223a7b226d65737361676573223a5b7b224074797065223a222f636f736d6f732e62616e6b2e763162657461312e4d73674d756c746953656e64222c22696e70757473223a5b7b2261646472657373223a22636f736d6f7331776d6b39797334397a78676d78373770717337636a6e70616d6e6e7875737071753272383779222c22636f696e73223a5b7b2264656e6f6d223a227561746f6d222c22616d6f756e74223a22313134373730227d5d7d5d2c226f757470757473223a5b7b2261646472657373223a22636f736d6f733135366b6b326b71747777776670733836673534377377646c7263326377367163746d36633877222c22636f696e73223a5b7b2264656e6f6d223a227561746f6d222c22616d6f756e74223a2231393936227d5d7d2c7b2261646472657373223a22636f736d6f73316b7579646538767074386330747934707871677877336d616b7365376d643830756d74687667222c22636f696e73223a5b7b2264656e6f6d223a227561746f6d222c22616d6f756e74223a223939383030227d5d7d2c7b2261646472657373223a22636f736d6f73316a6b6b68666c753871656471743463796173643674673730676a7778346a6b6872736536727a222c22636f696e73223a5b7b2264656e6f6d223a227561746f6d222c22616d6f756e74223a223132393734227d5d7d5d7d5d2c226d656d6f223a22222c2274696d656f75745f686569676874223a2230222c22657874656e73696f6e5f6f7074696f6e73223a5b5d2c226e6f6e5f637269746963616c5f657874656e73696f6e5f6f7074696f6e73223a5b5d7d2c22617574685f696e666f223a7b227369676e65725f696e666f73223a5b5d2c22666565223a7b22616d6f756e74223a5b7b2264656e6f6d223a227561746f6d222c22616d6f756e74223a2237353030227d5d2c226761735f6c696d6974223a2231353030303030222c227061796572223a22222c226772616e746572223a22227d7d2c227369676e617475726573223a5b5d7d"
	rawTx := "7b22626f6479223a7b226d65737361676573223a5b7b224074797065223a222f636f736d6f732e62616e6b2e763162657461312e4d73674d756c746953656e64222c22696e70757473223a5b7b2261646472657373223a22636f736d6f7331776d6b39797334397a78676d78373770717337636a6e70616d6e6e7875737071753272383779222c22636f696e73223a5b7b2264656e6f6d223a227561746f6d222c22616d6f756e74223a2231303539363338303631227d5d7d5d2c226f757470757473223a5b7b2261646472657373223a22636f736d6f733135366b6b326b71747777776670733836673534377377646c7263326377367163746d36633877222c22636f696e73223a5b7b2264656e6f6d223a227561746f6d222c22616d6f756e74223a2231303539363338303631227d5d7d5d7d5d2c226d656d6f223a22222c2274696d656f75745f686569676874223a2230222c22657874656e73696f6e5f6f7074696f6e73223a5b5d2c226e6f6e5f637269746963616c5f657874656e73696f6e5f6f7074696f6e73223a5b5d7d2c22617574685f696e666f223a7b227369676e65725f696e666f73223a5b5d2c22666565223a7b22616d6f756e74223a5b7b2264656e6f6d223a227561746f6d222c22616d6f756e74223a2237353030227d5d2c226761735f6c696d6974223a2231353030303030222c227061796572223a22222c226772616e746572223a22227d7d2c227369676e617475726573223a5b5d7d"
	// rawTx:="7b22626f6479223a7b226d65737361676573223a5b7b224074797065223a222f636f736d6f732e62616e6b2e763162657461312e4d73674d756c746953656e64222c22696e70757473223a5b7b2261646472657373223a22636f736d6f7331776d6b39797334397a78676d78373770717337636a6e70616d6e6e7875737071753272383779222c22636f696e73223a5b7b2264656e6f6d223a227561746f6d222c22616d6f756e74223a22313134373730227d5d7d5d2c226f757470757473223a5b7b2261646472657373223a22636f736d6f73316a6b6b68666c753871656471743463796173643674673730676a7778346a6b6872736536727a222c22636f696e73223a5b7b2264656e6f6d223a227561746f6d222c22616d6f756e74223a223132393734227d5d7d2c7b2261646472657373223a22636f736d6f733135366b6b326b71747777776670733836673534377377646c7263326377367163746d36633877222c22636f696e73223a5b7b2264656e6f6d223a227561746f6d222c22616d6f756e74223a2231393936227d5d7d2c7b2261646472657373223a22636f736d6f73316b7579646538767074386330747934707871677877336d616b7365376d643830756d74687667222c22636f696e73223a5b7b2264656e6f6d223a227561746f6d222c22616d6f756e74223a223939383030227d5d7d5d7d5d2c226d656d6f223a22222c2274696d656f75745f686569676874223a2230222c22657874656e73696f6e5f6f7074696f6e73223a5b5d2c226e6f6e5f637269746963616c5f657874656e73696f6e5f6f7074696f6e73223a5b5d7d2c22617574685f696e666f223a7b227369676e65725f696e666f73223a5b5d2c22666565223a7b22616d6f756e74223a5b7b2264656e6f6d223a227561746f6d222c22616d6f756e74223a2237353030227d5d2c226761735f6c696d6974223a2231353030303030222c227061796572223a22222c226772616e746572223a22227d7d2c227369676e617475726573223a5b5d7d"
	txBts, err := hex.DecodeString(rawTx)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(string(txBts))
}

func TestGetValidators(t *testing.T) {
	initClient()
	res, err := client.QueryValidators(10776032)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(res.Validators)
	t.Log(len(res.Validators))
}

func TestGetBlockResults(t *testing.T) {
	initClient()
	result, err := client.GetBlockResults(1325545)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(types.StringifyEvents(result.BeginBlockEvents))

}

func TestQueryRedelegations(t *testing.T) {
	initClient()
	res, err := client.QueryReDelegations("cosmos1wmk9ys49zxgmx77pqs7cjnpamnnxuspqu2r87y", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(res.Pagination.Total)
}

func TestWithdraw(t *testing.T) {
	initClient()
	balance, err := client.QueryBalance(client.GetFromAddress(), "stake", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("balance", balance)

	delegations, err := client.QueryDelegations(client.GetFromAddress(), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(delegations)

	valAddr, err := types.ValAddressFromBech32(delegations.DelegationResponses[0].Delegation.ValidatorAddress)
	if err != nil {
		t.Fatal(err)
	}

	rewards, err := client.QueryDelegationRewards(client.GetFromAddress(), valAddr, 0)
	if err != nil {
		t.Fatal(err)
	}

	t.Log("rewards", rewards)

	reward := rewards.Rewards.AmountOf("stake").TruncateInt()

	msgs := make([]types.Msg, 0)
	msgs = append(msgs, xStakingType.NewMsgDelegate(client.GetFromAddress(), valAddr, types.NewCoin("stake", reward)))
	// msgs = append(msgs, xDistributionType.NewMsgWithdrawDelegatorReward(client.GetFromAddress(), valAddr))

	txbts, err := client.ConstructAndSignTx(msgs...)
	if err != nil {
		t.Fatal(err)
	}

	txHash, err := client.BroadcastTx(txbts)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(txHash)

}

func TestQueryVotes(t *testing.T) {
	initClient()
	votesRes, err := client.QueryVotes(72, 11214988, 1, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(votesRes)
	voter := votesRes.Votes[0].Voter
	voter = "cosmos1zp0fgmh3sf3lls3xqy5xtax2m98l778mzgtlru"
	txs, err := client.GetTxs([]string{
		fmt.Sprintf("message.sender='%s'", voter),
		fmt.Sprintf("proposal_vote.proposal_id='%d'", 72),
	}, 1, 2, "desc")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(txs)
}

func TestQueryDelegations(t *testing.T) {
	initClient()

	delegator, _ := types.AccAddressFromBech32("cosmos1qtnr8xvcrv8daxy32yz2g6v9g69lwlh5rph4t4")
	delegations, err := client.QueryDelegationsNoLock(delegator, 11056795)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(delegations)
}
