// Package tsric provides a TapperVPNRustInContainer (tsric) implementation
// that runs the tappervpn-rs axum example inside a Docker container for
// integration testing with headscale.
//
// Unlike tsic (which runs the official TapperVPN client), tsric runs a Rust
// implementation of a TapperVPN node. It does not have the `tappervpn` CLI,
// so verification is done externally via headscale API and peer connectivity.
package tsric

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/juanfont/headscale/integration/dockertestutil"
	"github.com/juanfont/headscale/integration/integrationutil"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"tappervpn.com/util/rands"
)

const (
	tsricHashLength = 6
	caCertRoot      = "/usr/local/share/ca-certificates"

	dockerfileName    = "Dockerfile.tappervpn-rs"
	dockerContextPath = "../."

	buildArgRepo = "TAPPERVPN_RS_REPO"
	buildArgRef  = "TAPPERVPN_RS_REF"
)

// getPrebuiltImage returns the pre-built tappervpn-rs Docker image name if set.
func getPrebuiltImage() string {
	return os.Getenv("HEADSCALE_INTEGRATION_TAPPERVPN_RS_IMAGE")
}

// TapperVPNRustInContainer runs the tappervpn-rs axum example as an
// integration test peer.
type TapperVPNRustInContainer struct {
	hostname string

	pool      *dockertest.Pool
	container *dockertest.Resource
	network   *dockertest.Network

	caCerts      [][]byte
	headscaleURL string
	authKey      string
	extraHosts   []string
	repo         string
	ref          string
}

// Option represents optional settings for a TapperVPNRustInContainer instance.
type Option = func(c *TapperVPNRustInContainer)

// WithCACert adds a CA certificate to the trusted certificates of the container.
func WithCACert(cert []byte) Option {
	return func(t *TapperVPNRustInContainer) {
		t.caCerts = append(t.caCerts, cert)
	}
}

// WithNetwork sets the Docker [dockertest.Network].
func WithNetwork(network *dockertest.Network) Option {
	return func(t *TapperVPNRustInContainer) {
		t.network = network
	}
}

// WithHeadscaleURL sets the headscale control server URL.
func WithHeadscaleURL(url string) Option {
	return func(t *TapperVPNRustInContainer) {
		t.headscaleURL = url
	}
}

// WithAuthKey sets the pre-authentication key for joining the tailnet.
func WithAuthKey(key string) Option {
	return func(t *TapperVPNRustInContainer) {
		t.authKey = key
	}
}

// WithExtraHosts adds extra /etc/hosts entries to the container.
func WithExtraHosts(hosts []string) Option {
	return func(t *TapperVPNRustInContainer) {
		t.extraHosts = append(t.extraHosts, hosts...)
	}
}

// WithRepo overrides the tappervpn-rs git repository URL used by the
// Dockerfile. Defaults to the public github.com/tailscale/tailscale-rs.
func WithRepo(url string) Option {
	return func(t *TapperVPNRustInContainer) {
		t.repo = url
	}
}

// WithRef overrides the tappervpn-rs git ref (branch, tag, commit) used
// by the Dockerfile. Defaults to "main".
func WithRef(ref string) Option {
	return func(t *TapperVPNRustInContainer) {
		t.ref = ref
	}
}

// buildEntrypoint constructs the container entrypoint command.
//
// The axum example reads the control URL from TS_CONTROL_URL, the
// hostname from -H, and the auth key from -k. The key file (-c) is
// created on first run.
func (t *TapperVPNRustInContainer) buildEntrypoint() []string {
	var commands []string

	commands = append(commands,
		"while ! ip route show default >/dev/null 2>&1; do sleep 0.1; done")

	// CA certs are written by New after the container starts, so the
	// entrypoint races with that write. Block until the first cert lands.
	if len(t.caCerts) > 0 {
		commands = append(commands,
			fmt.Sprintf("while [ ! -f %s/user-0.crt ]; do sleep 0.1; done", caCertRoot))
	}

	commands = append(commands, "update-ca-certificates 2>/dev/null || true")

	commands = append(
		commands,
		fmt.Sprintf(`export TS_CONTROL_URL=%q`, t.headscaleURL),
		// The tappervpn crate refuses to run without this env gate;
		// see lib.rs in tappervpn-rs.
		"export TS_RS_EXPERIMENT=this_is_unstable_software",
	)

	axumCmd := "/usr/local/bin/axum -c /tmp/tsrs-keys.json -H " + t.hostname
	if t.authKey != "" {
		axumCmd += " -k " + t.authKey
	}

	commands = append(commands, "exec "+axumCmd)

	return []string{"/bin/sh", "-c", strings.Join(commands, " ; ")}
}

// New creates and starts a new [TapperVPNRustInContainer] instance.
func New(
	pool *dockertest.Pool,
	opts ...Option,
) (*TapperVPNRustInContainer, error) {
	hash := rands.HexString(tsricHashLength)

	var err error

	runID := dockertestutil.GetIntegrationRunID()

	var hostname string

	if runID != "" {
		runIDShort := runID[len(runID)-6:]
		hostname = fmt.Sprintf("tsrs-%s-%s", runIDShort, hash)
	} else {
		hostname = "tsrs-" + hash
	}

	t := &TapperVPNRustInContainer{
		hostname: hostname,
		pool:     pool,
	}

	for _, opt := range opts {
		opt(t)
	}

	if t.network == nil {
		return nil, errors.New("tsric: no network set") //nolint:err113
	}

	if t.headscaleURL == "" {
		return nil, errors.New("tsric: no headscale URL set") //nolint:err113
	}

	if t.authKey == "" {
		return nil, errors.New("tsric: no auth key set") //nolint:err113
	}

	entrypoint := t.buildEntrypoint()

	runOptions := &dockertest.RunOptions{
		Name:       hostname,
		Networks:   []*dockertest.Network{t.network},
		Entrypoint: entrypoint,
		ExtraHosts: append(t.extraHosts, "host.docker.internal:host-gateway"),
		Env:        []string{},
	}

	dockertestutil.DockerAddIntegrationLabels(runOptions, "tappervpn-rs")

	err = pool.RemoveContainerByName(hostname)
	if err != nil {
		return nil, err
	}

	var container *dockertest.Resource

	if prebuiltImage := getPrebuiltImage(); prebuiltImage != "" {
		log.Printf("Using pre-built tappervpn-rs image: %s", prebuiltImage)

		repo, tag, ok := strings.Cut(prebuiltImage, ":")
		if !ok {
			return nil, fmt.Errorf("tsric: invalid image format %q, expected repository:tag", prebuiltImage) //nolint:err113
		}

		runOptions.Repository = repo
		runOptions.Tag = tag

		container, err = pool.RunWithOptions(
			runOptions,
			dockertestutil.DockerRestartPolicy,
			dockertestutil.DockerAllowLocalIPv6,
			dockertestutil.DockerMemoryLimit,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"tsric: could not start pre-built tappervpn-rs container %s: %w",
				hostname, err,
			)
		}
	} else {
		// Build from the Dockerfile so callers don't need a local
		// tappervpn-rs checkout; the Dockerfile clones at build time.
		var buildArgs []docker.BuildArg

		if t.repo != "" {
			buildArgs = append(buildArgs, docker.BuildArg{Name: buildArgRepo, Value: t.repo})
		}

		if t.ref != "" {
			buildArgs = append(buildArgs, docker.BuildArg{Name: buildArgRef, Value: t.ref})
		}

		buildOptions := &dockertest.BuildOptions{
			Dockerfile: dockerfileName,
			ContextDir: dockerContextPath,
			BuildArgs:  buildArgs,
		}

		log.Printf("Building tappervpn-rs container %s from upstream (this may take a while for the first build)...", hostname)

		container, err = pool.BuildAndRunWithBuildOptions(
			buildOptions,
			runOptions,
			dockertestutil.DockerRestartPolicy,
			dockertestutil.DockerAllowLocalIPv6,
			dockertestutil.DockerMemoryLimit,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"tsric: could not build and start tappervpn-rs container %s: %w",
				hostname, err,
			)
		}
	}

	log.Printf("Created tappervpn-rs container %s", hostname)

	t.container = container

	for i, cert := range t.caCerts {
		err = t.WriteFile(fmt.Sprintf("%s/user-%d.crt", caCertRoot, i), cert)
		if err != nil {
			return nil, fmt.Errorf("writing TLS certificate to container: %w", err)
		}
	}

	return t, nil
}

// Hostname returns the hostname of the [TapperVPNRustInContainer] instance.
func (t *TapperVPNRustInContainer) Hostname() string {
	return t.hostname
}

// ContainerID returns the Docker container ID.
func (t *TapperVPNRustInContainer) ContainerID() string {
	return t.container.Container.ID
}

// Shutdown stops and cleans up the container.
func (t *TapperVPNRustInContainer) Shutdown() (string, string, error) {
	stdoutPath, stderrPath, err := t.SaveLog("/tmp/control")
	if err != nil {
		log.Printf(
			"saving log from %s: %s",
			t.hostname,
			fmt.Errorf("saving log: %w", err),
		)
	}

	return stdoutPath, stderrPath, t.pool.Purge(t.container)
}

// SaveLog saves the current container logs to the given path.
func (t *TapperVPNRustInContainer) SaveLog(path string) (string, string, error) {
	return dockertestutil.SaveLog(t.pool, t.container, path)
}

// WriteLogs writes the current stdout/stderr log of the container to
// the given [io.Writer]s.
func (t *TapperVPNRustInContainer) WriteLogs(stdout, stderr io.Writer) error {
	return dockertestutil.WriteLog(t.pool, t.container, stdout, stderr)
}

// Execute runs a command inside the container.
func (t *TapperVPNRustInContainer) Execute(
	command []string,
	options ...dockertestutil.ExecuteCommandOption,
) (string, string, error) {
	return dockertestutil.ExecuteCommand(
		t.container,
		command,
		[]string{},
		options...,
	)
}

// WriteFile writes a file into the container.
func (t *TapperVPNRustInContainer) WriteFile(path string, data []byte) error {
	return integrationutil.WriteFileToContainer(t.pool, t.container, path, data)
}
