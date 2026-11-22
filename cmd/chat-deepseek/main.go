package main

import (
	"fmt"
	"os"

	"gitlab.com/elegantgx/chat-deepseek/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}
