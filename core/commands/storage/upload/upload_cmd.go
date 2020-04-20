package upload

import (
	"strconv"
	"time"

	renterpb "github.com/TRON-US/go-btfs/protos/renter"

	cmds "github.com/TRON-US/go-btfs-cmds"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p-core/peer"
	cmap "github.com/orcaman/concurrent-map"
)

const (
	replicationFactorOptionName      = "replication-factor"
	hostSelectModeOptionName         = "host-select-mode"
	hostSelectionOptionName          = "host-selection"
	testOnlyOptionName               = "host-search-local"
	customizedPayoutOptionName       = "customize-payout"
	customizedPayoutPeriodOptionName = "customize-payout-period"

	defaultRepFactor     = 3
	defaultStorageLength = 30
)

var (
	shardErrChanMap = cmap.New()
)

var StorageUploadCmd = &cmds.Command{
	Helptext: cmds.HelpText{
		Tagline: "Store files on BTFS network nodes through BTT payment.",
		ShortDescription: `
By default, BTFS selects hosts based on overall score according to the current client's environment.
To upload a file, <file-hash> must refer to a reed-solomon encoded file.

To create a reed-solomon encoded file from a normal file:

    $ btfs add --chunker=reed-solomon <file>
    added <file-hash> <file>

Run command to upload:

    $ btfs storage upload <file-hash>

To custom upload and storage a file on specific hosts:
    Use -m with 'custom' mode, and put host identifiers in -s, with multiple hosts separated by ','.

    # Upload a file to a set of hosts
    # Total # of hosts (N) must match # of shards in the first DAG level of root file hash
    $ btfs storage upload <file-hash> -m=custom -s=<host1-peer-id>,<host2-peer-id>,...,<hostN-peer-id>

    # Upload specific shards to a set of hosts
    # Total # of hosts (N) must match # of shards given
    $ btfs storage upload <shard-hash1> <shard-hash2> ... <shard-hashN> -l -m=custom -s=<host1-peer-id>,<host2-peer-id>,...,<hostN-peer-id>

Use status command to check for completion:
    $ btfs storage upload status <session-id> | jq`,
	},
	Subcommands: map[string]*cmds.Command{
		"init":              StorageUploadInitCmd,
		"recvcontract":      StorageUploadRecvContractCmd,
		"status":            StorageUploadStatusCmd,
		"repair":            storageUploadRepairCmd,
		"getcontractbatch":  storageUploadGetContractBatchCmd,
		"signcontractbatch": storageUploadSignContractBatchCmd,
		"getunsigned":       storageUploadGetUnsignedCmd,
		"sign":              storageUploadSignCmd,
	},
	Arguments: []cmds.Argument{
		cmds.StringArg("file-hash", true, false, "Hash of file to upload."),
		cmds.StringArg("upload-peer-id", false, false, "Peer id when upload upload."),
		cmds.StringArg("upload-nonce-ts", false, false, "Nounce timestamp when upload upload."),
		cmds.StringArg("upload-signature", false, false, "Session signature when upload upload."),
	},
	Options: []cmds.Option{
		cmds.Int64Option(uploadPriceOptionName, "p", "Max price per GiB per day of storage in JUST."),
		cmds.IntOption(replicationFactorOptionName, "r", "Replication factor for the file with erasure coding built-in.").WithDefault(defaultRepFactor),
		cmds.StringOption(hostSelectModeOptionName, "m", "Based on this mode to select hosts and upload automatically. Default: mode set in config option Experimental.HostsSyncMode."),
		cmds.StringOption(hostSelectionOptionName, "s", "Use only these selected hosts in order on 'custom' mode. Use ',' as delimiter."),
		cmds.BoolOption(testOnlyOptionName, "t", "Enable host search under all domains 0.0.0.0 (useful for local test)."),
		cmds.IntOption(storageLengthOptionName, "len", "File storage period on hosts in days.").WithDefault(defaultStorageLength),
		cmds.BoolOption(customizedPayoutOptionName, "Enable file storage customized payout schedule.").WithDefault(false),
		cmds.IntOption(customizedPayoutPeriodOptionName, "Period of customized payout schedule.").WithDefault(1),
	},
	RunTimeout: 15 * time.Minute,
	Run: func(req *cmds.Request, res cmds.ResponseEmitter, env cmds.Environment) error {
		ssId := uuid.New().String()
		ctxParams, err := ExtractContextParams(req, env)
		if err != nil {
			return err
		}
		fileHash := req.Arguments[0]
		renterId := ctxParams.n.Identity
		offlineSigning := false
		if len(req.Arguments) > 1 {
			renterId, err = peer.IDB58Decode(req.Arguments[1])
			if err != nil {
				return err
			}
			offlineSigning = true
		}
		shardHashes, fileSize, shardSize, err := getShardHashes(ctxParams, fileHash)
		if err != nil {
			return err
		}
		price, storageLength, err := getPriceAndMinStorageLength(ctxParams)
		if err != nil {
			return err
		}
		hp := getHostsProvider(ctxParams, make([]string, 0))
		rss, err := GetRenterSession(ctxParams, ssId, fileHash, shardHashes)
		if err != nil {
			return err
		}
		if offlineSigning {
			offNonceTimestamp, err := strconv.ParseUint(req.Arguments[2], 10, 64)
			if err != nil {
				return err
			}
			err = rss.saveOfflineMeta(&renterpb.OfflineMeta{
				OfflinePeerId:    req.Arguments[1],
				OfflineNonceTs:   offNonceTimestamp,
				OfflineSignature: req.Arguments[3],
			})
			if err != nil {
				return err
			}
		}
		shardIndexes := make([]int, 0)
		for i, _ := range rss.shardHashes {
			shardIndexes = append(shardIndexes, i)
		}
		rss.uploadShard(hp, price, shardSize, storageLength, offlineSigning, renterId, fileSize, shardIndexes, nil)
		seRes := &Res{
			ID: ssId,
		}
		return res.Emit(seRes)
	},
	Type: Res{},
}

type Res struct {
	ID string
}
