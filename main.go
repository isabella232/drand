// drand is a distributed randomness beacon. It provides periodically an
// unpredictable, bias-resistant, and verifiable random value.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/drand/drand/core"
	"github.com/drand/drand/fs"
	"github.com/drand/drand/key"
	"github.com/drand/drand/log"
	"github.com/drand/drand/net"
	"github.com/drand/drand/protobuf/drand"
	"github.com/nikkolasg/slog"
	"github.com/urfave/cli/v2"
)

// Automatically set through -ldflags
// Example: go install -ldflags "-X main.version=`git describe --tags` -X main.buildDate=`date -u +%d/%m/%Y@%H:%M:%S` -X main.gitCommit=`git rev-parse HEAD`"
var (
	version   = "dev"
	gitCommit = "none"
	buildDate = "unknown"
)

const gname = "group.toml"
const dpublic = "dist_key.public"
const defaultPort = "8080"

func banner() {
	fmt.Printf("drand %v (date %v, commit %v) by nikkolasg\n", version, buildDate, gitCommit)
	s := "WARNING: this software has NOT received a full audit and must be used with caution and probably NOT in a production environment.\n"
	fmt.Printf(s)
}

var folderFlag = &cli.StringFlag{
	Name:  "folder",
	Value: core.DefaultConfigFolder(),
	Usage: "Folder to keep all drand cryptographic information, with absolute path.",
}

var leaderFlag = &cli.BoolFlag{
	Name:  "leader",
	Usage: "Set this node as the initator of the distributed key generation process.",
}

var verboseFlag = &cli.BoolFlag{
	Name:  "verbose",
	Usage: "If set, verbosity is at the debug level",
}

var tlsCertFlag = &cli.StringFlag{
	Name: "tls-cert",
	Usage: "Set the TLS certificate chain (in PEM format) for this drand node. " +
		"The certificates have to be specified as a list of whitespace-separated file paths. " +
		"This parameter is required by default and can only be omitted if the --tls-disable flag is used.",
}

var tlsKeyFlag = &cli.StringFlag{
	Name: "tls-key",
	Usage: "Set the TLS private key (in PEM format) for this drand node. " +
		"The key has to be specified as a file path. " +
		"This parameter is required by default and can only be omitted if the --tls-disable flag is used.",
}

var insecureFlag = &cli.BoolFlag{
	Name:  "tls-disable",
	Usage: "Disable TLS for all communications (not recommended).",
}

var controlFlag = &cli.StringFlag{
	Name:  "control",
	Usage: "Set the port you want to listen to for control port commands. If not specified, we will use the default port 8888.",
}

var listenFlag = &cli.StringFlag{
	Name:  "listen",
	Usage: "Set the listening (binding) address. Useful if you have some kind of proxy.",
}

var nodeFlag = &cli.StringFlag{
	Name:  "nodes",
	Usage: "Contact the nodes at the given list of whitespace-separated addresses which have to be present in group.toml.",
}

var roundFlag = &cli.IntFlag{
	Name:  "round",
	Usage: "Request the public randomness generated at round num. If the drand beacon does not have the requested value, it returns an error. If not specified, the current randomness is returned.",
}

var fromGroupFlag = &cli.StringFlag{
	Name:  "from",
	Usage: "If you want to replace keys into an existing group.toml file to perform a resharing later on, run the group command and specify the existing group.toml file with this flag.",
}

var certsDirFlag = &cli.StringFlag{
	Name:  "certs-dir",
	Usage: "directory containing trusted certificates. Useful for testing and self signed certificates",
}

var outFlag = &cli.StringFlag{
	Name: "out",
	Usage: "save the requested information into a separate file" +
		" instead of stdout",
}

var periodFlag = &cli.StringFlag{
	Name:  "period",
	Usage: "period to write in the group.toml file",
}

var thresholdFlag = &cli.IntFlag{
	Name:  "threshold",
	Usage: "threshold to set on the group file",
}

var genesisFlag = &cli.Int64Flag{
	Name:  "genesis",
	Usage: "genesis time to set on the group file - unix time",
}

var transitionFlag = &cli.Int64Flag{
	Name:  "transition",
	Usage: "transition time from the current network to the new network",
}

// XXX deleted flags : debugFlag, outFlag, groupFlag, seedFlag, periodFlag, distKeyFlag, thresholdFlag.

var oldGroupFlag = &cli.StringFlag{
	Name: "from",
	Usage: "Old group.toml path to specify when a new node wishes to participate " +
		"in a resharing protocol. This flag is optional in case a node is already" +
		"included in the current DKG.",
}

var timeoutFlag = &cli.StringFlag{
	Name:  "timeout",
	Usage: fmt.Sprintf("Timeout to use during the DKG, in string format. Default is %s", core.DefaultDKGTimeout),
}

var pushFlag = &cli.BoolFlag{
	Name:  "push",
	Usage: "Push mode forces the daemon to start making beacon requests to the other node, instead of waiting the other nodes contact it to catch-up on the round",
}

var sourceFlag = &cli.StringFlag{
	Name:  "source",
	Usage: "Source flag allows to provide an executable which output will be used as additional entropy during resharing step.",
}

var userEntropyOnlyFlag = &cli.BoolFlag{
	Name:  "user-source-only",
	Usage: "user-source-only flag used with the source flag allows to only use the user's entropy to pick the dkg secret (won't be mixed with crypto/rand). Should be used for reproducibility and debbuging purposes.",
}

var startInFlag = &cli.StringFlag{
	Name:  "start-in",
	Usage: "Duration to parse in which the setup or resharing phase will start. This flags sets the `GenesisTime` or `TransitionTime` in `start-in` period from now.",
}

func main() {
	app := cli.NewApp()

	cli.VersionPrinter = func(c *cli.Context) {
		fmt.Printf("drand %v (date %v, commit %v) by nikkolasg\n", version, buildDate, gitCommit)
	}

	app.Version = version
	app.Usage = "distributed randomness service"
	// =====Commands=====
	app.Commands = []*cli.Command{
		&cli.Command{
			Name:  "start",
			Usage: "Start the drand daemon.",
			Flags: toArray(folderFlag, tlsCertFlag, tlsKeyFlag,
				insecureFlag, controlFlag, listenFlag,
				certsDirFlag, pushFlag, verboseFlag),
			Action: func(c *cli.Context) error {
				banner()
				return startCmd(c)
			},
		},
		&cli.Command{
			Name:  "stop",
			Usage: "Stop the drand daemon.\n",
			Flags: toArray(controlFlag),
			Action: func(c *cli.Context) error {
				banner()
				return stopDaemon(c)
			},
		},
		&cli.Command{
			Name: "share",
			Usage: "Launch a sharing protocol. If one group is given as " +
				"argument, drand launches a DKG protocol to create a distributed " +
				"keypair between all participants listed in the group. An " +
				"existing group can also issue new shares to a new group: use " +
				"the flag --from to specify the current group and give " +
				"the new group as argument. Specify the --leader flag to make " +
				"this daemon start the protocol\n",
			ArgsUsage: "<group.toml> group file",
			Flags: toArray(folderFlag, insecureFlag, controlFlag,
				leaderFlag, oldGroupFlag, timeoutFlag, sourceFlag, userEntropyOnlyFlag),
			Action: func(c *cli.Context) error {
				banner()
				return shareCmd(c)
			},
		},
		&cli.Command{
			Name: "generate-keypair",
			Usage: "Generate the longterm keypair (drand.private, drand.public)" +
				"for this node.\n",
			ArgsUsage: "<address> is the public address for other nodes to contact",
			Flags:     toArray(folderFlag, insecureFlag),
			Action: func(c *cli.Context) error {
				banner()
				return keygenCmd(c)
			},
		},
		&cli.Command{
			Name: "group",
			Usage: "Merge the given list of whitespace-separated drand.public " +
				"keys into the group.toml file if one is provided, if not, create " +
				"a new group.toml file with the given identites.\n",
			ArgsUsage: "<key1 key2 key3...> must be the identities of the group " +
				"to create/to insert into the group",
			Flags: toArray(folderFlag, outFlag, periodFlag, thresholdFlag, genesisFlag, transitionFlag, fromGroupFlag, startInFlag),
			Action: func(c *cli.Context) error {
				banner()
				return groupCmd(c)
			},
		},
		&cli.Command{
			Name:  "check-group",
			Usage: "Check node in the group for accessibility over the gRPC communication",
			Flags: toArray(certsDirFlag),
			Action: func(c *cli.Context) error {
				banner()
				return checkGroup(c)
			},
		},
		{
			Name: "get",
			Usage: "get allows for public information retrieval from a remote " +
				"drand node.\n",
			Subcommands: []*cli.Command{
				{
					Name: "private",
					Usage: "Get private randomness from the drand beacon as " +
						"specified in group.toml. Only one node is contacted by " +
						"default. Requests are ECIES-encrypted towards the public " +
						"key of the contacted node. This command attempts to connect " +
						"to the drand beacon via TLS and falls back to " +
						"plaintext communication if the contacted node has not " +
						"activated TLS in which case it prints a warning.\n",
					ArgsUsage: "<group.toml> provides the group informations of " +
						"the nodes that we are trying to contact.",
					Flags: toArray(insecureFlag, tlsCertFlag, nodeFlag),
					Action: func(c *cli.Context) error {
						return getPrivateCmd(c)
					},
				},
				{
					Name: "public",
					Usage: "Get the latest public randomness from the drand " +
						"beacon and verify it against the collective public key " +
						"as specified in group.toml. Only one node is contacted by " +
						"default. This command attempts to connect to the drand " +
						"beacon via TLS and falls back to plaintext communication " +
						"if the contacted node has not activated TLS in which case " +
						"it prints a warning.\n",
					Flags: toArray(tlsCertFlag, insecureFlag, roundFlag, nodeFlag),
					Action: func(c *cli.Context) error {
						return getPublicRandomness(c)
					},
				},
				{
					Name: "cokey",
					Usage: "Get distributed public key generated during the " +
						"DKG step.",
					ArgsUsage: "<group.toml> provides the group informations of " +
						"the node that we are trying to contact.",
					Flags: toArray(tlsCertFlag, insecureFlag, nodeFlag),
					Action: func(c *cli.Context) error {
						return getCokeyCmd(c)
					},
				},
			},
		},
		{
			Name:  "ping",
			Usage: "pings the daemon checking its state\n",
			Flags: toArray(controlFlag),
			Action: func(c *cli.Context) error {
				return pingpongCmd(c)
			},
		},
		{
			Name:  "reset",
			Usage: "Resets the local distributed information (share, group file and random beacons). It KEEPS the private/public key pair.",
			Flags: toArray(folderFlag, controlFlag),
			Action: func(c *cli.Context) error {
				return resetCmd(c)
			},
		},
		{
			Name: "show",
			Usage: "local information retrieval about the node's cryptographic " +
				"material. Show prints the information about the collective " +
				"public key (drand.cokey), the group details (group.toml), the " +
				"long-term private key (drand.private), the long-term public key " +
				"(drand.public), or the private key share (drand.share), " +
				"respectively.\n",
			Flags: toArray(folderFlag, controlFlag),
			Subcommands: []*cli.Command{
				{
					Name:  "share",
					Usage: "shows the private share\n",
					Flags: toArray(controlFlag),
					Action: func(c *cli.Context) error {
						return showShareCmd(c)
					},
				},
				{
					Name: "group",
					Usage: "shows the current group.toml used. The group.toml " +
						"may contain the distributed public key if the DKG has been " +
						"ran already.\n",
					Flags: toArray(outFlag, controlFlag),
					Action: func(c *cli.Context) error {
						return showGroupCmd(c)
					},
				},
				{
					Name:  "cokey",
					Usage: "shows the collective key generated during DKG.\n",
					Flags: toArray(controlFlag),
					Action: func(c *cli.Context) error {
						return showCokeyCmd(c)
					},
				},
				{
					Name:  "private",
					Usage: "shows the long-term private key of a node.\n",
					Flags: toArray(controlFlag),
					Action: func(c *cli.Context) error {
						return showPrivateCmd(c)
					},
				},
				{
					Name:  "public",
					Usage: "shows the long-term public key of a node.\n",
					Flags: toArray(controlFlag),
					Action: func(c *cli.Context) error {
						return showPublicCmd(c)
					},
				},
			},
		},
	}
	app.Flags = toArray(verboseFlag, folderFlag)
	app.Before = func(c *cli.Context) error {
		testWindows(c)
		return nil
	}
	if err := app.Run(os.Args); err != nil {
		slog.Fatalf("drand: error running app: %s", err)
	}
}

func resetCmd(c *cli.Context) error {
	conf := contextToConfig(c)
	fmt.Printf("You are about to delete your local share, group file and generated random beacons. Are you sure you wish to perform this operation? [y/N]")
	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil {
		slog.Fatal("error reading: ", err)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" {
		slog.Print("drand: not reseting the state.")
		return nil
	}
	store := key.NewFileStore(conf.ConfigFolder())
	if err := store.Reset(); err != nil {
		fmt.Printf("drand: err reseting key store: %v\n", err)
		os.Exit(1)
	}
	if err := os.RemoveAll(conf.DBFolder()); err != nil {
		fmt.Printf("drand: err reseting beacons database: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("drand: database reset")
	return nil
}

func resetBeaconDB(config *core.Config) bool {
	if _, err := os.Stat(config.DBFolder()); err == nil {
		fmt.Printf("INCONSISTENT STATE: A beacon database exists already.\n"+
			"drand support only one identity at the time and thus needs to delete "+
			"the existing beacon database.\nCurrent folder is %s.\nAccept to delete "+
			"database ? [Y/n]: ", config.DBFolder())
		reader := bufio.NewReader(os.Stdin)
		answer, err := reader.ReadString('\n')
		if err != nil {
			slog.Fatal("error reading: ", err)
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" {
			slog.Print("drand: not deleting the database.")
			return true
		}

		if err := os.RemoveAll(config.DBFolder()); err != nil {
			slog.Fatal(err)
		}
		slog.Print("drand: removed existing beacon database.")
	}
	return false
}

func askPort() string {
	for {
		var port string
		slog.Print("No valid port given. Please, choose a port number (or ENTER for default port 8080): ")
		if _, err := fmt.Scanf("%s\n", &port); err != nil {
			continue
		}
		if port == "" {
			return defaultPort
		}
		_, err := strconv.Atoi(port)
		if len(port) > 2 && len(port) < 5 && err == nil {
			return port
		}
		return askPort()
	}
}

func testWindows(c *cli.Context) {
	//x509 not available on windows: must run without TLS
	if runtime.GOOS == "windows" && !c.Bool("tls-disable") {
		fatal("TLS is not available on Windows, please disable TLS")
	}
}

func fatal(str string, args ...interface{}) {
	fmt.Printf(str+"\n", args...)
	os.Exit(1)
}

func keygenCmd(c *cli.Context) error {
	args := c.Args()
	if !args.Present() {
		fatal("Missing drand address in argument. Abort.")
	}
	addr := args.First()
	var validID = regexp.MustCompile(`[:][0-9]+$`)
	if !validID.MatchString(addr) {
		fmt.Println("Invalid port.")
		addr = addr + ":" + askPort()
	}
	var priv *key.Pair
	if c.Bool("tls-disable") {
		fmt.Println("Generating private / public key pair without TLS.")
		priv = key.NewKeyPair(addr)
	} else {
		fmt.Println("Generating private / public key pair with TLS indication")
		priv = key.NewTLSKeyPair(addr)
	}

	config := contextToConfig(c)
	fs := key.NewFileStore(config.ConfigFolder())

	if _, err := fs.LoadKeyPair(); err == nil {
		fmt.Printf("Keypair already present in `%s`.\nRemove them before generating new one\n", config.ConfigFolder())
		return nil
	}
	if err := fs.SaveKeyPair(priv); err != nil {
		fatal("could not save key: ", err)
	}
	fullpath := path.Join(config.ConfigFolder(), key.KeyFolderName)
	absPath, err := filepath.Abs(fullpath)
	if err != nil {
		fatal("err getting full path: ", err)
	}
	fmt.Println("Generated keys at ", absPath)
	fmt.Println("You can copy paste the following snippet to a common group.toml file:")
	var buff bytes.Buffer
	buff.WriteString("[[Nodes]]\n")
	if err := toml.NewEncoder(&buff).Encode(priv.Public.TOML()); err != nil {
		panic(err)
	}
	buff.WriteString("\n")
	fmt.Println(buff.String())
	fmt.Println("Or just collect all public key files and use the group command!")
	return nil
}

func groupCmd(c *cli.Context) error {
	isResharing := c.IsSet(fromGroupFlag.Name)
	if !c.Args().Present() || (c.NArg() < 3 && !isResharing) {
		fatal("drand: group command take at least 3 keys as arguments")
	}
	if isResharing {
		// separate the logic
		return groupReshareCmd(c)
	}
	threshold := getThreshold(c)
	publics := getPublicKeys(c)
	var period = core.DefaultBeaconPeriod
	var err error
	if c.IsSet(periodFlag.Name) {
		period, err = time.ParseDuration(c.String(periodFlag.Name))
		if err != nil {
			fatal("drand: invalid period time given %s", err)
		}
	}
	genesis, err := getStartIn(c)
	if err != nil {
		if !c.IsSet(genesisFlag.Name) {
			fatal("no start-in flag nor genesis flag specified")
		}
		genesis = c.Int64(genesisFlag.Name)
	}
	if genesis <= time.Now().Unix() {
		fatal("drand: genesis time in the past or not specified")
	}
	fmt.Printf("Creating the new group file at time %s\n", time.Unix(genesis, 0).String())
	group := key.NewGroup(publics, threshold, genesis)
	group.Period = period
	groupOut(c, group)
	return nil
}

func getStartIn(c *cli.Context) (int64, error) {
	if !c.IsSet(startInFlag.Name) {
		return 0, errors.New("no start-in flag specified")
	}
	periodStr := c.String(startInFlag.Name)
	period, err := time.ParseDuration(periodStr)
	if err != nil {
		return 0, err
	}
	genesis := time.Now().Add(period)
	return genesis.Unix(), nil
}
func groupReshareCmd(c *cli.Context) error {
	// check if transition time is specified, otherwise we can't go on
	transitionTime, err := getStartIn(c)
	if err != nil {
		if !c.IsSet(transitionFlag.Name) {
			fatal("For a creating a group from a current group, transition time flag is required")
		}
		transitionTime = c.Int64(transitionFlag.Name)
	}
	if transitionTime <= time.Now().Unix() {
		fatal("transition time in the past/current")
	}

	fmt.Printf("Creating a new group for resharing at time %s\n", time.Unix(transitionTime, 0).String())
	oldGroupPath := c.String(fromGroupFlag.Name)
	group := new(key.Group)
	if err := key.Load(oldGroupPath, group); err != nil {
		return err
	}
	if len(group.GenesisSeed) == 0 {
		return errors.New("old group has an empty genesis seed")
	}
	newT := getThreshold(c)
	newNodes := getPublicKeys(c)

	// XXX Refactor that logic into group
	newGroup := key.NewGroup(newNodes, newT, group.GenesisTime)
	// NOTE: for now we keep the same period as the old group, changing period
	// for the same group is not implemented yet
	newGroup.Period = group.Period
	newGroup.GenesisSeed = group.GetGenesisSeed()
	newGroup.TransitionTime = c.Int64(transitionFlag.Name)

	groupOut(c, newGroup)
	return nil
}

func groupOut(c *cli.Context, group *key.Group) {
	if c.IsSet("out") {
		groupPath := c.String("out")
		if err := key.Save(groupPath, group, false); err != nil {
			fatal("drand: can't save group to specified file name: %v", err)
		}
	} else {
		var buff bytes.Buffer
		if err := toml.NewEncoder(&buff).Encode(group.TOML()); err != nil {
			fatal("drand: can't encode group to TOML: %v", err)
		}
		buff.WriteString("\n")
		fmt.Printf("Copy the following snippet into a new group.toml file " +
			"and distribute it to all the participants:\n")
		fmt.Printf(buff.String())
	}
}

func getThreshold(c *cli.Context) int {
	var threshold = key.DefaultThreshold(c.NArg())
	if c.IsSet(thresholdFlag.Name) {
		var localThr = c.Int(thresholdFlag.Name)
		if localThr < threshold {
			fatal(fmt.Sprintf("drand: threshold specified too low %d/%d", localThr, threshold))
		}
		return localThr
	}
	return threshold
}

func getPublicKeys(c *cli.Context) []*key.Identity {
	publics := make([]*key.Identity, c.NArg())
	for i, str := range c.Args().Slice() {
		pub := &key.Identity{}
		fmt.Printf("drand: reading public identity from %s\n", str)
		if err := key.Load(str, pub); err != nil {
			fatal("drand: can't load key %d: %v", i, err)
		}
		publics[i] = pub
	}
	return publics
}
func checkGroup(c *cli.Context) error {
	if !c.Args().Present() {
		fatal("drand: check-group expects a group argument")
	}
	conf := contextToConfig(c)
	testEmptyGroup(c.Args().First())
	group := new(key.Group)
	if err := key.Load(c.Args().First(), group); err != nil {
		fatal("drand: loading group failed")
	}
	var allGood = true
	var invalidIds []string
	for _, id := range group.Nodes {
		client := net.NewGrpcClientFromCertManager(conf.Certs())
		_, err := client.Home(id, &drand.HomeRequest{})
		if err != nil {
			fmt.Printf("drand: error checking id %s: %s\n", id.Address(), err)
			allGood = false
			invalidIds = append(invalidIds, id.Address())
			continue
		}
		fmt.Printf("drand: id %s answers correctly\n", id.Address())
	}
	if !allGood {
		return fmt.Errorf("Following nodes don't answer: %s", strings.Join(invalidIds, " ,"))
	}
	return nil
}

func toArray(flags ...cli.Flag) []cli.Flag {
	return flags
}

func getGroup(c *cli.Context) *key.Group {
	g := &key.Group{}
	groupPath := c.Args().First()
	testEmptyGroup(groupPath)
	if err := key.Load(groupPath, g); err != nil {
		fatal("drand: error loading group file: %s", err)
	}
	return g
}

// keyIDFromAddr looks at every node in the group file to retrieve to *key.Identity
func keyIDFromAddr(addr string, group *key.Group) *key.Identity {
	ids := group.Identities()
	for _, id := range ids {
		if id.Address() == addr {
			return id
		}
	}
	fatal("Could not retrive the node you are trying to contact in the group file.")
	return nil
}

func contextToConfig(c *cli.Context) *core.Config {
	var opts []core.ConfigOption

	if c.IsSet(verboseFlag.Name) {
		opts = append(opts, core.WithLogLevel(log.LogDebug))
	} else {
		opts = append(opts, core.WithLogLevel(log.LogInfo))
	}

	listen := c.String("listen")
	if listen != "" {
		opts = append(opts, core.WithListenAddress(listen))
	}
	port := c.String(controlFlag.Name)
	if port != "" {
		opts = append(opts, core.WithControlPort(port))
	}
	config := c.String(folderFlag.Name)
	opts = append(opts, core.WithConfigFolder(config))

	if c.Bool("tls-disable") {
		opts = append(opts, core.WithInsecure())
		if c.IsSet("tls-cert") || c.IsSet("tls-key") {
			panic("option 'tls-disable' used with 'tls-cert' or 'tls-key': combination is not valid")
		}
	} else {
		certPath, keyPath := c.String("tls-cert"), c.String("tls-key")
		opts = append(opts, core.WithTLS(certPath, keyPath))
	}
	if c.IsSet("certs-dir") {
		paths, err := fs.Files(c.String("certs-dir"))
		if err != nil {
			panic(err)
		}
		opts = append(opts, core.WithTrustedCerts(paths...))
	}
	conf := core.NewConfig(opts...)
	return conf
}

func getNodes(c *cli.Context) []*key.Identity {
	group := getGroup(c)
	var ids []*key.Identity
	gids := group.Identities()
	if c.IsSet("nodes") {
		// search nodes listed on the flag in the group
		for _, addr := range strings.Split(c.String("nodes"), ",") {
			for _, gid := range gids {
				if gid.Addr == addr {
					ids = append(ids, gid)
				}
			}
		}
		if len(ids) == 0 {
			fatal("drand: addresses specified don't exist in group.toml")
		}
	} else {
		// select them all in order
		ids = gids
	}
	if len(ids) == 0 {
		fatal("drand: no nodes specified with --nodes are in the group file")
	}
	return ids
}

func testEmptyGroup(path string) {
	file, err := os.Open(path)
	defer file.Close()
	if err != nil {
		fatal("drand: can't opern group path: %v", err)
	}
	fi, err := file.Stat()
	if err != nil {
		fatal("drand: can't open file info: %v", err)
	}
	if fi.Size() == 0 {
		fatal("drand: group file empty")
	}
}
