package services

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	"openlight/internal/config"
	"openlight/internal/skills"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type commandExecutor interface {
	CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error)
	Output(ctx context.Context, name string, args ...string) ([]byte, []byte, error)
}

type localExecutor struct{}

type sshExecutor struct {
	name string
	host config.RemoteHostConfig
}

func newExecutors(hosts map[string]config.RemoteHostConfig) (map[string]commandExecutor, error) {
	executors := map[string]commandExecutor{
		"": localExecutor{},
	}
	for name, host := range hosts {
		executors[name] = &sshExecutor{
			name: name,
			host: host,
		}
	}
	return executors, nil
}

func (localExecutor) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return runCombinedOutput(ctx, name, args...)
}

func (localExecutor) Output(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	return runOutput(ctx, name, args...)
}

func (e *sshExecutor) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	stdout, stderr, err := e.Output(ctx, name, args...)
	if len(stderr) == 0 {
		return stdout, err
	}

	output := make([]byte, 0, len(stdout)+len(stderr))
	output = append(output, stdout...)
	output = append(output, stderr...)
	return output, err
}

func (e *sshExecutor) Output(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	client, err := e.connect(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: open ssh session for %s: %v", skills.ErrUnavailable, e.name, err)
	}
	defer session.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	command := buildRemoteCommand(e.host.Sudo, name, args...)
	err = session.Run(command)
	return stdout.Bytes(), stderr.Bytes(), err
}

func (e *sshExecutor) connect(ctx context.Context) (*ssh.Client, error) {
	clientConfig, err := e.clientConfig()
	if err != nil {
		return nil, err
	}

	var dialer net.Dialer
	connection, err := dialer.DialContext(ctx, "tcp", e.host.Address)
	if err != nil {
		return nil, fmt.Errorf("%w: dial ssh host %s (%s): %v", skills.ErrUnavailable, e.name, e.host.Address, err)
	}

	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			connection.Close()
			return nil, fmt.Errorf("%w: set ssh deadline for %s: %v", skills.ErrUnavailable, e.name, err)
		}
	}

	clientConn, channels, requests, err := ssh.NewClientConn(connection, e.host.Address, clientConfig)
	if err != nil {
		connection.Close()
		return nil, fmt.Errorf("%w: connect ssh host %s (%s): %v", skills.ErrUnavailable, e.name, e.host.Address, err)
	}

	return ssh.NewClient(clientConn, channels, requests), nil
}

func (e *sshExecutor) clientConfig() (*ssh.ClientConfig, error) {
	authMethods, err := e.authMethods()
	if err != nil {
		return nil, err
	}

	callback, err := e.hostKeyCallback()
	if err != nil {
		return nil, err
	}

	clientConfig := &ssh.ClientConfig{
		User:            e.host.User,
		Auth:            authMethods,
		HostKeyCallback: callback,
	}

	return clientConfig, nil
}

func (e *sshExecutor) authMethods() ([]ssh.AuthMethod, error) {
	methods := make([]ssh.AuthMethod, 0, 3)

	if keyPath := strings.TrimSpace(e.host.PrivateKeyPath); keyPath != "" {
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("%w: read private key for host %s: %v", skills.ErrUnavailable, e.name, err)
		}

		passphrase := resolveSecret(e.host.PrivateKeyPassphrase, e.host.PrivateKeyPassphraseEnv)
		signer, err := parseSigner(key, passphrase)
		if err != nil {
			return nil, fmt.Errorf("%w: parse private key for host %s: %v", skills.ErrUnavailable, e.name, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	if password := resolveSecret(e.host.Password, e.host.PasswordEnv); password != "" {
		methods = append(methods, ssh.Password(password))
		methods = append(methods, ssh.KeyboardInteractive(func(_ string, _ string, questions []string, _ []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for idx := range questions {
				answers[idx] = password
			}
			return answers, nil
		}))
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("%w: no ssh auth methods configured for host %s", skills.ErrUnavailable, e.name)
	}

	return methods, nil
}

func (e *sshExecutor) hostKeyCallback() (ssh.HostKeyCallback, error) {
	if e.host.InsecureIgnoreHostKey {
		return ssh.InsecureIgnoreHostKey(), nil
	}

	callback, err := knownhosts.New(e.host.KnownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("%w: load known_hosts for host %s: %v", skills.ErrUnavailable, e.name, err)
	}
	return callback, nil
}

func buildRemoteCommand(sudo bool, name string, args ...string) string {
	commandArgs := make([]string, 0, len(args)+3)
	if sudo {
		commandArgs = append(commandArgs, "sudo", "-n")
	}
	commandArgs = append(commandArgs, name)
	commandArgs = append(commandArgs, args...)

	quoted := make([]string, 0, len(commandArgs))
	for _, arg := range commandArgs {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if isShellSafe(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func isShellSafe(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("@%_+=:,./-", r):
		default:
			return false
		}
	}
	return true
}

func parseSigner(key []byte, passphrase string) (ssh.Signer, error) {
	if strings.TrimSpace(passphrase) == "" {
		return ssh.ParsePrivateKey(key)
	}
	return ssh.ParsePrivateKeyWithPassphrase(key, []byte(passphrase))
}

func resolveSecret(value string, envName string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	if envName = strings.TrimSpace(envName); envName != "" {
		return strings.TrimSpace(os.Getenv(envName))
	}
	return ""
}
