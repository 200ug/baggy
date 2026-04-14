package main

import (
	"fmt"
	"os"

	"codeberg.org/2ug/wsftp/cmd"
)

const versionCode = "v1.1 (2026-04-14)"

const usage = `usage: wsftp <command> [flags]

commands:
  init     configure remote and verify connectivity
  sync     run the sync workflow against the configured remote
  status   dry-run the sync to see what changes would be made
  version  print tool version

`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "init":
		cmd.CmdInit(os.Args[2:])
	case "sync":
		cmd.CmdSync(os.Args[2:])
	case "status":
		cmd.CmdStatus(os.Args[2:])
	case "version":
		fmt.Printf("wsftp %s\n", versionCode)
	default:
		fmt.Print(usage)
		os.Exit(1)
	}
}
