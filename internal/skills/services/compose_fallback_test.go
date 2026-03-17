package services

import (
	"context"
	"errors"
	"testing"
)

type recordedCall struct {
	name string
	args []string
}

type stubExecutor struct {
	calls     []recordedCall
	responses []executorResponse
}

type executorResponse struct {
	stdout []byte
	stderr []byte
	err    error
}

func (s *stubExecutor) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	stdout, stderr, err := s.Output(ctx, name, args...)
	output := make([]byte, 0, len(stdout)+len(stderr))
	output = append(output, stdout...)
	output = append(output, stderr...)
	return output, err
}

func (s *stubExecutor) Output(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	s.calls = append(s.calls, recordedCall{name: name, args: append([]string(nil), args...)})
	if len(s.responses) == 0 {
		return nil, nil, errors.New("unexpected command")
	}

	response := s.responses[0]
	s.responses = s.responses[1:]
	return response.stdout, response.stderr, response.err
}

func TestRunComposeCommandFallsBackToLegacyBinary(t *testing.T) {
	t.Parallel()

	executor := &stubExecutor{
		responses: []executorResponse{
			{
				stderr: []byte("unknown shorthand flag: 'f' in -f"),
				err:    errors.New("exit status 125"),
			},
			{
				stdout: []byte(`{"Name":"jitsi_web_1","Service":"web","State":"running","Status":"Up","Image":"jitsi/web"}`),
			},
		},
	}

	target := serviceTarget{
		Name:           "jitsi-web",
		Backend:        backendCompose,
		ComposeFile:    "/opt/jitsi/docker-compose.yml",
		ComposeService: "web",
	}

	stdout, stderr, err := runComposeCommand(context.Background(), executor, target, "ps", "--format", "json", target.ComposeService)
	if err != nil {
		t.Fatalf("runComposeCommand returned error: %v", err)
	}
	if got := string(stdout); got == "" {
		t.Fatal("expected stdout from legacy docker-compose fallback")
	}
	if len(stderr) != 0 {
		t.Fatalf("expected empty stderr, got %q", string(stderr))
	}
	if len(executor.calls) != 2 {
		t.Fatalf("expected 2 compose attempts, got %d", len(executor.calls))
	}
	if executor.calls[0].name != "docker" {
		t.Fatalf("unexpected first command: %#v", executor.calls[0])
	}
	if executor.calls[1].name != "docker-compose" {
		t.Fatalf("unexpected fallback command: %#v", executor.calls[1])
	}
}

func TestShouldRetryWithLegacyCompose(t *testing.T) {
	t.Parallel()

	if !shouldRetryWithLegacyCompose(nil, []byte("unknown shorthand flag: 'f' in -f"), errors.New("exit status 125")) {
		t.Fatal("expected docker compose shorthand error to trigger fallback")
	}
	if shouldRetryWithLegacyCompose(nil, []byte("permission denied"), errors.New("exit status 1")) {
		t.Fatal("did not expect unrelated error to trigger fallback")
	}
}

func TestComposeStatusFallsBackToLegacyPS(t *testing.T) {
	t.Parallel()

	executor := &stubExecutor{
		responses: []executorResponse{
			{
				stderr: []byte("unknown shorthand flag: 'f' in -f"),
				err:    errors.New("exit status 125"),
			},
			{
				stderr: []byte("List containers.\n\nUsage: ps [options] [--] [SERVICE...]"),
				err:    errors.New("exit status 1"),
			},
			{
				stdout: []byte("       Name                     Command               State           Ports\n----------------------------------------------------------------------------------------\ndocker-jitsi-meet_web_1   /init                 Up About an hour   0.0.0.0:8000->80/tcp\n"),
			},
		},
	}

	manager := &SystemdManager{
		executors: map[string]commandExecutor{
			"": executor,
		},
	}
	target := serviceTarget{
		Name:           "jitsi-web",
		Backend:        backendCompose,
		ComposeFile:    "/opt/jitsi/docker-compose.yml",
		ComposeService: "web",
	}

	info, err := manager.composeStatus(context.Background(), target)
	if err != nil {
		t.Fatalf("composeStatus returned error: %v", err)
	}
	if info.Name != "jitsi-web" || info.ActiveState != "active" || info.SubState != "Up About an hour" {
		t.Fatalf("unexpected compose status: %#v", info)
	}
	if len(executor.calls) != 3 {
		t.Fatalf("expected 3 executor calls, got %d", len(executor.calls))
	}
	if executor.calls[2].name != "docker-compose" {
		t.Fatalf("unexpected fallback sequence: %#v", executor.calls)
	}
}

func TestShouldRetryWithLegacyComposePS(t *testing.T) {
	t.Parallel()

	if !shouldRetryWithLegacyComposePS(nil, []byte("List containers.\n\nUsage: ps [options] [--] [SERVICE...]"), errors.New("exit status 1")) {
		t.Fatal("expected legacy ps usage output to trigger compose status fallback")
	}
	if shouldRetryWithLegacyComposePS(nil, []byte("permission denied"), errors.New("exit status 1")) {
		t.Fatal("did not expect unrelated error to trigger compose ps fallback")
	}
}

func TestParseLegacyComposePS(t *testing.T) {
	t.Parallel()

	record, err := parseLegacyComposePS([]byte("       Name                     Command               State           Ports\n----------------------------------------------------------------------------------------\ndocker-jitsi-meet_web_1   /init                 Up About an hour   0.0.0.0:8000->80/tcp\n"))
	if err != nil {
		t.Fatalf("parseLegacyComposePS returned error: %v", err)
	}
	if record.Name != "docker-jitsi-meet_web_1" || record.State != "running" || record.Status != "Up About an hour" {
		t.Fatalf("unexpected parsed record: %#v", record)
	}
}

func TestDockerBackendStatusLogsAndRestart(t *testing.T) {
	t.Parallel()

	executor := &stubExecutor{
		responses: []executorResponse{
			{
				stdout: []byte(`[{"Name":"/docker-jitsi-meet_web_1","Config":{"Image":"jitsi/web:unstable"},"State":{"Status":"running","Health":{"Status":"healthy"}}}]`),
			},
			{
				stdout: []byte("line one\nline two\n"),
			},
			{
				stdout: []byte("docker-jitsi-meet_web_1\n"),
			},
		},
	}

	manager := &SystemdManager{
		executors: map[string]commandExecutor{
			"vps": executor,
		},
	}
	target := serviceTarget{
		Name:            "jitsi-web",
		Host:            "vps",
		Backend:         backendDocker,
		DockerContainer: "docker-jitsi-meet_web_1",
	}

	info, err := manager.dockerStatus(context.Background(), target)
	if err != nil {
		t.Fatalf("dockerStatus returned error: %v", err)
	}
	if info.LoadState != "docker" || info.ActiveState != "active" || info.SubState != "healthy" {
		t.Fatalf("unexpected docker status: %#v", info)
	}
	if info.Description != "docker container docker-jitsi-meet_web_1 (jitsi/web:unstable)" {
		t.Fatalf("unexpected docker description: %#v", info)
	}

	logsText, err := manager.dockerLogs(context.Background(), target, 30)
	if err != nil {
		t.Fatalf("dockerLogs returned error: %v", err)
	}
	if logsText != "line one\nline two" {
		t.Fatalf("unexpected docker logs: %q", logsText)
	}

	if err := manager.dockerRestart(context.Background(), target); err != nil {
		t.Fatalf("dockerRestart returned error: %v", err)
	}
	if len(executor.calls) != 3 {
		t.Fatalf("unexpected docker executor calls: %#v", executor.calls)
	}
}
