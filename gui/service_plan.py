"""systemd 服务命令拼装。

GUI 不直接调用 systemctl，只把参数拼成 `chat-deepseek service ...`；这里保持
纯函数，方便单测，也避免对话框里散落命令格式。
"""

from __future__ import annotations

import getpass
import os
import shlex


def current_user() -> str:
    # 通过 sudo 启动 GUI 时，SUDO_USER 才是系统级服务应该运行的普通用户。
    sudo_user = os.environ.get("SUDO_USER")
    if sudo_user:
        return sudo_user
    try:
        return getpass.getuser()
    except Exception:  # noqa: BLE001 - 取不到用户名时只影响提示文案
        return "<user>"


def install_args(
    data_dir: str,
    *,
    enable: bool,
    disable: bool = False,
    force: bool = False,
    scope: str = "user",
    run_as: str = "",
) -> list[str]:
    if enable and disable:
        raise ValueError("enable 和 disable 不能同时使用")
    args = ["service", "install"]
    if scope == "system":
        args.extend(["--system", "--run-as", run_as])
    if enable:
        args.append("--enable")
    if disable:
        args.append("--disable")
    if force:
        args.append("--force")
    args.extend(["--data-dir", data_dir])
    return args


def update_args(data_dir: str, *, enable: bool) -> list[str]:
    """更新已有 unit 的参数。

    必须显式带上 `--enable` 或 `--disable`：GUI 缓存的开机自启状态可能已经
    被外部 systemctl 改过，省略参数会让 unit 停留在旧状态。
    """
    return install_args(
        data_dir,
        enable=enable,
        disable=not enable,
        force=True,
    )


def system_install_command(
    binary: str,
    data_dir: str,
    *,
    run_as: str,
    enable: bool,
) -> str:
    """系统级安装命令；需要 root，GUI 只负责让用户复制。"""
    return shlex.join(
        [
            "sudo",
            binary,
            *install_args(
                data_dir, enable=enable, scope="system", run_as=run_as
            ),
        ]
    )


def service_help_command(binary: str) -> str:
    """安装后查看启动、停止、重启和卸载命令。"""
    return shlex.join([binary, "service", "--help"])


def linger_command(user: str, *, enable: bool = True) -> str:
    action = "enable-linger" if enable else "disable-linger"
    return f"loginctl {action} {shlex.quote(user)}"
