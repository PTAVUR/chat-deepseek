"""读取服务端 config.json，并给出与 CLI 一致的默认值。"""

from __future__ import annotations

import json
from pathlib import Path

DEFAULT_LISTEN = "127.0.0.1:8090"
DEFAULT_API_ORIGIN = "https://api.deepseek.com"


def defaults(binary_dir: str | Path) -> dict[str, str]:
    """按二进制目录计算一套默认参数。"""
    directory = Path(binary_dir).expanduser()
    return {
        "binary": str(directory),
        "data_dir": str(directory / "data"),
        "backup_dir": str(directory / "data" / "backups"),
        "listen": DEFAULT_LISTEN,
        "public_url": "",
        "api_origin": DEFAULT_API_ORIGIN,
    }


def load(data_dir: str | Path) -> dict[str, str]:
    """读取数据目录里的 config.json；缺失或损坏时回退到默认值。"""
    result = {
        "listen": DEFAULT_LISTEN,
        "public_url": "",
        "api_origin": DEFAULT_API_ORIGIN,
    }
    path = Path(data_dir).expanduser() / "config.json"
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return result
    if not isinstance(raw, dict):
        return result
    for key in result:
        value = raw.get(key)
        if isinstance(value, str) and value.strip():
            result[key] = value.strip()
    return result
