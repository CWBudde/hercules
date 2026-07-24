package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"path/filepath"
	"plugin"
	"regexp"
	"runtime/pprof"
	"sort"
	"strings"
	"text/template"
	"time"
	"unicode"

	"github.com/Masterminds/sprig"
	"github.com/cwbudde/hercules"
	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/pb"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	sivafs "github.com/cyraxred/go-billy-siva"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/go-git/go-git/v5/storage"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/gogo/protobuf/proto"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/crypto/ssh/terminal"
	progress "gopkg.in/cheggaaa/pb.v1"
)

type progressMode string

const (
	progressModeAuto  progressMode = "auto"
	progressModeBar   progressMode = "bar"
	progressModeLines progressMode = "lines"
	progressModeJSON  progressMode = "json"
	progressModeNone  progressMode = "none"

	progressEventWriteStart = "write-start"
)

type progressEvent struct {
	Event  string `json:"event"`
	Commit int    `json:"commit,omitempty"`
	Total  int    `json:"total,omitempty"`
	Action string `json:"action,omitempty"`
	Repo   string `json:"repo,omitempty"`
	Output string `json:"output,omitempty"`
}

func parseProgressMode(raw string) (progressMode, error) {
	switch progressMode(strings.ToLower(strings.TrimSpace(raw))) {
	case progressModeAuto:
		return progressModeAuto, nil
	case progressModeBar:
		return progressModeBar, nil
	case progressModeLines:
		return progressModeLines, nil
	case progressModeJSON:
		return progressModeJSON, nil
	case progressModeNone, "off":
		return progressModeNone, nil
	default:
		return "", fmt.Errorf("unknown progress mode %q", raw)
	}
}

func formatProgressEvent(event progressEvent, mode progressMode) (string, error) {
	switch mode {
	case progressModeLines:
		fields := []string{event.Event}
		if event.Commit != 0 {
			fields = append(fields, fmt.Sprintf("commit=%d", event.Commit))
		}
		if event.Total != 0 {
			fields = append(fields, fmt.Sprintf("total=%d", event.Total))
		}
		if event.Action != "" {
			fields = append(fields, "action="+event.Action)
		}
		if event.Repo != "" {
			fields = append(fields, "repo="+event.Repo)
		}
		if event.Output != "" {
			fields = append(fields, "output="+event.Output)
		}
		return strings.Join(fields, " ") + "\n", nil
	case progressModeJSON:
		data, err := json.Marshal(event)
		if err != nil {
			return "", err
		}
		return string(data) + "\n", nil
	default:
		return "", fmt.Errorf("progress mode %q does not support event formatting", mode)
	}
}

// oneLineWriter splits the output data by lines and outputs one on top of another using '\r'.
// It also does some dark magic to handle Git statuses.
type oneLineWriter struct {
	Writer io.Writer
}

func (writer oneLineWriter) Write(p []byte) (n int, err error) {
	strp := strings.TrimSpace(string(p))
	if strings.HasSuffix(strp, "done.") || len(strp) == 0 {
		strp = "cloning..."
	} else {
		strp = strings.Replace(strp, "\n", "\033[2K\r", -1)
	}
	_, err = writer.Writer.Write([]byte("\033[2K\r"))
	if err != nil {
		return n, err
	}
	n, err = writer.Writer.Write([]byte(strp))
	return n, err
}

func loadSSHIdentity(sshIdentity string) (*ssh.PublicKeys, error) {
	actual, err := homedir.Expand(sshIdentity)
	if err != nil {
		return nil, err
	}
	return ssh.NewPublicKeysFromFile("git", actual, "")
}

var regexUri = regexp.MustCompile("^[A-Za-z]\\w*@[A-Za-z0-9][\\w.]*:")

func createStubRepository() (*git.Repository, error) {
	repository, err := git.Init(memory.NewStorage(), memfs.New())
	if err != nil {
		return nil, err
	}
	worktree, err := repository.Worktree()
	if err != nil {
		return nil, err
	}
	// Provide an explicit signature so creating the stub repo does not
	// depend on ambient git user.name/user.email config (absent on fresh
	// CI runners, where go-git errors with "author field is required").
	signature := &object.Signature{Name: "Hercules", Email: "hercules@localhost", When: time.Now()}
	_, err = worktree.Commit("Initial", &git.CommitOptions{
		AllowEmptyCommits: true,
		Author:            signature,
		Committer:         signature,
	})
	if err != nil {
		return nil, fmt.Errorf("create virtual repository: %w", err)
	}
	return repository, nil
}

func loadRepository(uri, cachePath string, disableStatus bool, sshIdentity string,
) (*git.Repository, string, string) {
	repository, repoURI, repoFeature, err := loadRepositoryWithError(
		uri, cachePath, disableStatus, sshIdentity,
	)
	if err != nil {
		log.Panicf("failed to open %s: %v", uri, err)
	}
	return repository, repoURI, repoFeature
}

func loadRepositoryWithError(uri, cachePath string, disableStatus bool, sshIdentity string,
) (*git.Repository, string, string, error) {
	switch {
	case uri == "-" && cachePath == "":
		repository, err := createStubRepository()
		return repository, uri, core.FeatureGitStub, err
	case strings.Contains(uri, "://") || regexUri.MatchString(uri):
		repository, repoURI, err := cloneRemoteRepository(uri, cachePath, disableStatus, sshIdentity)
		return repository, repoURI, core.FeatureGitCommits, err
	case isSivaRepository(uri):
		repository, err := openSivaRepository(uri)
		return repository, uri, core.FeatureGitCommits, err
	default:
		uri = strings.TrimSuffix(uri, string(os.PathSeparator))
		repository, err := git.PlainOpen(uri)
		return repository, uri, core.FeatureGitCommits, err
	}
}

func cloneRemoteRepository(uri, cachePath string, disableStatus bool, sshIdentity string,
) (*git.Repository, string, error) {
	backend := remoteCloneStorage(cachePath)
	cloneOptions := &git.CloneOptions{URL: uri}
	repoURI := sanitizedRepositoryURI(uri)

	if !disableStatus {
		_, _ = fmt.Fprint(os.Stderr, "connecting...\r")
		cloneOptions.Progress = oneLineWriter{Writer: os.Stderr}
	}
	if sshIdentity != "" {
		auth, err := loadSSHIdentity(sshIdentity)
		if err != nil {
			log.Printf("Failed loading SSH Identity: %v\n", err)
		} else {
			cloneOptions.Auth = auth
		}
	}

	repository, err := git.Clone(backend, nil, cloneOptions)
	if !disableStatus {
		_, _ = fmt.Fprint(os.Stderr, "\033[2K\r")
	}
	return repository, repoURI, err
}

func remoteCloneStorage(cachePath string) storage.Storer {
	if cachePath == "" {
		return memory.NewStorage()
	}
	backend := filesystem.NewStorage(osfs.New(cachePath), cache.NewObjectLRUDefault())
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		log.Printf("warning: deleted %s\n", cachePath)
		_ = os.RemoveAll(cachePath)
	}
	return backend
}

func sanitizedRepositoryURI(uri string) string {
	parsed, err := url.Parse(uri)
	if err == nil && parsed.User != nil {
		parsed.User = nil
		return parsed.String()
	}
	return uri
}

func isSivaRepository(uri string) bool {
	stat, err := os.Stat(uri)
	return err == nil && !stat.IsDir()
}

func openSivaRepository(uri string) (*git.Repository, error) {
	localFS := osfs.New(filepath.Dir(uri))
	tempFS := memfs.New()
	fs, err := sivafs.NewFilesystem(localFS, filepath.Base(uri), tempFS)
	if err != nil {
		return nil, fmt.Errorf("create siva filesystem from %s: %w", uri, err)
	}
	sivaStorage := filesystem.NewStorage(fs, cache.NewObjectLRUDefault())
	return git.Open(sivaStorage, tempFS)
}

type arrayPluginFlags map[string]bool

func (apf *arrayPluginFlags) String() string {
	var list []string
	for key := range *apf {
		list = append(list, key)
	}
	return strings.Join(list, ", ")
}

func (apf *arrayPluginFlags) Set(value string) error {
	(*apf)[value] = true
	return nil
}

func (apf *arrayPluginFlags) Type() string {
	return "path"
}

func loadPlugins() {
	pluginFlags := arrayPluginFlags{}
	fs := pflag.NewFlagSet(os.Args[0], pflag.ContinueOnError)
	fs.SetOutput(ioutil.Discard)
	pluginFlagName := "plugin"
	const pluginDesc = "Load the specified plugin by the full or relative path. " +
		"Can be specified multiple times. Requires a hercules binary built with " +
		"CGO_ENABLED=1 (the default build is cgo-free and cannot load plugins); see PLUGINS.md."
	fs.Var(&pluginFlags, pluginFlagName, pluginDesc)
	err := cobra.MarkFlagFilename(fs, "plugin")
	if err != nil {
		panic(err)
	}
	pflag.Var(&pluginFlags, pluginFlagName, pluginDesc)
	_ = fs.Parse(os.Args[1:])
	for path := range pluginFlags {
		_, err := plugin.Open(path)
		if err != nil {
			log.Printf("Failed to load plugin from %s %s\n", path, err)
		}
	}
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "hercules",
	Short: "Analyse a Git repository.",
	Long: `Hercules is a flexible and fast Git repository analysis engine. The base command executes
the commit processing pipeline which is automatically generated from the dependencies of one
or several analysis targets. The list of the available targets is printed in --help. External
	targets can be added using the --plugin system.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runRoot,
}

type rootOptions struct {
	firstParent        bool
	commitsFile        string
	head               bool
	protobuf           bool
	profile            bool
	quiet              bool
	identityAudit      bool
	peopleDictTemplate string
	progress           progressMode
	sshIdentity        string
	uri                string
	cachePath          string
}

type commandFlagReader struct {
	flags *pflag.FlagSet
	err   error
}

func (reader *commandFlagReader) bool(name string) bool {
	if reader.err != nil {
		return false
	}
	value, err := reader.flags.GetBool(name)
	reader.err = err
	return value
}

func (reader *commandFlagReader) string(name string) string {
	if reader.err != nil {
		return ""
	}
	value, err := reader.flags.GetString(name)
	reader.err = err
	return value
}

func (reader *commandFlagReader) stringSlice(name string) []string {
	if reader.err != nil {
		return nil
	}
	value, err := reader.flags.GetStringSlice(name)
	reader.err = err
	return value
}

func (reader *commandFlagReader) stringArray(name string) []string {
	if reader.err != nil {
		return nil
	}
	value, err := reader.flags.GetStringArray(name)
	reader.err = err
	return value
}

func readRootOptions(cmd *cobra.Command, args []string) (rootOptions, error) {
	flags := cmd.Flags()
	applyPreset(flags)
	reader := commandFlagReader{flags: flags}
	firstParent := reader.bool("first-parent")
	commitsFile := reader.string("commits")
	head := reader.bool("head")
	protobuf := reader.bool("pb")
	profile := reader.bool("profile")
	quiet := reader.bool("quiet")
	identityAudit := reader.bool("identity-audit")
	peopleDictTemplate := reader.string("people-dict-template")
	progressValue := reader.string("progress")
	sshIdentity := reader.string("ssh-identity")
	if reader.err != nil {
		return rootOptions{}, reader.err
	}
	progressModeValue, err := parseProgressMode(progressValue)
	if err != nil {
		return rootOptions{}, err
	}
	if quiet && !flags.Changed("progress") {
		progressModeValue = progressModeNone
	} else if progressModeValue == progressModeAuto {
		progressModeValue = progressModeBar
	}

	cachePath := ""
	if len(args) == 2 {
		cachePath = args[1]
	}
	return rootOptions{
		firstParent:        firstParent,
		commitsFile:        commitsFile,
		head:               head,
		protobuf:           protobuf,
		profile:            profile,
		quiet:              quiet,
		identityAudit:      identityAudit,
		peopleDictTemplate: peopleDictTemplate,
		progress:           progressModeValue,
		sshIdentity:        sshIdentity,
		uri:                args[0],
		cachePath:          cachePath,
	}, nil
}

func runRoot(cmd *cobra.Command, args []string) error {
	options, err := readRootOptions(cmd, args)
	if err != nil {
		return err
	}
	stopProfile, err := startRootProfile(options.profile)
	if err != nil {
		return err
	}
	defer stopProfile()

	repository, repoURI, repoFeature, err := loadRepositoryWithError(
		options.uri, options.cachePath, options.quiet, options.sshIdentity,
	)
	if err != nil {
		return fmt.Errorf("open repository %s: %w", options.uri, err)
	}
	pipeline := newRootPipeline(repository, repoFeature)

	reporter := &progressReporter{mode: options.progress, lastCommit: -1}
	if options.progress != progressModeNone {
		pipeline.OnProgress = reporter.update
	}

	if err := loadRootCommits(pipeline, repository, repoURI, repoFeature, options, reporter); err != nil {
		return err
	}
	handled, err := runRequestedIdentityWorkflow(options)
	if err != nil || handled {
		return err
	}

	priorityFn := pipelinePriority(cmd.Flags(), pipeline)
	pipeline.DryRun, _ = cmdlineFacts[hercules.ConfigPipelineDryRun].(bool)
	deployedLeafs := deployItemsToPipeline(pipeline, cmd.Flags(), priorityFn)
	if err := pipeline.InitializeExt(cmdlineFacts, priorityFn, true); err != nil {
		return err
	}
	reporter.emit(progressEvent{Event: "pipeline-initialized", Repo: repoURI})

	results, err := pipeline.RunPreparedPlan()
	if err != nil {
		return fmt.Errorf("run pipeline: %w", err)
	}
	return writeRootResults(options, repoURI, deployedLeafs, results, reporter)
}

func newRootPipeline(repository *git.Repository, repoFeature string) *core.Pipeline {
	pipeline := hercules.NewPipeline(repository)
	if repoFeature != "" {
		pipeline.SetFeature(repoFeature)
	}
	pipeline.SetFeaturesFromFlags()
	return pipeline
}

func startRootProfile(enabled bool) (func(), error) {
	if !enabled {
		return func() {}, nil
	}
	profileFile, err := os.Create("hercules.pprof")
	if err != nil {
		return nil, fmt.Errorf("create CPU profile: %w", err)
	}
	if err := pprof.StartCPUProfile(profileFile); err != nil {
		_ = profileFile.Close()
		return nil, fmt.Errorf("start CPU profile: %w", err)
	}

	server := &http.Server{
		Addr:              "localhost:6060",
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("profiling server: %v", err)
		}
	}()
	return func() {
		pprof.StopCPUProfile()
		if err := profileFile.Close(); err != nil {
			log.Printf("close CPU profile: %v", err)
		}
		if err := server.Close(); err != nil {
			log.Printf("close profiling server: %v", err)
		}
	}, nil
}

type progressReporter struct {
	mode       progressMode
	bar        *progress.ProgressBar
	lastCommit int
}

func (reporter *progressReporter) emit(event progressEvent) {
	if reporter.mode != progressModeLines && reporter.mode != progressModeJSON {
		return
	}
	line, err := formatProgressEvent(event, reporter.mode)
	if err != nil {
		log.Printf("format progress event: %v", err)
		return
	}
	_, _ = os.Stderr.WriteString(line)
}

func (reporter *progressReporter) update(commit, length int, action string) {
	switch reporter.mode {
	case progressModeBar:
		reporter.updateBar(commit, length, action)
	case progressModeLines, progressModeJSON:
		reporter.updateStream(commit, length, action)
	}
}

func (reporter *progressReporter) updateBar(commit, length int, action string) {
	if reporter.bar == nil {
		reporter.bar = progress.New(length)
		reporter.bar.Callback = func(message string) {
			_, _ = os.Stderr.WriteString("\033[2K\r" + message)
		}
		reporter.bar.NotPrint = true
		reporter.bar.ShowPercent = false
		reporter.bar.ShowSpeed = false
		reporter.bar.SetMaxWidth(80).Start()
	}
	if action == hercules.MessageFinalize {
		reporter.bar.Finish()
		_, _ = fmt.Fprint(os.Stderr, "\033[2K\rfinalizing...")
		return
	}
	reporter.bar.Set(commit).Postfix(" [" + action + "] ")
}

func (reporter *progressReporter) updateStream(commit, length int, action string) {
	if action == hercules.MessageFinalize {
		reporter.emit(progressEvent{Event: "finalize", Commit: commit, Total: length, Action: action})
		return
	}
	if commit == 0 || commit == length || commit-reporter.lastCommit >= progressInterval(length) {
		reporter.emit(progressEvent{Event: "commit", Commit: commit, Total: length, Action: action})
		reporter.lastCommit = commit
	}
}

func progressInterval(length int) int {
	if length <= 0 {
		return 100
	}
	interval := length / 20
	if interval < 25 {
		return 25
	}
	if interval > 500 {
		return 500
	}
	return interval
}

func loadRootCommits(
	pipeline *core.Pipeline,
	repository *git.Repository,
	repoURI, repoFeature string,
	options rootOptions,
	reporter *progressReporter,
) error {
	if repoFeature != core.FeatureGitCommits {
		return nil
	}
	commits, err := selectRootCommits(pipeline, repository, repoURI, options, reporter)
	if err != nil {
		return fmt.Errorf("list commits: %w", err)
	}
	cmdlineFacts[hercules.ConfigPipelineCommits] = commits
	return nil
}

func selectRootCommits(
	pipeline *core.Pipeline,
	repository *git.Repository,
	repoURI string,
	options rootOptions,
	reporter *progressReporter,
) ([]*object.Commit, error) {
	if options.commitsFile != "" {
		return hercules.LoadCommitsFromFile(options.commitsFile, repository)
	}
	if options.head {
		return pipeline.HeadCommit()
	}
	if options.progress == progressModeBar {
		_, _ = fmt.Fprint(os.Stderr, "git log...\r")
	} else {
		reporter.emit(progressEvent{Event: "git-log-start", Repo: repoURI})
	}
	commits, err := pipeline.Commits(options.firstParent)
	reporter.emit(progressEvent{Event: "git-log-done", Repo: repoURI, Total: len(commits)})
	return commits, err
}

func runRequestedIdentityWorkflow(options rootOptions) (bool, error) {
	if !options.identityAudit && options.peopleDictTemplate == "" {
		return false, nil
	}
	commits, ok := cmdlineFacts[hercules.ConfigPipelineCommits].([]*object.Commit)
	if !ok {
		return true, errors.New("identity audit requires a Git commit repository")
	}
	err := runIdentityWorkflow(identityWorkflowOptions{
		Commits:      commits,
		Facts:        cmdlineFacts,
		Audit:        options.identityAudit,
		TemplatePath: options.peopleDictTemplate,
		Out:          os.Stdout,
	})
	return true, err
}

func pipelinePriority(flags *pflag.FlagSet, pipeline *core.Pipeline) func(
	[]core.PipelineItem,
) core.PipelineItem {
	return func(items []core.PipelineItem) core.PipelineItem {
		if len(items) == 0 {
			return nil
		}
		if len(items) > 1 {
			sort.Stable(&flagSorter{items: items, flagSet: flags, featureSet: pipeline})
		}
		return items[0]
	}
}

func writeRootResults(
	options rootOptions,
	repoURI string,
	deployedLeafs []hercules.LeafPipelineItem,
	results map[hercules.LeafPipelineItem]any,
	reporter *progressReporter,
) error {
	if options.progress == progressModeBar {
		_, _ = fmt.Fprint(os.Stderr, "\033[2K\r")
		// if not a terminal, the user will not see the output, so show the status
		if !terminal.IsTerminal(int(os.Stdout.Fd())) {
			_, _ = fmt.Fprint(os.Stderr, "writing...\r")
		}
	}
	if options.protobuf {
		reporter.emit(progressEvent{Event: progressEventWriteStart, Repo: repoURI, Output: "protobuf"})
		if err := protobufResults(repoURI, deployedLeafs, results); err != nil {
			return err
		}
	} else {
		reporter.emit(progressEvent{Event: progressEventWriteStart, Repo: repoURI, Output: "yaml"})
		if err := printResults(repoURI, deployedLeafs, results); err != nil {
			return err
		}
	}
	reporter.emit(progressEvent{Event: "write-done", Repo: repoURI})
	return nil
}

type identityWorkflowOptions struct {
	Commits      []*object.Commit
	Facts        map[string]interface{}
	Audit        bool
	TemplatePath string
	Out          io.Writer
}

func runIdentityWorkflow(options identityWorkflowOptions) error {
	facts := make(map[string]interface{}, len(options.Facts)+1)
	for key, value := range options.Facts {
		facts[key] = value
	}
	facts[hercules.ConfigPipelineCommits] = options.Commits
	delete(facts, identity.FactIdentityDetectorReversedPeopleDict)

	detector := identity.PeopleDetector{}
	if err := detector.Configure(facts); err != nil {
		return err
	}
	if options.TemplatePath != "" {
		if err := os.WriteFile(options.TemplatePath, []byte(detector.GeneratePeopleDictTemplate()), 0o644); err != nil {
			return err
		}
	}
	if options.Audit {
		out := options.Out
		if out == nil {
			out = os.Stdout
		}
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(detector.IdentityAudit()); err != nil {
			return err
		}
	}
	return nil
}

func deployItemsToPipeline(pipeline *core.Pipeline, flags *pflag.FlagSet,
	priorityFn func(items []core.PipelineItem) core.PipelineItem,
) (deployed []hercules.LeafPipelineItem) {
	deployList := make([][]string, 0, len(cmdlineDeployed))
	for name, valPtr := range cmdlineDeployed {
		if *valPtr {
			deployList = append(deployList, []string{name})
		}
	}

	flags.Visit(func(flag *pflag.Flag) {
		if names := activationByFlags[flag.Name]; len(names) > 0 {
			deployList = append(deployList, names)
		}
	})

	for _, names := range deployList {
		switch summons := hercules.Registry.Summon(names...); {
		case len(summons) == 0:
			log.Fatalf("missing item(s): %v", names)
		case len(summons) > 1:
			if len(names) == 2 {
				log.Printf("ambigous item: %v", names)
			}
			summons[0] = priorityFn(summons)
			summons = summons[:1]
			fallthrough
		default:
			item := pipeline.DeployItemOnce(summons[0])
			if !pipeline.DryRun && item == summons[0] {
				deployed = append(deployed, item.(hercules.LeafPipelineItem))
			}
		}
	}

	return deployed
}

type flagSorter struct {
	items      []core.PipelineItem
	flagSet    *pflag.FlagSet
	featureSet interface {
		GetFeature(string) (bool, bool)
	}
	cache []int
}

func (v *flagSorter) Len() int {
	return len(v.items)
}

func (v *flagSorter) Less(i, j int) bool {
	if v.cache == nil {
		v.cache = make([]int, len(v.items))
	}
	return v.itemWeight(i) > v.itemWeight(j)
}

func (v *flagSorter) Swap(i, j int) {
	v.cache[i], v.cache[j] = v.cache[j], v.cache[i]
	v.items[i], v.items[j] = v.items[j], v.items[i]
}

func (v *flagSorter) itemWeight(i int) int {
	if w := v.cache[i]; w != 0 {
		return w
	}
	w := v.weightFlagsOf(v.items[i], v.flagSet)
	v.cache[i] = w + 1
	return w
}

func (v *flagSorter) weightFlagsOf(item core.PipelineItem, flagSet *pflag.FlagSet) int {
	const (
		weightProvide   = -1 // excessive provides are not welcome
		weightParamFlag = 100
		weightFeature   = 100
	)

	w := weightProvide * len(item.Provides())
	for _, opt := range item.ListConfigurationOptions() {
		if flagSet.Changed(opt.Flag) {
			w += weightParamFlag
		}
	}
	if featured, ok := item.(core.FeaturedPipelineItem); v.featureSet != nil && ok {
		for _, feat := range featured.Features() {
			if ok, _ := v.featureSet.GetFeature(feat); ok {
				w += weightFeature
			}
		}
	}
	return w
}

func printResults(
	uri string, deployed []hercules.LeafPipelineItem,
	results map[hercules.LeafPipelineItem]interface{},
) error {
	commonResult := results[nil].(*hercules.CommonAnalysisResult)

	output := bufio.NewWriter(os.Stdout)
	if err := writeResultsHeader(output, uri, commonResult); err != nil {
		return err
	}

	for _, item := range deployed {
		result := results[item]
		if _, err := fmt.Fprintf(output, "%s:\n", item.Name()); err != nil {
			return fmt.Errorf("write %s result header: %w", item.Name(), err)
		}
		if err := item.Serialize(result, false, output); err != nil {
			return fmt.Errorf("serialize %s result: %w", item.Name(), err)
		}
	}
	if err := output.Flush(); err != nil {
		return fmt.Errorf("write YAML results: %w", err)
	}
	return nil
}

func writeResultsHeader(
	writer io.Writer,
	uri string,
	result *hercules.CommonAnalysisResult,
) error {
	_, err := fmt.Fprintf(
		writer,
		"hercules:\n"+
			"  version: %d\n"+
			"  hash: %s\n"+
			"  repository: %s\n"+
			"  begin_unix_time: %d\n"+
			"  end_unix_time: %d\n"+
			"  commits: %d\n"+
			"  run_time: %d\n",
		pb.SchemaVersion,
		hercules.BinaryGitHash,
		uri,
		result.BeginTime,
		result.EndTime,
		result.CommitsNumber,
		result.RunTime.Nanoseconds()/1e6,
	)
	if err != nil {
		return fmt.Errorf("write YAML result header: %w", err)
	}
	return nil
}

func protobufResults(
	uri string, deployed []hercules.LeafPipelineItem,
	results map[hercules.LeafPipelineItem]interface{},
) error {
	header := pb.Metadata{
		Version:    pb.SchemaVersion,
		Hash:       hercules.BinaryGitHash,
		Repository: uri,
	}
	results[nil].(*hercules.CommonAnalysisResult).FillMetadata(&header)

	message := pb.AnalysisResults{
		Header:   &header,
		Contents: map[string][]byte{},
	}

	for _, item := range deployed {
		result := results[item]
		buffer := &bytes.Buffer{}
		if err := item.Serialize(result, true, buffer); err != nil {
			return fmt.Errorf("serialize %s result: %w", item.Name(), err)
		}
		message.Contents[item.Name()] = buffer.Bytes()
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal protobuf results: %w", err)
	}
	if _, err := os.Stdout.Write(serialized); err != nil {
		return fmt.Errorf("write protobuf results: %w", err)
	}
	return nil
}

// trimRightSpace removes the trailing whitespace characters.
func trimRightSpace(s string) string {
	return strings.TrimRightFunc(s, unicode.IsSpace)
}

// rpad adds padding to the right of a string.
func rpad(s string, padding int) string {
	return fmt.Sprintf(fmt.Sprintf("%%-%ds", padding), s)
}

// tmpl was adapted from cobra/cobra.go
func tmpl(w io.Writer, text string, data interface{}) error {
	templateFuncs := template.FuncMap{
		"trim":                    strings.TrimSpace,
		"trimRightSpace":          trimRightSpace,
		"trimTrailingWhitespaces": trimRightSpace,
		"rpad":                    rpad,
		"gt":                      cobra.Gt,
		"eq":                      cobra.Eq,
	}
	for k, v := range sprig.TxtFuncMap() {
		templateFuncs[k] = v
	}
	t := template.New("top")
	t.Funcs(templateFuncs)
	template.Must(t.Parse(text))
	return t.Execute(w, data)
}

func formatUsage(c *cobra.Command) error {
	// the default UsageFunc() does some private magic c.mergePersistentFlags()
	// this should stay on top
	localFlags := c.LocalFlags()
	leaves := hercules.Registry.GetLeaves()
	plumbing := hercules.Registry.GetPlumbingItems()
	features := hercules.Registry.GetFeaturedItems()
	hercules.EnablePathFlagTypeMasquerade()
	filter := map[string]bool{}
	for _, l := range leaves {
		filter[l.Flag()] = true
		for _, cfg := range l.ListConfigurationOptions() {
			filter[cfg.Flag] = true
		}
	}
	for _, i := range plumbing {
		for _, cfg := range i.ListConfigurationOptions() {
			filter[cfg.Flag] = true
		}
	}

	for key := range filter {
		localFlags.Lookup(key).Hidden = true
	}
	args := map[string]interface{}{
		"c":        c,
		"leaves":   leaves,
		"plumbing": plumbing,
		"features": features,
	}

	helpTemplate := `Usage:{{if .c.Runnable}}
  {{.c.UseLine}}{{end}}{{if .c.HasAvailableSubCommands}}
  {{.c.CommandPath}} [command]{{end}}{{if gt (len .c.Aliases) 0}}

Aliases:
  {{.c.NameAndAliases}}{{end}}{{if .c.HasExample}}

Examples:
{{.c.Example}}{{end}}{{if .c.HasAvailableSubCommands}}

Available Commands:{{range .c.Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .c.HasAvailableLocalFlags}}

Flags:
{{range $line := .c.LocalFlags.FlagUsages | trimTrailingWhitespaces | split "\n"}}
{{- $desc := splitList "   " $line | last}}
{{- $offset := sub ($desc | len) ($desc | trim | len)}}
{{- $indent := splitList "   " $line | initial | join "   " | len | add 3 | add $offset | int}}
{{- $wrap := sub 120 $indent | int}}
{{- splitList "   " $line | initial | join "   "}}   {{cat "!" $desc | wrap $wrap | indent $indent | substr $indent -1 | substr 2 -1}}
{{end}}{{end}}

Analysis Targets:{{range .leaves}}
      --{{rpad .Flag 40}}Runs {{.Name}} analysis.{{wrap 72 .Description | nindent 48}}{{range .ListConfigurationOptions}}
          --{{if .Type.String}}{{rpad (print .Flag " " .Type.String) 40}}{{else}}{{rpad .Flag 40}}{{end}}
          {{- $desc := dict "desc" .Description}}
          {{- if .Default}}{{$_ := set $desc "desc" (print .Description " The default value is " .FormatDefault ".")}}
          {{- end}}
          {{- $desc := pluck "desc" $desc | first}}
          {{- $desc | wrap 68 | indent 52 | substr 52 -1}}{{end}}
{{end}}

Plumbing Options:{{range .plumbing}}{{$name := .Name}}{{range .ListConfigurationOptions}}
      --{{if .Type.String}}{{rpad (print .Flag " " .Type.String " [" $name "]") 40}}{{else}}{{rpad (print .Flag " [" $name "]") 40}}
        {{- end}}
        {{- $desc := dict "desc" .Description}}
        {{- if .Default}}{{$_ := set $desc "desc" (print .Description " The default value is " .FormatDefault ".")}}
        {{- end}}
        {{- $desc := pluck "desc" $desc | first}}{{$desc | wrap 72 | indent 48 | substr 48 -1}}{{end}}{{end}}

--feature:{{range $key, $value := .features}}
      {{rpad $key 42}}Enables {{range $index, $item := $value}}{{if $index}}, {{end}}{{$item.Name}}{{end}}.{{end}}{{if .c.HasAvailableInheritedFlags}}

Global Flags:
{{.c.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .c.HasHelpSubCommands}}

Additional help topics:{{range .c.Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .c.HasAvailableSubCommands}}

Use "{{.c.CommandPath}} [command] --help" for more information about a command.{{end}}
`
	err := tmpl(c.OutOrStderr(), helpTemplate, args)
	for key := range filter {
		localFlags.Lookup(key).Hidden = false
	}
	if err != nil {
		c.Println(err)
	}
	return err
}

// versionCmd prints the API version and the Git commit hash
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information and exit.",
	Long:  ``,
	Args:  cobra.MaximumNArgs(0),
	Run:   runVersion,
}

func runVersion(cmd *cobra.Command, _ []string) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Version: %d\nGit:     %s\nSchema:  %d\n",
		hercules.BinaryVersion, hercules.BinaryGitHash, pb.SchemaVersion)
}

var (
	cmdlineFacts      map[string]interface{}
	cmdlineDeployed   map[string]*bool
	activationByFlags map[string][]string
)

func init() {
	loadPlugins()
	rootFlags := rootCmd.Flags()
	rootFlags.String("commits", "", "Path to the text file with the "+
		"commit history to follow instead of the default 'git log'. "+
		"The format is the list of hashes, each hash on a "+
		"separate line. The first hash is the root.")
	err := rootCmd.MarkFlagFilename("commits")
	if err != nil {
		panic(err)
	}
	hercules.PathifyFlagValue(rootFlags.Lookup("commits"))
	rootFlags.Bool("head", false, "Analyze only the latest commit.")
	rootFlags.Bool("first-parent", false, "Follow only the first parent in the commit history - "+
		"\"git log --first-parent\".")
	rootFlags.Bool("pb", false, "The output format will be Protocol Buffers instead of YAML.")
	rootFlags.Bool("identity-audit", false,
		"Write detected identities, merge decisions, and ambiguous identity candidates as JSON and exit.")
	rootFlags.Bool("quiet", !terminal.IsTerminal(int(os.Stdin.Fd())),
		"Do not print status updates to stderr.")
	rootFlags.String("progress", "auto",
		"Progress output format on stderr: auto, bar, lines, json, none.")
	rootFlags.Bool("profile", false, "Collect the profile to hercules.pprof.")
	rootFlags.String("preset", "",
		"Apply a named set of flag defaults. Available: large-repo, quick. "+
			"Explicit flags override preset values.")
	rootFlags.String("ssh-identity", "", "Path to SSH identity file (e.g., ~/.ssh/id_rsa) to clone from an SSH remote.")
	err = rootCmd.MarkFlagFilename("ssh-identity")
	if err != nil {
		panic(err)
	}
	hercules.PathifyFlagValue(rootFlags.Lookup("ssh-identity"))
	rootFlags.String("people-dict-template", "",
		"Write detected identities to a people-dict template file and exit.")
	err = rootCmd.MarkFlagFilename("people-dict-template")
	if err != nil {
		panic(err)
	}
	hercules.PathifyFlagValue(rootFlags.Lookup("people-dict-template"))
	cmdlineFacts, cmdlineDeployed, activationByFlags = hercules.Registry.AddFlags(rootFlags)
	rootCmd.SetUsageFunc(formatUsage)
	rootCmd.AddCommand(versionCmd)
	versionCmd.SetUsageFunc(versionCmd.UsageFunc())
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
