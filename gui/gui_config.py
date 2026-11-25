"""GUI 本地设置（config.gui.json）。

服务端配置（监听地址、Public URL、API 地址、备份地址）由 CLI 写入数据目录的
config.json；这里只保存 GUI 自己的连接方式、路径和上次登录的用户名。数据目录
本身不能存在数据目录里的配置中，所以必须单独放在用户配置目录。

v3 结构：

    {
      "version": 3,
      "connection": "local" | "ssh",
      "local": {"binary_dir": "...", "data_dir": "...", "username": "..."},
      "ssh": {
        "host": "...",
        "binary_dir": "...",
        "data_dir": "...",
        "username": "..."
      }
    }

v1 的顶层 binary/data_dir/backup_dir/username 会被迁移到 local；backup_dir 不再
属于 GUI 配置，load 时单独返回，由界面通过 `config set backup_dir` 写进
config.json。v2 只有 local 段，迁移时补一个空的 ssh 段。未知字段原样保留。
"""

from __future__ import annotations

import json
import os
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path

SCHEMA_VERSION = 3
DEFAULT_CONNECTION = "local"
CONNECTIONS = ("local", "ssh")


@dataclass(frozen=True)
class LocalConfig:
    """本机连接的路径和上次登录用户名。"""

    binary_dir: str = ""
    data_dir: str = ""
    username: str = ""


@dataclass(frozen=True)
class SshConfig:
    """SSH 连接的远端路径和上次登录用户名。

    host 使用用户自己的 ssh_config 别名或 `user@host`；GUI 不保存密码。
    binary_dir 是远端绝对目录（必填），data_dir 留空表示用远端 CLI 默认值。
    """

    host: str = ""
    binary_dir: str = ""
    data_dir: str = ""
    username: str = ""


@dataclass(frozen=True)
class Config:
    """config.gui.json 的内存表示。"""

    version: int = SCHEMA_VERSION
    connection: str = DEFAULT_CONNECTION
    local: LocalConfig = LocalConfig()
    ssh: SshConfig = SshConfig()
    # v1 迁移时带出的备份地址；界面写进 config.json 后清空。
    legacy_backup_dir: str = ""
    migrated: bool = False

    @property
    def empty(self) -> bool:
        """没有可用的配置（新安装或模板为空）。"""
        local = self.local
        return not (
            local.binary_dir
            or local.data_dir
            or local.username
            or self.ssh.host
        )

    @property
    def active(self) -> LocalConfig | SshConfig:
        return self.ssh if self.connection == "ssh" else self.local


def config_dir() -> Path:
    """返回当前平台的用户配置目录。"""
    if sys.platform == "win32":
        base = os.environ.get("APPDATA")
        directory = Path(base) if base else Path.home() / "AppData" / "Roaming"
    elif sys.platform == "darwin":
        directory = Path.home() / "Library" / "Application Support"
    else:
        base = os.environ.get("XDG_CONFIG_HOME")
        directory = Path(base) if base else Path.home() / ".config"
    return directory / "chat-deepseek"


def default_path() -> Path:
    """返回 config.gui.json 的平台标准路径。"""
    return config_dir() / "config.gui.json"


def seed_paths() -> list[Path]:
    """返回 macOS .app 内和 DMG 根目录的只读配置模板。"""
    if sys.platform != "darwin" or not getattr(sys, "frozen", False):
        return []
    executable = Path(sys.executable).resolve()
    if len(executable.parents) < 4:
        return []
    resources = executable.parents[1] / "Resources" / "config.gui.json"
    root = executable.parents[3] / "config.gui.json"
    return [resources, root]


def _text(value: object) -> str:
    """只接受非空字符串，并去掉首尾空白。"""
    if isinstance(value, str):
        return value.strip()
    return ""


def _read_raw(path: Path) -> dict[str, object] | None:
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return None
    return raw if isinstance(raw, dict) else None


def _section(raw: dict[str, object], key: str) -> dict[str, object]:
    value = raw.get(key)
    return value if isinstance(value, dict) else {}


def load(path: str | Path) -> Config:
    """读取并归一化 GUI 配置；缺失、损坏或类型不对时返回空配置。"""
    path = Path(path).expanduser()
    raw = _read_raw(path)
    if raw is None:
        return Config()

    version = raw.get("version")
    connection = _text(raw.get("connection")) or DEFAULT_CONNECTION
    if connection not in CONNECTIONS:
        connection = DEFAULT_CONNECTION

    local_raw = raw.get("local")
    legacy = ""
    if isinstance(local_raw, dict):
        local = LocalConfig(
            binary_dir=_text(local_raw.get("binary_dir")),
            data_dir=_text(local_raw.get("data_dir")),
            username=_text(local_raw.get("username")),
        )
    else:
        # v1：顶层 binary/data_dir/backup_dir/username。
        local = LocalConfig(
            binary_dir=_text(raw.get("binary")),
            data_dir=_text(raw.get("data_dir")),
            username=_text(raw.get("username")),
        )
        legacy = _text(raw.get("backup_dir"))

    ssh_raw = _section(raw, "ssh")
    ssh = SshConfig(
        host=_text(ssh_raw.get("host")),
        binary_dir=_text(ssh_raw.get("binary_dir")),
        data_dir=_text(ssh_raw.get("data_dir")),
        username=_text(ssh_raw.get("username")),
    )

    return Config(
        version=SCHEMA_VERSION,
        connection=connection,
        local=local,
        ssh=ssh,
        legacy_backup_dir=legacy,
        migrated=version != SCHEMA_VERSION,
    )


def save(path: str | Path, config: Config) -> None:
    """原子写入 v3 配置，权限保持 0600；保留未知字段。"""
    path = Path(path).expanduser()
    path.parent.mkdir(parents=True, exist_ok=True)

    existing = _read_raw(path) or {}
    existing["version"] = SCHEMA_VERSION
    existing["connection"] = (
        config.connection if config.connection in CONNECTIONS else DEFAULT_CONNECTION
    )

    local = existing.get("local")
    if not isinstance(local, dict):
        local = {}
    local.update(
        {
            "binary_dir": config.local.binary_dir.strip(),
            "data_dir": config.local.data_dir.strip(),
            "username": config.local.username.strip(),
        }
    )
    existing["local"] = local
    # service_scope 是已废弃的卡片范围缓存；写入时顺手清掉。
    local.pop("service_scope", None)

    ssh = existing.get("ssh")
    if not isinstance(ssh, dict):
        ssh = {}
    ssh.update(
        {
            "host": config.ssh.host.strip(),
            "binary_dir": config.ssh.binary_dir.strip(),
            "data_dir": config.ssh.data_dir.strip(),
            "username": config.ssh.username.strip(),
        }
    )
    existing["ssh"] = ssh

    # 迁移完成后不再保留 v1 顶层字段；backup_dir 归 config.json。
    for key in ("binary", "data_dir", "backup_dir", "username"):
        existing.pop(key, None)

    fd, temporary = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
            json.dump(existing, handle, ensure_ascii=False, indent=2)
            handle.write("\n")
        os.chmod(temporary, 0o600)
        os.replace(temporary, path)
    except BaseException:
        try:
            os.unlink(temporary)
        except OSError:
            pass
        raise
