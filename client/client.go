package client

import (
	"fmt"
	"os"
	"sync"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	cryptoTypes "github.com/cosmos/cosmos-sdk/crypto/types"
	"github.com/cosmos/cosmos-sdk/types"
	xAuthTypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	rpcClient "github.com/tendermint/tendermint/rpc/client"
	rpcHttp "github.com/tendermint/tendermint/rpc/client/http"
)

//cosmos client
type Client struct {
	clientCtx           client.Context
	rpcClientList       []rpcClient.Client
	gasPrice            string
	denom               string
	accountNumber       uint64
	accountPrefix       string
	rpcClientIndex      int
	changeEndpointMutex sync.Mutex
}

func (c *Client) GetAccountPrefix() string {
	return c.accountPrefix
}

func (c *Client) SetAccountPrefix(prefix string) {
	c.accountPrefix = prefix
}
func NewClient(k keyring.Keyring, fromName, gasPrice, accountPrefix string, endPointList []string) (*Client, error) {
	if len(endPointList) == 0 {
		return nil, fmt.Errorf("no endpoint")
	}

	encodingConfig := MakeEncodingConfig()
	retClient := &Client{
		accountPrefix:  accountPrefix,
		rpcClientIndex: 0,
	}

	for _, endPoint := range endPointList {
		rpcClient, err := rpcHttp.New(endPoint, "/websocket")
		if err != nil {
			return nil, err
		}
		retClient.rpcClientList = append(retClient.rpcClientList, rpcClient)
	}

	if len(fromName) != 0 {
		info, err := k.Key(fromName)
		if err != nil {
			return nil, fmt.Errorf("keyring get address from name:%s err: %s", fromName, err)
		}

		initClientCtx := client.Context{}.
			WithCodec(encodingConfig.Marshaler).
			WithInterfaceRegistry(encodingConfig.InterfaceRegistry).
			WithTxConfig(encodingConfig.TxConfig).
			WithLegacyAmino(encodingConfig.Amino).
			WithInput(os.Stdin).
			WithAccountRetriever(xAuthTypes.AccountRetriever{}).
			WithBroadcastMode(flags.BroadcastBlock).
			WithClient(retClient.rpcClientList[0]).
			WithSkipConfirmation(true).         //skip password confirm
			WithFromName(fromName).             //keyBase need FromName to find key info
			WithFromAddress(info.GetAddress()). //accountRetriever need FromAddress
			WithKeyring(k)

		retClient.clientCtx = initClientCtx
		chaindId, err := retClient.GetChainId()
		if err != nil {
			return nil, err
		}
		retClient.clientCtx = retClient.clientCtx.WithChainID(chaindId)

		account, err := retClient.GetAccount()
		if err != nil {
			return nil, fmt.Errorf("Client.GetAccount failed: %s", err)
		}
		retClient.accountNumber = account.GetAccountNumber()
		bondedDenom, err := retClient.QueryBondedDenom()
		if err != nil {
			return nil, err
		}

		retClient.setDenom(bondedDenom.Params.BondDenom)
		err = retClient.SetGasPrice(gasPrice)
		if err != nil {
			return nil, err
		}
	} else {
		initClientCtx := client.Context{}.
			WithCodec(encodingConfig.Marshaler).
			WithInterfaceRegistry(encodingConfig.InterfaceRegistry).
			WithTxConfig(encodingConfig.TxConfig).
			WithLegacyAmino(encodingConfig.Amino).
			WithInput(os.Stdin).
			WithAccountRetriever(xAuthTypes.AccountRetriever{}).
			WithBroadcastMode(flags.BroadcastBlock).
			WithClient(retClient.rpcClientList[0]).
			WithSkipConfirmation(true) //skip password confirm

		retClient.clientCtx = initClientCtx
		bondedDenom, err := retClient.QueryBondedDenom()
		if err != nil {
			return nil, err
		}

		retClient.setDenom(bondedDenom.Params.BondDenom)
	}
	return retClient, nil
}

//update clientCtx.FromName and clientCtx.FromAddress
func (c *Client) SetFromName(fromName string) error {
	info, err := c.clientCtx.Keyring.Key(fromName)
	if err != nil {
		return fmt.Errorf("keyring get address from fromKeyname err: %s", err)
	}

	c.clientCtx = c.clientCtx.WithFromName(fromName).WithFromAddress(info.GetAddress())

	account, err := c.GetAccount()
	if err != nil {
		return err
	}
	c.accountNumber = account.GetAccountNumber()
	return nil
}

func (c *Client) GetFromName() string {
	return c.clientCtx.FromName
}

func (c *Client) GetFromAddress() types.AccAddress {
	return c.clientCtx.FromAddress
}

func (c *Client) SetGasPrice(gasPrice string) error {
	_, err := types.ParseDecCoins(gasPrice)
	if err != nil {
		return err
	}
	c.gasPrice = gasPrice
	return nil
}

func (c *Client) setDenom(denom string) {
	c.denom = denom
}

func (c *Client) GetDenom() string {
	return c.denom
}

func (c *Client) GetTxConfig() client.TxConfig {
	return c.clientCtx.TxConfig
}

func (c *Client) GetLegacyAmino() *codec.LegacyAmino {
	return c.clientCtx.LegacyAmino
}

func (c *Client) Sign(fromName string, toBeSigned []byte) ([]byte, cryptoTypes.PubKey, error) {
	return c.clientCtx.Keyring.Sign(fromName, toBeSigned)
}

func (c *Client) Ctx() client.Context {
	return c.clientCtx
}

func (c *Client) GetRpcClient() *rpcClient.Client {
	return &c.rpcClientList[0]
}

func (c *Client) ChangeEndpoint() {
	c.changeEndpointMutex.Lock()
	defer c.changeEndpointMutex.Unlock()

	willUseIndex := (c.rpcClientIndex + 1) % len(c.rpcClientList)
	c.clientCtx = c.clientCtx.WithClient(c.rpcClientList[willUseIndex])
	c.rpcClientIndex = willUseIndex
}
