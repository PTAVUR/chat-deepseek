"""服务端配置的默认值。

真正的 config.json 读写由 CLI 负责（`info` 读取，`config set` 写入）；GUI 只
在这里计算与 CLI 一致的默认路径，供首次启动、设置页的“恢复默认”和路径提示使用。
"""

from __future__ import annotations

import sys
from pathlib import Path

DEFAULT_LISTEN = "127.0.0.1:8090"
DEFAULT_API_ORIGIN = "https://api.deepseek.com"


def default_data_dir(binary_dir: str | Path) -> Path:
    """返回默认数据目录；macOS 的应用包在 /Applications 下通常不可写。"""
    directory = Path(binary_dir).expanduser()
    if sys.platform == "darwin":
        return Path.home() / "Library" / "Application Support" / "chat-deepseek" / "data"
    return directory / "data"


def default_backup_dir(data_dir: str | Path) -> str:
    """返回 backup_dir 未配置时的默认地址：数据目录下的 backups/。"""
    return str(Path(data_dir).expanduser() / "backups")


def defaults(binary_dir: str | Path) -> dict[str, str]:
    """按二进制目录计算一套默认参数。"""
    directory = Path(binary_dir).expanduser()
    data_dir = default_data_dir(directory)
    return {
        "binary": str(directory),
        "data_dir": str(data_dir),
        "backup_dir": default_backup_dir(data_dir),
        "listen": DEFAULT_LISTEN,
        "public_url": "",
        "api_origin": DEFAULT_API_ORIGIN,
    }
