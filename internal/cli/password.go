package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/elegantgx/chat-deepseek/internal/auth"
)

var stdinReader = bufio.NewReader(os.Stdin)

// promptLine 从标准输入读取一行普通文本。
func promptLine(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	line, err := stdinReader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("读取输入失败: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// readNewPassword 读取并确认新密码；非终端输入必须显式使用 --password-stdin。
func readNewPassword(fromStdin bool) (string, error) {
	if fromStdin {
		line, err := stdinReader.ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("读取密码失败: %w", err)
		}
		password := strings.TrimRight(line, "\r\n")
		if err := auth.ValidatePassword(password); err != nil {
			return "", errors.New("密码至少 8 位")
		}
		return password, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("标准输入不是终端；请用 --password-stdin 传入密码")
	}
	first, err := readSecret("密码: ")
	if err != nil {
		return "", err
	}
	if err := auth.ValidatePassword(first); err != nil {
		return "", errors.New("密码至少 8 位")
	}
	second, err := readSecret("确认密码: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("两次输入的密码不一致")
	}
	return first, nil
}

func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("读取密码失败: %w", err)
	}
	return string(value), nil
}
