package client

import (
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/types"
	cryptoCodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/std"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/auth/tx"
	authz "github.com/cosmos/cosmos-sdk/x/authz/module"
	"github.com/cosmos/cosmos-sdk/x/bank"
	"github.com/cosmos/cosmos-sdk/x/capability"
	"github.com/cosmos/cosmos-sdk/x/crisis"
	"github.com/cosmos/cosmos-sdk/x/distribution"
	"github.com/cosmos/cosmos-sdk/x/evidence"
	"github.com/cosmos/cosmos-sdk/x/genutil"
	"github.com/cosmos/cosmos-sdk/x/gov"
	"github.com/cosmos/cosmos-sdk/x/mint"
	"github.com/cosmos/cosmos-sdk/x/params"
	"github.com/cosmos/cosmos-sdk/x/slashing"
	"github.com/cosmos/cosmos-sdk/x/staking"
	"github.com/cosmos/cosmos-sdk/x/upgrade"
	ledger "github.com/stafihub/stafihub/x/ledger"

	interChain "github.com/cosmos/ibc-go/v7/modules/apps/27-interchain-accounts"
	ibcTransfer "github.com/cosmos/ibc-go/v7/modules/apps/transfer"
	ibcCore "github.com/cosmos/ibc-go/v7/modules/core"
	tendermintClient "github.com/cosmos/ibc-go/v7/modules/light-clients/07-tendermint"
)

// EncodingConfig specifies the concrete encoding types to use for a given app.
// This is provided for compatibility between protobuf and amino implementations.
type EncodingConfig struct {
	InterfaceRegistry types.InterfaceRegistry
	Marshaler         codec.Codec
	TxConfig          client.TxConfig
	Amino             *codec.LegacyAmino
}

// MakeEncodingConfig creates an EncodingConfig for testing
func MakeEncodingConfig() EncodingConfig {
	encodingConfig := makeEncodingConfig()
	std.RegisterLegacyAminoCodec(encodingConfig.Amino)
	std.RegisterInterfaces(encodingConfig.InterfaceRegistry)
	moduleBasics := module.NewBasicManager( //codec need
		auth.AppModuleBasic{},
		genutil.AppModuleBasic{},
		bank.AppModuleBasic{},
		capability.AppModuleBasic{},
		staking.AppModuleBasic{},
		distribution.AppModuleBasic{},
		params.AppModuleBasic{},
		crisis.AppModuleBasic{},
		slashing.AppModuleBasic{},
		gov.AppModuleBasic{},
		authz.AppModuleBasic{},
		evidence.AppModuleBasic{},
		mint.AppModuleBasic{},
		upgrade.AppModuleBasic{},

		ibcTransfer.AppModuleBasic{},
		ibcCore.AppModuleBasic{},
		interChain.AppModuleBasic{},
		// stafi
		ledger.AppModuleBasic{},
		tendermintClient.AppModuleBasic{},
	)
	moduleBasics.RegisterLegacyAminoCodec(encodingConfig.Amino)
	moduleBasics.RegisterInterfaces(encodingConfig.InterfaceRegistry)

	cryptoCodec.RegisterInterfaces(encodingConfig.InterfaceRegistry)

	return encodingConfig
}

// MakeEncodingConfig creates an EncodingConfig for an amino based test configuration.
func makeEncodingConfig() EncodingConfig {
	amino := codec.NewLegacyAmino()
	interfaceRegistry := types.NewInterfaceRegistry()
	marshaler := codec.NewProtoCodec(interfaceRegistry)
	txCfg := tx.NewTxConfig(marshaler, tx.DefaultSignModes)
	return EncodingConfig{
		InterfaceRegistry: interfaceRegistry,
		Marshaler:         marshaler,
		TxConfig:          txCfg,
		Amino:             amino,
	}
}

func marshalSignatureJSON(txConfig client.TxConfig, txBldr client.TxBuilder, signatureOnly bool) ([]byte, error) {
	parsedTx := txBldr.GetTx()
	if signatureOnly {
		sigs, err := parsedTx.GetSignaturesV2()
		if err != nil {
			return nil, err
		}
		return txConfig.MarshalSignatureJSON(sigs)
	}

	return txConfig.TxJSONEncoder()(parsedTx)
}
