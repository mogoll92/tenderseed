package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/subcommands"
	"github.com/tendermint/tendermint/config"
	cmn "github.com/tendermint/tendermint/libs/common"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/p2p/pex"
	"github.com/tendermint/tendermint/version"

	"github.com/marbar3778/tenderseed/internal/tenderseed"
)

// StartArgs for the start command
type StartArgs struct {
	HomeDir    string
	SeedConfig tenderseed.Config
}

// Name returns the command name
func (*StartArgs) Name() string { return "start" }

// Synopsis returns a ummary for the command
func (*StartArgs) Synopsis() string { return "start tenderseed" }

// Usage returns full usage for the command
func (*StartArgs) Usage() string {
	return `start

start the tenderseed
`
}

// SetFlags initializes any command flags
func (args *StartArgs) SetFlags(flagSet *flag.FlagSet) {
}

// Execute runs the command
func (args *StartArgs) Execute(_ context.Context, flagSet *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	logger := log.NewTMLogger(
		log.NewSyncWriter(os.Stdout),
	)

	chainID := args.SeedConfig.ChainID

	nodeKeyFilePath := args.SeedConfig.NodeKeyFile
	addrBookFilePath := args.SeedConfig.AddrBookFile

	if !filepath.IsAbs(nodeKeyFilePath) {
		nodeKeyFilePath = filepath.Join(args.HomeDir, nodeKeyFilePath)
	}
	if !filepath.IsAbs(addrBookFilePath) {
		addrBookFilePath = filepath.Join(args.HomeDir, addrBookFilePath)
	}

	tenderseed.MkdirAllPanic(filepath.Dir(nodeKeyFilePath), os.ModePerm)
	tenderseed.MkdirAllPanic(filepath.Dir(addrBookFilePath), os.ModePerm)

	cfg := config.DefaultP2PConfig()
	cfg.AllowDuplicateIP = true

	// allow a lot of inbound peers since we disconnect from them quickly in seed mode
	cfg.MaxNumInboundPeers = args.SeedConfig.MaxNumInboundPeers

	// keep trying to make outbound connections to exchange peering info
	cfg.MaxNumOutboundPeers = args.SeedConfig.MaxNumOutboundPeers

	nodeKey, err := p2p.LoadOrGenNodeKey(nodeKeyFilePath)
	if err != nil {
		panic(err)
	}

	logger.Info("tenderseed",
		"key", nodeKey.ID(),
		"listen", args.SeedConfig.ListenAddress,
		"chain", args.SeedConfig.ChainID,
		"strict-routing", args.SeedConfig.AddrBookStrict,
		"max-inbound", args.SeedConfig.MaxNumInboundPeers,
		"max-outbound", args.SeedConfig.MaxNumOutboundPeers,
	)

	// TODO(roman) expose per-module log levels in the config
	filteredLogger := log.NewFilter(logger, log.AllowError())

	protocolVersion :=
		p2p.NewProtocolVersion(
			version.P2PProtocol,
			version.BlockProtocol,
			0,
		)

	nodeInfo := p2p.DefaultNodeInfo{
		ProtocolVersion: protocolVersion,
		ID_:             nodeKey.ID(),
		ListenAddr:      args.SeedConfig.ListenAddress,
		Network:         chainID,
		Version:         "0.0.1",
		Channels:        []byte{pex.PexChannel},
		Moniker:         fmt.Sprintf("%s-seed", chainID),
	}

	addr, err := p2p.NewNetAddressString(p2p.IDAddressString(nodeInfo.ID_, nodeInfo.ListenAddr))
	if err != nil {
		panic(err)
	}

	transport := p2p.NewMultiplexTransport(nodeInfo, *nodeKey, p2p.MConnConfig(cfg))
	if err := transport.Listen(*addr); err != nil {
		panic(err)
	}

	book := pex.NewAddrBook(addrBookFilePath, args.SeedConfig.AddrBookStrict)
	book.SetLogger(filteredLogger.With("module", "book"))

	pexReactor := pex.NewPEXReactor(book, &pex.PEXReactorConfig{
		SeedMode: true,
		// TODO(roman) see SeedConfig.Seeds field comment for blocker
		// Seeds:    args.SeedConfig.Seeds,
	})
	pexReactor.SetLogger(filteredLogger.With("module", "pex"))

	sw := p2p.NewSwitch(cfg, transport)
	sw.SetLogger(filteredLogger.With("module", "switch"))
	sw.SetNodeKey(nodeKey)
	sw.SetAddrBook(book)
	sw.AddReactor("pex", pexReactor)

	// last
	sw.SetNodeInfo(nodeInfo)

	cmn.TrapSignal(logger, func() {
		logger.Info("shutting down...")
		book.Save()
		err := sw.Stop()
		if err != nil {
			panic(err)
		}
	})

	err = sw.Start()
	if err != nil {
		panic(err)
	}

	sw.Wait()
	return subcommands.ExitSuccess
}
