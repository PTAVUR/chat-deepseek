package main

import (
	"fmt"
	"os"

	"github.com/elegantgx/chat-deepseek/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(cli.ExitStatus(err))
	}
}
