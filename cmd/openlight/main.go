// openlight is the single binary entrypoint for the openLight runtime.
//
// Subcommands:
//
//	openlight agent    - run the Telegram bot (production)
//	openlight cli      - run a local CLI session against the same runtime
//	openlight doctor   - validate config, allowlists, dependencies
//	openlight version  - print build version
//
// All subcommands share the same config loader, runtime wiring, and skills.
// They differ only in the transport that drives the agent loop.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	sub := os.Args[1]
	args := os.Args[2:]

	switch sub {
	case "agent":
		runOrExit(runAgent(args))
	case "cli":
		runOrExit(runCLI(args))
	case "doctor":
		runOrExit(runDoctor(args))
	case "version", "-v", "--version":
		fmt.Println(version())
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "openlight: unknown subcommand %q\n\n", sub)
		usage()
		os.Exit(2)
	}
}

func runOrExit(err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "openlight: %v\n", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprint(os.Stderr, `openlight — lightweight local infrastructure agent

Usage:
  openlight <command> [flags]

Commands:
  agent     Run the Telegram bot
  cli       Run a one-shot or interactive CLI session
  doctor    Validate config, allowlists, and dependencies
  version   Print version
  help      Show this message

Run "openlight <command> -h" for command-specific flags.
`)
}

// version is overridden at build time via -ldflags.
var buildVersion = "dev"

func version() string { return "openlight " + buildVersion }
