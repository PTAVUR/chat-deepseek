"""systemd 服务的路径与命令拼装。

GUI 不直接调用 systemctl，只把参数拼成 `chat-deepseek service ...`；这里保持
纯函数，方便单测，也避免对话框里散落命令格式。
"""

from __future__ import annotations

import getpass
import os
import shlex
from pathlib import Path

UNIT_NAME = "chat-deepseek.service"


def current_user() -> str:
    try:
        return getpass.getuser()
    except Exception:  # noqa: BLE001 - 取不到用户名时只影响提示文案
        return "<user>"


def unit_path() -> str:
    """与 CLI 的 os.UserConfigDir() 一致：优先 XDG_CONFIG_HOME。"""
    base = os.environ.get("XDG_CONFIG_HOME") or str(Path.home() / ".config")
    return str(Path(base) / "systemd" / "user" / UNIT_NAME)


def install_args(
    data_dir: str, *, enable: bool, force: bool = False
) -> list[str]:
    args = ["service", "install"]
    if enable:
        args.append("--enable")
    if force:
        args.append("--force")
    args.extend(["--data-dir", data_dir])
    return args


def system_install_command(
    binary: str,
    data_dir: str,
    user: str,
    *,
    enable: bool,
) -> str:
    """系统级安装需要 root；GUI 只生成命令，由用户复制到终端执行。"""
    parts = [
        binary,
        "service",
        "install",
        "--system",
        "--run-as",
        user,
        "--data-dir",
        data_dir,
    ]
    if enable:
        parts.append("--enable")
    return " ".join(shlex.quote(part) for part in parts)


def linger_command(user: str) -> str:
    return f"loginctl enable-linger {shlex.quote(user)}"
